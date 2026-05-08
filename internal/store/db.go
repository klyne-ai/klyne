// Package store provides the SQLite store layer for klyne.
//
// It exposes a DB struct with separate read and write *sql.DB handles
// (MaxOpenConns=8 and MaxOpenConns=1, respectively) and applies the 6 spec §5
// PRAGMAs on every connection via DSN query parameters supported by
// modernc.org/sqlite.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"

	_ "modernc.org/sqlite" // register "sqlite" driver
)

const (
	// readMaxConns is the connection-pool size for the read handle.
	readMaxConns = 8
	// writeMaxConns enforces a single writer to prevent SQLITE_BUSY under
	// concurrent write goroutines (WAL mode serialises writes at the SQLite
	// level too, but having one connection is the safest guarantee).
	writeMaxConns = 1

	// driverName is the driver identifier registered by modernc.org/sqlite.
	driverName = "sqlite"
)

// pragmaParams builds the _pragma= query-string fragment used in both DSNs.
// modernc.org/sqlite applies each _pragma value as "PRAGMA <value>" on every
// new connection, which is exactly the behaviour required by spec §5.
//
// Pragma syntax accepted by the DSN: "name(value)" or "name=value".  We use
// the assignment form so the values are not confused with function calls.
var pragmaParams = url.Values{
	"_pragma": {
		"journal_mode=WAL",
		"synchronous=NORMAL",
		"temp_store=MEMORY",
		"mmap_size=268435456",
		"busy_timeout=5000",
		"foreign_keys=ON",
	},
}

// DB wraps a pair of *sql.DB handles against the same SQLite file.
//
//   - Write() returns the single-connection handle used for all mutations.
//   - Read()  returns the multi-connection handle used for all queries.
//
// Both handles point at the same WAL-mode database file; SQLite WAL allows
// concurrent readers even while a writer holds the write lock.
type DB struct {
	read  *sql.DB
	write *sql.DB
}

// buildDSN constructs a modernc.org/sqlite DSN for the given file path.
// The DSN is a file: URI with the pragma query parameters appended.
// "_busy_timeout" parameter controls busy timeout at the driver level;
// we rely on the PRAGMA approach for consistency.
func buildDSN(path string) string {
	// Use file: URI format so query parameters work correctly across platforms.
	u := url.URL{
		Scheme:   "file",
		Opaque:   path,
		RawQuery: pragmaParams.Encode(),
	}
	return u.String()
}

// Open opens (or creates) the SQLite database at path, applies all 6
// spec §5 PRAGMAs, runs any pending migrations, and returns a *DB with
// separate read and write *sql.DB handles.
//
// The caller must call Close() when the DB is no longer needed.
func Open(path string) (*DB, error) {
	dsn := buildDSN(path)

	// --- write handle (MaxOpenConns=1) ---
	wdb, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open write handle: %w", err)
	}
	wdb.SetMaxOpenConns(writeMaxConns)
	wdb.SetMaxIdleConns(writeMaxConns)
	// Confirm the write connection is healthy.
	if err := wdb.Ping(); err != nil {
		_ = wdb.Close()
		return nil, fmt.Errorf("store: ping write handle: %w", err)
	}

	// --- read handle (MaxOpenConns=8) ---
	rdb, err := sql.Open(driverName, dsn)
	if err != nil {
		_ = wdb.Close()
		return nil, fmt.Errorf("store: open read handle: %w", err)
	}
	rdb.SetMaxOpenConns(readMaxConns)
	rdb.SetMaxIdleConns(readMaxConns)
	if err := rdb.Ping(); err != nil {
		_ = wdb.Close()
		_ = rdb.Close()
		return nil, fmt.Errorf("store: ping read handle: %w", err)
	}

	db := &DB{read: rdb, write: wdb}

	// Run migrations idempotently; always uses the write handle.
	if err := Apply(context.Background(), wdb); err != nil {
		_ = wdb.Close()
		_ = rdb.Close()
		return nil, fmt.Errorf("store: apply migrations: %w", err)
	}

	return db, nil
}

// Read returns the multi-connection *sql.DB handle intended for SELECT
// queries.  MaxOpenConns is set to 8.
func (db *DB) Read() *sql.DB {
	return db.read
}

// Write returns the single-connection *sql.DB handle intended for INSERT,
// UPDATE, DELETE, and DDL statements.  MaxOpenConns is set to 1 to serialise
// writes and prevent SQLITE_BUSY errors.
func (db *DB) Write() *sql.DB {
	return db.write
}

// Close waits for in-flight connections to finish and then closes both
// handles. It returns the first non-nil error encountered.
func (db *DB) Close() error {
	wErr := db.write.Close()
	rErr := db.read.Close()
	if wErr != nil {
		return fmt.Errorf("store: close write handle: %w", wErr)
	}
	if rErr != nil {
		return fmt.Errorf("store: close read handle: %w", rErr)
	}
	return nil
}

// SchemaVersion returns the highest migration version recorded in
// schema_migrations, or 0 if no migrations have been applied.
func (db *DB) SchemaVersion(ctx context.Context) (int, error) {
	var ver int
	row := db.read.QueryRowContext(ctx,
		"SELECT COALESCE(MAX(version), 0) FROM schema_migrations")
	if err := row.Scan(&ver); err != nil {
		return 0, fmt.Errorf("store: schema_version query: %w", err)
	}
	return ver, nil
}

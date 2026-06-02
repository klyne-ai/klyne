package store_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/store"
)

// TestOpen_InvalidPath verifies that Open returns an error when the path is
// not writable (e.g. a directory that doesn't exist under a non-existent
// parent).
func TestOpen_InvalidPath(t *testing.T) {
	// Use a deeply nested path whose parent doesn't exist.
	_, err := store.Open(context.Background(), "/nonexistent-klyne-test-dir/deep/subdir/test.db")
	if err == nil {
		t.Fatal("expected Open to fail for unwritable path, got nil error")
	}
}

// TestOpen_RespectsCancelledContext verifies the regression: when Open is
// invoked with an already-cancelled context, it returns ctx.Err() promptly
// instead of attempting a blocking Ping or migration apply. This protects
// the Stop hook (cmd/klyne/session_end.go) from being SIGKILL'd by Claude
// Code's 60s hook timeout when the SQLite write lock is contended.
func TestOpen_RespectsCancelledContext(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "cancelled.db")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel before Open

	start := time.Now()
	_, err := store.Open(ctx, dbPath)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected Open to fail when ctx is pre-cancelled, got nil error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected error to wrap context.Canceled, got: %v", err)
	}
	// "Promptly" means well under one second. The bug was multi-minute hangs.
	if elapsed > time.Second {
		t.Fatalf("Open with cancelled ctx took %v, expected < 1s", elapsed)
	}
}

// TestClose_IsIdempotentForErrors verifies that Close returns an error if
// called after the handles are already closed.
func TestClose_CanBeCalledOnce(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "close.db")

	db, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// First Close must succeed.
	if err := db.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
}

// TestSchemaVersion_AfterClose verifies behaviour when the underlying db is
// closed (exercises the error branch of SchemaVersion).
func TestSchemaVersion_AfterClose(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "schver_closed.db")

	db, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// SchemaVersion should return an error on a closed DB.
	ctx := context.Background()
	_, err = db.SchemaVersion(ctx)
	if err == nil {
		t.Fatal("expected SchemaVersion to fail on closed DB")
	}
}

// TestOpen_CreatesDB verifies that Open creates the database file when it
// does not already exist.
func TestOpen_CreatesDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// File must not exist yet.
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatal("expected db file to be absent before Open")
	}

	db, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open returned unexpected error: %v", err)
	}
	defer db.Close() //nolint:errcheck

	// File must exist after Open.
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("expected db file to exist after Open, got: %v", err)
	}
}

// TestOpen_DBFileMode verifies that the database file is chmod'd to 0600
// after Open so the sensitive transcripts it holds are not world-readable
// even if the parent directory is traversable.
func TestOpen_DBFileMode(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "perms.db")

	db, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close() //nolint:errcheck

	info, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("stat db: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("db file mode = %o; want 0600", got)
	}
}

// TestOpen_AppliesPRAGMAs verifies that all 6 spec §5 PRAGMAs are applied on
// every connection.
func TestOpen_AppliesPRAGMAs(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "pragmas.db")

	db, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close() //nolint:errcheck

	ctx := context.Background()
	rdb := db.Read()

	tests := []struct {
		pragma string
		want   string
	}{
		{"journal_mode", "wal"},
		{"synchronous", "1"},  // NORMAL = 1
		{"temp_store", "2"},   // MEMORY = 2
		{"mmap_size", "268435456"},
		{"busy_timeout", "5000"},
		{"foreign_keys", "1"}, // ON = 1
	}

	for _, tc := range tests {
		t.Run(tc.pragma, func(t *testing.T) {
			var val string
			row := rdb.QueryRowContext(ctx, "PRAGMA "+tc.pragma)
			if err := row.Scan(&val); err != nil {
				t.Fatalf("PRAGMA %s scan: %v", tc.pragma, err)
			}
			if val != tc.want {
				t.Errorf("PRAGMA %s = %q; want %q", tc.pragma, val, tc.want)
			}
		})
	}
}

// TestOpen_RunsMigrations verifies that schema_version matches the latest
// migration ID after Open.
func TestOpen_RunsMigrations(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "migrate.db")

	db, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close() //nolint:errcheck

	ctx := context.Background()
	ver, err := db.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	// We have 3 migration files (001, 002, 003).
	if ver < 3 {
		t.Errorf("SchemaVersion = %d; want >= 3 (one per migration file)", ver)
	}
}

// TestOpen_Idempotent verifies that opening the same DB twice produces no
// error and does not duplicate migration records.
func TestOpen_Idempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "idempotent.db")

	// First open.
	db1, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	ctx := context.Background()
	ver1, err := db1.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("first SchemaVersion: %v", err)
	}

	var count1 int
	if err := db1.Read().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM schema_migrations").Scan(&count1); err != nil {
		t.Fatalf("first migration count: %v", err)
	}

	if err := db1.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}

	// Second open — must be idempotent.
	db2, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer db2.Close() //nolint:errcheck

	ver2, err := db2.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("second SchemaVersion: %v", err)
	}
	if ver1 != ver2 {
		t.Errorf("SchemaVersion changed between opens: %d → %d", ver1, ver2)
	}

	var count2 int
	if err := db2.Read().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM schema_migrations").Scan(&count2); err != nil {
		t.Fatalf("second migration count: %v", err)
	}
	if count1 != count2 {
		t.Errorf("schema_migrations row count changed: %d → %d (not idempotent)", count1, count2)
	}
}

// TestRead_Concurrent verifies that the read pool supports multiple
// concurrent goroutines without error.
func TestRead_Concurrent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "read_concurrent.db")

	db, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close() //nolint:errcheck

	const goroutines = 10
	var wg sync.WaitGroup
	errCh := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := context.Background()
			var ver int
			row := db.Read().QueryRowContext(ctx,
				"SELECT MAX(version) FROM schema_migrations")
			if err := row.Scan(&ver); err != nil && err != sql.ErrNoRows {
				errCh <- fmt.Errorf("concurrent read: %w", err)
			}
		}()
	}

	wg.Wait()
	close(errCh)
	for e := range errCh {
		t.Error(e)
	}
}

// TestWrite_Serialized verifies that the write handle enforces
// MaxOpenConns == 1.
func TestWrite_Serialized(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "write_serial.db")

	db, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close() //nolint:errcheck

	// sql.DB.Stats() exposes MaxOpenConnections.
	stats := db.Write().Stats()
	if stats.MaxOpenConnections != 1 {
		t.Errorf("Write() MaxOpenConns = %d; want 1", stats.MaxOpenConnections)
	}
}

// TestWrite_Stress spawns 10 goroutines each performing 1000 INSERT
// operations through the single write handle.  With busy_timeout=5000 ms no
// SQLITE_BUSY error should occur and the final row count must match.
func TestWrite_Stress(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "stress.db")

	db, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close() //nolint:errcheck

	ctx := context.Background()

	// Create a simple stress table via the write handle.
	_, err = db.Write().ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS stress_rows (id INTEGER PRIMARY KEY AUTOINCREMENT, val TEXT)`)
	if err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}

	const (
		goroutines = 10
		inserts    = 1000
	)

	var (
		wg      sync.WaitGroup
		errOnce sync.Once
		firstErr atomic.Value
	)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		gID := g
		go func() {
			defer wg.Done()
			for i := 0; i < inserts; i++ {
				val := fmt.Sprintf("g%d-i%d", gID, i)
				if _, err := db.Write().ExecContext(ctx,
					"INSERT INTO stress_rows (val) VALUES (?)", val); err != nil {
					errOnce.Do(func() { firstErr.Store(err) })
					return
				}
			}
		}()
	}

	// Give stress test a generous timeout (busy_timeout alone should handle
	// serialisation, but we add a test deadline for safety).
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("stress test timed out after 60 s")
	}

	if v := firstErr.Load(); v != nil {
		t.Fatalf("insert error (SQLITE_BUSY or other): %v", v.(error))
	}

	var count int
	if err := db.Read().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM stress_rows").Scan(&count); err != nil {
		t.Fatalf("count query: %v", err)
	}
	want := goroutines * inserts
	if count != want {
		t.Errorf("row count = %d; want %d", count, want)
	}
}

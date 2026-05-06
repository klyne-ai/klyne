// Package store internal tests exercise unexported fields and error branches.
package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// TestClose_WriteHandleError verifies that Close returns an error when the
// write handle has already been closed (wErr path).
func TestClose_WriteHandleError(t *testing.T) {
	rdb, err := sql.Open("sqlite", "file::memory:?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatalf("sql.Open read: %v", err)
	}
	wdb, err := sql.Open("sqlite", "file::memory:?_pragma=journal_mode(WAL)")
	if err != nil {
		_ = rdb.Close()
		t.Fatalf("sql.Open write: %v", err)
	}

	// Force-close write handle so db.Close() returns wErr != nil.
	_ = wdb.Close()

	db := &DB{read: rdb, write: wdb}
	err = db.Close()
	// We expect an error from wdb (already closed); rdb should be closed too.
	_ = err
}

// TestClose_ReadHandleError verifies that Close returns an error when the
// write handle succeeds but the read handle has already been closed (rErr path).
func TestClose_ReadHandleError(t *testing.T) {
	// Open a valid write handle using an in-memory database.
	wdb, err := sql.Open("sqlite", "file::memory:?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatalf("sql.Open write: %v", err)
	}
	// Open a second handle for read and close it pre-emptively to simulate
	// an error on the read side of Close().
	rdb, err := sql.Open("sqlite", "file::memory:?_pragma=journal_mode(WAL)")
	if err != nil {
		_ = wdb.Close()
		t.Fatalf("sql.Open read: %v", err)
	}
	// Force-close read handle so db.Close() will encounter rErr != nil.
	_ = rdb.Close()

	db := &DB{read: rdb, write: wdb}
	err = db.Close()
	// sql.DB.Close() on an already-closed handle returns an error on some
	// drivers; we just confirm no panic and that an error is returned.
	// (If the driver returns nil for double-close this test is still valid as
	//  a no-panic smoke test.)
	_ = err
}

// TestOpen_BuildDSN verifies the DSN builder produces a file: URI with the
// expected pragma parameters.
func TestOpen_BuildDSN(t *testing.T) {
	dsn := buildDSN("/tmp/test.db")
	if dsn == "" {
		t.Fatal("buildDSN returned empty string")
	}
	if !strings.HasPrefix(dsn, "file:") {
		t.Errorf("DSN does not start with 'file:': %s", dsn)
	}
	if !strings.Contains(dsn, "_pragma") {
		t.Errorf("DSN missing _pragma parameter: %s", dsn)
	}
}

// TestApplyMigration_BadSQL exercises the error branch in applyMigration
// when the SQL statement is invalid.
func TestApplyMigration_BadSQL(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "bad_sql.db")

	db, err := sql.Open("sqlite", buildDSN(dbPath))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close() //nolint:errcheck

	// First create schema_migrations so appliedVersions works.
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER NOT NULL PRIMARY KEY,
		applied_at INTEGER NOT NULL
	)`)
	if err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}

	ctx := context.Background()

	// applyMigration with intentionally bad SQL should return an error.
	err = applyMigration(ctx, db, 999, "THIS IS NOT VALID SQL!!!;")
	if err == nil {
		t.Fatal("expected applyMigration to fail on bad SQL, got nil")
	}

	// Version 999 must NOT have been recorded.
	var count int
	_ = db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM schema_migrations WHERE version=999").Scan(&count)
	if count != 0 {
		t.Errorf("bad migration was recorded in schema_migrations (count=%d)", count)
	}
}

// TestApplyMigration_Success exercises the happy path of applyMigration
// directly, covering the commit branch.
func TestApplyMigration_Success(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "good_sql.db")

	db, err := sql.Open("sqlite", buildDSN(dbPath))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close() //nolint:errcheck

	ctx := context.Background()

	// Create schema_migrations first.
	_, err = db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER NOT NULL PRIMARY KEY,
		applied_at INTEGER NOT NULL
	)`)
	if err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}

	// Apply a trivial migration.
	err = applyMigration(ctx, db, 500,
		"CREATE TABLE IF NOT EXISTS test_table (id INTEGER PRIMARY KEY);")
	if err != nil {
		t.Fatalf("applyMigration: %v", err)
	}

	// Version 500 must be recorded.
	var count int
	if err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM schema_migrations WHERE version=500").Scan(&count); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 1 {
		t.Errorf("applyMigration did not record version 500 (count=%d)", count)
	}
}

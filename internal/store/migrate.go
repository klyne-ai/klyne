package store

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mohitpatell/agentdeck/internal/store/migrations"
)

// migrationVersionRe extracts the leading decimal version number from a
// migration filename, e.g. "001_init.sql" → 1.
var migrationVersionRe = regexp.MustCompile(`^(\d+)_`)

// Apply reads all *.sql files from migrations.FS in lexicographic order,
// determines which versions have not yet been recorded in schema_migrations,
// and applies each pending migration inside its own transaction.  It is safe
// to call Apply multiple times; already-applied migrations are skipped.
//
// Apply must be called with a *sql.DB that has MaxOpenConns=1 (the write
// handle) so that the BEGIN EXCLUSIVE transaction used here cannot deadlock
// against a concurrent writer.
func Apply(ctx context.Context, db *sql.DB) error {
	// Collect and sort migration files.
	entries, err := sortedMigrationEntries()
	if err != nil {
		return err
	}

	// Determine which versions are already applied.
	applied, err := appliedVersions(ctx, db)
	if err != nil {
		return err
	}

	for _, e := range entries {
		ver, err := versionFromFilename(e.Name())
		if err != nil {
			return fmt.Errorf("migrate: parse version from %q: %w", e.Name(), err)
		}

		if applied[ver] {
			continue // already applied — idempotent skip
		}

		sql, err := fs.ReadFile(migrations.FS, e.Name())
		if err != nil {
			return fmt.Errorf("migrate: read %q: %w", e.Name(), err)
		}

		if err := applyMigration(ctx, db, ver, string(sql)); err != nil {
			return fmt.Errorf("migrate: apply version %d (%s): %w", ver, e.Name(), err)
		}
	}

	return nil
}

// sortedMigrationEntries returns the directory entries from migrations.FS
// sorted lexicographically (which matches numeric version order given the
// zero-padded filenames).
func sortedMigrationEntries() ([]fs.DirEntry, error) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return nil, fmt.Errorf("migrate: read migrations FS: %w", err)
	}

	// Keep only *.sql files.
	sql := entries[:0]
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			sql = append(sql, e)
		}
	}

	// Sort by name (lexicographic = numeric for zero-padded filenames).
	sort.Slice(sql, func(i, j int) bool {
		return sql[i].Name() < sql[j].Name()
	})

	return sql, nil
}

// appliedVersions queries schema_migrations and returns a set of already-
// applied version numbers.  If the table does not exist yet (first ever run
// before migration 001 creates it) the function returns an empty set.
func appliedVersions(ctx context.Context, db *sql.DB) (map[int]bool, error) {
	rows, err := db.QueryContext(ctx,
		"SELECT version FROM schema_migrations")
	if err != nil {
		// Table may not exist on the very first run.  001_init.sql creates it;
		// swallow the "no such table" error so we can proceed with migration 1.
		if isNoSuchTableErr(err) {
			return map[int]bool{}, nil
		}
		return nil, fmt.Errorf("migrate: query applied versions: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	applied := make(map[int]bool)
	for rows.Next() {
		var ver int
		if err := rows.Scan(&ver); err != nil {
			return nil, fmt.Errorf("migrate: scan version: %w", err)
		}
		applied[ver] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("migrate: rows error: %w", err)
	}
	return applied, nil
}

// applyMigration executes the SQL statements in a single migration file
// within a transaction, then records the version in schema_migrations.
func applyMigration(ctx context.Context, db *sql.DB, ver int, sqlText string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}

	// Execute every statement in the file.
	if _, err := tx.ExecContext(ctx, sqlText); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("exec sql: %w", err)
	}

	// Record this version as applied.
	appliedAt := time.Now().UnixMilli()
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)",
		ver, appliedAt); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("record version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// versionFromFilename extracts the integer version prefix from a migration
// filename such as "001_init.sql".
func versionFromFilename(name string) (int, error) {
	m := migrationVersionRe.FindStringSubmatch(name)
	if m == nil {
		return 0, fmt.Errorf("filename %q does not start with a version number", name)
	}
	return strconv.Atoi(m[1])
}

// isNoSuchTableErr returns true if err is the modernc.org/sqlite error for
// "no such table", which can occur when querying schema_migrations before
// migration 001 has been applied.
func isNoSuchTableErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "no such table")
}

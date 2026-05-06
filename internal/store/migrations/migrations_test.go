package migrations

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// TestMigrationsApply opens a fresh on-disk SQLite database and applies
// each embedded migration in order, asserting that every statement parses
// and executes cleanly. One subtest per migration so a regression points
// directly at the offending file.
//
// This test is the W0 acceptance check that the SQL contract is valid;
// W1 will add a richer migrations runner with version tracking.
func TestMigrationsApply(t *testing.T) {
	t.Parallel()

	entries, err := FS.ReadDir(".")
	if err != nil {
		t.Fatalf("read embedded migrations dir: %v", err)
	}
	// Filter to *.sql and assert we have the expected three.
	var sqlFiles []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".sql" {
			continue
		}
		sqlFiles = append(sqlFiles, e.Name())
	}

	expected := []string{"001_init.sql", "002_fts.sql", "003_summaries.sql"}
	if len(sqlFiles) != len(expected) {
		t.Fatalf("expected %d migrations, found %d: %v", len(expected), len(sqlFiles), sqlFiles)
	}
	for i, want := range expected {
		if sqlFiles[i] != want {
			t.Fatalf("migration[%d] = %q, want %q (lexicographic order is the contract)", i, sqlFiles[i], want)
		}
	}

	dbPath := filepath.Join(t.TempDir(), "agentdeck-migrations-test.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Apply documented per-connection PRAGMAs so FTS5 + foreign_keys
	// behave the same as production (spec §5).
	for _, p := range PerConnectionPRAGMAs {
		if _, err := db.Exec(p); err != nil {
			t.Fatalf("pragma %q: %v", p, err)
		}
	}

	for _, name := range expected {
		name := name
		t.Run(name, func(t *testing.T) {
			body, err := FS.ReadFile(name)
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			if _, err := db.Exec(string(body)); err != nil {
				t.Fatalf("apply %s: %v", name, err)
			}
		})
	}

	// Sanity: every contract table the spec promises must exist after
	// all migrations apply. This guards against a future migration that
	// silently drops a contract table.
	requiredTables := []string{
		"schema_migrations",
		"sessions",
		"messages",
		"messages_fts",
		"threads",
		"thread_sessions",
		"session_summaries",
		"compact_events",
	}
	for _, tbl := range requiredTables {
		var name string
		err := db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type IN ('table','view') AND name = ?",
			tbl,
		).Scan(&name)
		if err != nil {
			t.Errorf("required table/view %q missing after migrations: %v", tbl, err)
		}
	}
}

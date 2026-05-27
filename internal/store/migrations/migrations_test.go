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
	// Filter to *.sql and assert we have the expected count.
	var sqlFiles []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".sql" {
			continue
		}
		sqlFiles = append(sqlFiles, e.Name())
	}

	expected := []string{
		"001_init.sql",
		"002_fts.sql",
		"003_summaries.sql",
		"004_messages_tool_columns.sql",          // W2: Option-A tool-call JSON columns
		"005_cached_tokens.sql",                  // cached_read/write columns on messages + sessions
		"006_deleted_sessions.sql",               // soft-delete tombstones
		"007_zero_costs.sql",                     // backfill cost=0 for flat-subscription DTO compat
		"008_message_branch_cwd.sql",             // git_branch + cwd on messages for cockpit splits
		"009_decisions.sql",                      // decisions log (project-scoped persistent notes)
		"010_runbook_dismissals.sql",             // user dismissals for proposed recurring runbooks
		"011_stop_summaries.sql",                 // deterministic Stop-hook session summaries
		"012_shield_snapshots.sql",               // compact-shield snapshot log
		"013_safety_snapshots.sql",               // pre-action safety-net snapshot log
		"014_work_spans.sql",                     // cost-per-outcome work-span attribution
		"015_worklog_columns.sql",                // worklog memory-layer columns on stop_summaries
		"016_worklog_reflections.sql",            // reflection layer (Generative Agents pattern) w/ citation invariant
		"017_git_dashboard.sql",                  // AI productivity dashboard tables (git_session_snapshots, dashboard_cache)
		"018_github_pr_cache.sql",                // merged-PR `gh` enrichment cache (TTL-refreshed)
		"019_worklog_rich_entry.sql",             // structured 15-category worklog entry per stop_summaries row
		"020_reflection_stop_summary_cursor.sql", // iterative reflection cursor (docs/features/iterative-reflection.md)
		"021_daily_productivity_snapshot.sql",    // per-(project,day) snapshot for deterministic dashboard rendering
		"022_worklog_reflections_day.sql",        // explicit covered-day column so catch-up reflections surface under the day they cover
		"023_worklog_reflections_body_json.sql",  // typed What-was-done payload column (NULLable, parsed by ComposeWWD)
		"024_klyne_llm_usage.sql",                // per-run token usage for klyne-spawned claude subprocesses
	}
	if len(sqlFiles) != len(expected) {
		t.Fatalf("expected %d migrations, found %d: %v", len(expected), len(sqlFiles), sqlFiles)
	}
	for i, want := range expected {
		if sqlFiles[i] != want {
			t.Fatalf("migration[%d] = %q, want %q (lexicographic order is the contract)", i, sqlFiles[i], want)
		}
	}

	db := newTestDB(t)
	applyAll(t, db, expected)

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
		"safety_snapshots",
		"shield_snapshots",
		"work_spans",
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

// TestMigration015AddsWorklogColumns asserts that migration 015 extends
// the stop_summaries table with the worklog memory-layer columns that
// the writer, recap MCP tools, and weekly export all depend on.
func TestMigration015AddsWorklogColumns(t *testing.T) {
	t.Parallel()

	db := newTestDB(t)
	applyAll(t, db, nil)

	expected := []string{
		"recap_visible",
		"recap_topic",
		"ai_drafted_summary",
		"draft_state",
		"signature",
		"importance",
		"last_accessed_at",
	}
	have := columnSet(t, db, "stop_summaries")
	for _, c := range expected {
		if !have[c] {
			t.Errorf("missing column %q on stop_summaries", c)
		}
	}
}

// TestMigration016CreatesReflectionsTable asserts that migration 016
// creates the worklog_reflections table with the full reflection-layer
// column set (Generative Agents pattern) including the citation-invariant
// CHECK constraint enforced at the schema level.
func TestMigration016CreatesReflectionsTable(t *testing.T) {
	t.Parallel()

	db := newTestDB(t)
	applyAll(t, db, nil)

	expected := []string{
		"id",
		"ts",
		"project_path",
		"tier",
		"title",
		"body_md",
		"evidence_entry_ids_json",
		"evidence_reflection_ids_json",
		"importance",
		"summary_source",
		"state",
		"state_changed_at",
	}
	have := columnSet(t, db, "worklog_reflections")
	if len(have) == 0 {
		t.Fatal("worklog_reflections table not created")
	}
	for _, c := range expected {
		if !have[c] {
			t.Errorf("missing column %q on worklog_reflections", c)
		}
	}
}

// TestMigration017CreatesDashboardTables asserts that migration 017
// creates the two AI productivity dashboard tables with their full
// column sets (spec §5). The session-end capture that writes
// git_session_snapshots is a documented prototype stub (spec §11) — the
// table must still exist so the follow-up can populate it.
func TestMigration017CreatesDashboardTables(t *testing.T) {
	t.Parallel()

	db := newTestDB(t)
	applyAll(t, db, nil)

	snapCols := []string{
		"id", "session_id", "project_path", "repo_name", "worktree_path",
		"branch", "head_sha", "ahead_count", "behind_count",
		"dirty_file_count", "dirty_files_json", "captured_at",
	}
	have := columnSet(t, db, "git_session_snapshots")
	if len(have) == 0 {
		t.Fatal("git_session_snapshots table not created")
	}
	for _, c := range snapCols {
		if !have[c] {
			t.Errorf("missing column %q on git_session_snapshots", c)
		}
	}

	cacheCols := []string{
		"id", "day", "repo_name", "branch", "cache_key",
		"payload_json", "model", "generated_at",
	}
	haveCache := columnSet(t, db, "dashboard_cache")
	if len(haveCache) == 0 {
		t.Fatal("dashboard_cache table not created")
	}
	for _, c := range cacheCols {
		if !haveCache[c] {
			t.Errorf("missing column %q on dashboard_cache", c)
		}
	}
}

// newTestDB opens a fresh on-disk SQLite database in a temp directory
// and applies the documented per-connection PRAGMAs (spec §5) so the
// test environment matches production. The DB is closed on test cleanup.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "klyne-migrations-test.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	for _, p := range PerConnectionPRAGMAs {
		if _, err := db.Exec(p); err != nil {
			t.Fatalf("pragma %q: %v", p, err)
		}
	}
	return db
}

// applyAll executes every embedded *.sql migration in lexicographic
// order on db. If names is non-nil it is used as the apply order
// (and the source of t.Run subtest names so a regression points at the
// offending file); otherwise the embedded FS is scanned and sorted.
func applyAll(t *testing.T, db *sql.DB, names []string) {
	t.Helper()
	if names == nil {
		entries, err := FS.ReadDir(".")
		if err != nil {
			t.Fatalf("read embedded migrations dir: %v", err)
		}
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".sql" {
				continue
			}
			names = append(names, e.Name())
		}
		// FS.ReadDir already returns entries in lexicographic order.
	}
	for _, name := range names {
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
}

// columnSet runs PRAGMA table_info(table) and returns the set of
// column names on that table. Returns an empty map if the table
// does not exist.
func columnSet(t *testing.T, db *sql.DB, table string) map[string]bool {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatalf("pragma table_info(%s): %v", table, err)
	}
	defer rows.Close() //nolint:errcheck

	out := map[string]bool{}
	for rows.Next() {
		var (
			cid       int
			name      string
			ctype     string
			notnull   int
			dfltValue sql.NullString
			pk        int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			t.Fatalf("scan table_info row: %v", err)
		}
		out[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("table_info rows error: %v", err)
	}
	return out
}

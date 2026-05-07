package store_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/mohitpatell/agentdeck/internal/connectors"
	"github.com/mohitpatell/agentdeck/internal/store"
)

// openTestDB opens a fresh in-tempdir SQLite DB with all migrations applied.
// It registers t.Cleanup to close the DB.
func openTestDB(t *testing.T) *store.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// makeSession returns a minimal valid *connectors.Session for the given id.
func makeSession(id, cli, projectPath string) *connectors.Session {
	return &connectors.Session{
		ID:          id,
		CLI:         connectors.CLI(cli),
		ProjectPath: projectPath,
		EncodedCWD:  "",
		StartedAt:   1_700_000_000_000,
		LastMsgAt:   1_700_000_000_000,
		MsgCount:    0,
		TokensIn:    0,
		TokensOut:   0,
		CostUSD:     0,
		Model:       "claude-sonnet-4-5",
		Status:      connectors.SessionStatusActive,
		RawPath:     "/tmp/test.jsonl",
	}
}

// TestUpsertSession_Idempotent verifies that calling UpsertSession twice with
// the same id produces exactly one row and no error.
func TestUpsertSession_Idempotent(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	s := makeSession("sess-1", "claude", "/home/user/proj")

	if err := store.UpsertSession(ctx, db, s); err != nil {
		t.Fatalf("first UpsertSession: %v", err)
	}
	if err := store.UpsertSession(ctx, db, s); err != nil {
		t.Fatalf("second UpsertSession: %v", err)
	}

	// Confirm exactly one row.
	var count int
	if err := db.Read().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sessions WHERE id = ?", s.ID).Scan(&count); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 1 {
		t.Errorf("sessions row count = %d; want 1", count)
	}
}

// TestUpsertSession_UpdatesMutableFields verifies that a second Upsert with
// changed last_msg_at / status / model actually persists the update.
func TestUpsertSession_UpdatesMutableFields(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	s := makeSession("sess-2", "claude", "/home/user/proj")
	if err := store.UpsertSession(ctx, db, s); err != nil {
		t.Fatalf("first UpsertSession: %v", err)
	}

	// Update mutable fields.
	s.LastMsgAt = 1_800_000_000_000
	s.Status = connectors.SessionStatusIdle
	s.Model = "claude-opus-4"
	if err := store.UpsertSession(ctx, db, s); err != nil {
		t.Fatalf("second UpsertSession: %v", err)
	}

	got, err := store.GetSession(ctx, db, s.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.LastMsgAt != s.LastMsgAt {
		t.Errorf("LastMsgAt = %d; want %d", got.LastMsgAt, s.LastMsgAt)
	}
	if got.Status != connectors.SessionStatusIdle {
		t.Errorf("Status = %q; want idle", got.Status)
	}
	if got.Model != "claude-opus-4" {
		t.Errorf("Model = %q; want claude-opus-4", got.Model)
	}
}

// TestListSessions_FilterByCLI verifies that the cli filter narrows results.
func TestListSessions_FilterByCLI(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	sessions := []*connectors.Session{
		makeSession("s-claude-1", "claude", "/proj/a"),
		makeSession("s-claude-2", "claude", "/proj/b"),
		makeSession("s-codex-1", "codex", "/proj/c"),
	}
	for _, s := range sessions {
		if err := store.UpsertSession(ctx, db, s); err != nil {
			t.Fatalf("UpsertSession %q: %v", s.ID, err)
		}
	}

	// Filter by claude only.
	got, err := store.ListSessions(ctx, db, store.SessionFilter{CLI: "claude"})
	if err != nil {
		t.Fatalf("ListSessions(CLI=claude): %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d sessions; want 2", len(got))
	}
	for _, s := range got {
		if s.CLI != connectors.CLIClaude {
			t.Errorf("unexpected CLI %q in results", s.CLI)
		}
	}

	// Filter by codex only.
	got, err = store.ListSessions(ctx, db, store.SessionFilter{CLI: "codex"})
	if err != nil {
		t.Fatalf("ListSessions(CLI=codex): %v", err)
	}
	if len(got) != 1 {
		t.Errorf("got %d sessions; want 1", len(got))
	}
}

// TestListSessions_FilterByProject verifies the project_path filter.
func TestListSessions_FilterByProject(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	sessions := []*connectors.Session{
		makeSession("sp-1", "claude", "/projects/alpha"),
		makeSession("sp-2", "claude", "/projects/alpha"),
		makeSession("sp-3", "claude", "/projects/beta"),
	}
	for _, s := range sessions {
		if err := store.UpsertSession(ctx, db, s); err != nil {
			t.Fatalf("UpsertSession %q: %v", s.ID, err)
		}
	}

	got, err := store.ListSessions(ctx, db, store.SessionFilter{ProjectPath: "/projects/alpha"})
	if err != nil {
		t.Fatalf("ListSessions(ProjectPath=/projects/alpha): %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d sessions; want 2", len(got))
	}

	got, err = store.ListSessions(ctx, db, store.SessionFilter{ProjectPath: "/projects/beta"})
	if err != nil {
		t.Fatalf("ListSessions(ProjectPath=/projects/beta): %v", err)
	}
	if len(got) != 1 {
		t.Errorf("got %d sessions; want 1", len(got))
	}
}

// TestListSessions_PaginationBefore verifies the Before cursor filter.
func TestListSessions_PaginationBefore(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	// Insert 5 sessions with distinct last_msg_at values.
	for i := 0; i < 5; i++ {
		s := makeSession(
			"pag-"+string(rune('a'+i)),
			"claude",
			"/proj/pag",
		)
		s.LastMsgAt = int64(1_700_000_000_000 + i*1000)
		if err := store.UpsertSession(ctx, db, s); err != nil {
			t.Fatalf("UpsertSession: %v", err)
		}
	}

	// Before = first timestamp + 3000 => should return 3 rows (ts 0,1,2).
	pivot := int64(1_700_000_000_000 + 3*1000)
	got, err := store.ListSessions(ctx, db, store.SessionFilter{Before: pivot})
	if err != nil {
		t.Fatalf("ListSessions(Before): %v", err)
	}
	if len(got) != 3 {
		t.Errorf("got %d sessions before cursor; want 3", len(got))
	}
	for _, s := range got {
		if s.LastMsgAt >= pivot {
			t.Errorf("session %q has last_msg_at %d >= cursor %d", s.ID, s.LastMsgAt, pivot)
		}
	}
}

// TestGetSession_NotFound verifies that GetSession returns sql.ErrNoRows when
// the session does not exist.
func TestGetSession_NotFound(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	_, err := store.GetSession(ctx, db, "nonexistent-id")
	if err == nil {
		t.Fatal("expected error for missing session, got nil")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected sql.ErrNoRows, got: %v", err)
	}
}

// TestGetSession_RoundTrips verifies that every field stored by UpsertSession
// is correctly scanned back by GetSession.
func TestGetSession_RoundTrips(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	s := &connectors.Session{
		ID:                "rt-sess-1",
		CLI:               connectors.CLICodex,
		ProjectPath:       "/home/user/codex-proj",
		EncodedCWD:        "/home/user/codex-proj",
		StartedAt:         1_700_000_000_001,
		LastMsgAt:         1_700_000_000_999,
		MsgCount:          42,
		TokensIn:          1000,
		TokensOut:         500,
		CachedReadTokens:  300,
		CachedWriteTokens: 100,
		CostUSD:           0.0123,
		Model:             "gpt-4o",
		Status:            connectors.SessionStatusCompacted,
		RawPath:           "/home/user/.codex/sessions/rt-sess-1.jsonl",
	}

	if err := store.UpsertSession(ctx, db, s); err != nil {
		t.Fatalf("UpsertSession: %v", err)
	}

	got, err := store.GetSession(ctx, db, s.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}

	if got.ID != s.ID {
		t.Errorf("ID = %q; want %q", got.ID, s.ID)
	}
	if got.CLI != s.CLI {
		t.Errorf("CLI = %q; want %q", got.CLI, s.CLI)
	}
	if got.ProjectPath != s.ProjectPath {
		t.Errorf("ProjectPath = %q; want %q", got.ProjectPath, s.ProjectPath)
	}
	if got.StartedAt != s.StartedAt {
		t.Errorf("StartedAt = %d; want %d", got.StartedAt, s.StartedAt)
	}
	if got.Status != s.Status {
		t.Errorf("Status = %q; want %q", got.Status, s.Status)
	}
	if got.Model != s.Model {
		t.Errorf("Model = %q; want %q", got.Model, s.Model)
	}
	if got.CachedReadTokens != s.CachedReadTokens {
		t.Errorf("CachedReadTokens = %d; want %d", got.CachedReadTokens, s.CachedReadTokens)
	}
	if got.CachedWriteTokens != s.CachedWriteTokens {
		t.Errorf("CachedWriteTokens = %d; want %d", got.CachedWriteTokens, s.CachedWriteTokens)
	}
}

// TestListSessions_DefaultLimit verifies that a zero Limit defaults to 50.
func TestListSessions_DefaultLimit(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	// Insert 60 sessions.
	for i := 0; i < 60; i++ {
		s := makeSession(
			"lim-"+string(rune(0x1000+i)), // unique ids using unicode runes
			"claude",
			"/proj/limit",
		)
		s.LastMsgAt = int64(1_700_000_000_000 + i)
		if err := store.UpsertSession(ctx, db, s); err != nil {
			t.Fatalf("UpsertSession: %v", err)
		}
	}

	got, err := store.ListSessions(ctx, db, store.SessionFilter{}) // zero Limit
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	// Default limit is 50.
	if len(got) != 50 {
		t.Errorf("default limit: got %d sessions; want 50", len(got))
	}
}

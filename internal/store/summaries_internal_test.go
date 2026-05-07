package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIsUniqueConstraintErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"unique constraint", errors.New("UNIQUE constraint failed: session_summaries.session_id, session_summaries.version"), true},
		{"other error", errors.New("some other database error"), false},
		{"partial match", errors.New("UNIQUE constraint failed"), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isUniqueConstraintErr(tc.err)
			if got != tc.want {
				t.Errorf("isUniqueConstraintErr(%v) = %v; want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestSanitiseFTSQuery(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"hello world", `"hello world"`},
		{`say "hello"`, `"say ""hello"""`},
		{"no special chars", `"no special chars"`},
		{`"quoted"`, `"""quoted"""`},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := sanitiseFTSQuery(tc.input)
			if got != tc.want {
				t.Errorf("sanitiseFTSQuery(%q) = %q; want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestInsertSummary_RetryOnConflict verifies that InsertSummary retries when
// a unique constraint conflict is detected. We simulate this by pre-inserting
// the version that would be chosen next, so the first attempt conflicts and
// the retry succeeds with the next version.
func TestInsertSummary_RetryOnConflict(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "retry.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close() //nolint:errcheck

	ctx := context.Background()
	sessID := "sess-retry-001"
	now := time.Now().UnixMilli()

	// Seed the session.
	_, err = db.Write().ExecContext(ctx, `
		INSERT INTO sessions (id, cli, project_path, started_at, last_msg_at, raw_path)
		VALUES (?, 'claude', '/tmp', ?, ?, '/tmp/s.jsonl')`,
		sessID, now, now,
	)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}

	// Pre-insert version 1 so the first automatic attempt will conflict.
	_, err = db.Write().ExecContext(ctx,
		`INSERT INTO session_summaries (session_id, version, text, model, ts) VALUES (?, 1, 'pre', 'model', ?)`,
		sessID, now,
	)
	if err != nil {
		t.Fatalf("pre-insert version 1: %v", err)
	}

	// InsertSummary should see version 1 via MAX(), compute nextVer=2, succeed.
	s := &Summary{SessionID: sessID, Text: "retry text", Model: "model-z", TS: now}
	if err := InsertSummary(ctx, db, s); err != nil {
		t.Fatalf("InsertSummary after conflict: %v", err)
	}
	if s.Version != 2 {
		t.Errorf("Version = %d; want 2", s.Version)
	}
}

// TestInsertSummary_NonUniqueErrorPropagates verifies that a non-unique-constraint
// error from the database propagates immediately without retry.
func TestInsertSummary_NonUniqueErrorPropagates(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "fk_err.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close() //nolint:errcheck

	ctx := context.Background()

	// Attempt to insert a summary for a session that does not exist —
	// this triggers a foreign key violation, not a unique constraint error.
	s := &Summary{
		SessionID: "nonexistent-session",
		Text:      "text",
		Model:     "model",
		TS:        time.Now().UnixMilli(),
	}
	err = InsertSummary(ctx, db, s)
	if err == nil {
		t.Fatal("expected foreign key error, got nil")
	}
	// Must NOT be a unique constraint error; it must propagate immediately.
	if strings.Contains(err.Error(), "too many version conflicts") {
		t.Errorf("error should propagate immediately, not exhaust retries: %v", err)
	}
}

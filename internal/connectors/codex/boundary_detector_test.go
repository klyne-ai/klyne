package codex

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/store"
)

type sessionRow struct {
	ID, ProjectPath, CLI, Status string
	LastMsgAt                    time.Time
}

func newTestDBWithSessions(t *testing.T, rows []sessionRow) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "boundary.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, r := range rows {
		status := r.Status
		if status == "" {
			status = "active"
		}
		if _, err := db.Write().ExecContext(context.Background(),
			`INSERT INTO sessions (id, cli, project_path, encoded_cwd, started_at, last_msg_at,
                msg_count, tokens_in, tokens_out, cached_read_tokens, cached_write_tokens,
                cost_usd, model, status, raw_path)
             VALUES (?, ?, ?, '', ?, ?, 0, 0, 0, 0, 0, 0, '', ?, '')`,
			r.ID, r.CLI, r.ProjectPath, r.LastMsgAt.UnixMilli(), r.LastMsgAt.UnixMilli(), status); err != nil {
			t.Fatalf("seed session %s: %v", r.ID, err)
		}
	}
	return db
}

func TestDetectorFindsIdleCodexSessions(t *testing.T) {
	now := time.Now()
	db := newTestDBWithSessions(t, []sessionRow{
		{ID: "active", ProjectPath: "/p", CLI: "codex", LastMsgAt: now},
		{ID: "idle", ProjectPath: "/p", CLI: "codex", LastMsgAt: now.Add(-45 * time.Minute)},
		{ID: "claude_old", ProjectPath: "/p", CLI: "claude", LastMsgAt: now.Add(-45 * time.Minute)},
	})
	d := NewBoundaryDetector(30 * time.Minute)
	got, err := d.findEligibleSessions(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "idle" {
		t.Errorf("want exactly 'idle', got %v", got)
	}
}

func TestDetectorSkipsAlreadyWrittenSessions(t *testing.T) {
	now := time.Now()
	db := newTestDBWithSessions(t, []sessionRow{
		{ID: "idle1", ProjectPath: "/p", CLI: "codex", LastMsgAt: now.Add(-45 * time.Minute)},
	})
	// Pre-seed a stop_summaries row with a signature for idle1.
	_, err := db.Write().ExecContext(context.Background(),
		`INSERT INTO stop_summaries (session_id, ts, project_path, cli, summary, signature)
         VALUES ('idle1', ?, '/p', 'codex', '', 'pre-existing-sig')`,
		now.UnixMilli())
	if err != nil {
		t.Fatalf("seed stop_summary: %v", err)
	}
	d := NewBoundaryDetector(30 * time.Minute)
	got, err := d.findEligibleSessions(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("idle1 already has a signature row; must be filtered, got %v", got)
	}
}

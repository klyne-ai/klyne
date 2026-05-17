package worklog

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/store"
)

// seedExportEntry inserts a stop_summaries row with given fields directly
// so the export test does not depend on the writer pipeline.
func seedExportEntry(t *testing.T, db *store.DB, project, cli, sessionID string, visible bool, importance int, ts time.Time) {
	t.Helper()
	v := 0
	if visible {
		v = 1
	}
	_, err := db.Write().ExecContext(context.Background(),
		`INSERT INTO stop_summaries (
            session_id, ts, project_path, cli, summary, last_user, last_bash, files_json,
            recap_visible, recap_topic, ai_drafted_summary, draft_state, signature, importance, last_accessed_at
        ) VALUES (?, ?, ?, ?, '', 'last user prompt', '', '[]', ?, '', '', 'proposed', ?, ?, ?)`,
		sessionID, ts.UnixMilli(), project, cli, v, "sig-"+sessionID, importance, ts.UnixMilli())
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func newExportTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "export.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestIsoWeek(t *testing.T) {
	// 2026-05-17 is a Sunday in ISO week 2026-W20.
	got := IsoWeek(time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC))
	if got != "2026-W20" {
		t.Errorf("got %s, want 2026-W20", got)
	}
}

func TestExportWeekWritesOnlyWhenActive(t *testing.T) {
	db := newExportTestDB(t)
	tmp := t.TempDir()
	week := IsoWeek(time.Now())
	seedExportEntry(t, db, "/p", "claude", "s1", true, 7, time.Now())
	seedExportEntry(t, db, "/p", "codex", "s2", true, 8, time.Now())

	res, err := ExportWeek(context.Background(), db, ExportArgs{
		ProjectPath: "/p", Week: week, OutputRoot: tmp,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.FileWritten {
		t.Errorf("expected file written for active project")
	}
	if _, err := os.Stat(filepath.Join(tmp, "docs/worklog", week+".md")); err != nil {
		t.Errorf("expected MD file at %s, got %v", res.Path, err)
	}

	quietRoot := t.TempDir()
	res2, err := ExportWeek(context.Background(), db, ExportArgs{
		ProjectPath: "/q", Week: week, OutputRoot: quietRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res2.FileWritten {
		t.Errorf("quiet project must produce no file")
	}
	if _, err := os.Stat(filepath.Join(quietRoot, "docs/worklog", week+".md")); !os.IsNotExist(err) {
		t.Errorf("expected no file for quiet project, got err=%v", err)
	}
}

func TestExportWeekIncludesBothCLITagsInBody(t *testing.T) {
	db := newExportTestDB(t)
	tmp := t.TempDir()
	week := IsoWeek(time.Now())
	seedExportEntry(t, db, "/p", "claude", "s1", true, 7, time.Now())
	seedExportEntry(t, db, "/p", "codex", "s2", true, 8, time.Now())
	res, err := ExportWeek(context.Background(), db, ExportArgs{
		ProjectPath: "/p", Week: week, OutputRoot: tmp,
	})
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	sBody := string(body)
	for _, tag := range []string{"[claude]", "[codex]"} {
		if !strings.Contains(sBody, tag) {
			t.Errorf("body missing %q\n%s", tag, sBody)
		}
	}
}

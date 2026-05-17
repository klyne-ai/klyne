package worklog

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/store"
)

type fakeLM struct {
	response string
	err      error
}

func (f *fakeLM) Complete(_ context.Context, _ string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.response, nil
}

func newSynthesizerTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "synth.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func seedSynthEntry(t *testing.T, db *store.DB, project, cli, sessionID string, importance int, ts time.Time) {
	t.Helper()
	_, err := db.Write().ExecContext(context.Background(),
		`INSERT INTO stop_summaries (
            session_id, ts, project_path, cli, summary, last_user, last_bash, files_json,
            recap_visible, recap_topic, ai_drafted_summary, draft_state, signature, importance, last_accessed_at
        ) VALUES (?, ?, ?, ?, '', '', '', '[]', 1, '', '', 'proposed', ?, ?, ?)`,
		sessionID, ts.UnixMilli(), project, cli, "sig-"+sessionID, importance, ts.UnixMilli())
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func TestSynthesizerCitesEvidence(t *testing.T) {
	db := newSynthesizerTestDB(t)
	seedSynthEntry(t, db, "/p", "claude", "entry-1", 8, time.Now())
	seedSynthEntry(t, db, "/p", "codex", "entry-2", 7, time.Now())

	lm := &fakeLM{response: `{"insights":[{"text":"User shipped auth refactor","evidence":["entry-1","entry-2"]}]}`}
	refl, err := Synthesize(context.Background(), db, "/p", lm)
	if err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	if len(refl.EvidenceEntryIDs) != 2 {
		t.Errorf("expected 2 evidence entries, got %d (%v)", len(refl.EvidenceEntryIDs), refl.EvidenceEntryIDs)
	}
	if refl.ProjectPath != "/p" {
		t.Errorf("project_path lost on round-trip: %q", refl.ProjectPath)
	}
	if refl.Tier != 2 {
		t.Errorf("expected tier=2 (weekly), got %d", refl.Tier)
	}
}

func TestSynthesizerRejectsEmptyEvidence(t *testing.T) {
	db := newSynthesizerTestDB(t)
	seedSynthEntry(t, db, "/p", "claude", "entry-1", 8, time.Now())

	lm := &fakeLM{response: `{"insights":[{"text":"vague insight","evidence":[]}]}`}
	_, err := Synthesize(context.Background(), db, "/p", lm)
	if err == nil {
		t.Errorf("expected error for empty-evidence insight, got nil")
	}
}

func TestSynthesizerRejectsBadJSON(t *testing.T) {
	db := newSynthesizerTestDB(t)
	seedSynthEntry(t, db, "/p", "claude", "entry-1", 8, time.Now())

	lm := &fakeLM{response: `not json`}
	_, err := Synthesize(context.Background(), db, "/p", lm)
	if err == nil {
		t.Errorf("expected parse error, got nil")
	}
}

func TestSynthesizerStoresRow(t *testing.T) {
	db := newSynthesizerTestDB(t)
	for i := 0; i < 3; i++ {
		seedSynthEntry(t, db, "/p", "claude", fmt.Sprintf("e%d", i), 7, time.Now())
	}
	lm := &fakeLM{response: `{"insights":[{"text":"shipped 3 features","evidence":["e0","e1","e2"]}]}`}
	if _, err := Synthesize(context.Background(), db, "/p", lm); err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	rows, err := store.ListReflectionsForProject(context.Background(), db, "/p", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 stored reflection, got %d", len(rows))
	}
}

package worklog

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/store"
)

func newWriterTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "writer.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestWriteEntrySuppresses(t *testing.T) {
	db := newWriterTestDB(t)
	res, err := WriteEntry(context.Background(), db, Entry{
		SessionID: "s1", TS: time.Now(), ProjectPath: "/p", CLI: "claude",
		WallTime: 30 * time.Second, ToolCallCount: 1,
	}, nil, store.UpsertStopSummaryWithWorklog)
	if err != nil {
		t.Fatal(err)
	}
	if res.RecapVisible != 0 {
		t.Errorf("trivial must be invisible, got visible=%d", res.RecapVisible)
	}
	if res.SuppressedBy == "" {
		t.Errorf("must record reason")
	}
}

func TestWriteEntryPromotes(t *testing.T) {
	db := newWriterTestDB(t)
	res, err := WriteEntry(context.Background(), db, Entry{
		SessionID: "s2", TS: time.Now(), ProjectPath: "/p", CLI: "claude",
		CommitSHA: "abc", WallTime: 5 * time.Minute, ToolCallCount: 30,
		EditWriteCount: 4, Files: []string{"src/auth.go"},
		EventTags: []EventTag{TagCommitLanded, TagFileSignificantlyEdited},
	}, nil, store.UpsertStopSummaryWithWorklog)
	if err != nil {
		t.Fatal(err)
	}
	if res.RecapVisible != 1 {
		t.Errorf("substantive must be visible")
	}
	if res.Importance < 7 {
		t.Errorf("commit must score >= 7, got %d", res.Importance)
	}
	if res.Signature == "" {
		t.Errorf("signature must be set")
	}
}

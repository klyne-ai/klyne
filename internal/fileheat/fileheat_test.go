package fileheat

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/store"
)

func openTestDB(t *testing.T) *store.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "fileheat.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestExtractPaths_Read(t *testing.T) {
	tc := connectors.ToolCall{
		Name:  "Read",
		Input: `{"file_path":"/x/y.go"}`,
	}
	got := extractPaths(tc)
	if len(got) != 1 || got[0] != "/x/y.go" {
		t.Errorf("got %v", got)
	}
}

func TestExtractPaths_UnknownTool(t *testing.T) {
	tc := connectors.ToolCall{
		Name:  "mcp__weird__do_thing",
		Input: `{"arg":"/some/path.txt","note":"hello world"}`,
	}
	got := extractPaths(tc)
	if len(got) != 1 || got[0] != "/some/path.txt" {
		t.Errorf("got %v; want only /some/path.txt", got)
	}
}

func TestExtractPaths_ApplyPatch(t *testing.T) {
	tc := connectors.ToolCall{
		Name:  "apply_patch",
		Input: `{"patch":"*** Update File: foo.go\n@@ -1 +1 @@\n-x\n+y"}`,
	}
	got := extractPaths(tc)
	if len(got) != 1 || got[0] != "<apply_patch diff>" {
		t.Errorf("got %v", got)
	}
}

func TestExtractPaths_BadJSON(t *testing.T) {
	tc := connectors.ToolCall{Name: "Read", Input: "not json"}
	if got := extractPaths(tc); len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestExtractPaths_Empty(t *testing.T) {
	tc := connectors.ToolCall{Name: "Read", Input: ""}
	if got := extractPaths(tc); len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestCompute_AggregatesByPath(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	s := &connectors.Session{
		ID:          "sess-1",
		CLI:         connectors.CLIClaude,
		ProjectPath: "/proj",
		StartedAt:   1_700_000_000_000,
		LastMsgAt:   1_700_000_000_000,
		Status:      connectors.SessionStatusActive,
		Model:       "claude-sonnet-4-5",
		RawPath:     "/tmp/sess.jsonl",
	}
	if err := store.UpsertSession(ctx, db, s); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	mkMsg := func(id string, ts int64, calls []connectors.ToolCall) *connectors.Message {
		return &connectors.Message{
			ID: id, SessionID: s.ID, CLI: connectors.CLIClaude,
			Role: connectors.RoleAssistant, ToolCalls: calls,
			Model: "claude-sonnet-4-5", Ts: ts,
		}
	}
	msgs := []*connectors.Message{
		mkMsg("m1", 1_700_000_000_001, []connectors.ToolCall{
			{ID: "c1", Name: "Read", Input: `{"file_path":"/a.go"}`},
			{ID: "c2", Name: "Read", Input: `{"file_path":"/b.go"}`},
		}),
		mkMsg("m2", 1_700_000_000_002, []connectors.ToolCall{
			{ID: "c3", Name: "Edit", Input: `{"file_path":"/a.go"}`},
		}),
		mkMsg("m3", 1_700_000_000_003, []connectors.ToolCall{
			{ID: "c4", Name: "Write", Input: `{"file_path":"/a.go"}`},
		}),
	}
	for _, m := range msgs {
		if err := store.InsertMessage(ctx, db, m); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	stats, err := Compute(ctx, db, Filter{})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("expected 2 files, got %d", len(stats))
	}
	if stats[0].Path != "/a.go" {
		t.Errorf("top file = %q; want /a.go", stats[0].Path)
	}
	a := stats[0]
	if a.Reads != 1 || a.Edits != 1 || a.Writes != 1 || a.Total != 3 {
		t.Errorf("a counts wrong: %+v", a)
	}
	if a.SessionCount != 1 {
		t.Errorf("a session count = %d; want 1", a.SessionCount)
	}
	if a.LastTouched != 1_700_000_000_003 {
		t.Errorf("LastTouched = %d", a.LastTouched)
	}
}

func TestCompute_ProjectScope(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	for _, proj := range []string{"/p1", "/p2"} {
		if err := store.UpsertSession(ctx, db, &connectors.Session{
			ID: "s-" + proj, CLI: connectors.CLIClaude, ProjectPath: proj,
			StartedAt: 1, LastMsgAt: 1, Status: connectors.SessionStatusActive, RawPath: "/" + proj,
		}); err != nil {
			t.Fatalf("upsert: %v", err)
		}
		if err := store.InsertMessage(ctx, db, &connectors.Message{
			ID: "m-" + proj, SessionID: "s-" + proj, CLI: connectors.CLIClaude,
			Role:      connectors.RoleAssistant,
			ToolCalls: []connectors.ToolCall{{Name: "Read", Input: `{"file_path":"/x"}`}},
			Ts:        1, Model: "claude-sonnet-4-5",
		}); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	stats, err := Compute(ctx, db, Filter{ProjectPath: "/p1"})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(stats) != 1 || stats[0].Reads != 1 {
		t.Errorf("scope filter failed: %+v", stats)
	}
}

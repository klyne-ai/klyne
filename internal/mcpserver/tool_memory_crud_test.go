package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klyne-ai/klyne/internal/config"
)

// withFakeHomeAndConfigDir redirects HOME and pre-creates the ~/.klyne
// directory so store.Open() can land its SQLite file there. store.Open
// does not MkdirAll the parent — that's normally done by config.Save()
// or by the daemon's bootstrap path — so tests must create it themselves.
func withFakeHomeAndConfigDir(t *testing.T) string {
	t.Helper()
	home := withFakeHome(t)
	if err := os.MkdirAll(filepath.Dir(config.DBPath()), 0o700); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	return home
}

// mustRemember writes a memory via HandleRememberMemory and returns its id.
func mustRemember(t *testing.T, in RememberMemoryInput) string {
	t.Helper()
	_, out, err := HandleRememberMemory(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("HandleRememberMemory: %v", err)
	}
	return out.ID
}

func TestHandleUpdateMemory_TextOnly(t *testing.T) {
	withFakeHomeAndConfigDir(t)
	id := mustRemember(t, RememberMemoryInput{
		Text: "original", Scope: MemoryScopeProject, CWD: "/p1",
		Tags: []string{"a", "b"},
	})
	newText := "patched"
	_, out, err := HandleUpdateMemory(context.Background(), nil, UpdateMemoryInput{
		ID: id, Text: &newText,
	})
	if err != nil {
		t.Fatalf("HandleUpdateMemory: %v", err)
	}
	if !out.Updated || out.ID != id {
		t.Errorf("unexpected output: %+v", out)
	}
	// Round-trip via recall.
	_, recall, err := HandleRecallMemory(context.Background(), nil, RecallMemoryInput{CWD: "/p1"})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(recall.ProjectMemories) != 1 {
		t.Fatalf("want 1 project memory, got %d", len(recall.ProjectMemories))
	}
	if recall.ProjectMemories[0].Text != "patched" {
		t.Errorf("text not patched: %q", recall.ProjectMemories[0].Text)
	}
	if len(recall.ProjectMemories[0].Tags) != 2 {
		t.Errorf("tags should be unchanged: %+v", recall.ProjectMemories[0].Tags)
	}
}

func TestHandleUpdateMemory_TagsOnly(t *testing.T) {
	withFakeHomeAndConfigDir(t)
	id := mustRemember(t, RememberMemoryInput{
		Text: "k", Scope: MemoryScopeProject, CWD: "/p2", Tags: []string{"old"},
	})
	newTags := []string{"new1", "new2"}
	_, _, err := HandleUpdateMemory(context.Background(), nil, UpdateMemoryInput{
		ID: id, Tags: &newTags,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	_, recall, err := HandleRecallMemory(context.Background(), nil, RecallMemoryInput{CWD: "/p2"})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	got := recall.ProjectMemories[0]
	if got.Text != "k" {
		t.Errorf("text should be unchanged: %q", got.Text)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "new1" || got.Tags[1] != "new2" {
		t.Errorf("tags not updated: %+v", got.Tags)
	}
}

func TestHandleUpdateMemory_Both(t *testing.T) {
	withFakeHomeAndConfigDir(t)
	id := mustRemember(t, RememberMemoryInput{
		Text: "a", Scope: MemoryScopeProject, CWD: "/p3", Tags: []string{"x"},
	})
	newText := "b"
	newTags := []string{"y", "z"}
	_, _, err := HandleUpdateMemory(context.Background(), nil, UpdateMemoryInput{
		ID: id, Text: &newText, Tags: &newTags,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	_, recall, err := HandleRecallMemory(context.Background(), nil, RecallMemoryInput{CWD: "/p3"})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	got := recall.ProjectMemories[0]
	if got.Text != "b" || len(got.Tags) != 2 || got.Tags[0] != "y" {
		t.Errorf("update both failed: %+v", got)
	}
}

func TestHandleUpdateMemory_NoFieldsIsError(t *testing.T) {
	withFakeHomeAndConfigDir(t)
	id := mustRemember(t, RememberMemoryInput{
		Text: "a", Scope: MemoryScopeProject, CWD: "/p4",
	})
	_, _, err := HandleUpdateMemory(context.Background(), nil, UpdateMemoryInput{ID: id})
	if err == nil {
		t.Fatal("expected error when both text and tags are omitted")
	}
}

func TestHandleUpdateMemory_UnknownID(t *testing.T) {
	withFakeHomeAndConfigDir(t)
	newText := "x"
	_, _, err := HandleUpdateMemory(context.Background(), nil, UpdateMemoryInput{
		ID: "does-not-exist", Text: &newText,
	})
	if err == nil {
		t.Fatal("expected not-found error for unknown id")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention not found: %v", err)
	}
}

func TestHandleDeleteMemory(t *testing.T) {
	withFakeHomeAndConfigDir(t)
	id := mustRemember(t, RememberMemoryInput{
		Text: "doomed", Scope: MemoryScopeProject, CWD: "/p5",
	})
	_, out, err := HandleDeleteMemory(context.Background(), nil, DeleteMemoryInput{ID: id})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !out.Deleted || out.ID != id {
		t.Errorf("unexpected output: %+v", out)
	}
	_, recall, err := HandleRecallMemory(context.Background(), nil, RecallMemoryInput{CWD: "/p5"})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(recall.ProjectMemories) != 0 {
		t.Errorf("expected empty after delete, got %+v", recall.ProjectMemories)
	}
}

func TestHandleDeleteMemory_UnknownID(t *testing.T) {
	withFakeHomeAndConfigDir(t)
	_, _, err := HandleDeleteMemory(context.Background(), nil, DeleteMemoryInput{ID: "nope"})
	if err == nil {
		t.Fatal("expected error for unknown id")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention not found: %v", err)
	}
}

func TestHandleListMemories_ProjectAndGlobalWithNames(t *testing.T) {
	withFakeHomeAndConfigDir(t)
	// Two project memories under /p6 + one global.
	mustRemember(t, RememberMemoryInput{
		Text: "First project memory\nsecond line not in name",
		Scope: MemoryScopeProject, CWD: "/p6",
	})
	mustRemember(t, RememberMemoryInput{
		Text: "Second project memory",
		Scope: MemoryScopeProject, CWD: "/p6",
	})
	mustRemember(t, RememberMemoryInput{
		Text: "Global runbook", Scope: MemoryScopeGlobal,
	})

	_, out, err := HandleListMemories(context.Background(), nil, ListMemoriesInput{CWD: "/p6"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(out.ProjectMemories) != 2 {
		t.Fatalf("want 2 project memories, got %d", len(out.ProjectMemories))
	}
	if len(out.GlobalMemories) != 1 {
		t.Fatalf("want 1 global memory, got %d", len(out.GlobalMemories))
	}
	if out.Total != 3 {
		t.Errorf("Total = %d, want 3", out.Total)
	}
	// Names: first non-empty line, truncated to 60 runes. Order
	// between same-millisecond inserts is not stable, so check by
	// set membership rather than position.
	names := map[string]bool{}
	for _, m := range out.ProjectMemories {
		names[m.Name] = true
	}
	if !names["First project memory"] {
		t.Errorf("missing first project name; got %+v", names)
	}
	if !names["Second project memory"] {
		t.Errorf("missing second project name; got %+v", names)
	}
	// Confirm the multi-line memory only kept its first line as Name.
	for _, m := range out.ProjectMemories {
		if strings.Contains(m.Name, "\n") {
			t.Errorf("Name should be first line only: %q", m.Name)
		}
	}
	if got := out.GlobalMemories[0].Name; got != "Global runbook" {
		t.Errorf("global name = %q", got)
	}
}

func TestHandleListMemories_ScopeProjectOnly(t *testing.T) {
	withFakeHomeAndConfigDir(t)
	mustRemember(t, RememberMemoryInput{Text: "p", Scope: MemoryScopeProject, CWD: "/p7"})
	mustRemember(t, RememberMemoryInput{Text: "g", Scope: MemoryScopeGlobal})

	_, out, err := HandleListMemories(context.Background(), nil, ListMemoriesInput{
		CWD: "/p7", Scope: string(MemoryScopeProject),
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(out.ProjectMemories) != 1 {
		t.Errorf("want 1 project, got %d", len(out.ProjectMemories))
	}
	if len(out.GlobalMemories) != 0 {
		t.Errorf("want 0 global, got %d", len(out.GlobalMemories))
	}
}

func TestHandleListMemories_ScopeGlobalOnly(t *testing.T) {
	withFakeHomeAndConfigDir(t)
	mustRemember(t, RememberMemoryInput{Text: "p", Scope: MemoryScopeProject, CWD: "/p8"})
	mustRemember(t, RememberMemoryInput{Text: "g", Scope: MemoryScopeGlobal})

	_, out, err := HandleListMemories(context.Background(), nil, ListMemoriesInput{
		CWD: "/p8", Scope: string(MemoryScopeGlobal),
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(out.ProjectMemories) != 0 {
		t.Errorf("want 0 project, got %d", len(out.ProjectMemories))
	}
	if len(out.GlobalMemories) != 1 {
		t.Errorf("want 1 global, got %d", len(out.GlobalMemories))
	}
}

func TestDeriveMemoryName_TruncatesAtRunes(t *testing.T) {
	long := strings.Repeat("a", 70)
	got := deriveMemoryName(long)
	// 60 runes + ellipsis.
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected ellipsis: %q", got)
	}
	if got != strings.Repeat("a", 60)+"…" {
		t.Errorf("unexpected: %q", got)
	}
}

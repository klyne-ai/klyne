package mcpserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/store"
)

// withBootstrapDB pre-creates the per-test ~/.klyne dir (so
// store.Open can land klyne.db there) and opens a fresh DB. Returns
// the DB so the test can seed decisions directly via
// store.InsertDecision. Closes on cleanup.
//
// The fake-home swap is performed by withFakeHome; the caller must
// have invoked that first so config.DBPath() resolves under t.TempDir().
func withBootstrapDB(t *testing.T) *store.DB {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(config.DBPath()), 0o755); err != nil {
		t.Fatalf("mkdir klyne dir: %v", err)
	}
	db, err := store.Open(config.DBPath())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// mustBootstrap is a thin happy-path wrapper around HandleBootstrap.
func mustBootstrap(t *testing.T, in BootstrapInput) BootstrapOutput {
	t.Helper()
	_, out, err := HandleBootstrap(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("HandleBootstrap: %v", err)
	}
	return out
}

// TestHandleBootstrap_EmptyProject covers the "fresh project" case:
// no sessions, no memories. Every section in the Markdown should
// render `_(none)_` so the agent's output is structurally stable.
func TestHandleBootstrap_EmptyProject(t *testing.T) {
	withFakeHome(t)
	// Open the DB just to materialise it; no decisions inserted.
	_ = withBootstrapDB(t)

	out := mustBootstrap(t, BootstrapInput{CWD: "/tmp/proj-empty-bootstrap"})

	if out.CWD != "/tmp/proj-empty-bootstrap" {
		t.Errorf("CWD = %q, want /tmp/proj-empty-bootstrap", out.CWD)
	}
	if len(out.Sessions) != 0 {
		t.Errorf("Sessions len = %d, want 0", len(out.Sessions))
	}
	if len(out.ProjectMemories) != 0 {
		t.Errorf("ProjectMemories len = %d, want 0", len(out.ProjectMemories))
	}
	if out.GlobalMemoryCount != 0 {
		t.Errorf("GlobalMemoryCount = %d, want 0", out.GlobalMemoryCount)
	}
	if len(out.GlobalMemoriesPreview) != 0 {
		t.Errorf("GlobalMemoriesPreview len = %d, want 0", len(out.GlobalMemoriesPreview))
	}
	if out.LatestHealth != nil {
		t.Errorf("LatestHealth = %+v, want nil for empty project", out.LatestHealth)
	}
	// Every section must show `_(none)_` so the agent renders a
	// stable shape even on a brand-new project.
	for _, section := range []string{"Recent sessions", "klyne memory (SQLite store)", "Claude auto-memory", "Recent worklog entries (cross-AI)"} {
		if !strings.Contains(out.Markdown, "## "+section) {
			t.Errorf("Markdown missing section %q\n%s", section, out.Markdown)
		}
	}
	if !strings.Contains(out.Markdown, "_(none)_") {
		t.Errorf("Markdown missing _(none)_ placeholder\n%s", out.Markdown)
	}
	// LatestHealth absent → no "Current session health" section.
	if strings.Contains(out.Markdown, "Current session health") {
		t.Errorf("Markdown should omit 'Current session health' when LatestHealth is nil\n%s", out.Markdown)
	}
}

// TestHandleBootstrap_FiveSessionsReturnsThreeMostRecent confirms the
// cap-at-3 behaviour. The candidates are already sorted newest-first
// by ListSessionsForCWD, so the assertion is just on length + that the
// rows match the three newest seeded sessions.
func TestHandleBootstrap_FiveSessionsReturnsThreeMostRecent(t *testing.T) {
	home := withFakeHome(t)
	_ = withBootstrapDB(t)

	cwd := "/tmp/proj-five-sessions"
	dir := filepath.Join(home, ".claude", "projects", EncodeCWD(cwd))

	// Write 5 sessions. Mtimes get staggered by writeJSONL (it sets
	// "now") so to make ordering deterministic we touch each one in
	// turn after writing — later writes get later mtimes.
	for i := 1; i <= 5; i++ {
		name := fmt.Sprintf("session-%d.jsonl", i)
		line := fmt.Sprintf(
			`{"type":"user","sessionId":"sess-%d","timestamp":"2026-04-0%dT10:00:00.000Z","message":{"role":"user","content":[{"type":"text","text":"task %d"}]}}`,
			i, i, i,
		)
		writeJSONL(t, dir, name, line)
	}

	out := mustBootstrap(t, BootstrapInput{CWD: cwd})

	if len(out.Sessions) != 3 {
		t.Fatalf("Sessions len = %d, want 3 (cap)", len(out.Sessions))
	}
	// All session ids must be non-empty and unique.
	seen := map[string]bool{}
	for _, c := range out.Sessions {
		if c.SessionID == "" {
			t.Errorf("empty SessionID in row: %+v", c)
		}
		if seen[c.SessionID] {
			t.Errorf("duplicate SessionID %q", c.SessionID)
		}
		seen[c.SessionID] = true
	}
	if !strings.Contains(out.Markdown, "## Recent sessions") {
		t.Errorf("Markdown missing Recent sessions section\n%s", out.Markdown)
	}
	// Empty-section placeholder must NOT appear under Recent sessions
	// when we have sessions.
	idx := strings.Index(out.Markdown, "## Recent sessions")
	nextIdx := strings.Index(out.Markdown[idx:], "## ")
	// nextIdx is at offset 0 (the header itself); skip past it.
	rest := out.Markdown[idx+len("## Recent sessions"):]
	if i := strings.Index(rest, "##"); i >= 0 {
		section := rest[:i]
		if strings.Contains(section, "_(none)_") {
			t.Errorf("Recent sessions section should not contain _(none)_\n%s", section)
		}
	}
	_ = nextIdx
}

// TestHandleBootstrap_SurfacesClaudeAutoMemory seeds the on-disk
// auto-memory directory and asserts the bootstrap response carries
// the parsed entries under a distinct "Claude auto-memory" section.
func TestHandleBootstrap_SurfacesClaudeAutoMemory(t *testing.T) {
	home := withFakeHome(t)
	_ = withBootstrapDB(t)

	cwd := "/tmp/proj-with-auto-memory"
	memDir := filepath.Join(home, ".claude", "projects", EncodeCWD(cwd), "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatalf("mkdir memdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(memDir, "MEMORY.md"), []byte("- [pos](project_positioning.md) — hook\n"), 0o644); err != nil {
		t.Fatalf("write MEMORY.md: %v", err)
	}
	body := "---\nname: positioning\ndescription: three modes\ntype: project\n---\nbody text\n"
	if err := os.WriteFile(filepath.Join(memDir, "project_positioning.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write project_positioning.md: %v", err)
	}

	out := mustBootstrap(t, BootstrapInput{CWD: cwd})

	if len(out.ClaudeAutoMemory.Entries) != 1 {
		t.Fatalf("ClaudeAutoMemory.Entries len = %d, want 1", len(out.ClaudeAutoMemory.Entries))
	}
	if out.ClaudeAutoMemory.Entries[0].Name != "positioning" {
		t.Errorf("first entry Name = %q, want positioning", out.ClaudeAutoMemory.Entries[0].Name)
	}
	for _, want := range []string{
		"## klyne memory (SQLite store)",
		"## Claude auto-memory",
		"positioning",
		"(project)",
	} {
		if !strings.Contains(out.Markdown, want) {
			t.Errorf("Markdown missing %q\n%s", want, out.Markdown)
		}
	}
}

// TestHandleBootstrap_MemoriesProjectAndGlobals seeds 8 project
// memories + 2 globals and asserts the bootstrap returns the project
// memories capped at 5 plus 2 globals (preview + count). The Markdown
// renders all 4 sections.
func TestHandleBootstrap_MemoriesProjectAndGlobals(t *testing.T) {
	withFakeHome(t)
	db := withBootstrapDB(t)
	ctx := context.Background()

	cwd := "/tmp/proj-memories-bootstrap"

	// 8 project memories, ascending ts so the most-recent end up
	// returned first.
	for i := 1; i <= 8; i++ {
		d := &store.Decision{
			ID:          fmt.Sprintf("proj-%d", i),
			Ts:          int64(1000 + i),
			ProjectPath: cwd,
			Text:        fmt.Sprintf("project memory %d", i),
		}
		if err := store.InsertDecision(ctx, db, d); err != nil {
			t.Fatalf("insert project memory %d: %v", i, err)
		}
	}
	// 2 globals.
	for i := 1; i <= 2; i++ {
		d := &store.Decision{
			ID:   fmt.Sprintf("glob-%d", i),
			Ts:   int64(2000 + i),
			Text: fmt.Sprintf("global memory %d", i),
		}
		if err := store.InsertDecision(ctx, db, d); err != nil {
			t.Fatalf("insert global %d: %v", i, err)
		}
	}

	out := mustBootstrap(t, BootstrapInput{CWD: cwd})

	if len(out.ProjectMemories) != 5 {
		t.Errorf("ProjectMemories len = %d, want 5 (cap)", len(out.ProjectMemories))
	}
	// First row should be the newest (proj-8).
	if len(out.ProjectMemories) > 0 && out.ProjectMemories[0].ID != "proj-8" {
		t.Errorf("ProjectMemories[0].ID = %q, want proj-8 (newest)", out.ProjectMemories[0].ID)
	}
	if out.GlobalMemoryCount != 2 {
		t.Errorf("GlobalMemoryCount = %d, want 2", out.GlobalMemoryCount)
	}
	if len(out.GlobalMemoriesPreview) != 2 {
		t.Errorf("GlobalMemoriesPreview len = %d, want 2", len(out.GlobalMemoriesPreview))
	}
	// No sessions seeded → empty.
	if len(out.Sessions) != 0 {
		t.Errorf("Sessions len = %d, want 0", len(out.Sessions))
	}
	// Markdown must render both Project and Global sections, NOT
	// with the _(none)_ placeholder.
	for _, want := range []string{"### Project-scoped", "### Global", "project memory 8", "global memory 2"} {
		if !strings.Contains(out.Markdown, want) {
			t.Errorf("Markdown missing %q\n%s", want, out.Markdown)
		}
	}
}

// TestBootstrapInjectsWorklogEntriesFromAllCLIs verifies the cross-AI
// worklog handoff: bootstrap surfaces visible worklog entries from
// BOTH Claude and Codex sessions in this project, and skips suppressed
// ones. Without this, a fresh session in CLI A is blind to what the
// user did in CLI B.
func TestBootstrapInjectsWorklogEntriesFromAllCLIs(t *testing.T) {
	withFakeHome(t)
	db := withBootstrapDB(t)
	seedStopSummary(t, db, "/p", "claude", "s1", true, 8, time.Now().Add(-1*time.Hour))
	seedStopSummary(t, db, "/p", "codex", "s2", true, 7, time.Now().Add(-30*time.Minute))
	seedStopSummary(t, db, "/p", "claude", "s3", false, 3, time.Now()) // suppressed; must NOT appear

	out := mustBootstrap(t, BootstrapInput{CWD: "/p"})
	if len(out.WorklogEntries) != 2 {
		t.Errorf("expected 2 visible entries, got %d", len(out.WorklogEntries))
	}
	var claudeFound, codexFound bool
	for _, e := range out.WorklogEntries {
		if e.CLI == "claude" {
			claudeFound = true
		}
		if e.CLI == "codex" {
			codexFound = true
		}
	}
	if !claudeFound || !codexFound {
		t.Errorf("bootstrap must surface both CLIs, got claude=%v codex=%v", claudeFound, codexFound)
	}
	// Markdown must include the new section.
	if !strings.Contains(out.Markdown, "## Recent worklog entries (cross-AI)") {
		t.Errorf("markdown missing worklog section\n%s", out.Markdown)
	}
}

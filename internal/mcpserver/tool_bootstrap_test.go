package mcpserver

import (
	"context"
	"fmt"
	"os"
	"os/exec"
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
	db, err := store.Open(context.Background(), config.DBPath())
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
	for _, section := range []string{"Recent sessions", "klyne memory (SQLite store)", "Claude auto-memory", "Recent reflections", "Recent worklog entries (cross-AI)"} {
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
	// No entries → no reflection-due advisory.
	if out.ReflectionDue {
		t.Errorf("ReflectionDue must be false on empty project")
	}
	if strings.Contains(out.Markdown, "Reflection due") {
		t.Errorf("Markdown must NOT include the Reflection-due advisory on an empty project\n%s", out.Markdown)
	}
}

// TestBootstrapSurfacesReflectionDueAdvisory seeds enough high-importance
// entries to cross the default 150 threshold; bootstrap must surface
// both the structured ReflectionDue flag AND the markdown advisory.
func TestBootstrapSurfacesReflectionDueAdvisory(t *testing.T) {
	withFakeHome(t)
	db := withBootstrapDB(t)
	// Three entries scoring 60 each → 180 ≥ 150 → trigger fires.
	seedStopSummary(t, db, "/p", "claude", "e1", true, 60, time.Now().Add(-3*time.Hour))
	seedStopSummary(t, db, "/p", "claude", "e2", true, 60, time.Now().Add(-2*time.Hour))
	seedStopSummary(t, db, "/p", "codex", "e3", true, 60, time.Now().Add(-1*time.Hour))

	out := mustBootstrap(t, BootstrapInput{CWD: "/p"})
	if !out.ReflectionDue {
		t.Errorf("ReflectionDue must be true when importance-sum exceeds threshold")
	}
	if !strings.Contains(out.Markdown, "Reflection due") {
		t.Errorf("Markdown must include the Reflection-due advisory\n%s", out.Markdown)
	}
	if !strings.Contains(out.Markdown, "/klyne:reflect") {
		t.Errorf("Markdown must mention the /klyne:reflect slash command\n%s", out.Markdown)
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

	// Write 5 sessions, all backdated outside the 30s active window so
	// the bootstrap active-session filter doesn't strip them. Mtimes
	// are staggered (older index = older mtime) so newest-first ordering
	// is deterministic.
	base := time.Now().Add(-2 * time.Hour)
	for i := 1; i <= 5; i++ {
		name := fmt.Sprintf("session-%d.jsonl", i)
		line := fmt.Sprintf(
			`{"type":"user","sessionId":"sess-%d","timestamp":"2026-04-0%dT10:00:00.000Z","message":{"role":"user","content":[{"type":"text","text":"task %d"}]}}`,
			i, i, i,
		)
		p := writeJSONL(t, dir, name, line)
		mtime := base.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatalf("chtimes %s: %v", name, err)
		}
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

func TestBootstrapInjectsLatestReflection(t *testing.T) {
	withFakeHome(t)
	db := withBootstrapDB(t)
	seedStopSummary(t, db, "/p", "claude", "s1", true, 8, time.Now())
	seedReflection(t, db, "/p", []string{"s1"}, "Weekly: shipped auth refactor")

	out := mustBootstrap(t, BootstrapInput{CWD: "/p"})
	if len(out.Reflections) == 0 {
		t.Errorf("bootstrap must surface latest reflection")
	}
	if !strings.Contains(out.Markdown, "## Recent reflections") {
		t.Errorf("markdown missing recent reflections section\n%s", out.Markdown)
	}
	if !strings.Contains(out.Markdown, "shipped auth refactor") {
		t.Errorf("markdown should include reflection title:\n%s", out.Markdown)
	}
}

// TestBootstrapExcludesActiveSessionsFromRecentList verifies that
// HandleBootstrap filters out IsActive==true sessions from out.Sessions.
// The calling session is almost always the freshest (mod-time within the
// 30s active window), so it would otherwise dominate the "Recent
// sessions" list — redundant information, since the agent is already in
// that session. Bootstrap exists to surface context the agent doesn't
// already have.
func TestBootstrapExcludesActiveSessionsFromRecentList(t *testing.T) {
	home := withFakeHome(t)
	_ = withBootstrapDB(t)

	cwd := filepath.Join(home, "proj")
	dir := filepath.Join(home, ".claude", "projects", EncodeCWD(cwd))

	// Active session: writeJSONL stamps mtime = now, so this lands
	// inside the 30s active window.
	activeLine := `{"type":"user","sessionId":"active-now","timestamp":"2026-05-17T10:00:00.000Z","message":{"role":"user","content":[{"type":"text","text":"active work"}]}}`
	writeJSONL(t, dir, "active-now.jsonl", activeLine)

	// Inactive session: write it, then backdate the mtime so it falls
	// well outside the 30s active window.
	inactiveLine := `{"type":"user","sessionId":"old-session","timestamp":"2026-05-17T08:00:00.000Z","message":{"role":"user","content":[{"type":"text","text":"older work"}]}}`
	oldPath := writeJSONL(t, dir, "old-session.jsonl", inactiveLine)
	twoHoursAgo := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(oldPath, twoHoursAgo, twoHoursAgo); err != nil {
		t.Fatalf("chtimes old-session: %v", err)
	}

	out := mustBootstrap(t, BootstrapInput{CWD: cwd})

	if len(out.Sessions) != 1 {
		t.Fatalf("Sessions len = %d, want 1 (active session must be filtered)", len(out.Sessions))
	}
	got := out.Sessions[0]
	if got.SessionID != "old-session" {
		t.Errorf("Sessions[0].SessionID = %q, want old-session", got.SessionID)
	}
	if got.IsActive {
		t.Errorf("Sessions[0].IsActive = true, want false (no row should be active after fix)")
	}
	for _, s := range out.Sessions {
		if s.SessionID == "active-now" {
			t.Errorf("active-now leaked into Sessions list: %+v", s)
		}
		if s.IsActive {
			t.Errorf("active row in Sessions: %+v", s)
		}
	}
}

// TestHandleBootstrap_WorktreeSharesProjectMemory locks in the
// worktree-canonicalization fix: a memory seeded under the main repo
// path must surface when bootstrap is invoked from a worktree of the
// same repo. Both checkouts are the same logical project.
func TestHandleBootstrap_WorktreeSharesProjectMemory(t *testing.T) {
	withFakeHome(t)
	db := withBootstrapDB(t)

	// Set up a main repo with one commit, then add a worktree.
	main := t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git -C %s %v: %v\n%s", dir, args, err, out)
		}
	}
	run(main, "init")
	if err := os.WriteFile(filepath.Join(main, "README"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(main, "add", "README")
	run(main, "commit", "-m", "init")
	wt := t.TempDir() + "/wt"
	run(main, "worktree", "add", wt)

	// Seed a memory under the CANONICAL main-repo path (matching what
	// HandleRememberMemory would produce when invoked from the main
	// checkout). EvalSymlinks because macOS /var → /private/var.
	mainCanonical, _ := filepath.EvalSymlinks(main)
	if err := store.InsertDecision(context.Background(), db, &store.Decision{
		ID:          "d-worktree-test",
		Ts:          time.Now().UnixMilli(),
		ProjectPath: mainCanonical,
		Text:        "shared across worktrees",
	}); err != nil {
		t.Fatalf("seed memory: %v", err)
	}

	// Bootstrap from the WORKTREE — the canonical resolver should
	// resolve worktree → main, and the memory should surface.
	out := mustBootstrap(t, BootstrapInput{CWD: wt})
	if len(out.ProjectMemories) != 1 {
		t.Fatalf("ProjectMemories len = %d, want 1 (worktree must see main repo's memory). out=%+v", len(out.ProjectMemories), out)
	}
	if out.ProjectMemories[0].Text != "shared across worktrees" {
		t.Errorf("ProjectMemories[0].Text = %q, want %q", out.ProjectMemories[0].Text, "shared across worktrees")
	}
	// Literal cwd preserved for display:
	if out.CWD != wt {
		t.Errorf("out.CWD = %q, want literal worktree path %q (display field)", out.CWD, wt)
	}
}

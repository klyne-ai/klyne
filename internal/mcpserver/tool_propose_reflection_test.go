package mcpserver

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHandleProposeReflection_ReturnsPendingEntries(t *testing.T) {
	withFakeHome(t)
	db := withBootstrapDB(t)
	seedStopSummary(t, db, "/p", "claude", "s1", true, 8, time.Now().Add(-1*time.Hour))
	seedStopSummary(t, db, "/p", "codex", "s2", true, 7, time.Now().Add(-30*time.Minute))
	// Suppressed entry must NOT appear in the proposal.
	seedStopSummary(t, db, "/p", "claude", "s3", false, 9, time.Now())

	out, err := handleProposeReflection(context.Background(), db, ProposeReflectionInput{ProjectPath: "/p"})
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if len(out.Entries) != 2 {
		t.Errorf("expected 2 pending entries (visible only), got %d", len(out.Entries))
	}
	if out.ProjectPath != "/p" {
		t.Errorf("project_path lost: %q", out.ProjectPath)
	}
	// Reason should classify: 8 + 7 = 15, below default 150 → user-invoked
	// (unless the CI clock falls on Sunday evening, which we tolerate).
	if !strings.Contains(out.Markdown, "Reflection proposal for /p") {
		t.Errorf("markdown missing header: %s", out.Markdown)
	}
	if !strings.Contains(out.Markdown, "[claude]") || !strings.Contains(out.Markdown, "[codex]") {
		t.Errorf("markdown should tag both CLIs: %s", out.Markdown)
	}
}

func TestHandleProposeReflection_ImportanceSumReason(t *testing.T) {
	withFakeHome(t)
	db := withBootstrapDB(t)
	// Three entries scoring 60 each → sum 180 ≥ 150 → importance-sum.
	seedStopSummary(t, db, "/p", "claude", "e1", true, 60, time.Now().Add(-3*time.Hour))
	seedStopSummary(t, db, "/p", "claude", "e2", true, 60, time.Now().Add(-2*time.Hour))
	seedStopSummary(t, db, "/p", "codex", "e3", true, 60, time.Now().Add(-1*time.Hour))

	out, err := handleProposeReflection(context.Background(), db, ProposeReflectionInput{ProjectPath: "/p"})
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if !strings.HasPrefix(out.Reason, "importance-sum") {
		t.Errorf("expected importance-sum reason, got %q", out.Reason)
	}
	if !strings.Contains(out.Markdown, "importance-sum") {
		t.Errorf("markdown should surface trigger reason: %s", out.Markdown)
	}
}

func TestHandleProposeReflection_CanonicalizesWorktreePath(t *testing.T) {
	// Entries seeded under the main repo's canonical path must still
	// surface when the MCP caller passes a worktree cwd as project_path
	// — otherwise /klyne:reflect from a worktree returns "nothing
	// pending" even though the canonical repo has entries.
	withFakeHome(t)
	db := withBootstrapDB(t)
	main, wt := newRepoWithWorktreeForMCP(t)
	mainAbs, _ := filepath.EvalSymlinks(main)
	seedStopSummary(t, db, mainAbs, "claude", "s1", true, 8, time.Now().Add(-1*time.Hour))
	seedStopSummary(t, db, mainAbs, "codex", "s2", true, 7, time.Now().Add(-30*time.Minute))

	out, err := handleProposeReflection(context.Background(), db, ProposeReflectionInput{ProjectPath: wt})
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if len(out.Entries) != 2 {
		t.Errorf("expected 2 pending entries via worktree→canonical rollup, got %d", len(out.Entries))
	}
}

// newRepoWithWorktreeForMCP creates a main git repo with one commit
// plus a linked worktree, returning both absolute paths. Local copy of
// the worklog-package helper to keep the test package self-contained.
func newRepoWithWorktreeForMCP(t *testing.T) (mainRepo, worktree string) {
	t.Helper()
	main := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", main}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	if err := os.WriteFile(filepath.Join(main, "README"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "README")
	run("commit", "-m", "init")
	wt := t.TempDir() + "/wt"
	cmd := exec.Command("git", "-C", main, "worktree", "add", wt)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("worktree add: %v\n%s", err, out)
	}
	return main, wt
}

func TestHandleProposeReflection_EmptyProject(t *testing.T) {
	withFakeHome(t)
	db := withBootstrapDB(t)

	out, err := handleProposeReflection(context.Background(), db, ProposeReflectionInput{ProjectPath: "/p"})
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if len(out.Entries) != 0 {
		t.Errorf("expected 0 entries on empty project, got %d", len(out.Entries))
	}
	if !strings.Contains(out.Markdown, "Nothing to synthesize") {
		t.Errorf("markdown should explain empty case: %s", out.Markdown)
	}
}

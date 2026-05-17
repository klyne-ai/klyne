package mcpserver

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCanonicalProjectPath_NonGitDir(t *testing.T) {
	tmp := t.TempDir()
	got := CanonicalProjectPath(tmp)
	if got != tmp {
		t.Errorf("got %q, want %q (non-git dir must return cwd)", got, tmp)
	}
}

func TestCanonicalProjectPath_RegularRepo(t *testing.T) {
	repo := t.TempDir()
	cmd := exec.Command("git", "-C", repo, "init")
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}
	got := CanonicalProjectPath(repo)
	// EvalSymlinks because macOS /var → /private/var.
	wantAbs, _ := filepath.EvalSymlinks(repo)
	gotAbs, _ := filepath.EvalSymlinks(got)
	if gotAbs != wantAbs {
		t.Errorf("got %q, want %q", gotAbs, wantAbs)
	}
}

func TestCanonicalProjectPath_Worktree(t *testing.T) {
	// Create a main repo with one commit, then add a worktree.
	// Assert CanonicalProjectPath(worktree) == main_repo path.
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

	got := CanonicalProjectPath(wt)
	wantAbs, _ := filepath.EvalSymlinks(main)
	gotAbs, _ := filepath.EvalSymlinks(got)
	if gotAbs != wantAbs {
		t.Errorf("worktree canonical = %q, want main %q", gotAbs, wantAbs)
	}
}

func TestCanonicalProjectPath_EmptyCWD(t *testing.T) {
	if got := CanonicalProjectPath(""); got != "" {
		t.Errorf("empty cwd should return empty, got %q", got)
	}
}

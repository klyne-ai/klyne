package mcpserver

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initRepo creates a tmp git repo with one committed file and one
// unstaged modification, returning the repo root. Skips the test
// when git isn't on PATH.
func initRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	clean := filepath.Join(dir, "clean.txt")
	dirty := filepath.Join(dir, "dirty.txt")
	if err := os.WriteFile(clean, []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dirty, []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "init")
	if err := os.WriteFile(dirty, []byte("a\nmodified\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCaptureGitDirty_ReportsModifiedFiles(t *testing.T) {
	repo := initRepo(t)
	set, ok := captureGitDirty(repo)
	if !ok {
		t.Fatal("captureGitDirty failed unexpectedly")
	}
	dirty := filepath.Join(repo, "dirty.txt")
	clean := filepath.Join(repo, "clean.txt")
	if !set[dirty] {
		t.Errorf("expected %q in dirty set, got %v", dirty, set)
	}
	if set[clean] {
		t.Errorf("did not expect %q in dirty set, got %v", clean, set)
	}
}

func TestCaptureGitDirty_NotARepoReturnsOkFalse(t *testing.T) {
	dir := t.TempDir() // empty, no git init
	_, ok := captureGitDirty(dir)
	if ok {
		t.Fatal("expected ok=false for non-repo dir")
	}
}

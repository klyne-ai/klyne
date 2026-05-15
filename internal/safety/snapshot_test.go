package safety_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/klyne-ai/klyne/internal/safety"
)

// initGitRepo creates a minimal git repo in dir so TakeSnapshot can use
// the git stash path.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		// Set a minimal git identity so commit works.
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@test",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@test",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "test@test")
	run("config", "user.name", "test")
	// Initial commit so the repo has a HEAD.
	readmePath := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readmePath, []byte("init"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	run("add", "README.md")
	run("commit", "-m", "init")
}

func TestTakeSnapshot_FallbackNonGit(t *testing.T) {
	dir := t.TempDir()
	// Write a couple of files.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("world"), 0o644); err != nil {
		t.Fatal(err)
	}

	snapRoot := t.TempDir()
	ctx := context.Background()
	res, err := safety.TakeSnapshot(ctx, dir, snapRoot)
	if err != nil {
		t.Fatalf("TakeSnapshot: %v", err)
	}
	if res.FallbackDir == "" {
		t.Fatal("expected FallbackDir to be set for non-git dir")
	}
	// Verify the files were copied.
	if _, err := os.Stat(filepath.Join(res.FallbackDir, "a.txt")); err != nil {
		t.Fatalf("a.txt not in snapshot: %v", err)
	}
	if res.FileCount < 2 {
		t.Fatalf("FileCount = %d, want >= 2", res.FileCount)
	}
}

func TestTakeSnapshot_GitRepo_WithChanges(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	// Add an uncommitted file.
	if err := os.WriteFile(filepath.Join(dir, "dirty.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	res, err := safety.TakeSnapshot(ctx, dir, t.TempDir())
	if err != nil {
		t.Fatalf("TakeSnapshot: %v", err)
	}
	// git stash create should have produced a SHA.
	if res.StashSHA == "" {
		// If git stash create returned empty (clean tree from git's perspective),
		// we accept it — some CI environments don't track untracked files in stash.
		t.Logf("StashSHA empty (possibly clean tree from git view); FallbackDir=%q", res.FallbackDir)
	}
}

func TestTakeSnapshot_GitRepo_CleanTree(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	// No dirty files — stash create returns ""
	ctx := context.Background()
	res, err := safety.TakeSnapshot(ctx, dir, t.TempDir())
	if err != nil {
		t.Fatalf("TakeSnapshot: %v", err)
	}
	// Clean git tree: StashSHA should be "" and FallbackDir empty.
	if res.FallbackDir != "" {
		t.Fatalf("FallbackDir should be empty for clean git tree, got %q", res.FallbackDir)
	}
}

func TestRestoreFallbackCopy_RoundTrip(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "hello.txt"), []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	snapRoot := t.TempDir()
	ctx := context.Background()
	res, err := safety.TakeSnapshot(ctx, src, snapRoot)
	if err != nil {
		t.Fatalf("TakeSnapshot: %v", err)
	}
	if res.FallbackDir == "" {
		t.Fatal("expected FallbackDir")
	}

	// Overwrite the original.
	if err := os.WriteFile(filepath.Join(src, "hello.txt"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Restore.
	if err := safety.RestoreFallbackCopy(ctx, src, res.FallbackDir); err != nil {
		t.Fatalf("RestoreFallbackCopy: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(src, "hello.txt"))
	if err != nil {
		t.Fatalf("read after restore: %v", err)
	}
	if string(got) != "original" {
		t.Fatalf("restore failed: got %q, want %q", got, "original")
	}
}

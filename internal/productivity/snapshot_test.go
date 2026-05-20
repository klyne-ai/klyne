package productivity

import (
	"path/filepath"
	"testing"
	"time"
)

func TestCaptureSnapshot_CleanRepo(t *testing.T) {
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q", "-b", "feat/CLI-1396-pipeline")
	commitFile(t, dir, "a.go", "package a\n", "first commit", "mohitpatel9753@gmail.com", time.Now().Add(-time.Hour))

	snap, err := CaptureSnapshot(dir)
	if err != nil {
		t.Fatalf("CaptureSnapshot: %v", err)
	}
	if snap.Branch != "feat/CLI-1396-pipeline" {
		t.Errorf("Branch = %q; want feat/CLI-1396-pipeline", snap.Branch)
	}
	if len(snap.HeadSHA) < 7 {
		t.Errorf("HeadSHA = %q; want a non-trivial sha", snap.HeadSHA)
	}
	if snap.RepoName != RepoName(dir) {
		t.Errorf("RepoName = %q; want %q", snap.RepoName, RepoName(dir))
	}
	if snap.ProjectPath == "" {
		t.Errorf("ProjectPath empty; want canonical repo root")
	}
	if snap.WorktreePath == "" {
		t.Errorf("WorktreePath empty; want the working dir")
	}
	if snap.DirtyFileCount != 0 {
		t.Errorf("DirtyFileCount = %d; want 0 (clean tree)", snap.DirtyFileCount)
	}
	if len(snap.DirtyFiles) != 0 {
		t.Errorf("DirtyFiles = %v; want empty", snap.DirtyFiles)
	}
	// No upstream configured: every commit on HEAD counts as ahead.
	if snap.AheadCount < 1 {
		t.Errorf("AheadCount = %d; want >= 1 (no upstream)", snap.AheadCount)
	}
	if snap.BehindCount != 0 {
		t.Errorf("BehindCount = %d; want 0", snap.BehindCount)
	}
}

func TestCaptureSnapshot_DirtyTree(t *testing.T) {
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q", "-b", "main")
	commitFile(t, dir, "tracked.go", "package a\n", "first", "mohitpatel9753@gmail.com", time.Now().Add(-time.Hour))

	// Modify a tracked file and add an untracked one — both must count.
	if err := writeFile(filepath.Join(dir, "tracked.go"), "package a\n// changed\n"); err != nil {
		t.Fatalf("write modified: %v", err)
	}
	if err := writeFile(filepath.Join(dir, "untracked.go"), "package b\n"); err != nil {
		t.Fatalf("write untracked: %v", err)
	}

	snap, err := CaptureSnapshot(dir)
	if err != nil {
		t.Fatalf("CaptureSnapshot: %v", err)
	}
	if snap.DirtyFileCount != 2 {
		t.Errorf("DirtyFileCount = %d; want 2 (1 modified + 1 untracked)", snap.DirtyFileCount)
	}
	got := map[string]bool{}
	for _, f := range snap.DirtyFiles {
		got[f] = true
	}
	if !got["tracked.go"] {
		t.Errorf("DirtyFiles missing tracked.go; got %v", snap.DirtyFiles)
	}
	if !got["untracked.go"] {
		t.Errorf("DirtyFiles missing untracked.go; got %v", snap.DirtyFiles)
	}
}

func TestCaptureSnapshot_CapsDirtyFilesAt50(t *testing.T) {
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q", "-b", "main")
	commitFile(t, dir, "seed.go", "package a\n", "seed", "mohitpatel9753@gmail.com", time.Now().Add(-time.Hour))

	for i := 0; i < 70; i++ {
		name := "f" + string(rune('a'+i%26)) + string(rune('0'+i/26)) + ".txt"
		if err := writeFile(filepath.Join(dir, name), "x"); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	snap, err := CaptureSnapshot(dir)
	if err != nil {
		t.Fatalf("CaptureSnapshot: %v", err)
	}
	// Count reflects the true number; the listed paths are bounded.
	if snap.DirtyFileCount != 70 {
		t.Errorf("DirtyFileCount = %d; want 70 (true count, not capped)", snap.DirtyFileCount)
	}
	if len(snap.DirtyFiles) > snapshotMaxDirtyFiles {
		t.Errorf("DirtyFiles len = %d; want <= %d (bounded list)", len(snap.DirtyFiles), snapshotMaxDirtyFiles)
	}
}

func TestCaptureSnapshot_Worktree(t *testing.T) {
	main := t.TempDir()
	gitCmd(t, main, "init", "-q", "-b", "main")
	commitFile(t, main, "a.go", "package a\n", "first", "mohitpatel9753@gmail.com", time.Now().Add(-time.Hour))

	wt := filepath.Join(t.TempDir(), "wt-CLI-9999")
	gitCmd(t, main, "worktree", "add", "-q", "-b", "feat/CLI-9999-x", wt)

	snap, err := CaptureSnapshot(wt)
	if err != nil {
		t.Fatalf("CaptureSnapshot(worktree): %v", err)
	}
	if snap.Branch != "feat/CLI-9999-x" {
		t.Errorf("Branch = %q; want feat/CLI-9999-x", snap.Branch)
	}
	// WorktreePath is the actual checkout; ProjectPath canonicalizes to
	// the repo's main root, which differs from the worktree dir.
	if snap.WorktreePath == "" {
		t.Errorf("WorktreePath empty")
	}
}

func TestCaptureSnapshot_NotAGitRepo(t *testing.T) {
	dir := t.TempDir()
	_, err := CaptureSnapshot(dir)
	if err == nil {
		t.Fatal("expected an error for a non-git directory")
	}
}

func TestCaptureSessionSnapshots_CoversMainAndWorktrees(t *testing.T) {
	main := t.TempDir()
	gitCmd(t, main, "init", "-q", "-b", "main")
	commitFile(t, main, "a.go", "package a\n", "first", "mohitpatel9753@gmail.com", time.Now().Add(-time.Hour))

	wt := filepath.Join(t.TempDir(), "wt-CLI-1396")
	gitCmd(t, main, "worktree", "add", "-q", "-b", "feat/CLI-1396", wt)

	// CaptureSessionSnapshots called on EITHER the main tree or a sibling
	// worktree must return one SnapshotData per worktree of the repo (D5).
	snaps := CaptureSessionSnapshots(wt)
	if len(snaps) < 2 {
		t.Fatalf("want >= 2 snapshots (main + worktree); got %d: %+v", len(snaps), snaps)
	}
	branches := map[string]bool{}
	for _, s := range snaps {
		branches[s.Branch] = true
	}
	if !branches["main"] {
		t.Errorf("missing main-tree snapshot; got branches %v", branches)
	}
	if !branches["feat/CLI-1396"] {
		t.Errorf("missing worktree snapshot; got branches %v", branches)
	}
}

func TestCaptureSessionSnapshots_NonGitDirIsEmpty(t *testing.T) {
	// Best-effort: a non-git dir yields no snapshots and never panics.
	if snaps := CaptureSessionSnapshots(t.TempDir()); len(snaps) != 0 {
		t.Errorf("want 0 snapshots for a non-git dir; got %d", len(snaps))
	}
	if snaps := CaptureSessionSnapshots(""); len(snaps) != 0 {
		t.Errorf("want 0 snapshots for an empty dir; got %d", len(snaps))
	}
}

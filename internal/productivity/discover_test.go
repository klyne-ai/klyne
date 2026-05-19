package productivity

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// fakeLister is a stub SessionPathLister for discovery tests — no DB.
type fakeLister struct{ paths []string }

func (f fakeLister) SessionProjectPaths(_ context.Context, _, _ time.Time) ([]string, error) {
	return f.paths, nil
}

func TestDiscoverRepos_CanonicalizesAndIncludesWorktrees(t *testing.T) {
	// Main repo with one commit so worktrees can be added.
	main := t.TempDir()
	gitCmd(t, main, "init", "-q", "-b", "main")
	commitFile(t, main, "f.go", "package f\n", "init", "mohitpatel9753@gmail.com", time.Now().Add(-time.Hour))

	// Sibling worktree on a feature branch (D5: cover parallel worktrees).
	wt := filepath.Join(t.TempDir(), "CLI-1396")
	gitCmd(t, main, "worktree", "add", "-q", "-b", "feat/CLI-1396", wt)

	lister := fakeLister{paths: []string{main}}
	repos, err := DiscoverRepos(context.Background(), lister, time.Now().Add(-2*time.Hour), time.Now())
	if err != nil {
		t.Fatalf("DiscoverRepos: %v", err)
	}

	dirs := map[string]bool{}
	for _, r := range repos {
		dirs[r.Dir] = true
	}
	wantMain, _ := filepath.EvalSymlinks(main)
	wantWT, _ := filepath.EvalSymlinks(wt)
	foundMain, foundWT := false, false
	for _, r := range repos {
		ev, _ := filepath.EvalSymlinks(r.Dir)
		if ev == wantMain {
			foundMain = true
		}
		if ev == wantWT {
			foundWT = true
		}
	}
	if !foundMain {
		t.Errorf("main repo not discovered; got %v", dirs)
	}
	if !foundWT {
		t.Errorf("sibling worktree not discovered (D5); got %v", dirs)
	}
}

func TestUserEmails_SeedSetAndGitConfig(t *testing.T) {
	em := UserEmails()
	for _, want := range []string{"mohitpatel9753@gmail.com", "coders@clinikk.com"} {
		if !em[want] {
			t.Errorf("seed email %q missing from UserEmails(); got %v", want, em)
		}
	}
}

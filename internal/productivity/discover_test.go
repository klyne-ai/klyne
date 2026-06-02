package productivity

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
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
	commitFile(t, main, "f.go", "package f\n", "init", "dev@example.com", time.Now().Add(-time.Hour))

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

func TestDiscoverRepos_ExcludesNonGitPaths(t *testing.T) {
	// A non-git directory (e.g. a parent dir like /Users/x/Desktop a
	// session happened to run in) must NOT become a target/Service.
	nonGit := t.TempDir()

	// A real repo so we can prove the git one survives the filter.
	repo := t.TempDir()
	gitCmd(t, repo, "init", "-q", "-b", "main")
	commitFile(t, repo, "f.go", "package f\n", "init", "dev@example.com", time.Now().Add(-time.Hour))

	lister := fakeLister{paths: []string{nonGit, repo}}
	repos, err := DiscoverRepos(context.Background(), lister, time.Now().Add(-2*time.Hour), time.Now())
	if err != nil {
		t.Fatalf("DiscoverRepos: %v", err)
	}

	wantNonGit, _ := filepath.EvalSymlinks(nonGit)
	wantRepo, _ := filepath.EvalSymlinks(repo)
	foundRepo := false
	for _, r := range repos {
		ev, _ := filepath.EvalSymlinks(r.Dir)
		if ev == wantNonGit {
			t.Errorf("non-git path leaked into targets: %+v", r)
		}
		evP, _ := filepath.EvalSymlinks(r.ProjectPath)
		if ev == wantRepo || evP == wantRepo {
			foundRepo = true
		}
	}
	if !foundRepo {
		t.Errorf("real git repo dropped by the non-git filter; got %v", repos)
	}
}

func TestDiscoverRepos_WorktreesShareOneCanonicalProjectPath(t *testing.T) {
	// One repo with a sibling worktree on a different branch must yield
	// targets that ALL share a single canonical ProjectPath (the main
	// repo root), so report assembly can collapse them into ONE Service.
	main := t.TempDir()
	gitCmd(t, main, "init", "-q", "-b", "main")
	commitFile(t, main, "f.go", "package f\n", "init", "dev@example.com", time.Now().Add(-time.Hour))

	wt := filepath.Join(t.TempDir(), "feat-CLI-1396")
	gitCmd(t, main, "worktree", "add", "-q", "-b", "feat/CLI-1396", wt)

	lister := fakeLister{paths: []string{main, wt}}
	repos, err := DiscoverRepos(context.Background(), lister, time.Now().Add(-2*time.Hour), time.Now())
	if err != nil {
		t.Fatalf("DiscoverRepos: %v", err)
	}
	if len(repos) < 2 {
		t.Fatalf("expected the main tree + worktree as targets; got %v", repos)
	}
	canon := map[string]bool{}
	for _, r := range repos {
		ev, _ := filepath.EvalSymlinks(r.ProjectPath)
		canon[ev] = true
	}
	if len(canon) != 1 {
		t.Errorf("worktrees of one repo must share ONE canonical ProjectPath; got %d distinct: %v", len(canon), canon)
	}
	wantMain, _ := filepath.EvalSymlinks(main)
	if !canon[wantMain] {
		t.Errorf("canonical ProjectPath = %v; want the main repo root %q", canon, wantMain)
	}
}

func TestUserEmails_GitConfigOnlyByDefault(t *testing.T) {
	SetUserEmailAliases(nil)
	em := UserEmails()

	// Compute the legitimate `git config user.email` (if any) so we can
	// distinguish a leaked seed from the developer's own identity.
	var localGit string
	if out, err := exec.Command("git", "config", "user.email").Output(); err == nil {
		localGit = strings.ToLower(strings.TrimSpace(string(out)))
	}

	// The set must be empty OR contain only the local git identity.
	// Anything else means a hardcoded seed leaked back in.
	for got := range em {
		if got != localGit {
			t.Errorf("UserEmails() returned %q, which is not the local git user.email %q — suggests a hardcoded seed leaked back into the binary", got, localGit)
		}
	}
	if localGit != "" && !em[localGit] {
		t.Errorf("git user.email %q missing from UserEmails(); got %v", localGit, em)
	}
}

func TestUserEmails_AliasesAreAdditive(t *testing.T) {
	SetUserEmailAliases([]string{"work@example.com", "  PERSONAL@example.com  "})
	t.Cleanup(func() { SetUserEmailAliases(nil) })

	em := UserEmails()
	if !em["work@example.com"] {
		t.Errorf("alias %q missing; got %v", "work@example.com", em)
	}
	if !em["personal@example.com"] {
		t.Errorf("alias %q (after trim+lowercase) missing; got %v", "personal@example.com", em)
	}
}

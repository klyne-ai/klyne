package productivity

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/klyne-ai/klyne/internal/projectpath"
)

// SessionPathLister is the narrow store dependency repo discovery needs:
// the distinct session project_paths active in a window. Defined as an
// interface so this package stays testable with a stub and free of a
// direct *store.DB dependency (the API handler — Task 6, not built here
// — adapts the real store to this interface).
type SessionPathLister interface {
	SessionProjectPaths(ctx context.Context, since, until time.Time) ([]string, error)
}

// RepoTarget is one (canonical-repo, working-dir) pair to scan. Dir may
// be the canonical root itself or a sibling worktree of it (D5).
type RepoTarget struct {
	Repo        string // display name
	ProjectPath string // canonical repo root (via internal/projectpath)
	Dir         string // actual working dir to git-scan (root or worktree)
}

// DiscoverRepos implements D5 / §6.1: take the distinct session
// project_paths in the window, canonicalize each via internal/projectpath,
// then enumerate `git worktree list` so sibling worktrees (per-ticket
// checkouts) are covered too. Deduplicated by Dir.
func DiscoverRepos(ctx context.Context, l SessionPathLister, since, until time.Time) ([]RepoTarget, error) {
	paths, err := l.SessionProjectPaths(ctx, since, until)
	if err != nil {
		return nil, err
	}

	seenCanon := map[string]bool{}
	seenDir := map[string]bool{}
	var out []RepoTarget

	for _, p := range paths {
		if strings.TrimSpace(p) == "" {
			continue
		}
		// D5/§6.1: only sessions that ran inside a real git work tree
		// describe a repo. A non-git project_path (e.g. a parent dir
		// like /Users/x/Desktop, or observer/agent sessions with cwd at
		// a non-repo) must NOT become a Service — that is the bogus
		// Desktop/Learning/Project/observer-sessions noise.
		if !insideGitWorkTree(p) {
			continue
		}
		// Canonicalize to the repo's MAIN root so every worktree of a
		// repo (per-ticket checkouts, claude/codex worktrees) collapses
		// to a single canonical identity — one Service per repo (§6.6).
		canon := projectpath.Canonical(p)
		if seenCanon[canon] {
			continue
		}
		seenCanon[canon] = true

		name := RepoName(canon)
		add := func(dir string) {
			if dir == "" || seenDir[dir] {
				return
			}
			seenDir[dir] = true
			out = append(out, RepoTarget{Repo: name, ProjectPath: canon, Dir: dir})
		}

		add(canon)
		for _, w := range worktreePaths(canon) {
			add(w)
		}
	}
	return out, nil
}

// insideGitWorkTree reports whether dir is within a git working tree.
// `git -C <dir> rev-parse --is-inside-work-tree` prints "true" only for
// a real checkout/worktree; it errors (or prints false for a bare repo)
// otherwise. This is the D5/§6.1 filter that drops non-repo
// project_paths (parent dirs, observer/agent sessions) before discovery.
func insideGitWorkTree(dir string) bool {
	out, err := gitOut(dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

// worktreePaths parses `git worktree list --porcelain` and returns every
// worktree path (including the main one — dedup happens in the caller).
func worktreePaths(dir string) []string {
	out, err := gitOut(dir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil
	}
	var paths []string
	for _, ln := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(ln, "worktree "); ok {
			paths = append(paths, strings.TrimSpace(rest))
		}
	}
	return paths
}

// seedUserEmails is the hardcoded §6.3 identity alias set.
//
// PROTOTYPE STUB (spec §11): the alias set is a hardcoded seed here;
// wiring it to klyne config (git user.email ∪ configurable aliases) is a
// documented production follow-up. coders@clinikk.com is included because
// it is the operating identity for this environment.
var seedUserEmails = []string{
	"mohitpatel9753@gmail.com",
	"coders@clinikk.com",
	"mohit@clinikk.com", // git author identity on the work microservices (mohit-clinikk)
}

// UserEmails returns the §6.3 identity set: the local `git config
// user.email` (when resolvable) ∪ the hardcoded seed alias set. Commits
// whose author email is NOT in this set are co-actors/bots and are
// excluded from the user's productivity figures.
func UserEmails() map[string]bool {
	set := map[string]bool{}
	for _, e := range seedUserEmails {
		set[strings.ToLower(e)] = true
	}
	if out, err := exec.Command("git", "config", "user.email").Output(); err == nil {
		if e := strings.ToLower(strings.TrimSpace(string(out))); e != "" {
			set[e] = true
		}
	}
	return set
}

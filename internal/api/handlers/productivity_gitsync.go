package handlers

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// --- git remote refresh (companion to the merged-PR enrichment) -----------
//
// The dashboard's ahead/behind, ship-state and "unpushed" risk are all
// computed against the LOCAL mirror of origin (origin/*). If the user
// hasn't run `git fetch` recently those refs are stale — that is the
// same root cause as the original "PRs merged shows 0" bug
// (operations-app #400 had merged on GitHub, but the squash commit
// wasn't in the local origin/main). To keep the dashboard honest we
// piggyback on the merged-PR cache TTL: when a repo's last fetch is
// older than prCacheTTL() we run `git fetch --no-tags origin` before
// the scan reads the refs. Failures are silent — the dashboard still
// renders against whatever the local mirror has, and the per-Service
// "git as of N ago" surface tells the user how fresh the data is.

// gitFetchOverall caps how long the dashboard will wait for all
// per-repo fetches combined. Slow individual remotes get cancelled
// when this elapses; the dashboard renders with whatever refs already
// landed.
const gitFetchOverall = 12 * time.Second

// refreshStaleRemotes runs `git fetch --no-tags --quiet origin` for
// every dir whose .git FETCH_HEAD is older than ttl, concurrently and
// bounded by gitFetchOverall. dirs may include both canonical roots
// and sibling worktrees — fetching from any of them updates the
// shared origin/* refs once.
func refreshStaleRemotes(ctx context.Context, dirs []string, ttl time.Duration) {
	stale := stalemarkRepoDirs(dirs, ttl)
	if len(stale) == 0 {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, gitFetchOverall)
	defer cancel()

	var wg sync.WaitGroup
	for _, d := range stale {
		wg.Add(1)
		go func(d string) {
			defer wg.Done()
			cmd := exec.CommandContext(cctx, "git", "-C", d,
				"fetch", "--no-tags", "--quiet", "origin")
			_ = cmd.Run() // best-effort
		}(d)
	}
	wg.Wait()
}

// stalemarkRepoDirs returns the subset of dirs whose effective git dir
// FETCH_HEAD is older than ttl (or absent — never fetched in this
// clone). Sharing a canonical git dir (worktrees of one repo) is
// deduplicated by gitDir so we don't fetch the same remote twice.
func stalemarkRepoDirs(dirs []string, ttl time.Duration) []string {
	seenGitDir := map[string]bool{}
	var out []string
	for _, d := range dirs {
		gd := gitDir(d)
		if gd == "" {
			continue
		}
		if seenGitDir[gd] {
			continue
		}
		seenGitDir[gd] = true
		if age := timeSinceFetch(gd); age > ttl {
			out = append(out, d)
		}
	}
	return out
}

// gitDir resolves the actual on-disk .git directory for dir — handles
// the worktree case where <dir>/.git is a file containing
// "gitdir: <path>". Returns "" when dir isn't inside a git repo.
func gitDir(dir string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--git-dir").Output()
	if err != nil {
		return ""
	}
	gd := strings.TrimSpace(string(out))
	if gd == "" {
		return ""
	}
	if !filepath.IsAbs(gd) {
		gd = filepath.Join(dir, gd)
	}
	return gd
}

// timeSinceFetch returns how long ago `git fetch` last ran in this
// git directory, via FETCH_HEAD mtime. Returns a very large duration
// when FETCH_HEAD does not exist (never fetched here) so the caller
// schedules a fetch.
func timeSinceFetch(gd string) time.Duration {
	fi, err := os.Stat(filepath.Join(gd, "FETCH_HEAD"))
	if err != nil {
		return 24 * time.Hour * 365
	}
	return time.Since(fi.ModTime())
}

// gitFetchedAt is the wall-clock time of the last `git fetch` for the
// repo at dir (FETCH_HEAD mtime), or zero when unknown. Surfaced to
// the UI as "git as of N ago" so the user can see how fresh the local
// origin/* mirror is.
func gitFetchedAt(dir string) time.Time {
	gd := gitDir(dir)
	if gd == "" {
		return time.Time{}
	}
	fi, err := os.Stat(filepath.Join(gd, "FETCH_HEAD"))
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

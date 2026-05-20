package worklog

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/klyne-ai/klyne/internal/productivity"
	"github.com/klyne-ai/klyne/internal/projectpath"
)

// BuildProjectSubstrate builds the deterministic productivity Report for
// a SINGLE project over [since, until] — the substrate the worklog
// reflection generator consumes (spec §7.2 / D8). It is the bridge that
// lets the MCP propose_reflection / record_reflection tools enrich a
// reflection with git-grounded facts without depending on the dashboard
// API handler.
//
// It scans the project's canonical repo and every sibling worktree,
// applies the §6.3 identity filter (productivity.UserEmails), runs the
// ship-state machine + risk signals, and assembles the same Report type
// the dashboard uses — so the dashboard and reflections agree by
// construction (D8).
//
// Graceful degradation (D8): a projectPath that is not a git work tree
// yields an empty Report and a nil error — the caller keeps the existing
// reflection flow unchanged. NO LLM is invoked.
func BuildProjectSubstrate(projectPath string, since, until time.Time) (productivity.Report, error) {
	canon := projectpath.Canonical(projectPath)
	if !insideGitWorkTree(canon) {
		// Not a git repo — nothing to ground against. Degrade gracefully.
		return productivity.Report{Day: until.Format("2006-01-02")}, nil
	}

	userEmails := productivity.UserEmails()

	// Scan every worktree of the repo so per-ticket checkouts are covered
	// (D5). `git worktree list` already includes the main worktree, so it
	// is the single source of truth — adding `canon` separately risks a
	// duplicate scan when canon and the listed path differ only by a
	// symlink prefix (/var vs /private/var). Dedup by symlink-resolved
	// path; fall back to canon when the list is empty.
	dirs := dedupResolved(gitWorktreePaths(canon))
	if len(dirs) == 0 {
		dirs = []string{canon}
	}

	var scans []productivity.ScanResult
	commitCounts := map[string]int{}
	projectPaths := map[string]string{}
	for _, dir := range dirs {
		sc, err := productivity.ScanRepo(dir, since, until, userEmails)
		if err != nil {
			// A worktree that won't scan is skipped, never fatal — keeps
			// the substrate stable across a heterogeneous checkout set.
			continue
		}
		scans = append(scans, sc)
		commitCounts[sc.Dir] = len(sc.Commits)
		// Every worktree of this repo rolls up to the one canonical path
		// so BuildReport collapses them into a single Service.
		projectPaths[sc.Dir] = canon
	}

	in := productivity.ReportInput{
		Day:          until.Format("2006-01-02"),
		Now:          until,
		Scans:        scans,
		ProjectPaths: projectPaths,
	}
	// nil ReflectionLookup: the substrate build does not need the L3
	// nudge — the reflection generator is the L2 layer itself.
	return productivity.BuildReport(context.Background(), in, nil)
}

// insideGitWorkTree reports whether dir is within a git working tree.
// Mirrors the productivity package's discovery filter; kept local so the
// worklog package has no need to export it from productivity.
func insideGitWorkTree(dir string) bool {
	out, err := gitC(dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

// gitWorktreePaths returns every worktree path of the repo at dir
// (including the main one — the caller dedups).
func gitWorktreePaths(dir string) []string {
	out, err := gitC(dir, "worktree", "list", "--porcelain")
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

// dedupResolved returns dirs with duplicates removed, comparing by
// symlink-resolved absolute path so /var and /private/var (and any
// other symlink alias) collapse to one entry. The first-seen original
// path is kept so git -C still gets a usable directory.
func dedupResolved(dirs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range dirs {
		if d == "" {
			continue
		}
		key := d
		if r, err := filepath.EvalSymlinks(d); err == nil {
			key = r
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, d)
	}
	return out
}

// gitC runs `git -C dir args...` and returns trimmed stdout.
func gitC(dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir}, args...)
	out, err := exec.Command("git", full...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

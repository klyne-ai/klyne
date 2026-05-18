// Package projectpath resolves a working directory to its canonical
// project root — the main repo path that all worktrees of that repo
// share. This is the project identity klyne keys worklog entries,
// reflections, decisions, and memories on, so worktrees (git, claude,
// codex) of the same repo roll up under one project narrative.
//
// Lives in a leaf package (depends only on stdlib) so callers like
// internal/worklog can use it without pulling in internal/mcpserver.
package projectpath

import (
	"os/exec"
	"path/filepath"
	"strings"
)

// Canonical returns the canonical project root for cwd.
// For a git worktree, this is the main repo path (so worktrees of the
// same repo share project-scoped memory). For a regular checkout, this
// is the repo root. For a non-git directory, returns cwd unchanged.
//
// The mechanism is `git -C <cwd> rev-parse --git-common-dir`, which
// returns `.git` for a regular checkout and the main-repo `.git`
// directory's absolute path for any worktree. The parent of that path
// is the canonical project root.
//
// Errors are swallowed deliberately: a project resolver that can't
// answer must not break the caller's flow. Callers always get back a
// non-empty string when given one.
func Canonical(cwd string) string {
	if strings.TrimSpace(cwd) == "" {
		return cwd
	}
	cmd := exec.Command("git", "-C", cwd, "rev-parse", "--git-common-dir")
	out, err := cmd.Output()
	if err != nil {
		return cwd
	}
	gitDir := strings.TrimSpace(string(out))
	if gitDir == "" {
		return cwd
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(cwd, gitDir)
	}
	return filepath.Dir(gitDir)
}

package mcpserver

import (
	"os/exec"
	"path/filepath"
	"strings"
)

// CanonicalProjectPath returns the canonical project root for cwd.
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
func CanonicalProjectPath(cwd string) string {
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
	// git-common-dir is the .git directory; the repo root is its parent.
	return filepath.Dir(gitDir)
}

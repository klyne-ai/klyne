package mcpserver

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/klyne-ai/klyne/internal/connectors"
)

// captureGitDirtyTimeout caps how long we wait for `git status` to
// return. A misbehaving repo (huge worktree, network FS) should not
// block snapshot loading indefinitely.
const captureGitDirtyTimeout = 3 * time.Second

// captureGitDirty runs `git status --porcelain` rooted at cwd and
// returns the set of absolute file paths with uncommitted changes.
// Second return is false when git is unavailable, cwd is not a
// repo, or the command fails / times out — callers should treat
// the dirty-state of files as "unknown" in that case.
func captureGitDirty(cwd string) (map[string]bool, bool) {
	if cwd == "" {
		return nil, false
	}
	if _, err := exec.LookPath("git"); err != nil {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), captureGitDirtyTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", cwd, "status", "--porcelain", "-z")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return nil, false
	}
	out := map[string]bool{}
	// -z output: each entry is "<XY> <path>\0", optionally followed by
	// "\0<old-path>" for renames. We just need the dirty paths.
	for _, entry := range bytes.Split(stdout.Bytes(), []byte{0}) {
		if len(entry) < 4 {
			continue
		}
		// Skip leading status bytes and the single space.
		path := string(entry[3:])
		if path == "" {
			continue
		}
		abs := path
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(cwd, path)
		}
		out[abs] = true
	}
	return out, true
}

// firstCwdFromMessages returns the first non-empty Cwd across the
// snapshot's messages. Used by LoadSnapshot to determine which repo
// to ask git about.
func firstCwdFromMessages(msgs []*connectors.Message) string {
	for _, m := range msgs {
		if m != nil && strings.TrimSpace(m.Cwd) != "" {
			return m.Cwd
		}
	}
	return ""
}

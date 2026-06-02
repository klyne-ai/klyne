// Package safety implements the pre-action snapshot writer for klyne's
// safety net. A snapshot captures working-tree state before a risky
// command runs, so it can be restored via `klyne restore`.
package safety

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/klyne-ai/klyne/internal/config"
)

// stashSHAPattern validates a git object name before it is passed to
// `git stash apply`, as cheap insurance against argument injection if a
// caller ever sources the SHA from untrusted input.
var stashSHAPattern = regexp.MustCompile(`^[0-9a-fA-F]{7,64}$`)

// SnapshotResult is returned by TakeSnapshot.
type SnapshotResult struct {
	// StashSHA is the object name returned by `git stash create`, non-empty
	// when the repo had changes to stash.
	StashSHA string
	// FallbackDir is the path of the cp-r fallback copy (non-empty when git
	// stash was unavailable or inapplicable).
	FallbackDir string
	// FileCount is the number of files captured (best-effort; 0 when unknown).
	FileCount int
}

// TakeSnapshot captures the current working-tree state in cwd.
//
// For git repositories it runs `git stash create --include-untracked`.
// When that is unavailable or the repo has no changes (stash SHA is empty),
// it falls back to a recursive copy under snapshotRoot. When snapshotRoot
// is "", the fallback copies to os.TempDir()/.klyne-snapshots/<ts>/.
//
// Errors from git are treated as a signal to use the fallback rather than
// propagating — losing a snapshot is worse than silently falling back.
func TakeSnapshot(ctx context.Context, cwd, snapshotRoot string) (*SnapshotResult, error) {
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("safety: getwd: %w", err)
		}
	}

	// Try git stash create first.
	if isGitRepo(ctx, cwd) {
		sha, err := gitStashCreate(ctx, cwd)
		if err == nil && sha != "" {
			fc, _ := gitChangedFileCount(ctx, cwd)
			return &SnapshotResult{StashSHA: sha, FileCount: fc}, nil
		}
		// git stash create returned "" meaning a clean working tree. Still
		// record a stash SHA of "" so the caller knows git was available but
		// there was nothing to stash.
		if err == nil {
			return &SnapshotResult{StashSHA: "", FileCount: 0}, nil
		}
		// err != nil: fall through to cp-r fallback below.
	}

	// cp-r fallback for non-git dirs or when git stash fails.
	dest, fc, err := copyFallback(ctx, cwd, snapshotRoot)
	if err != nil {
		return nil, fmt.Errorf("safety: fallback copy: %w", err)
	}
	return &SnapshotResult{FallbackDir: dest, FileCount: fc}, nil
}

// RestoreGitStash applies a previously-created stash SHA back into cwd.
func RestoreGitStash(ctx context.Context, cwd, stashSHA string) error {
	if stashSHA == "" {
		return fmt.Errorf("safety: restore: stash SHA is empty")
	}
	if !stashSHAPattern.MatchString(stashSHA) {
		return fmt.Errorf("safety: restore: invalid stash SHA %q", stashSHA)
	}
	// `--` guards against a SHA that begins with `-` being parsed as a flag
	// (belt-and-suspenders with the hex validation above).
	cmd := exec.CommandContext(ctx, "git", "stash", "apply", "--", stashSHA) //nolint:gosec
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("safety: git stash apply %s: %w\n%s", stashSHA, err, out)
	}
	return nil
}

// RestoreFallbackCopy copies fallbackDir over cwd, restoring the backed-up state.
func RestoreFallbackCopy(ctx context.Context, cwd, fallbackDir string) error {
	if fallbackDir == "" {
		return fmt.Errorf("safety: restore: fallback dir is empty")
	}
	return copyDir(ctx, fallbackDir, cwd)
}

// isGitRepo returns true when cwd is inside a git repository.
func isGitRepo(ctx context.Context, cwd string) bool {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--git-dir")
	cmd.Dir = cwd
	return cmd.Run() == nil
}

// gitStashCreate runs `git stash create --include-untracked` and returns
// the resulting stash SHA (or "" for a clean tree). An error is returned
// for unexpected git failures.
func gitStashCreate(ctx context.Context, cwd string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "stash", "create", "--include-untracked")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git stash create: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// gitChangedFileCount returns the number of tracked+untracked changed files.
func gitChangedFileCount(ctx context.Context, cwd string) (int, error) {
	cmd := exec.CommandContext(ctx, "git", "status", "--short")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	count := 0
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count, nil
}

// shouldSkipSnapshotEntry reports whether a path basename should be excluded
// from the cp-r fallback copy. The fallback duplicates working-tree files
// outside the repo, so we refuse to copy version-control internals, vendored
// trees, and well-known secret material (env files, private keys, npm creds).
// This is a basename denylist applied at every level of the walk.
func shouldSkipSnapshotEntry(name string) bool {
	switch name {
	case ".git", "node_modules", ".env", ".npmrc", "id_rsa":
		return true
	}
	// .env.* (e.g. .env.local, .env.production)
	if strings.HasPrefix(name, ".env.") {
		return true
	}
	// *.pem certificates / keys
	if strings.HasSuffix(name, ".pem") {
		return true
	}
	// id_* SSH private keys (id_ed25519, id_ecdsa, ...). The .pub siblings
	// are public, but copying them adds no value and the prefix is cheap to
	// drop wholesale.
	if strings.HasPrefix(name, "id_") {
		return true
	}
	return false
}

// copyFallback copies cwd recursively into a timestamped sub-directory
// under snapshotRoot and returns the destination path + file count.
//
// When snapshotRoot is "", it defaults to a private directory under the
// user's klyne dir (~/.klyne/snapshots) created 0700 — never the shared
// os.TempDir(), which is world-listable. The per-snapshot directory and all
// copied files are created with restrictive modes (0700 dirs, 0600 files).
//
// A basename denylist (see shouldSkipSnapshotEntry) skips .git, node_modules,
// .env / .env.*, *.pem, id_* private keys, and .npmrc so secrets are not
// duplicated outside the repo. The snapshotRoot itself is also skipped to
// avoid copying snapshots into themselves when it lives under cwd.
//
// Remaining exposure: any *other* secret-bearing file the denylist does not
// recognise (custom credential filenames, tokens embedded in tracked source,
// etc.) is still copied into the snapshot. The snapshot dir is owner-only, so
// the exposure is limited to the local user, but it is not zero. Callers that
// handle highly sensitive trees should prefer the git-stash path (which keeps
// everything inside the repo's object store).
func copyFallback(_ context.Context, cwd, snapshotRoot string) (string, int, error) {
	if snapshotRoot == "" {
		snapshotRoot = filepath.Join(config.ConfigDir(), "snapshots")
	}
	// Create the root 0700 so the parent is owner-only even if it lives in a
	// shared location.
	if err := os.MkdirAll(snapshotRoot, 0o700); err != nil {
		return "", 0, fmt.Errorf("mkdir snapshot root: %w", err)
	}
	absRoot, _ := filepath.Abs(snapshotRoot)
	ts := time.Now().UTC().Format("20060102T150405Z")
	dest := filepath.Join(snapshotRoot, ts)
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return "", 0, fmt.Errorf("mkdir fallback dest: %w", err)
	}

	fc := 0
	walkErr := filepath.WalkDir(cwd, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		// Never descend into / copy the snapshot root itself (avoids copying
		// snapshots into themselves when snapshotRoot is under cwd).
		if absPath, aerr := filepath.Abs(path); aerr == nil && absRoot != "" {
			if absPath == absRoot {
				return filepath.SkipDir
			}
		}
		// Denylist: skip VCS internals, vendored trees, and secret material.
		if path != cwd && shouldSkipSnapshotEntry(d.Name()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(cwd, path)
		if relErr != nil {
			return nil
		}
		target := filepath.Join(dest, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		fc++
		return copyFile(path, target)
	})
	return dest, fc, walkErr
}

// copyFile copies src to dst, creating dst's parent directories as needed.
// The destination file is created 0600 (owner read/write only) so snapshot
// copies of working-tree files are never group/world readable.
func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	in, err := os.Open(src) //nolint:gosec
	if err != nil {
		return err
	}
	defer in.Close() //nolint:errcheck

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec
	if err != nil {
		return err
	}
	defer out.Close() //nolint:errcheck

	_, err = io.Copy(out, in)
	return err
}

// copyDir recursively copies src directory tree into dst.
func copyDir(_ context.Context, src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		return copyFile(path, target)
	})
}

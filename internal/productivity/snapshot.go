package productivity

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/klyne-ai/klyne/internal/projectpath"
)

// snapshotMaxDirtyFiles caps how many dirty file paths CaptureSnapshot
// lists (spec §5: dirty_files_json is a bounded list, cap N ≈ 50). The
// dirty *count* is always exact; only the enumerated paths are bounded so
// a session that left hundreds of generated files dirty does not bloat
// the row.
const snapshotMaxDirtyFiles = 50

// SnapshotData is a point-in-time git working-tree capture for one repo
// working directory (spec D6 — the session-end snapshot half). It is the
// deterministic source for a git_session_snapshots row: every field is
// read straight from git, no LLM, no guesswork.
//
// ProjectPath is the canonical repo root (worktrees collapse to the main
// checkout via internal/projectpath); WorktreePath is the actual working
// dir scanned, which differs from ProjectPath for a sibling worktree.
type SnapshotData struct {
	ProjectPath    string
	RepoName       string
	WorktreePath   string
	Branch         string
	HeadSHA        string
	AheadCount     int
	BehindCount    int
	DirtyFileCount int
	DirtyFiles     []string
}

// CaptureSnapshot reads the current git state of one working directory:
// canonical project root, repo name, worktree path, branch, HEAD sha,
// ahead/behind vs upstream, and the dirty (uncommitted + untracked) file
// set. It reuses the existing git helpers in this package (gitOut,
// aheadBehind, RepoName) — no redundant shelling out.
//
// It returns an error only when dir is not inside a git working tree;
// once that is established, missing sub-facts (no branch on a detached
// HEAD, no upstream) degrade to zero values rather than failing, so a
// best-effort caller (the session-end hook) still gets a usable row.
func CaptureSnapshot(dir string) (SnapshotData, error) {
	if !insideGitWorkTree(dir) {
		return SnapshotData{}, fmt.Errorf("productivity: %q is not a git working tree", dir)
	}

	canon := projectpath.Canonical(dir)
	d := SnapshotData{
		ProjectPath:  canon,
		RepoName:     RepoName(canon),
		WorktreePath: dir,
	}

	// Branch name; empty (or "HEAD") on a detached HEAD — left blank.
	if branch, err := gitOut(dir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil && branch != "HEAD" {
		d.Branch = branch
	}
	if head, err := gitOut(dir, "rev-parse", "HEAD"); err == nil {
		d.HeadSHA = head
	}

	d.AheadCount, d.BehindCount = aheadBehind(dir)
	d.DirtyFileCount, d.DirtyFiles = dirtyFiles(dir)
	return d, nil
}

// dirtyFiles parses `git status --porcelain` and returns the exact count
// of dirty (modified, staged, deleted, renamed, untracked) paths plus a
// list of those paths bounded to snapshotMaxDirtyFiles. A git failure
// degrades to (0, nil) — best-effort, never fatal.
func dirtyFiles(dir string) (count int, files []string) {
	// --porcelain v1 is stable: each entry is "XY <path>" (or
	// "XY <old> -> <new>" for a rename). -z terminates each entry with
	// a NUL instead of a newline, so a status line is never trimmed or
	// split incorrectly — important here because the leading column of
	// an unstaged change is a space (gitOut's TrimSpace would eat it).
	cmd := exec.Command("git", "-C", dir, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	out, err := cmd.Output()
	if err != nil {
		return 0, nil
	}
	entries := strings.Split(string(out), "\x00")
	for i := 0; i < len(entries); i++ {
		entry := entries[i]
		if len(entry) < 4 {
			continue
		}
		// Columns 0-1 are the status code, column 2 is a space; the path
		// begins at index 3. With -z a rename/copy (status R/C) emits the
		// new path here and the old path as the NEXT NUL-terminated field
		// (no status prefix) — consume and skip that field so it is not
		// double-counted as a separate dirty file.
		code := entry[0:2]
		if strings.ContainsAny(code, "RC") {
			i++
		}
		path := entry[3:]
		if path == "" {
			continue
		}
		count++
		if len(files) < snapshotMaxDirtyFiles {
			files = append(files, path)
		}
	}
	return count, files
}

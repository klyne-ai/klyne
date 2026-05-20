package productivity

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// osWriteFile is a thin indirection so tests in this package can write
// fixture files without importing os directly in every test file.
func osWriteFile(path string, b []byte, perm os.FileMode) error {
	return os.WriteFile(path, b, perm)
}

// ScanResult is the deterministic per-repo+worktree git scan output
// (spec §6.2). All facts are read straight from git — no LLM, no
// guesswork.
type ScanResult struct {
	Repo     string
	Dir      string
	Branch   string
	TicketID string
	HeadSHA  string
	Ship     ShipState
	Ahead    int
	Behind   int
	Commits  []Commit
	// AheadCommits is the actual commit list ahead of the effective
	// remote ref (short SHA + subject) — the evidence behind the Ahead
	// count, so the "unpushed" risk can show WHICH commits are not on
	// origin. Capped to aheadCommitCap, newest first. Populated by
	// ScanRepo; always non-nil.
	AheadCommits []RiskCommit
}

// ticketRe parses a raw ticket token from a branch name (D3: raw
// branch-derived string, no external lookup). Upper-cased on output.
var ticketRe = regexp.MustCompile(`(?i)[a-z]{2,}-\d+`)

// gitOut runs `git -C dir <args...>` and returns trimmed stdout. A
// non-zero exit is returned as an error with stderr attached so callers
// can distinguish "no upstream" (expected) from real failures.
func gitOut(dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", full...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// ScanRepo scans one git working directory for commits in
// [since, until], the current branch + ticket id, ahead/behind vs
// upstream, and the ship state (D2/§6.5). userEmails is the §6.3
// identity set: a commit's IsUser is true iff its author email is in
// the set. Records with no matching commits still return branch/ship
// metadata.
func ScanRepo(dir string, since, until time.Time, userEmails map[string]bool) (ScanResult, error) {
	res := ScanResult{Dir: dir, Repo: RepoName(dir)}

	branch, err := gitOut(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err == nil {
		res.Branch = branch
		if m := ticketRe.FindString(branch); m != "" {
			res.TicketID = strings.ToUpper(m)
		}
	}
	if head, err := gitOut(dir, "rev-parse", "HEAD"); err == nil {
		res.HeadSHA = head
	}

	commits, err := scanCommits(dir, since, until, userEmails)
	if err != nil {
		return res, err
	}
	res.Commits = commits

	res.Ahead, res.Behind = aheadBehind(dir)
	res.AheadCommits = aheadCommits(dir)
	res.Ship = shipState(dir, res.Ahead, res.Behind)
	return res, nil
}

// aheadCommitCap bounds how many ahead-commit references a scan carries
// — enough to make the "unpushed" risk concrete without unbounded
// payloads on a long-diverged branch.
const aheadCommitCap = 40

// aheadRef resolves the ref HEAD's "ahead" commits are measured
// against — the same precedence aheadBehind uses: the configured
// upstream (@{u}), else origin/<branch>, else the remote default
// branch. Returns "" when the repo has no usable remote ref (a
// genuinely local repo — every commit is then "ahead").
func aheadRef(dir string) string {
	if _, err := gitOut(dir, "rev-parse", "--verify", "--quiet", "@{u}"); err == nil {
		return "@{u}"
	}
	if br, err := gitOut(dir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil && br != "HEAD" {
		if _, err := gitOut(dir, "rev-parse", "--verify", "--quiet", "origin/"+br); err == nil {
			return "origin/" + br
		}
	}
	return remoteDefaultRef(dir)
}

// aheadCommits lists the commits on HEAD not on its effective remote
// ref — the evidence behind ScanResult.Ahead. Merges are included so
// the list stays consistent with the Ahead count. Capped to
// aheadCommitCap (newest first); always returns a non-nil slice.
func aheadCommits(dir string) []RiskCommit {
	spec := "HEAD"
	if ref := aheadRef(dir); ref != "" {
		spec = ref + "..HEAD"
	}
	out, err := gitOut(dir, "log", spec,
		"--max-count="+strconv.Itoa(aheadCommitCap),
		"--pretty=format:%H"+fieldSep+"%s")
	if err != nil || strings.TrimSpace(out) == "" {
		return []RiskCommit{}
	}
	commits := []RiskCommit{}
	for _, ln := range strings.Split(out, "\n") {
		f := strings.SplitN(ln, fieldSep, 2)
		if len(f) != 2 {
			continue
		}
		commits = append(commits, RiskCommit{SHA: shortSHA(f[0]), Subject: f[1]})
	}
	return commits
}

// commitSep / fieldSep are ASCII control chars unlikely to appear in a
// commit subject, used to delimit the custom git-log pretty format so
// parsing is unambiguous even with newlines in bodies.
const (
	recSep   = "\x1e" // record separator (between commits)
	fieldSep = "\x1f" // unit separator (between fields)
)

func scanCommits(dir string, since, until time.Time, userEmails map[string]bool) ([]Commit, error) {
	// recSep leads each record so numstat lines (emitted by git AFTER the
	// pretty body) stay attached to the commit they belong to. Fields:
	// %H sha, %an author, %ae email, %cI committer ISO date, %s subject.
	format := recSep + strings.Join([]string{"%H", "%an", "%ae", "%cI", "%s"}, fieldSep)
	args := []string{
		"log",
		"--no-merges",
		"--since=" + since.Format(time.RFC3339),
		"--until=" + until.Format(time.RFC3339),
		"--numstat",
		"--pretty=format:" + format,
	}
	out, err := gitOut(dir, args...)
	if err != nil {
		// No commits / unborn branch is not an error for our purposes.
		return nil, nil
	}
	if strings.TrimSpace(out) == "" {
		return nil, nil
	}

	var commits []Commit
	for _, rec := range strings.Split(out, recSep) {
		rec = strings.TrimLeft(rec, "\n")
		if strings.TrimSpace(rec) == "" {
			continue
		}
		lines := strings.Split(rec, "\n")
		fields := strings.Split(lines[0], fieldSep)
		if len(fields) < 5 {
			continue
		}
		when, _ := time.Parse(time.RFC3339, fields[3])
		c := Commit{
			SHA:         shortSHA(fields[0]),
			Author:      fields[1],
			AuthorEmail: fields[2],
			CommittedAt: when,
			Subject:     fields[4],
			IsUser:      userEmails[strings.ToLower(strings.TrimSpace(fields[2]))],
		}
		// Remaining lines are numstat rows: "<ins>\t<del>\t<path>".
		for _, ln := range lines[1:] {
			ln = strings.TrimSpace(ln)
			if ln == "" {
				continue
			}
			parts := strings.Split(ln, "\t")
			if len(parts) < 3 {
				continue
			}
			c.Files++
			c.Insertions += atoiSafe(parts[0])
			c.Deletions += atoiSafe(parts[1])
		}
		commits = append(commits, c)
	}
	return commits, nil
}

// aheadBehind returns commits ahead/behind the branch's effective
// remote counterpart. It tries, in order: the configured upstream
// tracking ref (@{u}); origin/<current-branch> (a branch pushed
// without -u has no @{u} but does have this); the remote default
// branch. Only when the repo has no origin remote at all does it fall
// back to counting HEAD's whole history as ahead (a genuinely local,
// never-pushed repo). This prevents a detached or untracked checkout
// from reporting its entire history as "unpushed".
func aheadBehind(dir string) (ahead, behind int) {
	if a, b, ok := countRange(dir, "@{u}...HEAD"); ok {
		return a, b
	}
	if br, err := gitOut(dir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil && br != "HEAD" {
		if a, b, ok := countRange(dir, "origin/"+br+"...HEAD"); ok {
			return a, b
		}
	}
	if ref := remoteDefaultRef(dir); ref != "" {
		if a, b, ok := countRange(dir, ref+"...HEAD"); ok {
			return a, b
		}
	}
	// No remote at all: a genuinely local repo — every commit is ahead.
	if out, err := gitOut(dir, "rev-list", "--count", "HEAD"); err == nil {
		return atoiSafe(out), 0
	}
	return 0, 0
}

// countRange runs `git rev-list --left-right --count <left>...<right>`
// and returns (ahead, behind, ok): ahead = commits on the right (HEAD)
// not on the left (remote/base), behind = the reverse.
func countRange(dir, spec string) (ahead, behind int, ok bool) {
	out, err := gitOut(dir, "rev-list", "--left-right", "--count", spec)
	if err != nil {
		return 0, 0, false
	}
	parts := strings.Fields(out)
	if len(parts) != 2 {
		return 0, 0, false
	}
	return atoiSafe(parts[1]), atoiSafe(parts[0]), true
}

// remoteDefaultRef resolves the repo's default remote-tracking ref
// (e.g. "origin/main"), or "" when the repo has no usable origin ref.
func remoteDefaultRef(dir string) string {
	if out, err := gitOut(dir, "symbolic-ref", "refs/remotes/origin/HEAD"); err == nil {
		return strings.TrimPrefix(out, "refs/remotes/")
	}
	for _, cand := range []string{"origin/main", "origin/master", "origin/init", "origin/develop"} {
		if _, err := gitOut(dir, "rev-parse", "--verify", "--quiet", cand); err == nil {
			return cand
		}
	}
	return ""
}

// shipState resolves the locked three-state machine (D2/§6.5):
//   - merged into default branch        → ShipMerged
//   - present on origin (has upstream)  → ShipPushed
//   - local commits, ahead>0, no remote → ShipLocal
func shipState(dir string, ahead, behind int) ShipState {
	def := defaultBranch(dir)
	if def != "" && mergedInto(dir, def) {
		return ShipMerged
	}
	if hasUpstream(dir) {
		return ShipPushed
	}
	return ShipLocal
}

// hasUpstream reports whether the current branch exists on the remote —
// via a configured upstream tracking ref, OR as origin/<branch> even
// when no local tracking ref is set (a branch pushed without -u).
func hasUpstream(dir string) bool {
	if _, err := gitOut(dir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"); err == nil {
		return true
	}
	if br, err := gitOut(dir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil && br != "HEAD" {
		if _, err := gitOut(dir, "rev-parse", "--verify", "--quiet", "origin/"+br); err == nil {
			return true
		}
	}
	return false
}

// defaultBranch resolves the repo's default branch robustly (spec §10):
// origin/HEAD symbolic-ref first, then main, then master, then the
// current branch as a last resort (covers klyne's own `init` default).
func defaultBranch(dir string) string {
	if out, err := gitOut(dir, "symbolic-ref", "refs/remotes/origin/HEAD"); err == nil {
		return strings.TrimPrefix(out, "refs/remotes/origin/")
	}
	for _, cand := range []string{"main", "master"} {
		if _, err := gitOut(dir, "rev-parse", "--verify", "--quiet", cand); err == nil {
			return cand
		}
	}
	if cur, err := gitOut(dir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		return cur
	}
	return ""
}

// mergedInto reports whether HEAD is an ancestor of def AND HEAD is not
// def itself (being on the default branch is not "merged into" it).
func mergedInto(dir, def string) bool {
	cur, _ := gitOut(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if cur == def {
		return false
	}
	cmd := exec.Command("git", "-C", dir, "merge-base", "--is-ancestor", "HEAD", def)
	return cmd.Run() == nil
}

// RepoName is the canonical repo display name (the basename of the dir).
func RepoName(dir string) string {
	base := filepath.Base(strings.TrimRight(dir, string(filepath.Separator)))
	if base == "." || base == "/" || base == "" {
		return dir
	}
	return base
}

func shortSHA(full string) string {
	if len(full) >= 8 {
		return full[:8]
	}
	return full
}

func atoiSafe(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0 // "-" for binary files in numstat
	}
	return n
}

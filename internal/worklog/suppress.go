package worklog

import (
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	trivialWallTime = 90 * time.Second
	trivialToolMin  = 5
	maxFiles        = 25
)

// substantialSummaryMin is the minimum AIDraftedSummary length we treat as
// objective evidence of real work. Any per-turn summary that mentions a
// CLI-NNNN ticket / commit SHA / PR # / feature branch already qualifies
// regardless of length; this length floor exists for summaries that name
// concrete files or behavior without those structured tokens.
const substantialSummaryMin = 80

// ticketRe / commitShaRe / prRefRe / featureBranchRe identify objective
// "real engineering work happened" tokens in the per-turn AI summary.
// Presence of ANY of these in AIDraftedSummary is treated as a hard signal
// that bypasses the read-only / trivial-size / require-signal filters —
// these tokens cannot appear by accident in tool-only or read-only turns.
var (
	ticketRe        = regexp.MustCompile(`\bCLI-\d+\b`)
	commitShaLikeRe = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)
	prRefLikeRe     = regexp.MustCompile(`\bPR\s*#\d+\b`)
	featureBranchRe = regexp.MustCompile(`\b(?:feature|fix|hotfix|chore|refactor)/[A-Za-z0-9._-]+\b`)
)

// hasSubstantialSummary returns true when the per-turn AIDraftedSummary
// carries an objective signal of real work — a ticket id, commit SHA,
// PR reference, feature branch name, or simply enough prose (≥80 chars
// non-whitespace) describing concrete actions. The signal is INDEPENDENT
// of file edits / tool counts / wall time: an investigation turn that
// names the file path and root cause is real work even if the model
// only ran grep + read.
func hasSubstantialSummary(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if ticketRe.MatchString(s) ||
		commitShaLikeRe.MatchString(s) ||
		prRefLikeRe.MatchString(s) ||
		featureBranchRe.MatchString(s) {
		return true
	}
	return len(s) >= substantialSummaryMin
}

func ruleSkipTrivialSize(e Entry) (bool, string) {
	if e.CommitSHA != "" {
		return false, ""
	}
	// A substantial per-turn summary overrides the trivial-size gate: a
	// 30-second turn that produced a real investigation finding or named
	// a ticket id is not trivial just because the wall time was short.
	if hasSubstantialSummary(e.AIDraftedSummary) {
		return false, ""
	}
	if e.WallTime < trivialWallTime && e.ToolCallCount < trivialToolMin {
		return true, "skip_trivial_size"
	}
	return false, ""
}

// readOnlyBashAllow lists bash prefixes that, on their own, prove the turn
// produced work even without an Edit/Write tool call in this particular
// turn (the actual edit may have happened earlier in the session). git
// push / rebase / merge / revert MUST be on this list — a commit-then-push
// flow often lands the push in a separate turn after the file Edit, and
// that push turn carries the commit SHA in the AI summary but no Edit tool
// call. Dropping it loses the most important "shipped" signal of the day.
var readOnlyBashAllow = []string{
	"git commit",
	"git push",
	"git rebase",
	"git merge",
	"git revert",
	"git cherry-pick",
	"gh pr create",
	"gh ",
	"npm test",
	"go test",
	"pytest",
	"cargo test",
	"make ",
	"npm run build",
	"yarn build",
}

func ruleSkipReadOnly(e Entry) (bool, string) {
	if e.EditWriteCount > 0 {
		return false, ""
	}
	// A substantial AI summary (ticket / SHA / PR / branch / ≥80 chars)
	// overrides the read-only gate: investigations, reviews, and trace-
	// throughs are real work even when no Edit tool call fires.
	if hasSubstantialSummary(e.AIDraftedSummary) {
		return false, ""
	}
	cmd := strings.TrimSpace(e.LastBash)
	for _, p := range readOnlyBashAllow {
		if strings.HasPrefix(cmd, p) {
			return false, ""
		}
	}
	return true, "skip_read_only"
}

var routineCommitPrefix = regexp.MustCompile(`(?i)^(chore|style|fmt|lint|format)(\(|:)`)

func ruleSkipDismissedSignature(_ Entry, sig string, dismissed map[string]bool) (bool, string) {
	if dismissed[sig] {
		return true, "skip_dismissed_signature"
	}
	return false, ""
}

func ruleRequireSignal(e Entry) (bool, string) {
	if e.Has(TagExplicitUserLog) || e.CommitSHA != "" {
		return false, ""
	}
	for _, t := range []EventTag{TagDecisionRecorded, TagRunbookAccepted, TagFileSignificantlyEdited, TagPROpened} {
		if e.Has(t) {
			return false, ""
		}
	}
	// A substantial AI summary counts as a signal in its own right. The
	// per-turn summary is the assistant's verbatim claim of what landed;
	// when it names a ticket / SHA / PR / branch or runs to ≥80 chars of
	// concrete prose, that IS the evidence the require-signal rule was
	// looking for.
	if hasSubstantialSummary(e.AIDraftedSummary) {
		return false, ""
	}
	return true, "require_signal"
}

var lockfiles = map[string]bool{
	"package-lock.json": true, "pnpm-lock.yaml": true, "yarn.lock": true,
	"go.sum": true, "Cargo.lock": true, "poetry.lock": true,
}

var irrelevantRoots = []string{"node_modules/", ".git/", "dist/", "build/", "target/",
	"__pycache__/", ".next/", ".svelte-kit/", ".venv/", "vendor/"}

func FilterFiles(files []string) (kept []string, depLockTouched bool) {
	seen := map[string]bool{}
	for _, f := range files {
		if lockfiles[filepath.Base(f)] {
			depLockTouched = true
			continue
		}
		skip := false
		for _, r := range irrelevantRoots {
			if strings.Contains(f, r) {
				skip = true
				break
			}
		}
		if skip || seen[f] {
			continue
		}
		seen[f] = true
		kept = append(kept, f)
	}
	if len(kept) > maxFiles {
		kept = kept[:maxFiles]
	}
	return
}

func ShouldSuppress(e Entry, sig string, dismissed map[string]bool) (bool, string) {
	if dismissed == nil {
		dismissed = map[string]bool{}
	}
	for _, rule := range []func(Entry) (bool, string){
		ruleSkipTrivialSize, ruleSkipReadOnly, ruleRequireSignal,
	} {
		if drop, reason := rule(e); drop {
			return true, reason
		}
	}
	if drop, reason := ruleSkipDismissedSignature(e, sig, dismissed); drop {
		return true, reason
	}
	return false, ""
}

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

func ruleSkipTrivialSize(e Entry) (bool, string) {
	if e.CommitSHA == "" && e.WallTime < trivialWallTime && e.ToolCallCount < trivialToolMin {
		return true, "skip_trivial_size"
	}
	return false, ""
}

func ruleSkipReadOnly(e Entry) (bool, string) {
	if e.EditWriteCount > 0 {
		return false, ""
	}
	cmd := strings.TrimSpace(e.LastBash)
	for _, p := range []string{"git commit", "gh ", "npm test", "go test", "pytest", "cargo test", "make ", "npm run build", "yarn build"} {
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

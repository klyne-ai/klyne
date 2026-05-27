package worklog

import (
	"testing"
	"time"
)

func TestRuleSkipTrivialSize(t *testing.T) {
	if drop, _ := ruleSkipTrivialSize(Entry{WallTime: 60 * time.Second, ToolCallCount: 3}); !drop {
		t.Errorf("trivial entry must drop")
	}
	if drop, _ := ruleSkipTrivialSize(Entry{WallTime: 60 * time.Second, ToolCallCount: 3, CommitSHA: "abc"}); drop {
		t.Errorf("commit lifts out of trivial")
	}
	// Substantial AI summary (ticket id) lifts out of trivial — a 30-second
	// turn that produced a CLI-1340 investigation is real work.
	ticketLift := Entry{
		WallTime:         30 * time.Second,
		ToolCallCount:    2,
		AIDraftedSummary: "Traced CLI-1340 dropdown empty-state to subscription-service members[] data shape mismatch.",
	}
	if drop, reason := ruleSkipTrivialSize(ticketLift); drop {
		t.Errorf("substantial summary with CLI-NNNN must lift trivial gate; dropped: %s", reason)
	}
}

func TestRuleSkipReadOnly(t *testing.T) {
	if drop, _ := ruleSkipReadOnly(Entry{}); !drop {
		t.Errorf("empty must drop")
	}
	if drop, _ := ruleSkipReadOnly(Entry{EditWriteCount: 2}); drop {
		t.Errorf("edits must pass")
	}
	if drop, _ := ruleSkipReadOnly(Entry{LastBash: "git commit -m foo"}); drop {
		t.Errorf("git commit must pass")
	}
	// Regression: a commit-then-push flow where the push lands in its own
	// turn (LastBash="git push", no Edit tool call in that turn) MUST pass.
	// Pre-fix, this dropped — silently losing the actual ship signal of
	// the day. See operations-app 2026-05-26 15:31:53 (commit e88e57a0).
	if drop, reason := ruleSkipReadOnly(Entry{LastBash: "git push"}); drop {
		t.Errorf("git push (separate turn) must pass; dropped: %s", reason)
	}
	if drop, _ := ruleSkipReadOnly(Entry{LastBash: "git rebase origin/main"}); drop {
		t.Errorf("git rebase must pass")
	}
	// Substantial AI summary with a commit SHA shape overrides read-only
	// even when no Edit tool call ran and bash isn't in the allow list.
	investigationLift := Entry{
		LastBash:         "grep -rn ORDER_CANCELLATION_POLL_MS src/",
		AIDraftedSummary: "Confirmed PR #428 covers CLI-1340 and the poll interval lives in ItemManager.tsx:38; commit e88e57a0 is the latest landed change.",
	}
	if drop, reason := ruleSkipReadOnly(investigationLift); drop {
		t.Errorf("substantial summary must lift read-only gate; dropped: %s", reason)
	}
}

func TestRuleRequireSignal(t *testing.T) {
	if drop, _ := ruleRequireSignal(Entry{}); !drop {
		t.Errorf("no signal must drop")
	}
	if drop, _ := ruleRequireSignal(Entry{CommitSHA: "abc"}); drop {
		t.Errorf("commit is signal")
	}
	if drop, _ := ruleRequireSignal(Entry{EventTags: []EventTag{TagDecisionRecorded}}); drop {
		t.Errorf("decision is signal")
	}
	if drop, _ := ruleRequireSignal(Entry{EventTags: []EventTag{TagExplicitUserLog}}); drop {
		t.Errorf("explicit always passes")
	}
	// AI summary mentioning a ticket id is a signal on its own — the
	// hook captured a real engineering claim, no event tag required.
	if drop, _ := ruleRequireSignal(Entry{AIDraftedSummary: "Investigated CLI-1340 root cause."}); drop {
		t.Errorf("ticket id in AI summary is a signal")
	}
	if drop, _ := ruleRequireSignal(Entry{AIDraftedSummary: "Confirmed feature/CLI-1452-followup-cta-banner is rebased; PR #432 open."}); drop {
		t.Errorf("branch + PR ref is a signal")
	}
}

func TestHasSubstantialSummary(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		{"too short", "did a thing", false},
		{"ticket id", "Looked at CLI-1473", true},
		{"commit sha", "Landed e88e57a0", true},
		{"pr ref", "Opened PR #432 as follow-up", true},
		{"feature branch", "Worked on feature/CLI-1340-cancelled-bill", true},
		{"long prose without tokens", "Worked through the customer dropdown bug this morning carefully checking each route and helper before deciding to skip the workaround.", true},
	}
	for _, c := range cases {
		if got := hasSubstantialSummary(c.in); got != c.want {
			t.Errorf("%s: hasSubstantialSummary(%q)=%v, want %v", c.name, c.in, got, c.want)
		}
	}
}

func TestFilterFiles(t *testing.T) {
	files := []string{"src/a.go", "node_modules/x/y.js", "package-lock.json", "src/a.go"}
	kept, depLock := FilterFiles(files)
	if len(kept) != 1 || kept[0] != "src/a.go" {
		t.Errorf("expected just src/a.go, got %v", kept)
	}
	if !depLock {
		t.Errorf("expected depLockTouched=true")
	}
}

func TestShouldSuppressAggregator(t *testing.T) {
	if drop, _ := ShouldSuppress(Entry{}, "", nil); !drop {
		t.Errorf("empty entry must suppress")
	}
	substantive := Entry{
		CommitSHA: "abc", EditWriteCount: 3, WallTime: 5 * time.Minute,
		ToolCallCount: 20, Files: []string{"x.go"},
		EventTags: []EventTag{TagCommitLanded, TagFileSignificantlyEdited},
	}
	if drop, reason := ShouldSuppress(substantive, "sig", nil); drop {
		t.Errorf("substantive must pass; got %s", reason)
	}
}

func BenchmarkShouldSuppress(b *testing.B) {
	e := Entry{CommitSHA: "abc", EditWriteCount: 5, WallTime: 5 * time.Minute,
		ToolCallCount: 20, Files: []string{"x.go"},
		EventTags: []EventTag{TagCommitLanded}}
	for i := 0; i < b.N; i++ {
		_, _ = ShouldSuppress(e, "sig", nil)
	}
}

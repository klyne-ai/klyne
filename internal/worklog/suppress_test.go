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

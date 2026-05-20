package richentry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/ai"
)

// --- HeuristicGate --------------------------------------------------------
//
// Per the plan, admit on real work, deny on noise, defer the rest:
//
//	ADMIT
//	  - >=1 user commit with (>=10 lines changed OR >=1 non-doc file), OR
//	  - active duration >=15 min AND >=3 user messages AND (decision|bug|blocker keyword present)
//	DENY
//	  - active duration <3 min, OR
//	  - (0 commits AND <3 messages AND <5 min duration), OR
//	  - (>=1 commit AND <10 lines changed AND 0 non-doc files)  -- doc-only typo fixes
//	BORDERLINE
//	  - everything else; the LLM tiebreak decides.

func TestHeuristic_AdmitByCommitWithLines(t *testing.T) {
	m := SessionMetrics{UserCommits: 1, LinesChanged: 50, NonDocFilesTouched: 0}
	if got := HeuristicGate(m); got != HeuristicAdmit {
		t.Errorf("got %v, want admit", got)
	}
}

func TestHeuristic_AdmitByCommitWithNonDocFile(t *testing.T) {
	// Even a tiny commit admits if it touches a non-doc file (real work).
	m := SessionMetrics{UserCommits: 1, LinesChanged: 2, NonDocFilesTouched: 1}
	if got := HeuristicGate(m); got != HeuristicAdmit {
		t.Errorf("got %v, want admit", got)
	}
}

func TestHeuristic_AdmitByDurationAndKeyword(t *testing.T) {
	m := SessionMetrics{
		ActiveDuration: 20 * time.Minute,
		UserMessages:   4,
		HasBugKeyword:  true,
	}
	if got := HeuristicGate(m); got != HeuristicAdmit {
		t.Errorf("got %v, want admit", got)
	}
}

func TestHeuristic_LongChatWithoutKeyword_IsBorderline(t *testing.T) {
	// Plenty of discussion but no decision/bug/blocker signal → defer to LLM.
	m := SessionMetrics{ActiveDuration: 20 * time.Minute, UserMessages: 4}
	if got := HeuristicGate(m); got != HeuristicBorderline {
		t.Errorf("got %v, want borderline", got)
	}
}

func TestHeuristic_DenyVeryShort(t *testing.T) {
	m := SessionMetrics{ActiveDuration: 2 * time.Minute, UserMessages: 1}
	if got := HeuristicGate(m); got != HeuristicDeny {
		t.Errorf("got %v, want deny", got)
	}
}

func TestHeuristic_DenyNoSubstance(t *testing.T) {
	// No commits, 2 user msgs, 4 min — drive-by.
	m := SessionMetrics{ActiveDuration: 4 * time.Minute, UserMessages: 2}
	if got := HeuristicGate(m); got != HeuristicDeny {
		t.Errorf("got %v, want deny", got)
	}
}

func TestHeuristic_DenyDocOnlyTypoFix(t *testing.T) {
	// 1 commit, <10 lines, 0 non-doc files — typo fix on a README.
	m := SessionMetrics{UserCommits: 1, LinesChanged: 5, NonDocFilesTouched: 0}
	if got := HeuristicGate(m); got != HeuristicDeny {
		t.Errorf("got %v, want deny", got)
	}
}

func TestHeuristic_DefaultBorderline(t *testing.T) {
	// 10 min, 2 messages, no commits — could go either way.
	m := SessionMetrics{ActiveDuration: 10 * time.Minute, UserMessages: 2}
	if got := HeuristicGate(m); got != HeuristicBorderline {
		t.Errorf("got %v, want borderline", got)
	}
}

// --- Decide (full gate: heuristic first, LLM tiebreak on borderline) ------

type fakeAI struct {
	reply  string
	err    error
	called bool
}

func (f *fakeAI) Chat(_ context.Context, _ ai.ChatRequest) (*ai.ChatResponse, error) {
	f.called = true
	if f.err != nil {
		return nil, f.err
	}
	return &ai.ChatResponse{Text: f.reply}, nil
}

func TestDecide_HeuristicAdmit_DoesNotCallLLM(t *testing.T) {
	ai := &fakeAI{reply: `{"worklog_worthy": false}`}
	verdict, admit := Decide(context.Background(), ai,
		SessionMetrics{UserCommits: 1, LinesChanged: 50, NonDocFilesTouched: 1},
		"unused prose")
	if !admit || verdict != "admitted-heuristic" {
		t.Errorf("verdict=%q admit=%v", verdict, admit)
	}
	if ai.called {
		t.Error("LLM was called even though heuristic admitted")
	}
}

func TestDecide_HeuristicDeny_DoesNotCallLLM(t *testing.T) {
	ai := &fakeAI{reply: `{"worklog_worthy": true}`}
	verdict, admit := Decide(context.Background(), ai,
		SessionMetrics{ActiveDuration: 2 * time.Minute},
		"unused prose")
	if admit || verdict != "skipped-heuristic" {
		t.Errorf("verdict=%q admit=%v", verdict, admit)
	}
	if ai.called {
		t.Error("LLM was called even though heuristic denied")
	}
}

func TestDecide_BorderlineLLMAdmits(t *testing.T) {
	ai := &fakeAI{reply: `{"worklog_worthy": true}`}
	verdict, admit := Decide(context.Background(), ai,
		SessionMetrics{ActiveDuration: 10 * time.Minute, UserMessages: 2},
		"found refund bug, didn't commit yet")
	if !admit || verdict != "admitted-llm" {
		t.Errorf("verdict=%q admit=%v", verdict, admit)
	}
	if !ai.called {
		t.Error("LLM should have been called for borderline")
	}
}

func TestDecide_BorderlineLLMDenies(t *testing.T) {
	ai := &fakeAI{reply: `{"worklog_worthy": false}`}
	verdict, admit := Decide(context.Background(), ai,
		SessionMetrics{ActiveDuration: 10 * time.Minute, UserMessages: 2},
		"just chatting")
	if admit || verdict != "skipped-llm" {
		t.Errorf("verdict=%q admit=%v", verdict, admit)
	}
}

func TestDecide_BorderlineLLMError_DefaultsToSkip(t *testing.T) {
	// LLM transient failure on a borderline turn — safe default is to
	// skip rather than admit. The Phase 5 worker may re-enqueue based
	// on attempts < MAX (that decision belongs to the worker, not the
	// gate); the gate itself never returns "pending".
	ai := &fakeAI{err: errors.New("network down")}
	verdict, admit := Decide(context.Background(), ai,
		SessionMetrics{ActiveDuration: 10 * time.Minute, UserMessages: 2},
		"borderline")
	if admit || verdict != "skipped-llm" {
		t.Errorf("verdict=%q admit=%v", verdict, admit)
	}
}

func TestDecide_BorderlineMalformedLLMReply_DefaultsToSkip(t *testing.T) {
	// "yes I will write a worthy entry" — not parseable as the
	// expected JSON. Default to skip.
	ai := &fakeAI{reply: "I think yes."}
	verdict, _ := Decide(context.Background(), ai,
		SessionMetrics{ActiveDuration: 10 * time.Minute, UserMessages: 2},
		"borderline")
	if verdict != "skipped-llm" {
		t.Errorf("verdict=%q", verdict)
	}
}

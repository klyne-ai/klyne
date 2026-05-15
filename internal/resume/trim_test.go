package resume

import (
	"strings"
	"testing"
)

// makeTurn is a helper for building test turns.
func makeTurn(role, content string, opts ...func(*Turn)) Turn {
	t := Turn{Role: role, Content: content}
	for _, o := range opts {
		o(&t)
	}
	return t
}

func withDecision(ts int64) func(*Turn) {
	return func(t *Turn) { t.IsDecision = true; t.TsMs = ts }
}
func withTs(ts int64) func(*Turn) { return func(t *Turn) { t.TsMs = ts } }
func withToolResult(output string) func(*Turn) {
	return func(t *Turn) { t.ToolResultOutput = output }
}
func withFilePaths(paths ...string) func(*Turn) {
	return func(t *Turn) { t.FilePaths = paths }
}

// TestTrim_BudgetAlwaysRespected is the core property: after Trim, estimated
// tokens must be <= budget for any valid payload.
func TestTrim_BudgetAlwaysRespected(t *testing.T) {
	payload := Payload{
		Turns: []Turn{
			makeTurn("user", strings.Repeat("a", 4000)),
			makeTurn("assistant", strings.Repeat("b", 4000)),
			makeTurn("user", strings.Repeat("c", 4000)),
			makeTurn("assistant", strings.Repeat("d", 4000), withDecision(1000)),
			makeTurn("tool", strings.Repeat("e", 8000), withToolResult(strings.Repeat("e", 8000))),
		},
		Decisions: []string{"use postgres", "never mutate shared state"},
	}

	budgets := []int{200, 500, 1000, 2000}
	for _, budget := range budgets {
		t.Run("", func(t *testing.T) {
			res := Trim(payload, budget)
			if res.EstimatedTokens > budget {
				t.Errorf("budget=%d: got EstimatedTokens=%d (exceeded)", budget, res.EstimatedTokens)
			}
		})
	}
}

// TestTrim_NoBudget checks that a 0/negative budget returns the payload unchanged.
func TestTrim_NoBudget(t *testing.T) {
	payload := Payload{
		Turns:     []Turn{makeTurn("user", "hello")},
		Decisions: []string{"a decision"},
	}
	res := Trim(payload, 0)
	if len(res.Payload.Turns) != 1 {
		t.Errorf("expected 1 turn, got %d", len(res.Payload.Turns))
	}
	if res.CutSummary != "" {
		t.Errorf("expected no cut summary for unlimited budget, got %q", res.CutSummary)
	}
}

// TestTrim_FitsWithinBudget checks that a payload already under budget is not modified.
func TestTrim_FitsWithinBudget(t *testing.T) {
	payload := Payload{
		Turns:     []Turn{makeTurn("user", "hello")},
		Decisions: []string{"a"},
	}
	res := Trim(payload, 100000)
	if res.CutSummary != "" {
		t.Errorf("expected no cuts for already-fit payload, got %q", res.CutSummary)
	}
}

// TestTrim_DecisionsNeverCutFirst verifies that decision turns survive
// through cuts 1-4; only cut 5 may remove them.
func TestTrim_DecisionsNeverCutFirst(t *testing.T) {
	// Large non-decision turns + one decision turn. With a tight budget,
	// the non-decision turns should be cut before the decision turn.
	bigContent := strings.Repeat("x", 10000) // ~2500 tokens

	payload := Payload{
		Turns: []Turn{
			makeTurn("user", bigContent, withTs(100)),
			makeTurn("assistant", bigContent, withTs(200)),
			makeTurn("assistant", "use postgres for all writes", withDecision(300)),
		},
	}

	// Budget is ~300 tokens — just enough for the decision turn.
	res := Trim(payload, 300)

	// The decision must still be present.
	foundDecision := false
	for _, turn := range res.Payload.Turns {
		if turn.IsDecision && strings.Contains(turn.Content, "use postgres") {
			foundDecision = true
		}
	}
	if !foundDecision {
		t.Error("decision turn was cut before non-decision turns — violates invariant")
	}
}

// TestTrim_Cut1_PreDecisionTurns verifies that turns older than the most
// recent decision are dropped in cut 1.
func TestTrim_Cut1_PreDecisionTurns(t *testing.T) {
	bigOld := strings.Repeat("z", 8000) // ~2000 tokens
	payload := Payload{
		Turns: []Turn{
			makeTurn("user", bigOld, withTs(50)),      // pre-decision, should be cut
			makeTurn("assistant", bigOld, withTs(100)), // pre-decision, should be cut
			makeTurn("assistant", "decision content", withDecision(200)),
			makeTurn("user", "post-decision", withTs(300)),
		},
	}

	res := Trim(payload, 500)

	// Old turns (ts=50, ts=100) should be gone.
	for _, turn := range res.Payload.Turns {
		if turn.TsMs == 50 || turn.TsMs == 100 {
			t.Errorf("pre-decision turn with ts=%d should have been cut", turn.TsMs)
		}
	}
	if res.CutSummary == "" {
		t.Error("expected a cut summary describing what was dropped")
	}
}

// TestTrim_Cut2_LargeToolResults checks that tool results over 500 tokens
// are replaced with a placeholder.
func TestTrim_Cut2_LargeToolResults(t *testing.T) {
	largeOutput := strings.Repeat("y", 4000) // > 500 tokens
	payload := Payload{
		Turns: []Turn{
			makeTurn("user", "run the test suite"),
			makeTurn("tool", "", withToolResult(largeOutput)),
		},
	}

	res := Trim(payload, 500)
	for _, turn := range res.Payload.Turns {
		if strings.Contains(turn.ToolResultOutput, strings.Repeat("y", 100)) {
			t.Error("large tool result was not trimmed")
		}
	}
}

// TestTrim_CutSummaryPopulated checks that a non-empty CutSummary is
// returned whenever cuts are applied.
func TestTrim_CutSummaryPopulated(t *testing.T) {
	payload := Payload{
		Turns: []Turn{
			makeTurn("user", strings.Repeat("q", 20000)),
			makeTurn("assistant", strings.Repeat("r", 20000)),
		},
	}
	res := Trim(payload, 100)
	if res.CutSummary == "" {
		t.Error("expected non-empty CutSummary when cuts were applied")
	}
}

// TestTrim_EmptyPayload checks that Trim does not panic on empty input.
func TestTrim_EmptyPayload(t *testing.T) {
	res := Trim(Payload{}, 1000)
	if res.EstimatedTokens != 0 {
		t.Errorf("empty payload: expected 0 tokens, got %d", res.EstimatedTokens)
	}
}

// TestEstimateTokens exercises the standalone estimator helper.
func TestEstimateTokens(t *testing.T) {
	// 400 chars → 100 tokens.
	text := strings.Repeat("a", 400)
	got := EstimateTokens(text)
	if got != 100 {
		t.Errorf("EstimateTokens(400 chars) = %d, want 100", got)
	}

	if EstimateTokens("") != 0 {
		t.Error("EstimateTokens(\"\") should be 0")
	}
}

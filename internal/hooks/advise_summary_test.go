package hooks

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestKlyneSummaryInstructionConstant asserts the instruction text is
// well-formed and contains the literal tokens the Stop hook's
// extractor expects. If either side drifts, this fails before any
// real session has a chance to miss summaries.
func TestKlyneSummaryInstructionConstant(t *testing.T) {
	if !strings.Contains(klyneSummaryInstruction, "KLYNE_SUMMARY:") {
		t.Fatal("instruction must contain literal KLYNE_SUMMARY: token")
	}
	if !strings.Contains(klyneSummaryInstruction, "skip") {
		t.Fatal("instruction must mention skip semantics")
	}
	// The instruction must be safe to JSON-encode as a string (no
	// control characters that would break additionalContext on the
	// wire).
	b, err := json.Marshal(klyneSummaryInstruction)
	if err != nil {
		t.Fatalf("instruction not JSON-safe: %v", err)
	}
	if len(b) < 50 {
		t.Fatalf("instruction unexpectedly short: %s", string(b))
	}
}

// TestAdviseHookPayloadShape encodes a sample advise output exactly
// the way computeAdvisory does and validates the wire contract:
// hookSpecificOutput.additionalContext MUST carry the KLYNE_SUMMARY
// instruction so every UserPromptSubmit injects it. When the advisor
// has its own line to emit, both must be present (advisor first).
func TestAdviseHookPayloadShape(t *testing.T) {
	for _, tc := range []struct {
		name         string
		advisoryLine string
		wantAdvisor  bool
	}{
		{"no-advisor", "", false},
		{"with-advisor", "klyne: example warning", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			context := klyneSummaryInstruction
			if tc.advisoryLine != "" {
				context = tc.advisoryLine + "\n\n" + context
			}

			body, err := json.Marshal(adviseHookOutput{
				HookSpecificOutput: adviseHookSpecificOutput{
					HookEventName:     "UserPromptSubmit",
					AdditionalContext: context,
				},
			})
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			var got adviseHookOutput
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}

			if got.HookSpecificOutput.HookEventName != "UserPromptSubmit" {
				t.Errorf("hookEventName = %q; want UserPromptSubmit", got.HookSpecificOutput.HookEventName)
			}
			if !strings.Contains(got.HookSpecificOutput.AdditionalContext, "KLYNE_SUMMARY:") {
				t.Errorf("additionalContext missing KLYNE_SUMMARY instruction: %q", got.HookSpecificOutput.AdditionalContext)
			}
			if tc.wantAdvisor && !strings.Contains(got.HookSpecificOutput.AdditionalContext, tc.advisoryLine) {
				t.Errorf("expected advisor line %q in additionalContext", tc.advisoryLine)
			}
		})
	}
}

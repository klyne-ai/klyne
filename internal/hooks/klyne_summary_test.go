package hooks

import (
	"strings"
	"testing"

	"github.com/klyne-ai/klyne/internal/connectors"
)

// TestExtractKlyneSummary covers the parser the Stop hook uses to pull
// the assistant's per-turn `KLYNE_SUMMARY: ...` line out of the last
// reply. The UserPromptSubmit hook injects the instruction; the
// assistant follows it; sessionend.go writes the captured text to
// stop_summaries.ai_drafted_summary. No LM call anywhere on this path.
func TestExtractKlyneSummary(t *testing.T) {
	tests := []struct {
		name string
		msgs []*connectors.Message
		want string
	}{
		{
			name: "single assistant message ending with KLYNE_SUMMARY",
			msgs: []*connectors.Message{
				{Role: connectors.RoleUser, Content: "Hi"},
				{Role: connectors.RoleAssistant, Content: "Done.\n\nKLYNE_SUMMARY: Added foo to bar.go and ran tests; all green."},
			},
			want: "Added foo to bar.go and ran tests; all green.",
		},
		{
			name: "skip token returns empty",
			msgs: []*connectors.Message{
				{Role: connectors.RoleAssistant, Content: "Nothing actionable.\nKLYNE_SUMMARY: skip"},
			},
			want: "",
		},
		{
			name: "skip is case-insensitive",
			msgs: []*connectors.Message{
				{Role: connectors.RoleAssistant, Content: "KLYNE_SUMMARY: SKIP"},
			},
			want: "",
		},
		{
			name: "missing line returns empty",
			msgs: []*connectors.Message{
				{Role: connectors.RoleAssistant, Content: "Did some work but forgot the summary line."},
			},
			want: "",
		},
		{
			name: "newer assistant message wins over older",
			msgs: []*connectors.Message{
				{Role: connectors.RoleAssistant, Content: "KLYNE_SUMMARY: First turn."},
				{Role: connectors.RoleUser, Content: "more"},
				{Role: connectors.RoleAssistant, Content: "Second turn done.\nKLYNE_SUMMARY: Second turn."},
			},
			want: "Second turn.",
		},
		{
			name: "leading whitespace ignored",
			msgs: []*connectors.Message{
				{Role: connectors.RoleAssistant, Content: "   KLYNE_SUMMARY:   Trimmed."},
			},
			want: "Trimmed.",
		},
		{
			name: "trailing whitespace trimmed",
			msgs: []*connectors.Message{
				{Role: connectors.RoleAssistant, Content: "KLYNE_SUMMARY: Trim me.   "},
			},
			want: "Trim me.",
		},
		{
			name: "tool-only assistant turn skipped, prior text turn matched",
			msgs: []*connectors.Message{
				{Role: connectors.RoleAssistant, Content: "Wrote x.\nKLYNE_SUMMARY: From earlier."},
				{Role: connectors.RoleAssistant, Content: ""}, // tool-call-only turn, no text
			},
			want: "From earlier.",
		},
		{
			name: "very long capture truncated to klyneSummaryMaxLen",
			msgs: []*connectors.Message{
				{Role: connectors.RoleAssistant, Content: "KLYNE_SUMMARY: " + strings.Repeat("x", klyneSummaryMaxLen+200)},
			},
			want: strings.Repeat("x", klyneSummaryMaxLen),
		},
		{
			name: "multiple KLYNE_SUMMARY lines in same turn — last wins",
			msgs: []*connectors.Message{
				{Role: connectors.RoleAssistant, Content: "KLYNE_SUMMARY: draft\nactually:\nKLYNE_SUMMARY: final"},
			},
			want: "final",
		},
		{
			name: "must be start-of-line; inline mentions are ignored",
			msgs: []*connectors.Message{
				{Role: connectors.RoleAssistant, Content: "as a passing mention KLYNE_SUMMARY: not-this is fine, but no real line follows."},
			},
			want: "",
		},
		{
			name: "empty message list",
			msgs: nil,
			want: "",
		},
		{
			name: "no assistant messages at all",
			msgs: []*connectors.Message{
				{Role: connectors.RoleUser, Content: "alone"},
			},
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractKlyneSummary(tc.msgs)
			if got != tc.want {
				t.Errorf("extractKlyneSummary = %q; want %q", got, tc.want)
			}
		})
	}
}

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

		// --- Recap-leakage guard (new) ---------------------------------
		// Claude Code's per-turn recap generator runs as a separate model
		// call, picks up the same UserPromptSubmit instruction klyne
		// injects, and emits `KLYNE_SUMMARY: skip` because the recap
		// turn is trivial. That message lands AFTER the user's real
		// assistant reply in the transcript, with no user message
		// between them. The naive last-wins parser then prefers the
		// recap's skip over the user's authoritative summary, silently
		// losing it from stop_summaries.ai_drafted_summary.
		//
		// Fix: when consecutive assistant messages span a real summary
		// followed by skip(s), the real summary wins. A user message
		// resets the scope — a skip AFTER a new user prompt is an
		// intentional skip for that new turn, not a recap artifact.
		{
			name: "recap leakage: real summary followed by recap skip — real wins",
			msgs: []*connectors.Message{
				{Role: connectors.RoleUser, Content: "do work"},
				{Role: connectors.RoleAssistant, Content: "Did it.\n\nKLYNE_SUMMARY: Did the thing."},
				{Role: connectors.RoleAssistant, Content: "* recap: paragraph from claude code\nKLYNE_SUMMARY: skip"},
			},
			want: "Did the thing.",
		},
		{
			name: "recap leakage: multiple consecutive skips walk back to real",
			msgs: []*connectors.Message{
				{Role: connectors.RoleAssistant, Content: "Did it.\nKLYNE_SUMMARY: Real summary."},
				{Role: connectors.RoleAssistant, Content: "KLYNE_SUMMARY: skip"},
				{Role: connectors.RoleAssistant, Content: "KLYNE_SUMMARY: skip"},
			},
			want: "Real summary.",
		},
		{
			name: "skip after a user message is intentional (not recap leakage)",
			msgs: []*connectors.Message{
				{Role: connectors.RoleAssistant, Content: "KLYNE_SUMMARY: Old turn real."},
				{Role: connectors.RoleUser, Content: "ok now do something trivial"},
				{Role: connectors.RoleAssistant, Content: "Nothing actionable.\nKLYNE_SUMMARY: skip"},
			},
			want: "",
		},
		{
			name: "recap leakage: missing-summary message between real and skip still resolves to real",
			// The middle message has neither real summary nor explicit
			// skip — but we still walk past it to find the real one.
			// Without this, a future Claude Code recap that doesn't
			// emit KLYNE_SUMMARY at all would shadow the real summary.
			msgs: []*connectors.Message{
				{Role: connectors.RoleAssistant, Content: "KLYNE_SUMMARY: The real one."},
				{Role: connectors.RoleAssistant, Content: "some text but no summary line"},
				{Role: connectors.RoleAssistant, Content: "KLYNE_SUMMARY: skip"},
			},
			want: "The real one.",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractKlyneSummary(tc.msgs)
			if got != tc.want {
				t.Errorf("ExtractKlyneSummary = %q; want %q", got, tc.want)
			}
		})
	}
}

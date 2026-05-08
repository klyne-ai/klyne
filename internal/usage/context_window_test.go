package usage

import "testing"

func TestContextWindowForModel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		model string
		want  int64
	}{
		// Empty / unknown
		{"empty model returns 0", "", 0},
		{"unknown model returns default Claude", "made-up-model", DefaultClaudeContextWindow},

		// Claude Sonnet — 1M for 4.x family (GA), 200k for 3.x.
		{"sonnet 4 bare", "claude-sonnet-4", LongContextWindow},
		{"sonnet 4.5 default 1M", "claude-sonnet-4-5", LongContextWindow},
		{"sonnet 4.6 default 1M", "claude-sonnet-4-6", LongContextWindow},
		{"sonnet 4.5 dated 1M", "claude-sonnet-4-5-20251001", LongContextWindow},
		{"sonnet 3.5 dated stays 200k", "claude-3-5-sonnet-20241022", DefaultClaudeContextWindow},
		{"sonnet 3 stays 200k", "claude-3-sonnet-20240229", DefaultClaudeContextWindow},
		{"sonnet 1m suffix", "claude-sonnet-4-5-1m", LongContextWindow},
		{"sonnet long-context label", "claude-sonnet-4-long-context", LongContextWindow},

		// Claude Haiku — always 200k (no 1M tier).
		{"haiku 4", "claude-haiku-4", DefaultClaudeContextWindow},
		{"haiku 4.5", "claude-haiku-4-5", DefaultClaudeContextWindow},
		{"haiku 3.5", "claude-3-5-haiku-20241022", DefaultClaudeContextWindow},

		// Claude Opus — 1M for 4.x family (GA), 200k for 3.x.
		{"opus 4 bare", "claude-opus-4", LongContextWindow},
		{"opus 4.5 default 1M", "claude-opus-4-5", LongContextWindow},
		{"opus 4.7 default 1M", "claude-opus-4-7", LongContextWindow},
		{"opus 4.7 with [1m] cli label", "claude-opus-4-7[1m]", LongContextWindow},
		{"opus 1m variant", "claude-opus-4-1m", LongContextWindow},
		{"opus 3 stays 200k", "claude-3-opus-20240229", DefaultClaudeContextWindow},

		// OpenAI
		{"gpt-5", "gpt-5", LongContextWindow},
		{"gpt-5-mini", "gpt-5-mini", LongContextWindow},
		{"gpt-4.1", "gpt-4.1", LongContextWindow},
		{"gpt-4.1-mini", "gpt-4.1-mini", LongContextWindow},
		{"gpt-4o", "gpt-4o", GPT4oContextWindow},
		{"gpt-4o-mini", "gpt-4o-mini", GPT4oContextWindow},

		// Gemini
		{"gemini-1.5-pro", "gemini-1.5-pro", LongContextWindow},
		{"gemini-2.5-flash", "gemini-2.5-flash", LongContextWindow},
		{"gemini-2.0-flash", "gemini-2.0-flash", LongContextWindow},

		// Case insensitivity
		{"upper case sonnet 4.6 → 1M", "CLAUDE-SONNET-4-6", LongContextWindow},
		{"upper case haiku → 200k", "CLAUDE-HAIKU-4", DefaultClaudeContextWindow},
		{"upper case gpt-5", "GPT-5", LongContextWindow},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ContextWindowForModel(tc.model); got != tc.want {
				t.Errorf("ContextWindowForModel(%q) = %d, want %d", tc.model, got, tc.want)
			}
		})
	}
}

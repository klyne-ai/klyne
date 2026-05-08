package usage

import "strings"

// Context-window sizes in tokens. These mirror published vendor limits as
// of v1; the table is intentionally conservative — when in doubt we prefer
// the smaller "default" limit so the UI's fill bar errs on the side of
// "consider compacting" rather than under-counting.
const (
	// DefaultClaudeContextWindow is the standard Claude window for all
	// Sonnet/Haiku/Opus 3/4 family models.
	DefaultClaudeContextWindow int64 = 200_000

	// LongContextWindow is the 1M-token tier offered by some Sonnet 4.x
	// betas, Opus 4 long-context, gpt-5, gpt-4.1, and gemini-1.5/2.x.
	LongContextWindow int64 = 1_000_000

	// GPT4oContextWindow is the 128k window used by gpt-4o family models.
	GPT4oContextWindow int64 = 128_000
)

// ContextWindowForModel returns the model's maximum input-context size in
// tokens. The lookup is intentionally a small switch over substring
// patterns rather than a giant table — we only need to disambiguate the
// half-dozen model families v1 actually surfaces.
//
// An empty model returns 0 (signal to the handler that the session is
// fresh enough we cannot compute fill percentage). Any unknown non-empty
// model gets the conservative DefaultClaudeContextWindow so the UI bar
// always has something to render.
//
// Claude 4 family note: Anthropic ships the Opus 4.x and Sonnet 4.x
// families with 1M-token context as the marketed default (Opus 4.0+ GA,
// Sonnet 4.5+ GA). The transcript model field — e.g. "claude-opus-4-7"
// — does NOT carry a "1m" suffix; the 1M tier is selected via a beta
// header on the request, but every Claude 4 model in the wild can hit
// it. Treating the 4 family as 1M by default eliminates the bug where
// active sessions blew past the apparent cap (e.g. "297K of 200K").
// Older 3.x Claude models stay at 200k — no 1M tier is offered there.
func ContextWindowForModel(model string) int64 {
	if model == "" {
		return 0
	}
	m := strings.ToLower(model)

	// 1M-tier signal — explicit suffix wins over the default 200k Claude
	// table. Both "claude-sonnet-4-1m" and "...-long-context" appear in
	// the wild as we scrape JSONL transcripts.
	hasLongContextHint := strings.Contains(m, "1m") ||
		strings.Contains(m, "-1m-") ||
		strings.Contains(m, "long-context")

	switch {
	// Claude Sonnet — 1M for the 4.x family (GA), 200k for 3.x.
	case strings.Contains(m, "claude-sonnet"),
		strings.Contains(m, "claude-3-5-sonnet"),
		strings.Contains(m, "claude-3-sonnet"):
		if hasLongContextHint || strings.Contains(m, "sonnet-4") {
			return LongContextWindow
		}
		return DefaultClaudeContextWindow

	// Claude Haiku — 200k for all variants (no 1M tier offered).
	case strings.Contains(m, "claude-haiku"),
		strings.Contains(m, "claude-3-5-haiku"),
		strings.Contains(m, "claude-3-haiku"):
		return DefaultClaudeContextWindow

	// Claude Opus — 1M for the 4.x family (GA), 200k for 3.x.
	case strings.Contains(m, "claude-opus"):
		if hasLongContextHint || strings.Contains(m, "opus-4") {
			return LongContextWindow
		}
		return DefaultClaudeContextWindow

	// OpenAI gpt-5 family and gpt-4.1 — 1M context.
	case strings.HasPrefix(m, "gpt-5"),
		strings.HasPrefix(m, "gpt-4.1"):
		return LongContextWindow

	// gpt-4o family — 128k.
	case strings.HasPrefix(m, "gpt-4o"):
		return GPT4oContextWindow

	// Google Gemini 1.5 and 2.x — 1M context.
	case strings.HasPrefix(m, "gemini-1.5"),
		strings.HasPrefix(m, "gemini-2"):
		return LongContextWindow

	// Unknown but non-empty → conservative default so the UI bar still
	// renders. Better to under-state the fill % than to omit the bar.
	default:
		return DefaultClaudeContextWindow
	}
}

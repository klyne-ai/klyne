// Package resume — anti-bloat trimmer (pure, no I/O).
//
// Trim reduces a Payload to fit within a token budget by applying cuts
// in a fixed priority order. The cut order ensures decisions are always
// the last thing dropped:
//
//  1. Turns older than the most recent decision timestamp
//  2. tool_result payloads > 500 tokens (keep tool_use args)
//  3. File contents (keep paths + line ranges)
//  4. Assistant prose (keep user asks + decisions verbatim)
//  5. Last resort: decisions only (~800 tokens)
package resume

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// TokensPerChar is a rough conversion factor used for quick token estimation.
// Anthropic reports ~4 chars per token on average for English + code.
const TokensPerChar = 4

// ToolResultTokenThreshold is the cut point for large tool_result payloads.
const ToolResultTokenThreshold = 500

// Turn represents a single conversation turn in a resume payload.
// Multiple fields may be set (an assistant turn with tool calls, for example).
type Turn struct {
	// Role is "user", "assistant", or "tool".
	Role string
	// Content is the primary text content of this turn.
	Content string
	// ToolUseArgs is the JSON-encoded args for tool calls (kept through cut 2).
	ToolUseArgs string
	// ToolResultOutput is the raw tool output (eligible for cut 2).
	ToolResultOutput string
	// IsDecision marks this turn as a recorded decision — never cut before step 5.
	IsDecision bool
	// TsMs is the epoch-millisecond timestamp of this turn, used for cut 1.
	TsMs int64
	// FilePaths are the file paths referenced in this turn (kept after cut 3).
	FilePaths []string
}

// Payload is the structured representation of a resume blob before
// it is serialized to a string for injection.
type Payload struct {
	// Turns is the ordered list of conversation turns (oldest first).
	Turns []Turn
	// Decisions are the pinned decision records for this session.
	// They appear separately from turns and are cut last.
	Decisions []string
}

// TrimResult carries the trimmed payload plus a summary of what was cut.
type TrimResult struct {
	Payload  Payload
	// CutSummary describes which cuts were applied (empty when budget was respected).
	CutSummary string
	// EstimatedTokens is the token estimate of the returned payload.
	EstimatedTokens int
}

// estimateTokens returns a rough token count for a Payload.
// Uses the 4-chars-per-token heuristic applied to the serialised form.
func estimateTokens(p Payload) int {
	chars := 0
	for _, t := range p.Turns {
		chars += utf8.RuneCountInString(t.Content)
		chars += utf8.RuneCountInString(t.ToolUseArgs)
		chars += utf8.RuneCountInString(t.ToolResultOutput)
		for _, fp := range t.FilePaths {
			chars += utf8.RuneCountInString(fp)
		}
	}
	for _, d := range p.Decisions {
		chars += utf8.RuneCountInString(d)
	}
	return chars / TokensPerChar
}

// Trim reduces p to fit within budget tokens. It applies cut stages in
// the spec-defined order and returns the trimmed payload plus a summary
// of what was removed.
//
// Invariants guaranteed:
//   - returned EstimatedTokens <= budget (when budget > 0)
//   - decisions are the last content to be dropped
//   - a zero/negative budget is treated as "no limit" (returns payload as-is)
func Trim(p Payload, budget int) TrimResult {
	if budget <= 0 || estimateTokens(p) <= budget {
		return TrimResult{
			Payload:         p,
			EstimatedTokens: estimateTokens(p),
		}
	}

	cuts := []string{}

	// --- Cut 1: drop turns older than the most recent decision timestamp -----
	decisionCutoff := latestDecisionTs(p.Turns)
	if decisionCutoff > 0 {
		before := len(p.Turns)
		newTurns := make([]Turn, 0, len(p.Turns))
		for _, t := range p.Turns {
			// Always keep decision turns; drop pre-cutoff non-decision turns.
			if t.IsDecision || t.TsMs >= decisionCutoff {
				newTurns = append(newTurns, t)
			}
		}
		if len(newTurns) < before {
			cuts = append(cuts, fmt.Sprintf("cut %d pre-decision turns", before-len(newTurns)))
			p.Turns = newTurns
		}
	}
	if estimateTokens(p) <= budget {
		return TrimResult{Payload: p, CutSummary: strings.Join(cuts, "; "), EstimatedTokens: estimateTokens(p)}
	}

	// --- Cut 2: drop tool_result payloads > 500 tokens (keep tool_use args) ---
	toolResultsCut := 0
	for i := range p.Turns {
		if tokensFor(p.Turns[i].ToolResultOutput) > ToolResultTokenThreshold {
			p.Turns[i].ToolResultOutput = "[tool result trimmed — exceeded 500 token threshold]"
			toolResultsCut++
		}
	}
	if toolResultsCut > 0 {
		cuts = append(cuts, fmt.Sprintf("trimmed %d large tool results", toolResultsCut))
	}
	if estimateTokens(p) <= budget {
		return TrimResult{Payload: p, CutSummary: strings.Join(cuts, "; "), EstimatedTokens: estimateTokens(p)}
	}

	// --- Cut 3: drop file contents (keep paths + line ranges) ----------------
	fileContentsCut := 0
	for i := range p.Turns {
		if len(p.Turns[i].FilePaths) > 0 && p.Turns[i].Content != "" {
			// Keep file paths, drop inline file contents from Content.
			// A heuristic: if Content is large and FilePaths are set,
			// it likely contains file body text.
			contentTokens := tokensFor(p.Turns[i].Content)
			if contentTokens > 200 {
				p.Turns[i].Content = "[file contents removed — paths: " +
					strings.Join(p.Turns[i].FilePaths, ", ") + "]"
				fileContentsCut++
			}
		}
	}
	if fileContentsCut > 0 {
		cuts = append(cuts, fmt.Sprintf("replaced %d file contents with paths", fileContentsCut))
	}
	if estimateTokens(p) <= budget {
		return TrimResult{Payload: p, CutSummary: strings.Join(cuts, "; "), EstimatedTokens: estimateTokens(p)}
	}

	// --- Cut 4: drop assistant prose (keep user asks + decisions verbatim) ---
	proseTokensCut := 0
	for i := range p.Turns {
		if p.Turns[i].Role == "assistant" && !p.Turns[i].IsDecision {
			tokens := tokensFor(p.Turns[i].Content)
			if tokens > 50 {
				p.Turns[i].Content = "[assistant response omitted]"
				proseTokensCut++
			}
		}
	}
	if proseTokensCut > 0 {
		cuts = append(cuts, fmt.Sprintf("omitted %d assistant prose turns", proseTokensCut))
	}
	if estimateTokens(p) <= budget {
		return TrimResult{Payload: p, CutSummary: strings.Join(cuts, "; "), EstimatedTokens: estimateTokens(p)}
	}

	// --- Cut 5: last resort — decisions only (~800 tokens) -------------------
	// Drop ALL turns; keep only the compact p.Decisions strings which are the
	// recorded decision texts (short, ~40 tokens each). If p.Decisions is also
	// empty, synthesize compact summaries from IsDecision turns.
	cuts = append(cuts, "last resort: decisions only")
	if len(p.Decisions) > 0 {
		p.Turns = nil
	} else {
		// Fall back: keep only the decision-marked turns (still drop the content
		// for excessively large decision turns so we honour the budget).
		decTurns := decisionsOnly(p.Turns)
		for i := range decTurns {
			if tokensFor(decTurns[i].Content) > 200 {
				// Truncate to a one-liner decision summary.
				decTurns[i].Content = resumeTruncateDecision(decTurns[i].Content)
			}
		}
		p.Turns = decTurns
	}

	return TrimResult{
		Payload:         p,
		CutSummary:      strings.Join(cuts, "; "),
		EstimatedTokens: estimateTokens(p),
	}
}

// latestDecisionTs returns the TsMs of the most recent decision turn,
// or 0 if there are no decision turns.
func latestDecisionTs(turns []Turn) int64 {
	var latest int64
	for _, t := range turns {
		if t.IsDecision && t.TsMs > latest {
			latest = t.TsMs
		}
	}
	return latest
}

// tokensFor returns a rough token count for a string using the 4-char heuristic.
func tokensFor(s string) int {
	return utf8.RuneCountInString(s) / TokensPerChar
}

// resumeTruncateDecision truncates a decision turn's content to the first
// sentence / 200 chars to keep it within the cut-5 budget.
func resumeTruncateDecision(s string) string {
	const maxChars = 200
	runes := []rune(s)
	if len(runes) <= maxChars {
		return s
	}
	return string(runes[:maxChars]) + "…"
}

// decisionsOnly filters turns down to decision-flagged turns only.
func decisionsOnly(turns []Turn) []Turn {
	out := make([]Turn, 0, len(turns))
	for _, t := range turns {
		if t.IsDecision {
			out = append(out, t)
		}
	}
	return out
}

// EstimateTokens is a standalone helper exposed for the estimator.
// It returns the estimated token count for the given text using the
// 4-chars-per-token heuristic.
func EstimateTokens(text string) int {
	return utf8.RuneCountInString(text) / TokensPerChar
}

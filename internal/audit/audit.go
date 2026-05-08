// Package audit re-derives "ground truth" from raw JSONL transcripts and
// compares it against what klyne has stored in SQLite. The goal is
// not testing — it is a reproducible diagnostic the user runs against
// their own data to know whether.klyne's numbers are trustworthy
// before they (or an MCP client) act on them.
//
// The Phase 0 audit is intentionally narrow: one check per slice. The
// first slice covers "latest assistant input tokens" — the value that
// drives the session-page context-fill bar — because that was the
// number behind the original Opus 4.7 1M context display bug.
//
// Later slices add: codex parity, /compact boundary detection coverage,
// and end-to-end HTTP comparison against the live daemon.
package audit

// SourceTokens is the ground-truth value extracted from a single JSONL
// transcript. Tokens is the input_tokens reported by the most-recent
// assistant message that has non-zero usage; Model is the model id on
// that same message. SessionID is the value carried in the JSONL line
// itself, NOT inferred from the file path — the two can differ in the
// presence of subagent / sidechain sessions.
type SourceTokens struct {
	// SessionID is the connector-level session identifier as reported
	// in the JSONL. Empty when the transcript carries none.
	SessionID string
	// Tokens is the input_tokens of the most-recent assistant message
	// with non-zero usage. Zero when the transcript has no assistant
	// turns yet.
	Tokens int64
	// Model is the model id on the most-recent assistant message with
	// non-zero usage. Empty when Tokens is 0.
	Model string
	// MessageID is the uuid of that same most-recent assistant message,
	// useful for cross-referencing with the DB.
	MessageID string
}

// CompactStats summarises every /compact event in a single transcript.
// The marker is a system-message line with `subtype: "compact_boundary"`
// that Claude Code writes when /compact runs (verified against real
// ~/.claude/projects data on 2026-05-08). The stats here are the raw
// material for the cross-session insight report.
type CompactStats struct {
	// Count is the total number of /compact events in this transcript.
	Count int
	// Manual is the subset triggered by the user typing /compact.
	Manual int
	// Auto is the subset triggered automatically when Claude Code
	// detected a near-limit context window. Auto compacts are the
	// painful kind — the user did not choose to lose context.
	Auto int
	// TotalPreTokens is the sum of `compactMetadata.preTokens` across
	// every event — i.e. how much context, in raw tokens, was traded
	// away for a summary in this session over its lifetime.
	TotalPreTokens int64
	// LargestPreTokens is the biggest single compaction the session
	// ever experienced. Useful for ranking "where the user lost the
	// most context in one shot."
	LargestPreTokens int64
}

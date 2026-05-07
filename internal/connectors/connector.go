// Package connectors defines the canonical contract every CLI connector
// (Claude Code, Codex, and any future v2 contribution) must implement, plus
// the shared wire-format types those connectors emit.
//
// W0-FROZEN CONTRACT
// ------------------
// This file is owned by Workstream 0 (bootstrap). Every type and method
// declared here is part of the cross-workstream contract. Changes require
// a PR labeled `contract-change` with human review and a heads-up posted
// on every affected workstream branch — see docs/contracts.md for the full
// change protocol. In-stream agents must NOT silently edit this file.
//
// Spec references:
//   - §7  data flow (canonical Message shape)
//   - §11 repo layout + Connector interface (verbatim)
//   - §18 locked decisions (#9: v1 connectors are claude + codex only)
package connectors

import "context"

// CLI identifies which command-line agent produced a given message or
// session. The set is locked at v1 (spec §18 #9); adding a new connector
// requires a v2 contract-change PR.
type CLI string

const (
	// CLIClaude is the Claude Code CLI (~/.claude/projects/...).
	CLIClaude CLI = "claude"
	// CLICodex is the OpenAI Codex CLI (~/.codex/sessions/...).
	CLICodex CLI = "codex"
)

// Role enumerates the canonical message authorship values stored in the
// `messages.role` column. Connectors MUST normalize their native role
// strings into one of these four values.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
	RoleSystem    Role = "system"
)

// SessionStatus enumerates lifecycle values stored in `sessions.status`.
type SessionStatus string

const (
	SessionStatusActive    SessionStatus = "active"
	SessionStatusIdle      SessionStatus = "idle"
	SessionStatusCompacted SessionStatus = "compacted"
)

// Connector is the v2 contribution surface (spec §11). A connector parses
// one specific CLI's on-disk transcript format and emits canonical
// Message values. v1 ships with implementations for "claude" and "codex"
// only (spec §18 #9).
//
// Implementations MUST:
//   - Be safe to call concurrently for read-only methods (Name, Pricing).
//   - Return promptly when ctx is cancelled in Discover and Watch.
//   - Never mutate or write to the source JSONL files (read-only).
//   - Emit canonical Message values with int64 epoch-ms timestamps.
type Connector interface {
	// Name returns the connector's stable identifier ("claude", "codex").
	Name() string

	// Discover returns absolute paths to every existing JSONL file the
	// connector can parse (used for back-fill on startup).
	Discover(ctx context.Context) ([]string, error)

	// Watch streams RawEvent values for newly-appended lines until ctx
	// is cancelled. Implementations are expected to use fsnotify (or an
	// equivalent OS-level watcher) and a tail-style read loop.
	Watch(ctx context.Context, events chan<- RawEvent) error

	// Parse converts a single raw JSONL line (as it appears on disk) into
	// a canonical *Message. The path is provided so the connector can
	// derive session_id and project_path from the file location when the
	// payload itself does not carry them.
	Parse(line []byte, path string) (*Message, error)

	// Pricing returns the connector's contribution to the cost engine's
	// pricing table. Most connectors return an empty/default table; the
	// Codex connector overrides this in v1 only if its native log carries
	// pricing hints. The cost engine is the authoritative source — see
	// internal/cost/pricing_schema.go.
	Pricing() PricingTable
}

// RawEvent is a single tailed line plus its source coordinates. Connectors
// produce RawEvent values from Watch and the writer goroutine forwards
// them to Parse for normalization (spec §7).
type RawEvent struct {
	// Path is the absolute path to the JSONL file that produced this line.
	Path string `json:"path"`
	// Line is the raw bytes of the JSONL entry (no trailing newline).
	Line []byte `json:"line"`
	// Ts is the time the watcher observed the line, in epoch-milliseconds.
	// This is a wall-clock fallback; the canonical Message.Ts is parsed
	// from the line payload itself when available.
	Ts int64 `json:"ts"`
}

// ToolCall describes a single tool invocation issued by an assistant
// message. Multiple tool calls per message are allowed.
type ToolCall struct {
	// ID is the connector-native tool-call identifier (used to correlate
	// with a subsequent ToolResult).
	ID string `json:"id"`
	// Name is the tool/function name (e.g. "Bash", "Read", "Edit").
	Name string `json:"name"`
	// Input is the raw JSON-encoded argument payload as sent to the tool,
	// preserved verbatim so the UI can pretty-print it.
	Input string `json:"input"`
}

// ToolResult is the response produced by a tool execution, correlated
// back to a ToolCall by ID.
type ToolResult struct {
	// ID matches the originating ToolCall.ID.
	ID string `json:"id"`
	// Output is the raw stringified tool output (may be JSON, plain text,
	// or stderr lines depending on the tool).
	Output string `json:"output"`
	// IsError is true if the tool reported a non-success outcome.
	IsError bool `json:"is_error"`
}

// Message is the canonical, connector-agnostic representation of a single
// turn in a CLI session. Every connector normalizes into this shape (spec
// §7). Field ordering and JSON tags here are part of the wire contract.
//
// Locked decisions (docs/plan/04-shared-contracts.md §3):
//   - Ts is int64 epoch-milliseconds. Connectors do not pick a different
//     unit or representation.
//   - Role is one of {"user", "assistant", "tool", "system"}.
//   - CLI is one of {"claude", "codex"}.
type Message struct {
	// ID is the message's globally-unique identifier (typically the UUID
	// emitted by the source CLI).
	ID string `json:"id"`
	// SessionID groups messages belonging to one CLI session.
	SessionID string `json:"session_id"`
	// CLI identifies the producing connector ("claude" | "codex").
	CLI CLI `json:"cli"`
	// ProjectPath is the absolute working directory the session ran in.
	ProjectPath string `json:"project_path"`
	// Role is the message authorship — see Role constants.
	Role Role `json:"role"`
	// Content is the canonical, connector-normalized text payload.
	Content string `json:"content"`
	// ToolCalls is the (possibly empty) list of tool invocations issued
	// by this message. Only meaningful for assistant messages.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	// ToolResults is the (possibly empty) list of tool outputs attached
	// to this message. Only meaningful for tool/user messages.
	ToolResults []ToolResult `json:"tool_results,omitempty"`
	// TokensIn is the TOTAL prompt-side token count for this message,
	// inclusive of fresh, cache-read, and cache-write portions. Zero
	// means "unknown". Cached subsets are exposed separately below so
	// the cost engine can apply differentiated rates; the fresh portion
	// is derived as TokensIn - CachedReadTokens - CachedWriteTokens.
	TokensIn int64 `json:"tokens_in"`
	// TokensOut is the completion-side token count for this message.
	// Zero means "unknown".
	TokensOut int64 `json:"tokens_out"`
	// CachedReadTokens is the subset of TokensIn served from the
	// provider's prompt cache (Anthropic cache_read_input_tokens /
	// OpenAI cached_input_tokens). Billed at the cache-read rate.
	CachedReadTokens int64 `json:"cached_read_tokens"`
	// CachedWriteTokens is the subset of TokensIn that wrote new entries
	// to the provider's prompt cache (Anthropic
	// cache_creation_input_tokens). OpenAI does not currently expose a
	// directly comparable value, so this is always 0 for Codex.
	CachedWriteTokens int64 `json:"cached_write_tokens"`
	// CostUSD is the connector- or cost-engine-computed dollar cost for
	// this message. Zero means "unknown / free".
	CostUSD float64 `json:"cost_usd"`
	// Model is the model identifier used to produce this message
	// (e.g. "claude-sonnet-4.5", "gpt-5-mini"). Empty for non-assistant
	// messages.
	Model string `json:"model"`
	// Ts is the message timestamp in epoch-milliseconds.
	Ts int64 `json:"ts"`
	// ParentUUID points at the parent message in a CLI's native message
	// graph (used to reconstruct branching threads). Empty for roots.
	ParentUUID string `json:"parent_uuid,omitempty"`
}

// Session is the canonical session row, mirroring the `sessions` SQL
// table (see internal/store/migrations/001_init.sql). The store layer
// (W1/W2) is the authoritative writer; connectors do not construct
// Session values directly — they emit Messages and the store derives
// session metadata.
type Session struct {
	ID          string        `json:"id"`
	CLI         CLI           `json:"cli"`
	ProjectPath string        `json:"project_path"`
	EncodedCWD  string        `json:"encoded_cwd"`
	StartedAt   int64         `json:"started_at"`
	LastMsgAt   int64         `json:"last_msg_at"`
	MsgCount    int64         `json:"msg_count"`
	TokensIn    int64         `json:"tokens_in"`
	TokensOut   int64         `json:"tokens_out"`
	// CachedReadTokens is the cumulative cache-read subset of TokensIn
	// across all messages in the session. Mirrors Message.CachedReadTokens.
	CachedReadTokens  int64 `json:"cached_read_tokens"`
	// CachedWriteTokens is the cumulative cache-write subset of TokensIn
	// across all messages in the session. Mirrors Message.CachedWriteTokens.
	CachedWriteTokens int64         `json:"cached_write_tokens"`
	CostUSD           float64       `json:"cost_usd"`
	Model             string        `json:"model"`
	Status            SessionStatus `json:"status"`
	RawPath           string        `json:"raw_path"`
}

// PricingTable is the contract returned by Connector.Pricing(). v1
// connectors generally return an empty table; the cost engine
// (internal/cost, W9) owns the canonical pricing data, loaded from
// pricing.json. Defined here as a value type (not an interface) so the
// Connector interface stays small and stable.
//
// The shape mirrors internal/cost/pricing_schema.go's PricingFile.Models.
type PricingTable struct {
	// Models maps model identifier -> per-token rates. Connectors that
	// expose no native pricing hints return PricingTable{Models: nil}.
	Models map[string]PerTokenRates `json:"models"`
}

// PerTokenRates is the pricing for one model, expressed per-million-tokens
// (the LiteLLM convention). Field names match pricing.json keys.
type PerTokenRates struct {
	PromptPerMtok     float64 `json:"prompt_per_mtok"`
	CompletionPerMtok float64 `json:"completion_per_mtok"`
	CacheReadPerMtok  float64 `json:"cache_read_per_mtok"`
	CacheWritePerMtok float64 `json:"cache_write_per_mtok"`
}

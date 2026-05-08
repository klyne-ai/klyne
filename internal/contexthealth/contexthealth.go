// Package contexthealth turns a snapshot of a CLI session into an opinionated
// "is this conversation still healthy?" verdict, with a per-source attribution
// of what is bloating the context window.
//
// The classifier is intentionally a pure function: it takes a snapshot
// (session metadata + the most recent N messages + a context-fill percentage
// already computed by internal/usage) and returns a Result. It performs no
// I/O, no DB access, and no AI calls. Callers wire it behind an HTTP handler
// or directly inside a UI component as needed.
//
// The four states (Healthy, Drifting, Risky, RescueNow) and their detection
// rules are specified in docs/marketing/context-rescue-eval.md. The eval
// dataset under docs/eval/context-health/ is the source of truth for whether
// the rule thresholds are correct; this package only implements them.
//
// AI polish is explicitly out of scope. A separate Phase 3 surface can take
// a Result and rewrite the Reason string against an LLM, but the State and
// Action fields stay deterministic so users can trust them without an API
// key.
package contexthealth

import "github.com/klyne-ai/klyne/internal/connectors"

// State is the headline classification surfaced in the UI.
type State string

const (
	// StateHealthy means continue normally — no action recommended.
	StateHealthy State = "healthy"
	// StateDrifting means the session is growing or has shifted topic;
	// consider compacting soon. Not urgent.
	StateDrifting State = "drifting"
	// StateRisky means context fill or repeated tool noise is likely
	// degrading the next turns. Compact now.
	StateRisky State = "risky"
	// StateRescueNow means continuing is wasting tokens and probably
	// quality; generate a clean handoff and start fresh.
	StateRescueNow State = "rescue_now"
)

// Action is the next-step recommendation. Mirrors the legacy break-advice
// verdicts so existing UI surfaces can consume the same value without
// rewriting their switch statements (see docs/marketing/context-rescue-migration.md).
type Action string

const (
	ActionContinue   Action = "continue"
	ActionCompact    Action = "compact"
	ActionStartFresh Action = "start_fresh"
)

// BloatKind describes what kind of source a BloatRow attributes to.
type BloatKind string

const (
	// BloatKindFileRead means the dominant operation on this file path
	// was a Read/View/Cat/Open call — i.e. the AI loaded the file
	// contents into context to look at them. Repetition is a real
	// signal of "lost the prior content."
	BloatKindFileRead BloatKind = "file_read"
	// BloatKindFileEdit means the dominant operation on this file path
	// was an Edit/MultiEdit call. Repetition is NOT a signal of
	// context loss — it's iterative refactor progress (each Edit is
	// a different change to the same file). Counted toward total
	// context bytes because the old_string + new_string snippets do
	// land in the prompt, but does NOT trigger the rescue rule.
	BloatKindFileEdit BloatKind = "file_edit"
	// BloatKindFileMixed means the file had both Read and Edit calls
	// in roughly comparable numbers. Treated as the union for bloat
	// attribution; the rescue rule still only counts Reads.
	BloatKindFileMixed BloatKind = "file_mixed"
	// BloatKindCommand is a repeated shell/Bash invocation with the
	// same normalized command stem.
	BloatKindCommand BloatKind = "command"
	// BloatKindToolResult is a single large tool result that doesn't
	// fit the file-read or command groupings.
	BloatKindToolResult BloatKind = "tool_result"
)

// BloatRow is one entry in the "what is eating the context window"
// scorecard. Sorted by SharePct desc; the classifier returns at most five.
type BloatRow struct {
	// Label is the user-facing description. For file rows it includes
	// the basename plus the operation breakdown — e.g. "plan-config.server.ts
	// (3 reads, 7 edits)" — so consumers see whether repetition is
	// context-loss or refactor progress. For commands: "Bash: npm test".
	// For unattributed: "Tool result". Always non-empty.
	Label string `json:"label"`
	// Kind disambiguates how the row was grouped. file_read / file_edit
	// / file_mixed split the old "file_read" bucket so consumers and
	// rescue rules can distinguish content-loading from refactor-edit
	// repetition.
	Kind BloatKind `json:"kind"`
	// Count is the total number of tool calls/results aggregated into
	// this row (ReadCount + EditCount for file rows; otherwise just
	// the call count).
	Count int `json:"count"`
	// ReadCount is the subset of Count attributable to Read/View/Cat/
	// Open calls. Zero for non-file rows. The rescue rule that fires
	// on "same file read N+ times" uses this number, NOT Count, so a
	// long iterative-refactor session does not falsely escalate.
	ReadCount int `json:"read_count,omitempty"`
	// EditCount is the subset of Count attributable to Edit/MultiEdit
	// calls. Zero for non-file rows.
	EditCount int `json:"edit_count,omitempty"`
	// SharePct is this row's contribution to the total tool-output bytes
	// in the snapshot, expressed 0..100. The five rows do NOT need to
	// sum to 100 (the snapshot may include unattributed output).
	SharePct float64 `json:"share_pct"`
}

// Signals exposes the inputs the classifier used so the UI can explain its
// own reasoning. Every field is informational; callers must not branch on
// them — branch on State/Action instead.
type Signals struct {
	// ContextFillPct mirrors the input as a convenience for the UI.
	ContextFillPct float64 `json:"context_fill_pct"`
	// MsgCount is the total session message count (NOT just the snapshot
	// window).
	MsgCount int `json:"msg_count"`
	// AssistantMsgCount is the number of role=assistant messages in the
	// snapshot window.
	AssistantMsgCount int `json:"assistant_msg_count"`
	// HiddenRatio is hidden (tool+system) / max(1, assistant) inside the
	// snapshot. 3.0 means the session is three tool/system rows for every
	// assistant turn.
	HiddenRatio float64 `json:"hidden_ratio"`
	// TopRepeatedFile is the path of the most-read file in the snapshot,
	// or "" when no file was read more than once.
	TopRepeatedFile string `json:"top_repeated_file"`
	// TopRepeatedFileCount is the number of times TopRepeatedFile was
	// read in the snapshot. Zero when TopRepeatedFile is empty.
	TopRepeatedFileCount int `json:"top_repeated_file_count"`
	// TopFailedCommand is the most-frequently-failing command stem, or
	// "" when no command failed more than once.
	TopFailedCommand string `json:"top_failed_command"`
	// TopFailedCommandCount is the number of failures attributed to
	// TopFailedCommand. Zero when TopFailedCommand is empty.
	TopFailedCommandCount int `json:"top_failed_command_count"`
	// TopicShifted is true when the bag-of-words overlap between the
	// session's earliest user prompts and its most recent ones falls
	// below the topic-overlap threshold.
	TopicShifted bool `json:"topic_shifted"`
}

// Result is the classifier's full verdict.
type Result struct {
	// State is the headline classification.
	State State `json:"state"`
	// Action is the recommended next step.
	Action Action `json:"action"`
	// Reason is a single user-readable sentence summarising why this
	// State was chosen. Always non-empty.
	Reason string `json:"reason"`
	// Bloat is the top-5 attribution rows, sorted by SharePct desc.
	// May be empty when the snapshot has no tool results.
	Bloat []BloatRow `json:"bloat"`
	// Signals exposes the classifier's inputs.
	Signals Signals `json:"signals"`
}

// Input is the snapshot the caller hands to Classify. Sourced from
// internal/store + internal/usage; the classifier itself touches neither.
type Input struct {
	// SessionID is the session under analysis. Carried through for
	// logging; the classifier does not switch on it.
	SessionID string
	// CLI identifies the producing connector. The classifier currently
	// applies the same rubric to both, but the field is kept so future
	// per-CLI thresholds can branch here without changing the API.
	CLI connectors.CLI
	// Model is the assistant model used in the most recent turn. Carried
	// through to Signals consumers; the classifier does not switch on it.
	Model string
	// ContextFillPct is the percent of the model's context window
	// consumed by the next turn (computed by internal/usage). 0..100.
	ContextFillPct float64
	// MsgCount is the total message count in the session, taken from the
	// sessions table so the snapshot doesn't have to walk every row.
	MsgCount int
	// Messages is the most-recent slice of canonical messages, in
	// chronological order (oldest first). The caller controls the window
	// size; the classifier reads at most the last 20 for repetition
	// detection and the first/last 5 user messages for topic shift.
	Messages []*connectors.Message
}

// Classify runs the deterministic rubric and returns a Result. The function
// is pure: same Input always yields the same Result.
func Classify(in Input) Result {
	return classify(in)
}

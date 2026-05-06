// Package api — SSE event payload types.
//
// W0-FROZEN CONTRACT
// ------------------
// Each Event* string constant names an event sent over GET /events. The
// frontend (W13) listens for these names verbatim. Renaming or
// reshaping any event requires a `contract-change` PR.
//
// Spec references:
//   - docs/plan/04-shared-contracts.md §6 (event table)
//   - spec §7 (live update flow)
package api

// Event name constants. The values are the strings emitted as the SSE
// `event:` field by W8's hub.
const (
	EventMsgNew          = "msg.new"
	EventSummaryReady    = "summary.ready"
	EventSessionUpdate   = "session.update"
	EventCostTick        = "cost.tick"
	EventThreadRebuild   = "thread.rebuild"
	EventCompactDetected = "compact.detected"
)

// AllEvents returns every SSE event name in declaration order. Used by
// dump-contracts and (later) by the frontend's contract-check.
func AllEvents() []string {
	return []string{
		EventMsgNew,
		EventSummaryReady,
		EventSessionUpdate,
		EventCostTick,
		EventThreadRebuild,
		EventCompactDetected,
	}
}

// MsgNew fires when a new message has been parsed and inserted.
type MsgNew struct {
	SessionID string  `json:"session_id"`
	MessageID string  `json:"message_id"`
	Ts        int64   `json:"ts"`
	Role      string  `json:"role"`
	Model     string  `json:"model"`
	TokensIn  int64   `json:"tokens_in"`
	TokensOut int64   `json:"tokens_out"`
	CostUSD   float64 `json:"cost_usd"`
}

// SummaryReady fires when the summarizer worker (W11) writes a new
// session_summaries row.
type SummaryReady struct {
	SessionID string `json:"session_id"`
	Version   int    `json:"version"`
	Ts        int64  `json:"ts"`
	Model     string `json:"model"`
}

// SessionUpdate fires when session aggregate counters change (msg
// inserted, status flipped, etc.).
type SessionUpdate struct {
	SessionID string  `json:"session_id"`
	LastMsgAt int64   `json:"last_msg_at"`
	MsgCount  int64   `json:"msg_count"`
	CostUSD   float64 `json:"cost_usd"`
	Status    string  `json:"status"`
}

// CostTick fires periodically (W9) with the rolling daily total.
type CostTick struct {
	Ts            int64   `json:"ts"`
	TotalUSDToday float64 `json:"total_usd_today"`
}

// ThreadRebuild fires after the v1.1 thread builder regenerates
// thread groupings. v1 emits a single startup tick; the field is kept
// here to avoid event-name drift later.
type ThreadRebuild struct {
	ThreadCount int   `json:"thread_count"`
	Ts          int64 `json:"ts"`
}

// CompactDetected fires when W15's compact-detector recognizes a
// `/compact` event in either CLI's source log.
type CompactDetected struct {
	SessionID string `json:"session_id"`
	Ts        int64  `json:"ts"`
}

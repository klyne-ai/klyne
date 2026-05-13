// Package api owns the HTTP-facing contract: route paths, request DTOs,
// and response DTOs. Handler implementations live in W7; this file is
// only the type surface.
//
// W0-FROZEN CONTRACT
// ------------------
// Every exported route constant and DTO declared here is part of the
// cross-workstream wire contract. The frontend (W13/W14) consumes these
// types via tools/dump-contracts; mutating a field name or JSON tag will
// break the UI's contract-check. Changes require a `contract-change` PR
// — see docs/contracts.md.
//
// Spec references:
//   - docs/plan/04-shared-contracts.md §5 (route table)
//   - spec §7 (data model)
//   - spec §8 (settings / wizard for BYOK)
package api

import (
	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/connectors/codereviewgraph"
	"github.com/klyne-ai/klyne/internal/store"
)

// ---------------------------------------------------------------------------
// Routes
// ---------------------------------------------------------------------------

// Route* constants enumerate every HTTP path the daemon exposes. They
// MUST be unique (verified by contracts_test.go). Path parameters use
// the chi-router ":id" syntax.
const (
	RouteSessions            = "/sessions"
	RouteSession             = "/sessions/{id}"
	RouteSessionMessages     = "/sessions/{id}/messages"
	RouteSessionRestore      = "/sessions/{id}/restore"
	RouteSessionSummary      = "/sessions/{id}/summary"
	RouteSessionUsage        = "/sessions/{id}/usage"
	RouteSessionBreakAdvice  = "/sessions/{id}/break-advice"
	RouteSessionTokenTimeline = "/sessions/{id}/token-timeline"
	RouteSearch              = "/search"
	RouteCostSummary         = "/cost/summary"
	RouteUsage               = "/usage"
	RouteUsageStats          = "/usage/stats"
	RouteCockpitThreads      = "/cockpit/threads"
	RouteAdvisories          = "/advisories"
	RouteSessionAdvisorDetail = "/sessions/{id}/advisor-detail"
	RouteEvents              = "/events"
	RouteHealthz             = "/healthz"
	RouteCodeReviewContext   = "/code-review-context"
	// Memory: SPA page lives at `/memory`, so the API uses a
	// /memory/items[/{id}] subpath to avoid colliding with the SPA
	// route (same pattern as /cockpit/threads and /usage/stats).
	RouteMemory              = "/memory/items"
	RouteMemoryItem          = "/memory/items/{id}"
	// Insights: per-project rollup powering the Insights dashboard.
	// Returns one rich record per project with agent split, cache hit %,
	// efficiency, /compact pain signal, top sessions, daily sparkline,
	// and trend vs the prior-period window of equal duration.
	RouteInsightsProjects    = "/insights/projects"
)

// AllRoutes returns the canonical, ordered list of every HTTP path
// constant declared above. Used by contracts_test.go to assert
// uniqueness, and (later) by the dump-contracts tool.
func AllRoutes() []string {
	return []string{
		RouteSessions,
		RouteSession,
		RouteSessionMessages,
		RouteSessionRestore,
		RouteSessionSummary,
		RouteSessionUsage,
		RouteSessionBreakAdvice,
		RouteSessionTokenTimeline,
		RouteSearch,
		RouteCostSummary,
		RouteUsage,
		RouteUsageStats,
		RouteCockpitThreads,
		RouteAdvisories,
		RouteSessionAdvisorDetail,
		RouteEvents,
		RouteHealthz,
		RouteCodeReviewContext,
		RouteMemory,
		RouteMemoryItem,
		RouteInsightsProjects,
	}
}

// ---------------------------------------------------------------------------
// /sessions
// ---------------------------------------------------------------------------

// SessionListResponse is the envelope returned by GET /sessions.
// Pagination is cursor-based: the caller passes ?before=<ts> to fetch
// older pages; NextBefore is the cursor for the next page (zero when
// the list is exhausted).
type SessionListResponse struct {
	Sessions   []connectors.Session `json:"sessions"`
	NextBefore int64                `json:"next_before"`
}

// SessionResponse is GET /sessions/{id}.
type SessionResponse struct {
	Session connectors.Session `json:"session"`
}

// MessageListResponse is GET /sessions/{id}/messages — paginated by ts.
type MessageListResponse struct {
	Messages   []connectors.Message `json:"messages"`
	NextBefore int64                `json:"next_before"`
}

// RestoreResponse is GET /sessions/{id}/restore — the "Restore context"
// payload (spec Flow C). The Markdown field is the ready-to-paste resume
// prompt; Summary and Tail are exposed separately for UI rendering.
type RestoreResponse struct {
	SessionID    string               `json:"session_id"`
	Summary      string               `json:"summary"`        // Markdown
	Tail         []connectors.Message `json:"tail"`           // last N raw messages
	Markdown     string               `json:"markdown"`       // single-block resume prompt
	ResumeCmd    string               `json:"resume_cmd"`     // e.g. `claude --resume <id>`
	ProjectPath  string               `json:"project_path"`
	GeneratedAt  int64                `json:"generated_at"`   // epoch-ms
}

// SummaryResponse is GET /sessions/{id}/summary — the latest rolling
// summary plus its provenance.
type SummaryResponse struct {
	SessionID string `json:"session_id"`
	Version   int    `json:"version"`
	Text      string `json:"text"`
	Model     string `json:"model"`
	Ts        int64  `json:"ts"`
}

// ---------------------------------------------------------------------------
// /sessions/{id}/usage  — token-savings per-session indicator
// ---------------------------------------------------------------------------

// SessionUsageResponse is GET /sessions/{id}/usage. It backs the
// "context fill + cost-per-turn" indicator on the session detail page.
//
// All Pct5h fields are percentages of the user's 5-hour rate limit, NOT
// percentages of the context window. They are -1 when the server cannot
// calibrate (no recent /usage data, or zero observed utilization). The UI
// renders -1 as "—" rather than a misleading number.
//
// Why these specific fields:
//   - ContextFillPct drives the visual fill bar (green/yellow/red).
//   - NextTurnPct5h tells the user the cost of doing nothing (the headline
//     anti-feature: every additional turn re-sends this whole context).
//   - CompactedNextTurnPct5h / RestartedNextTurnPct5h are the deltas they
//     gain by acting — what makes the CTA concrete.
type SessionUsageResponse struct {
	SessionID string `json:"session_id"`
	// Model is the most-recent assistant model used in this session.
	// Empty when the session has no assistant turns yet.
	Model string `json:"model"`

	// Context-window state.

	// ContextWindow is the model's maximum context size in tokens
	// (e.g. 200000 for sonnet-4.5, 1000000 for opus-4.7 long-context).
	// Zero when the model is unknown.
	ContextWindow int64 `json:"context_window"`
	// ContextUsed is the approximate token count the next turn would
	// re-send as input. Derived from the most recent assistant message's
	// reported tokens_in, which is the closest empirical signal to "what
	// did the CLI actually pack into the context for that turn".
	ContextUsed int64 `json:"context_used"`
	// ContextFillPct = ContextUsed / ContextWindow * 100. Can exceed 100
	// for sessions that overflowed and were silently truncated.
	ContextFillPct float64 `json:"context_fill_pct"`

	// 5h-limit projections — the cost language the user actually cares about.
	// All percentages of the user's 5h rolling rate-limit cap.

	// NextTurnPct5h is the projected % of the 5h limit the next turn
	// will burn at the current context size.
	NextTurnPct5h float64 `json:"next_turn_pct_5h"`
	// CompactedNextTurnPct5h is the projected per-turn cost AFTER a
	// /compact, assuming compaction reduces context by CompactRatio.
	CompactedNextTurnPct5h float64 `json:"compacted_next_turn_pct_5h"`
	// RestartedNextTurnPct5h is the projected per-turn cost in a fresh
	// session with empty context. Effectively the system-prompt floor.
	RestartedNextTurnPct5h float64 `json:"restarted_next_turn_pct_5h"`

	// Savings deltas, exposed so the UI doesn't recompute them.
	// CompactSavingsPct5h = NextTurnPct5h - CompactedNextTurnPct5h.
	CompactSavingsPct5h float64 `json:"compact_savings_pct_5h"`
	// RestartSavingsPct5h = NextTurnPct5h - RestartedNextTurnPct5h.
	RestartSavingsPct5h float64 `json:"restart_savings_pct_5h"`

	// CompactRatio is the assumed post-compact size as a fraction of
	// current context (e.g. 0.15 = "compaction cuts to 15%"). Sourced
	// from observed Claude Code /compact behavior. Exposed so the UI
	// can show its math when asked.
	CompactRatio float64 `json:"compact_ratio"`

	// CalibratedFromOAuth is true when the percentages were derived from
	// vendor-canonical /usage data; false when the server fell back to a
	// hard-coded calibration. Lets the UI label "estimate" vs "live".
	CalibratedFromOAuth bool `json:"calibrated_from_oauth"`
}

// ---------------------------------------------------------------------------
// /sessions/{id}/token-timeline — per-turn token usage line chart
// ---------------------------------------------------------------------------

// TokenTimelinePoint mirrors a single per-assistant-turn row of the
// session's token-usage timeline. Field semantics line up 1:1 with the
// internal contexthealth.TimelinePoint type — duplicated here so the
// HTTP contract is fully described in this package and the api wire
// type cannot drift from changes to the internal computation struct.
//
// The two-axis story:
//   - total_input is the prefix size at that turn (TokensIn from the
//     provider). This is what fills the model's context window — the
//     line the cockpit chart plots as the primary series.
//   - effective_input is TokensIn - CachedReadTokens — the portion
//     that burns the user's 5h rate-limit budget at full rate.
type TokenTimelinePoint struct {
	// TsMs is the assistant message timestamp in epoch-ms.
	TsMs int64 `json:"ts_ms"`
	// EffectiveInput is TokensIn - CachedReadTokens — the portion
	// that burns the user's rate-limit budget at full rate.
	EffectiveInput int64 `json:"effective_input"`
	// TotalInput is the raw TokensIn (fresh + cached read + cached
	// write). The primary curve plotted on the chart.
	TotalInput int64 `json:"total_input"`
	// CachedReadTokens is the prefix served from prompt cache.
	CachedReadTokens int64 `json:"cached_read_tokens"`
	// CachedWriteTokens is the new content written to cache.
	CachedWriteTokens int64 `json:"cached_write_tokens"`
	// OutputTokens is the completion token count.
	OutputTokens int64 `json:"output_tokens"`
}

// TokenTimelineResponse is GET /sessions/{id}/token-timeline. It backs
// the cockpit session-detail line chart, mirroring the data shape that
// `klyne tokens` and the get_token_timeline MCP tool already emit so
// the three surfaces stay consistent.
//
// Query params:
//   - window  Go duration string (e.g. "30m", "5h"); min 1m, max 24h.
//     Empty means "entire session" (default — long-paused sessions
//     surface their full history rather than being clipped to 5h).
//   - hours   convenience integer hours; ignored when window is set.
type TokenTimelineResponse struct {
	SessionID     string               `json:"session_id"`
	// Model is the model id on the most recent qualifying turn. Empty
	// when the session has no assistant turns yet.
	Model         string               `json:"model"`
	// ContextWindow is the model's maximum context size in tokens.
	// Zero when the model is unknown.
	ContextWindow int64                `json:"context_window"`
	// WindowStartMs / WindowEndMs bracket the timestamps included.
	// For an "entire session" view, WindowStartMs equals the first
	// point's TsMs so the chart axis reads honestly.
	WindowStartMs int64                `json:"window_start_ms"`
	WindowEndMs   int64                `json:"window_end_ms"`
	// Points are per-assistant-turn rows in chronological order.
	Points        []TokenTimelinePoint `json:"points"`
	// FirstInput is the oldest qualifying turn's TokensIn — "where
	// this session started".
	FirstInput    int64                `json:"first_input"`
	// LatestInput is the most recent qualifying turn's TokensIn —
	// "how big is the prefix right now".
	LatestInput   int64                `json:"latest_input"`
	// PeakInput is max(TotalInput) across Points — the largest single-
	// turn prefix the session ever carried.
	PeakInput     int64                `json:"peak_input"`
	// PctOfContext = LatestInput / ContextWindow × 100, capped at 100.
	// Zero when ContextWindow is unknown.
	PctOfContext  float64              `json:"pct_of_context"`
}

// ---------------------------------------------------------------------------
// /sessions/{id}/break-advice — AI-powered session-break recommender
// ---------------------------------------------------------------------------

// BreakAdviceVerdict enumerates the small set of recommendations the
// advisor returns. Strings (not int) so they round-trip cleanly through
// JSON without needing a parser side-table.
type BreakAdviceVerdict string

const (
	// BreakAdviceStartFresh — a recent topic ended; user should start a
	// new session for the next thing they want to work on.
	BreakAdviceStartFresh BreakAdviceVerdict = "start_fresh"
	// BreakAdviceCompact — user is mid-task; compacting is the right
	// move because it preserves continuity but kills the context bloat.
	BreakAdviceCompact BreakAdviceVerdict = "compact"
	// BreakAdviceContinue — too early to break; just keep going.
	BreakAdviceContinue BreakAdviceVerdict = "continue"
	// BreakAdviceUnavailable — no AI provider configured, so we can't
	// advise. UI should hide the button or show a "configure AI" prompt.
	BreakAdviceUnavailable BreakAdviceVerdict = "unavailable"
)

// BreakAdviceResponse is GET /sessions/{id}/break-advice. Backed by a
// single Haiku-class call against the last ~10 messages, cached
// in-memory for 10 minutes per session so re-clicks are free.
type BreakAdviceResponse struct {
	SessionID string             `json:"session_id"`
	Verdict   BreakAdviceVerdict `json:"verdict"`
	// Reason is a one-sentence human-readable explanation of the verdict.
	// Always populated, even for "unavailable" (in which case it explains
	// why advice can't be given).
	Reason string `json:"reason"`
	// SuggestedTopic is a short label for the new session if Verdict is
	// "start_fresh" (e.g. "refactor cost engine"). Empty otherwise.
	SuggestedTopic string `json:"suggested_topic,omitempty"`
	// Provider is the AI provider that generated the advice
	// (e.g. "anthropic", "gemini"). Empty when Verdict is "unavailable".
	Provider string `json:"provider,omitempty"`
	// Model is the model identifier used. Empty when "unavailable".
	Model string `json:"model,omitempty"`
	// CachedAt is the epoch-ms at which this advice was generated. The UI
	// uses this to show "advised X ago"; ALSO lets the UI invalidate its
	// own cache when the server returns a stale entry past the user's
	// preferred staleness window.
	CachedAt int64 `json:"cached_at"`
}

// ---------------------------------------------------------------------------
// /search
// ---------------------------------------------------------------------------

// SearchHit is one ranked FTS5 result.
type SearchHit struct {
	MessageID   string  `json:"message_id"`
	SessionID   string  `json:"session_id"`
	CLI         string  `json:"cli"`
	ProjectPath string  `json:"project_path"`
	Role        string  `json:"role"`
	Snippet     string  `json:"snippet"`        // FTS-highlighted excerpt
	Score       float64 `json:"score"`          // BM25, lower = better
	Ts          int64   `json:"ts"`
}

// SearchResponse is GET /search?q=&limit=.
type SearchResponse struct {
	Query string      `json:"query"`
	Hits  []SearchHit `json:"hits"`
	Took  int64       `json:"took_ms"`         // server-side latency
}

// ---------------------------------------------------------------------------
// /cost/summary
// ---------------------------------------------------------------------------

// CostGroup enumerates the supported group-by axes for the cost summary
// endpoint (spec table §11 routes / W9).
type CostGroup string

const (
	CostGroupSession CostGroup = "session"
	CostGroupProject CostGroup = "project"
	CostGroupDay     CostGroup = "day"
	CostGroupModel   CostGroup = "model"
)

// CostBucket is one row of the grouped cost summary.
type CostBucket struct {
	Key       string  `json:"key"`        // session id, project path, ISO-day, or model
	TokensIn  int64   `json:"tokens_in"`
	TokensOut int64   `json:"tokens_out"`
	CostUSD   float64 `json:"cost_usd"`
	Count     int64   `json:"count"`      // # messages contributing
}

// CostSummaryResponse is GET /cost/summary.
type CostSummaryResponse struct {
	Group   CostGroup    `json:"group"`
	Since   int64        `json:"since"`     // epoch-ms (0 = unbounded)
	Until   int64        `json:"until"`     // epoch-ms (0 = unbounded)
	Buckets []CostBucket `json:"buckets"`
	Total   CostBucket   `json:"total"`     // aggregate across all buckets
}

// ---------------------------------------------------------------------------
// /usage
// ---------------------------------------------------------------------------

// UsageWindow is one rolling-window aggregate (5h, 7d, or 7d-Sonnet) for a
// single CLI. Limits are deliberately NOT computed server-side — vendor
// caps are not published, change without notice, and depend on the user's
// plan tier. The frontend converts tokens → percentage using its own
// (user-editable) tier table.
type UsageWindow struct {
	WindowSeconds int64 `json:"window_seconds"` // size of the rolling window
	Tokens        int64 `json:"tokens"`         // sum tokens_in + tokens_out within the window
	TokensIn      int64 `json:"tokens_in"`
	TokensOut     int64 `json:"tokens_out"`
	Messages      int64 `json:"messages"`
	// FirstMsgTs is the epoch-ms timestamp of the oldest message currently
	// inside this window. Zero when the window is empty. The frontend
	// derives the "resets in" countdown as FirstMsgTs + WindowSeconds*1000.
	FirstMsgTs int64 `json:"first_msg_ts"`
}

// UsageCLI groups all rolling-window aggregates for a single CLI. Sonnet7d
// is omitted (zero value) for non-Claude CLIs.
//
// OAuth, when populated, carries the vendor-canonical utilization numbers
// returned by Anthropic's /api/oauth/usage endpoint — pre-bound to the
// user's actual plan tier. The frontend MUST prefer these over the
// token-based estimates above when present, since the local sums only
// approximate what the rate limiter actually meters.
type UsageCLI struct {
	Window5h       UsageWindow `json:"window_5h"`
	Window7d       UsageWindow `json:"window_7d"`
	Window7dSonnet UsageWindow `json:"window_7d_sonnet"`
	// OAuth is nil when no OAuth token is available, when the call to
	// Anthropic failed, or for CLIs without an equivalent endpoint
	// (Codex). Always nil for non-Claude CLIs in v1.
	OAuth *OAuthUsage `json:"oauth,omitempty"`
}

// OAuthWindow is one vendor-reported rolling-window utilization slice.
type OAuthWindow struct {
	// UtilizationPct is the percentage of the plan-tier cap consumed in
	// this window, as Anthropic computes it. Range [0, 100+] (the API
	// can return >100 when the cap was breached).
	UtilizationPct float64 `json:"utilization_pct"`
	// ResetsAt is the epoch-ms at which the window rolls over. Zero when
	// the API omits the field.
	ResetsAt int64 `json:"resets_at"`
}

// OAuthUsage mirrors the relevant subset of Anthropic's /api/oauth/usage
// payload. Each window is nullable (pointer) because the API itself
// returns null for windows that don't apply to the user's plan.
type OAuthUsage struct {
	FiveHour       *OAuthWindow `json:"five_hour,omitempty"`
	SevenDay       *OAuthWindow `json:"seven_day,omitempty"`
	SevenDaySonnet *OAuthWindow `json:"seven_day_sonnet,omitempty"`
	// SubscriptionType is the user's plan label as Claude Code stored it
	// (e.g. "pro", "max"). Echoed for display only — the percentages
	// already account for it.
	SubscriptionType string `json:"subscription_type,omitempty"`
}

// UsageResponse is GET /usage. Computed at request time from the messages
// table; cheap (~ms) so polling at 60s is safe.
type UsageResponse struct {
	Now    int64    `json:"now"`     // epoch-ms server clock at calc time
	Claude UsageCLI `json:"claude"`
	Codex  UsageCLI `json:"codex"`
}

// ---------------------------------------------------------------------------
// /healthz
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// /cockpit/threads
// ---------------------------------------------------------------------------

// CockpitThread is one tile on the cockpit page. It is a sub-thread of a
// session disambiguated by (git_branch, cwd) so two parallel
// `claude --resume <id>` invocations from different worktrees show as
// separate tiles instead of merging.
//
// Older messages (pre-migration) have empty git_branch / cwd; those rows
// collapse to a single bucket per session, matching pre-migration UX.
type CockpitThread struct {
	SessionID   string `json:"session_id"`
	CLI         string `json:"cli"`
	ProjectPath string `json:"project_path"`
	GitBranch   string `json:"git_branch"`
	Cwd         string `json:"cwd"`
	Model       string `json:"model"`
	LastMsgAt   int64  `json:"last_msg_at"`
	MsgCount    int64  `json:"msg_count"`
	TokensIn    int64  `json:"tokens_in"`
	TokensOut   int64  `json:"tokens_out"`
}

// CockpitThreadsResponse is GET /cockpit/threads.
type CockpitThreadsResponse struct {
	Threads []CockpitThread `json:"threads"`
}

// ---------------------------------------------------------------------------
// /advisories
// ---------------------------------------------------------------------------

// AdvisoryKind names which klyne advisor trigger produced the row.
// Strings match the on-disk transition-state names so a future surface
// that wants to cross-reference can use them directly.
type AdvisoryKind string

const (
	// AdvisoryKindStale is the relevance-drift trigger.
	AdvisoryKindStale AdvisoryKind = "stale"
	// AdvisoryKindAcceleration is the per-turn uncached doubling.
	AdvisoryKindAcceleration AdvisoryKind = "acceleration"
	// AdvisoryKindHardCeiling is the >=75% context-fill warning.
	AdvisoryKindHardCeiling AdvisoryKind = "hard_ceiling"
	// AdvisoryKindFiveHourWarn is the 50% rate-limit warning.
	AdvisoryKindFiveHourWarn AdvisoryKind = "window_50"
	// AdvisoryKindFiveHourUrgent is the 75% rate-limit warning.
	AdvisoryKindFiveHourUrgent AdvisoryKind = "window_75"
	// AdvisoryKindTopicShift is the partial-pivot trigger that
	// fires when the user's prompts have shifted topic from the
	// session opening AND some loaded files (≥20%) are stale
	// relative to the new direction. Milder than AdvisoryKindStale.
	AdvisoryKindTopicShift AdvisoryKind = "topic_shift"
	// AdvisoryKindUnknown is the fallback when the text doesn't match
	// any recognised trigger pattern. Surfaces as a real row so a
	// future hook can add advisories without UI breakage.
	AdvisoryKindUnknown AdvisoryKind = "unknown"
)

// AdvisoryRow is one rendered advisory across any klyne-monitored
// session. The Markdown content is exactly what the hook injected
// into chat — preserving punctuation, percentages, file names — so
// the cockpit can render it verbatim.
type AdvisoryRow struct {
	MessageID   string       `json:"message_id"`
	SessionID   string       `json:"session_id"`
	CLI         string       `json:"cli"`
	ProjectPath string       `json:"project_path"`
	Kind        AdvisoryKind `json:"kind"`
	Content     string       `json:"content"`
	TS          int64        `json:"ts"`
}

// AdvisoryListResponse is GET /advisories — the cockpit's "every
// advisory klyne has ever fired" feed. Newest first.
type AdvisoryListResponse struct {
	Advisories []AdvisoryRow `json:"advisories"`
}

// FileRelevanceProof is one file's contribution to the loaded
// context, scored against the user's latest direction. Proves
// the stale-context advisor's claim by naming exact paths plus
// their per-file relevance score.
type FileRelevanceProof struct {
	Path     string  `json:"path"`
	Basename string  `json:"basename"`
	Bytes    int     `json:"bytes"`
	Score    float64 `json:"score"`
	Stale    bool    `json:"stale"`
	// LastTouchTs is the epoch-millisecond timestamp of the most
	// recent Read or Edit message attributed to this file. Zero is
	// possible when the underlying touch message had no timestamp
	// (defensive); the UI renders it as "—" in that case.
	LastTouchTs int64 `json:"last_touch_ts"`
}

// StaleProof bundles the relevance scorer's outputs in a shape the
// cockpit modal can render directly.
type StaleProof struct {
	Files          []FileRelevanceProof `json:"files"`
	StaleBytes     int                  `json:"stale_bytes"`
	TotalBytes     int                  `json:"total_bytes"`
	StaleShare     float64              `json:"stale_share"`
	Threshold      float64              `json:"threshold"`
}

// AccelerationProof is the per-turn cost trajectory the
// acceleration advisor judges on. RecentMean / PriorMean / Ratio
// are the numbers the user sees in the modal's "why klyne thinks
// you're accelerating" panel.
type AccelerationProof struct {
	RecentMean       float64 `json:"recent_mean"`
	PriorMean        float64 `json:"prior_mean"`
	Ratio            float64 `json:"ratio"`
	LatestEffective  int64   `json:"latest_effective"`
	SampledTurns     int     `json:"sampled_turns"`
	WouldFire        bool    `json:"would_fire"`
}

// ContextWindowProof proves the hard-ceiling advisor: current
// fill percentage, latest prefix size, and the configured
// threshold (always 75% in v1).
type ContextWindowProof struct {
	FillPct       float64 `json:"fill_pct"`
	LatestInput   int64   `json:"latest_input"`
	ContextWindow int64   `json:"context_window"`
	Model         string  `json:"model"`
	Threshold     float64 `json:"threshold"`
	WouldFire     bool    `json:"would_fire"`
}

// FiveHourProof is the cross-session aggregate the 5-hour-window
// advisor judges on.
type FiveHourProof struct {
	TotalEffective int64   `json:"total_effective"`
	Cap            int64   `json:"cap"`
	PctUsed        float64 `json:"pct_used"`
	PlanTier       string  `json:"plan_tier,omitempty"`
}

// TopicShiftProof is the live signal for the topic_shift advisor.
// The detector compares the bag-of-words of the first 5 user prompts
// against the last 5; Shifted is true when their Jaccard overlap
// falls below the topic-overlap threshold. WouldFire combines that
// with a minimum stale-share floor so the panel matches the
// advisor's firing logic 1:1.
type TopicShiftProof struct {
	Shifted   bool `json:"shifted"`
	WouldFire bool `json:"would_fire"`
}

// AdvisorDetailResponse is GET /sessions/{id}/advisor-detail —
// per-session advisory list PLUS the underlying proof data the
// cockpit modal needs to show the user *why* klyne fired each
// advisory. The proof object is always populated regardless of
// whether the corresponding trigger has fired yet, so the user
// can see the live state and judge for themselves.
type AdvisorDetailResponse struct {
	SessionID     string             `json:"session_id"`
	Advisories    []AdvisoryRow      `json:"advisories"`
	Stale         StaleProof         `json:"stale"`
	Acceleration  AccelerationProof  `json:"acceleration"`
	ContextWindow ContextWindowProof `json:"context_window"`
	FiveHour      FiveHourProof      `json:"five_hour,omitempty"`
	TopicShift    TopicShiftProof    `json:"topic_shift"`
}

// ---------------------------------------------------------------------------
// /healthz
// ---------------------------------------------------------------------------

// HealthzResponse is GET /healthz.
type HealthzResponse struct {
	OK            bool   `json:"ok"`
	Version       string `json:"version"`
	SchemaVersion int    `json:"schema_version"`
}

// ---------------------------------------------------------------------------
// /code-review-context  — optional code-review-graph enrichment
// ---------------------------------------------------------------------------

// CodeReviewContextResponse is GET /code-review-context — the parsed
// `<project_root>/.code-review-graph/summary.json` enrichment. When the
// upstream tool is not installed for the queried repo, Detected is
// false and every slice is empty (non-nil) so the UI can render
// "enrichment unavailable" without nil-check gymnastics.
type CodeReviewContextResponse struct {
	// ProjectRoot echoes the path that was inspected — the request's
	// ?project_root= argument or the daemon's working directory.
	ProjectRoot string `json:"project_root"`
	// Detected mirrors codereviewgraph.Detect: true iff the
	// `.code-review-graph/` directory exists AND is non-empty.
	Detected bool `json:"detected"`
	// HighRiskFiles lists repo-relative paths upstream flagged as
	// historically risky.
	HighRiskFiles []string `json:"high_risk_files"`
	// RecentBlockers is the open review-blocking issues feed.
	RecentBlockers []codereviewgraph.Blocker `json:"recent_blockers"`
	// FrequentReviewers is the de-duped reviewer-handle list.
	FrequentReviewers []string `json:"frequent_reviewers"`
}

// ---------------------------------------------------------------------------
// /memory  — user-facing facade over the decisions table
// ---------------------------------------------------------------------------

// MemoryProjectGroup is one bucket in MemoryResponse.ByProject —
// every memory recorded under the same project_path, grouped together
// for the dashboard's per-service view.
type MemoryProjectGroup struct {
	// ProjectPath is the absolute project root (e.g.
	// "/Users/mohitpatel/Desktop/Learning/consultation-service").
	ProjectPath string `json:"project_path"`
	// Name is the last path segment of ProjectPath — the human-
	// readable "service" label used as the group heading.
	Name string `json:"name"`
	// Memories are the rows scoped to this project, newest first.
	Memories []store.Decision `json:"memories"`
	// Count mirrors len(Memories) for JSON consumers.
	Count int `json:"count"`
}

// MemoryResponse is GET /memory. Splits memories into one "global"
// list (project_path == "") and one group per project. Mirrors the
// MCP `recall` tool's output shape but expanded across every project
// for the dashboard view.
type MemoryResponse struct {
	// Global is every memory with project_path == "" (applies
	// everywhere). Newest first.
	Global []store.Decision `json:"global"`
	// ByProject is one bucket per distinct project_path, sorted by
	// most-recent-activity DESC.
	ByProject []MemoryProjectGroup `json:"by_project"`
	// Totals — pre-computed so the UI doesn't need to re-iterate.
	GlobalCount  int `json:"global_count"`
	ProjectCount int `json:"project_count"` // distinct projects with ≥ 1 memory
	Total        int `json:"total"`         // grand total of memories
}

// ---------------------------------------------------------------------------
// /insights/projects — per-project rollup for the Insights dashboard
// ---------------------------------------------------------------------------

// AgentSlice carries one CLI's contribution within a project bucket.
// Always populated (zero-valued when the project has no activity for
// that CLI in the window) so the UI can render a stacked bar without
// nil-check branching.
type AgentSlice struct {
	// Tokens is tokens_in + tokens_out for this CLI within the window.
	Tokens int64 `json:"tokens"`
	// TokensIn / TokensOut are exposed separately so the UI can compute
	// cache and efficiency ratios per-agent if it ever wants to.
	TokensIn  int64 `json:"tokens_in"`
	TokensOut int64 `json:"tokens_out"`
	Messages  int64 `json:"messages"`
	Sessions  int64 `json:"sessions"`
}

// DailyPoint is one day-bucket on the per-project activity sparkline.
// Day is an ISO-8601 calendar day in UTC (e.g. "2026-05-12").
type DailyPoint struct {
	Day    string `json:"day"`
	Tokens int64  `json:"tokens"`
}

// TopSession is one of the heaviest sessions inside a project bucket —
// the drill-down target the user clicks to answer "which session ate
// the tokens here?". Always tokens_in + tokens_out for the session row.
type TopSession struct {
	SessionID string `json:"session_id"`
	CLI       string `json:"cli"`
	Model     string `json:"model"`
	Tokens    int64  `json:"tokens"`
	Messages  int64  `json:"messages"`
	LastMsgAt int64  `json:"last_msg_at"`
}

// ProjectInsight is one fully-enriched row of the Insights dashboard.
// Everything the UI needs to render the project row + expanded panel
// without a follow-up round-trip.
//
// Subscription-aware design note: this DTO deliberately does NOT carry
// dollar costs. Klyne users are on flat plans; raw $ figures don't
// match the bill and create more confusion than insight. The
// "weight" of a project is conveyed via share-of-total and the trend
// vs prior window instead.
type ProjectInsight struct {
	// ProjectPath is the absolute path used as the stable key. Empty
	// string is the catch-all "(unknown)" bucket for sessions without
	// a resolved project_path.
	ProjectPath string `json:"project_path"`
	// Name is the last path segment of ProjectPath — the human-readable
	// label used for the row heading.
	Name string `json:"name"`

	// Tokens is tokens_in + tokens_out within the window. The primary
	// sort key when the UI sorts by tokens.
	Tokens    int64 `json:"tokens"`
	TokensIn  int64 `json:"tokens_in"`
	TokensOut int64 `json:"tokens_out"`
	// Messages and Sessions counts within the window.
	Messages int64 `json:"messages"`
	Sessions int64 `json:"sessions"`
	// LastMsgAt is the most-recent session activity within the window
	// (epoch-ms). Zero when the project has no sessions in the window.
	LastMsgAt int64 `json:"last_msg_at"`

	// Per-CLI breakdown. Always both present (zero when absent) so the
	// UI can render the stacked bar without a presence check.
	Claude AgentSlice `json:"claude"`
	Codex  AgentSlice `json:"codex"`

	// CachedReadTokens is the prompt-cache prefix served during the
	// window. CacheHitPct = CachedReadTokens / TokensIn * 100; zero
	// when TokensIn is zero. Low cache-hit % is the closest available
	// "wasted effort" signal under flat-rate subscriptions.
	CachedReadTokens int64   `json:"cached_read_tokens"`
	CacheHitPct      float64 `json:"cache_hit_pct"`

	// TokensPerMessage = Tokens / Messages; zero when Messages is zero.
	// High values flag context-bloated projects regardless of plan tier.
	TokensPerMessage float64 `json:"tokens_per_message"`

	// CompactCount is the number of /compact events the project's
	// sessions hit within the window — a pain signal (compact = the
	// user ran out of context room).
	CompactCount int64 `json:"compact_count"`

	// PriorTokens is the same Tokens metric computed over the prior
	// window of equal duration. TrendPct = (Tokens-PriorTokens) /
	// PriorTokens * 100; zero when PriorTokens is zero AND Tokens is
	// zero, or +100*Tokens when PriorTokens is zero and Tokens > 0
	// (encoded as the largest meaningful jump for the UI to flag).
	PriorTokens int64   `json:"prior_tokens"`
	TrendPct    float64 `json:"trend_pct"`

	// Daily is the per-day token sparkline for the window, oldest
	// first. Empty when there is no activity. Days with zero tokens
	// are omitted (the UI can fill gaps if it wants a continuous axis).
	Daily []DailyPoint `json:"daily"`

	// TopSessions are the heaviest sessions in this project within
	// the window, tokens DESC, capped at 3. Empty when the project
	// has no sessions in the window.
	TopSessions []TopSession `json:"top_sessions"`
}

// ProjectInsightsResponse is GET /insights/projects.
//
// Query params:
//   - since int64   epoch-ms lower bound (default 0 = all time)
//   - until int64   epoch-ms upper bound (default 0 = "now")
//   - top   int     # of top sessions to attach per project (default 3,
//                   max 10). Zero disables the drill-down.
//
// The prior-window comparison reuses the same duration as
// [Since, Until], shifted left by exactly that duration. When Since==0
// the prior window is empty (no comparable past), TrendPct is 0, and
// PriorTokens is 0 for every project.
type ProjectInsightsResponse struct {
	Since      int64 `json:"since"`
	Until      int64 `json:"until"`
	PriorSince int64 `json:"prior_since"`
	PriorUntil int64 `json:"prior_until"`

	// Projects ordered by Tokens DESC. The first entry is the "top
	// project" headline the UI surfaces above the list.
	Projects []ProjectInsight `json:"projects"`

	// Totals aggregates every Projects row for the current window.
	// Name is "total". Daily / TopSessions are intentionally nil here —
	// the per-project rows already carry that detail, and aggregating
	// them at this level would just duplicate work the UI never
	// requested.
	Totals ProjectInsight `json:"totals"`
}

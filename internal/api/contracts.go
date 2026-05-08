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

import "github.com/klyne-ai/klyne/internal/connectors"

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
	RouteSearch              = "/search"
	RouteCostSummary         = "/cost/summary"
	RouteUsage               = "/usage"
	RouteCockpitThreads      = "/cockpit/threads"
	RouteSettings            = "/settings"
	RouteWizardDetect        = "/wizard/detect"
	RouteWizardComplete      = "/wizard/complete"
	RouteEvents              = "/events"
	RouteHealthz             = "/healthz"
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
		RouteSearch,
		RouteCostSummary,
		RouteUsage,
		RouteCockpitThreads,
		RouteSettings,
		RouteWizardDetect,
		RouteWizardComplete,
		RouteEvents,
		RouteHealthz,
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
// /settings
// ---------------------------------------------------------------------------

// TaskModel is the per-internal-task model selection (spec §8 BYOK matrix).
// Provider is one of "anthropic", "openai", "gemini", "ollama"; Model is
// the provider-native model id.
type TaskModel struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// SettingsAI mirrors config.AIConfig as exposed over HTTP.
type SettingsAI struct {
	SummaryModel TaskModel `json:"summary_model"`
	TitleModel   TaskModel `json:"title_model"`
	EmbedModel   TaskModel `json:"embed_model"`
}

// SettingsResponse is GET /settings.
type SettingsResponse struct {
	AI       SettingsAI `json:"ai"`
	Detected DetectedProviders `json:"detected"`
}

// DetectedProviders is the wizard / settings view of what credentials
// the daemon currently sees. Booleans only — never echo the keys.
type DetectedProviders struct {
	Anthropic bool `json:"anthropic"`
	OpenAI    bool `json:"openai"`
	Gemini    bool `json:"gemini"`
	Ollama    bool `json:"ollama"`
}

// SettingsUpdateRequest is PUT /settings — partial updates allowed; nil
// pointers mean "leave unchanged".
type SettingsUpdateRequest struct {
	AI *SettingsAI `json:"ai,omitempty"`
}

// ---------------------------------------------------------------------------
// /wizard/*
// ---------------------------------------------------------------------------

// WizardDetectResponse is GET /wizard/detect — first-run welcome screen
// telemetry. It echoes which CLI source dirs and which BYOK creds are
// present, with no key material returned.
type WizardDetectResponse struct {
	Connectors WizardConnectors  `json:"connectors"`
	Providers  DetectedProviders `json:"providers"`
	// Recommendations is the "auto-selected model for each task" list
	// shown on the wizard's smart-picker screen (spec §8).
	Recommendations []WizardRecommendation `json:"recommendations"`
}

// WizardConnectors reports whether the v1 source directories exist.
type WizardConnectors struct {
	ClaudeRoot string `json:"claude_root"`
	ClaudeOK   bool   `json:"claude_ok"`
	CodexRoot  string `json:"codex_root"`
	CodexOK    bool   `json:"codex_ok"`
}

// WizardRecommendation is one row of the smart-model-picker screen.
type WizardRecommendation struct {
	Task     string    `json:"task"`     // "summary" | "title" | "embed"
	Selected TaskModel `json:"selected"`
	Reason   string    `json:"reason"`
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
// /healthz
// ---------------------------------------------------------------------------

// HealthzResponse is GET /healthz.
type HealthzResponse struct {
	OK            bool   `json:"ok"`
	Version       string `json:"version"`
	SchemaVersion int    `json:"schema_version"`
}

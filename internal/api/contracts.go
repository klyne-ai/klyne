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

import "github.com/mohitpatell/agentdeck/internal/connectors"

// ---------------------------------------------------------------------------
// Routes
// ---------------------------------------------------------------------------

// Route* constants enumerate every HTTP path the daemon exposes. They
// MUST be unique (verified by contracts_test.go). Path parameters use
// the chi-router ":id" syntax.
const (
	RouteSessions         = "/sessions"
	RouteSession          = "/sessions/{id}"
	RouteSessionMessages  = "/sessions/{id}/messages"
	RouteSessionRestore   = "/sessions/{id}/restore"
	RouteSessionSummary   = "/sessions/{id}/summary"
	RouteSearch           = "/search"
	RouteCostSummary      = "/cost/summary"
	RouteSettings         = "/settings"
	RouteWizardDetect     = "/wizard/detect"
	RouteWizardComplete   = "/wizard/complete"
	RouteEvents           = "/events"
	RouteHealthz          = "/healthz"
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
		RouteSearch,
		RouteCostSummary,
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

// HealthzResponse is GET /healthz.
type HealthzResponse struct {
	OK            bool   `json:"ok"`
	Version       string `json:"version"`
	SchemaVersion int    `json:"schema_version"`
}

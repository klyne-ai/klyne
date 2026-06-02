package handlers

import (
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/cost"
	"github.com/klyne-ai/klyne/internal/store"
)

// Deps holds the dependencies needed by all W7 handlers.
// W12 populates this struct and passes it to NewMounter.
//
// The daemon no longer wires AI providers — all synthesis runs in the
// user's interactive Claude/Codex session.
type Deps struct {
	DB     *store.DB
	Cfg    *config.Config
	Cost   *cost.Engine
	Logger *slog.Logger
}

// Mounter implements api.RouterMounter and registers all W7 read-only
// routes on the chi router passed to Mount.
//
// Usage (W12 wiring):
//
//	m := handlers.NewMounter(handlers.Deps{DB: db, Cfg: cfg, Cost: eng, Logger: log})
//	router := api.NewRouter(api.Deps{Mounters: []api.RouterMounter{m, sseMounter}})
type Mounter struct {
	deps Deps
}

// NewMounter constructs a Mounter that will register all W7 routes.
func NewMounter(deps Deps) *Mounter {
	return &Mounter{deps: deps}
}

// Mount registers all W7 read-only routes on r.
// Called by api.NewRouter for each entry in Deps.Mounters.
func (m *Mounter) Mount(r chi.Router) {
	logger := m.deps.Logger
	if logger == nil {
		logger = slog.Default()
	}

	hSessions := NewSessionsHandler(m.deps.DB)
	r.Get(api.RouteSessions, hSessions.List)
	r.Get(api.RouteSession, hSessions.Get)
	r.Delete(api.RouteSession, hSessions.Delete)
	r.Get(api.RouteSessionMessages, hSessions.Messages)
	r.Get(api.RouteSessionSummary, hSessions.Summary)

	hSearch := NewSearchHandler(m.deps.DB)
	r.Get(api.RouteSearch, hSearch.Search)

	hCost := NewCostHandler(m.deps.DB, m.deps.Cost)
	r.Get(api.RouteCostSummary, hCost.Summary)

	hUsage := NewUsageHandler(m.deps.DB, logger)
	r.Get(api.RouteUsage, hUsage.Get)

	hUsageStats := NewUsageStatsHandler(m.deps.DB, m.deps.Cost)
	r.Get(api.RouteUsageStats, hUsageStats.Get)

	// Session-scoped usage projection — same calibration data sources
	// as /usage so the percentages line up with the dashboard badge.
	hSessionUsage := NewSessionUsageHandler(SessionUsageDeps{
		DB:        m.deps.DB,
		OAuth:     hUsage.oauth,
		CodexSnap: hUsage.codexSnap,
		Logger:    logger,
	})
	r.Get(api.RouteSessionUsage, hSessionUsage.Get)

	// Per-session token-usage timeline — backs the cockpit line
	// chart. Shares contexthealth.ComputeTimeline with `klyne tokens`
	// and the get_token_timeline MCP tool so all three surfaces
	// stay aligned.
	hTokenTimeline := NewSessionTokenTimelineHandler(SessionTokenTimelineDeps{
		DB:     m.deps.DB,
		Logger: logger,
	})
	r.Get(api.RouteSessionTokenTimeline, hTokenTimeline.Get)

	hCockpit := NewCockpitHandler(m.deps.DB)
	r.Get(api.RouteCockpitThreads, hCockpit.Threads)

	hAdvisorDetail := NewAdvisorDetailHandler(m.deps.DB)
	r.Get(api.RouteSessionAdvisorDetail, hAdvisorDetail.Get)

	hHealth := NewHealthHandler(m.deps.DB)
	r.Get(api.RouteHealthz, hHealth.Healthz)

	// Optional code-review-graph enrichment — gracefully no-ops when
	// the upstream tool is not installed for the queried repo.
	hCodeReview := NewCodeReviewContextHandler(m.deps.DB)
	r.Get(api.RouteCodeReviewContext, hCodeReview.Get)

	// /memory — the dashboard's grouped-by-service view over the
	// decisions table. Read-only list + per-id delete; writes go
	// through the MCP `remember` tool (or the CLI).
	hMemory := NewMemoryHandler(m.deps.DB)
	r.Get(api.RouteMemory, hMemory.List)
	r.Delete(api.RouteMemoryItem, hMemory.Delete)

	// /worklog — project-scoped browser over the stop_summaries table.
	// Read-only; both visible and suppressed rows returned so users can
	// audit suppression behavior.
	hWorklog := NewWorklogHandler(m.deps.DB)
	r.Get(api.RouteWorklog, hWorklog.List)
	r.Get(api.RouteWorklogProject, hWorklog.Project)

	// POST /worklog/reflect/run — browser-initiated reflection. Spawns
	// `claude -p '/klyne:reflect'` for a path already in the worklog
	// rollup. Same-origin guarded; see worklog_reflect_run.go for the
	// full threat model.
	hReflectRun := NewWorklogReflectRunHandler(m.deps.DB)
	r.Post(api.RouteWorklogReflectRun, hReflectRun.Run)

	// /insights/projects — per-project rollup powering the Insights
	// dashboard. Reads from sessions + messages + compact_events; no
	// dollar figures (subscription users don't pay per-token).
	hInsights := NewProjectInsightsHandler(m.deps.DB)
	r.Get(api.RouteInsightsProjects, hInsights.Get)

	// /productivity — the deterministic-first AI productivity dashboard.
	// Fuses live git activity with session telemetry into a
	// Service→Branch→Topic record + reflection_status/nudge. No LLM at
	// render (spec §7.1 determinism boundary).
	hProductivity := NewProductivityHandler(m.deps.DB)
	r.Get(api.RouteProductivity, hProductivity.Get)
	r.Get(api.RouteProductivityDates, hProductivity.Dates)

	// POST /api/productivity/recompose — re-derive typed What-was-done
	// cards from worklog_reflections.body_json for (project_path, day)
	// and persist them into the day's productivity snapshot. Pure-Go
	// composer, no LLM call; chained from /worklog/reflect/run after a
	// successful reflect. See docs/plan/2026-05-26-wwd-typed-cards.md.
	hProductivityRecompose := NewProductivityRecomposeHandler(m.deps.DB)
	r.Post(api.RouteProductivityRecompose, hProductivityRecompose.Run)

	// POST /api/productivity/compile/start + GET …/compile/status — the
	// detached background second LLM pass. start computes the day's
	// floor-based pending services and launches one goroutine running
	// /klyne:productivity-sync per service sequentially on context.Background()
	// (survives client reload); status reports live per-service progress.
	// One job per day; double-start is idempotent. Replaces the old
	// blocking POST /api/productivity/compile.
	hProductivityCompile := NewProductivityCompileHandler(m.deps.DB)
	r.Post(api.RouteProductivityCompileStart, hProductivityCompile.Start)
	r.Get(api.RouteProductivityCompileStatus, hProductivityCompile.Status)

	// DELETE /api/projects — project-wide wipe of the worklog/decision/
	// runbook/work-span/git-snapshot tables for a given project_path.
	// Sessions and messages (the transcript layer) are preserved.
	hProjectDelete := NewProjectDeleteHandler(m.deps.DB)
	r.Delete(api.RouteProjectDelete, hProjectDelete.Delete)

	// GET /api/klyne-usage — per-day klyne subprocess token totals +
	// user's daily Claude denominator, used by the productivity tile
	// "Klyne is X% of today's Claude usage". Tokens only, never USD.
	hKlyneUsage := NewKlyneUsageHandler(m.deps.DB)
	r.Get(api.RouteKlyneUsage, hKlyneUsage.Get)

	// POST /api/ask — Ask Klyne chat drawer endpoint. Stateless. Each
	// turn carries (projects, range, question, history); the handler
	// pulls matching stop_summaries via LoadAskContext, builds a prompt,
	// and shells out to `claude -p` (60s timeout). No DB writes.
	hAsk := NewAskHandler(m.deps.DB)
	r.Post(api.RouteAsk, hAsk.Run)
}

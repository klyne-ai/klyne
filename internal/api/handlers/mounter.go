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
type Deps struct {
	DB     *store.DB
	Cfg    *config.Config
	Cost   *cost.Engine
	Logger *slog.Logger

	// AIFactory is used by the break-advice handler to obtain a
	// Haiku-class chat provider on demand. When nil (no AI provider
	// configured at app boot), the handler returns the "unavailable"
	// verdict without touching the network.
	AIFactory AIProviderFactory
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

	// Session-scoped usage projection — same calibration data sources
	// as /usage so the percentages line up with the dashboard badge.
	hSessionUsage := NewSessionUsageHandler(SessionUsageDeps{
		DB:        m.deps.DB,
		OAuth:     hUsage.oauth,
		CodexSnap: hUsage.codexSnap,
		Logger:    logger,
	})
	r.Get(api.RouteSessionUsage, hSessionUsage.Get)

	hBreakAdvice := NewBreakAdviceHandler(BreakAdviceDeps{
		DB:        m.deps.DB,
		Logger:    logger,
		AIFactory: m.deps.AIFactory,
	})
	r.Get(api.RouteSessionBreakAdvice, hBreakAdvice.Get)

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

	hAdvisories := NewAdvisoriesHandler(m.deps.DB)
	r.Get(api.RouteAdvisories, hAdvisories.List)

	hAdvisorDetail := NewAdvisorDetailHandler(m.deps.DB)
	r.Get(api.RouteSessionAdvisorDetail, hAdvisorDetail.Get)

	hSettings := NewSettingsHandler(m.deps.Cfg, logger)
	r.Get(api.RouteSettings, hSettings.Get)
	r.Put(api.RouteSettings, hSettings.Put)

	hHealth := NewHealthHandler(m.deps.DB)
	r.Get(api.RouteHealthz, hHealth.Healthz)

	// Optional code-review-graph enrichment — gracefully no-ops when
	// the upstream tool is not installed for the queried repo.
	hCodeReview := NewCodeReviewContextHandler()
	r.Get(api.RouteCodeReviewContext, hCodeReview.Get)
}

package handlers

import (
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/mohitpatell/agentdeck/internal/api"
	"github.com/mohitpatell/agentdeck/internal/config"
	"github.com/mohitpatell/agentdeck/internal/cost"
	"github.com/mohitpatell/agentdeck/internal/store"
)

// Deps holds the dependencies needed by all W7 handlers.
// W12 populates this struct and passes it to NewMounter.
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
	r.Get(api.RouteSessionMessages, hSessions.Messages)
	r.Get(api.RouteSessionSummary, hSessions.Summary)

	hSearch := NewSearchHandler(m.deps.DB)
	r.Get(api.RouteSearch, hSearch.Search)

	hCost := NewCostHandler(m.deps.DB, m.deps.Cost)
	r.Get(api.RouteCostSummary, hCost.Summary)

	hSettings := NewSettingsHandler(m.deps.Cfg, logger)
	r.Get(api.RouteSettings, hSettings.Get)
	r.Put(api.RouteSettings, hSettings.Put)

	hHealth := NewHealthHandler(m.deps.DB)
	r.Get(api.RouteHealthz, hHealth.Healthz)
}

package handlers

import (
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/store"
)

// WizardMounterDeps holds the dependencies for the WizardMounter.
// W12 populates this struct and passes it to NewWizardMounter.
//
// Integration contract for W12:
//
//	deps.Mounters = append(deps.Mounters, handlers.NewWizardMounter(handlers.WizardMounterDeps{
//	    DB:     db,
//	    Cfg:    cfg,
//	    Logger: log,
//	}))
type WizardMounterDeps struct {
	// DB is the store database handle — used by the restore handler to
	// fetch session data, summaries, and messages.
	DB *store.DB
	// Cfg is the live config loaded at startup — the wizard complete handler
	// patches and saves it via config.Save.
	Cfg *config.Config
	// Logger is the structured logger. When nil, slog.Default() is used.
	Logger *slog.Logger
}

// WizardMounter implements api.RouterMounter and registers the W15 routes:
//   - GET /sessions/{id}/restore
//   - GET /wizard/detect
//   - POST /wizard/complete
type WizardMounter struct {
	deps WizardMounterDeps
}

// NewWizardMounter constructs a WizardMounter.
func NewWizardMounter(deps WizardMounterDeps) *WizardMounter {
	return &WizardMounter{deps: deps}
}

// Mount registers all W15 routes on r.
// Called by api.NewRouter for each entry in Deps.Mounters.
//
// Routes registered:
//   - GET /sessions/{id}/restore — flow C restore context
//   - GET /wizard/detect         — first-run detection
//   - POST /wizard/complete       — save wizard result
func (m *WizardMounter) Mount(r chi.Router) {
	logger := m.deps.Logger
	if logger == nil {
		logger = slog.Default()
	}

	hRestore := NewRestoreHandler(m.deps.DB)
	r.Get(api.RouteSessionRestore, hRestore.Restore)

	hWizard := NewWizardHandler(m.deps.Cfg, logger)
	r.Get(api.RouteWizardDetect, hWizard.Detect)
	r.Post(api.RouteWizardComplete, hWizard.Complete)
}

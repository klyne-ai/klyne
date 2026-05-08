package handlers

import (
	"net/http"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/store"
)

// version is the daemon binary version. Injected at build time via
// -ldflags "-X ..."; defaults to "dev" in local builds.
var version = "dev"

// HealthHandler handles GET /healthz.
type HealthHandler struct {
	db *store.DB
}

// NewHealthHandler constructs a HealthHandler.
func NewHealthHandler(db *store.DB) *HealthHandler {
	return &HealthHandler{db: db}
}

// Healthz handles GET /healthz.
// Returns 200 when the store is reachable, 503 otherwise.
func (h *HealthHandler) Healthz(w http.ResponseWriter, r *http.Request) {
	schemaVer, err := h.db.SchemaVersion(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, api.HealthzResponse{
			OK:            false,
			Version:       version,
			SchemaVersion: 0,
		})
		return
	}

	writeJSON(w, http.StatusOK, api.HealthzResponse{
		OK:            true,
		Version:       version,
		SchemaVersion: schemaVer,
	})
}

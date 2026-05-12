package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/klyne-ai/klyne/internal/cost"
	"github.com/klyne-ai/klyne/internal/store"
	"github.com/klyne-ai/klyne/internal/usagestats"
)

// UsageStatsHandler powers GET /usage/stats. Returns the cross-session
// Overview / Daily / Stats / Models aggregates used by the new web
// dashboard (and any future tokscale-style consumer).
type UsageStatsHandler struct {
	db     *store.DB
	engine *cost.Engine
}

// NewUsageStatsHandler constructs the handler. The cost engine is used
// to apply the live pricing table to recorded token counts, matching
// klyne's audit-sessions contract.
func NewUsageStatsHandler(db *store.DB, engine *cost.Engine) *UsageStatsHandler {
	return &UsageStatsHandler{db: db, engine: engine}
}

// Get handles GET /usage/stats.
//
// Query parameters:
//   - cli=claude|codex (optional; both when blank)
//   - days=N           (defaults to 30; max 365)
//   - heatmap_weeks=N  (0 disables; defaults to 12; max 52)
//
// Response: JSON-serialised *usagestats.Stats.
func (h *UsageStatsHandler) Get(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	cli := q.Get("cli")
	if cli != "" && cli != "claude" && cli != "codex" {
		http.Error(w, "invalid cli: must be claude or codex", http.StatusBadRequest)
		return
	}
	days := atoiOrDefault(q.Get("days"), 30, 1, 365)
	heatmapWeeks := atoiOrDefault(q.Get("heatmap_weeks"), 12, 0, 52)

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	stats, err := usagestats.Compute(ctx, h.db, h.engine, usagestats.Filter{
		CLI:  cli,
		Days: days,
	}, heatmapWeeks)
	if err != nil {
		http.Error(w, "compute usage stats: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(stats); err != nil {
		// Best-effort: the connection may have been hung up by the
		// client. Log via stderr — the daemon's logger lives behind a
		// dep we don't have here, and the handler interface mirrors
		// every other read endpoint in this package.
		_ = err
	}
}

// atoiOrDefault parses s as a base-10 int and clamps to [lo, hi].
// Returns def for empty / unparsable input.
func atoiOrDefault(s string, def, lo, hi int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}

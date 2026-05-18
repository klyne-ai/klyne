package handlers

import (
	"net/http"
	"sort"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/store"
)

// WorklogHandler serves /worklog/items — the per-project reflection rollup
// powering the cockpit's Worklog page. Surfaces the synthesized reflection
// tier (worklog_reflections) plus a stale-ness signal computed from visible
// stop_summaries written after the latest reflection.
type WorklogHandler struct {
	db *store.DB
}

// NewWorklogHandler constructs a WorklogHandler.
func NewWorklogHandler(db *store.DB) *WorklogHandler {
	return &WorklogHandler{db: db}
}

// List handles GET /worklog/items.
//
// Returns one entry per project that has either a reflection or at least one
// visible stop_summaries row. Sorted so the most actionable rows surface
// first:
//
//  1. Stale projects that already have a prior reflection (refresh needed)
//  2. Cold-start projects: entries but no reflection yet (first-time synth needed)
//  3. Fresh projects: reflection covers everything (nothing to do)
//
// Within each tier rows are ordered by the most recent activity (newer first).
func (h *WorklogHandler) List(w http.ResponseWriter, r *http.Request) {
	rollup, err := store.ListWorklogRollup(r.Context(), h.db)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	sortWorklogRollup(rollup)
	writeJSON(w, http.StatusOK, api.WorklogResponse{Projects: rollup})
}

// sortWorklogRollup orders rollups in tiers (see List for the rule) and then
// by newest activity within each tier. Operates in place.
func sortWorklogRollup(rows []store.WorklogProjectRollup) {
	sort.SliceStable(rows, func(i, j int) bool {
		ti := tierOf(rows[i])
		tj := tierOf(rows[j])
		if ti != tj {
			return ti < tj
		}
		// Within tier, newer-active first. activityTs prefers latest entry
		// when present, falling back to the reflection ts so cold projects
		// don't all collapse to 0.
		return activityTs(rows[i]) > activityTs(rows[j])
	})
}

// tierOf returns 0=stale-with-reflection, 1=cold-start, 2=fresh.
func tierOf(r store.WorklogProjectRollup) int {
	switch {
	case r.Stale && r.LatestReflection != nil:
		return 0
	case r.LatestReflection == nil:
		return 1
	default:
		return 2
	}
}

func activityTs(r store.WorklogProjectRollup) int64 {
	if r.LatestEntryTs > 0 {
		return r.LatestEntryTs
	}
	if r.LatestReflection != nil {
		return r.LatestReflection.TS
	}
	return 0
}

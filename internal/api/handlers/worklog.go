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

// sortWorklogRollup orders by most-recent activity (entry OR reflection),
// newest first. We intentionally don't tier-group because the user expects
// "what I just did" to be at the top — color/status pills on the card carry
// the stale/cold/fresh signal without needing to reorder the list.
func sortWorklogRollup(rows []store.WorklogProjectRollup) {
	sort.SliceStable(rows, func(i, j int) bool {
		return activityTs(rows[i]) > activityTs(rows[j])
	})
}

// activityTs returns the most-recent timestamp for the project, preferring
// the latest entry but falling back to the reflection ts so projects with
// only a reflection still get ordered correctly.
func activityTs(r store.WorklogProjectRollup) int64 {
	ts := r.LatestEntryTs
	if r.LatestReflection != nil && r.LatestReflection.TS > ts {
		ts = r.LatestReflection.TS
	}
	return ts
}

// Project handles GET /worklog/items/project?path=<abs>.
//
// Returns the project's rollup (so the page header can render the stale-ness
// pill and last-activity timestamp) PLUS the full daily-reflection list for
// that project, newest-first. When the project has no rows in either table,
// Reflections is empty and the rollup carries zero counts — the caller
// receives a 200 with an empty payload so the drill-in page can render its
// "no reflections yet" empty state without a separate error path.
func (h *WorklogHandler) Project(w http.ResponseWriter, r *http.Request) {
	projectPath := r.URL.Query().Get("path")
	if projectPath == "" {
		http.Error(w, "missing required ?path=<abs>", http.StatusBadRequest)
		return
	}

	// Pull the full rollup, then pluck the matching row. We reuse the
	// existing rollup query so the staleness/pending math stays in one
	// place. For projects with no rows the rollup returns nothing — we
	// fall back to a zero-valued rollup so the response shape stays
	// predictable for the UI's empty state.
	rollup, err := store.ListWorklogRollup(r.Context(), h.db)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	var project store.WorklogProjectRollup
	found := false
	for _, p := range rollup {
		if p.ProjectPath == projectPath {
			project = p
			found = true
			break
		}
	}
	if !found {
		project = store.WorklogProjectRollup{
			ProjectPath: projectPath,
			Name:        basenameProjectPath(projectPath),
		}
	}

	reflections, err := store.ListReflectionsForProject(r.Context(), h.db, projectPath, 200)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, api.WorklogProjectResponse{
		Project:     project,
		Reflections: reflections,
	})
}

// basenameProjectPath returns the last "/"-separated segment of p. Inline
// because store.basename is unexported; duplicating one tiny helper avoids
// widening that file's API surface for one caller.
func basenameProjectPath(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[i+1:]
		}
	}
	return p
}

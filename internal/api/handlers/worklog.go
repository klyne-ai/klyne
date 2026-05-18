package handlers

import (
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/store"
)

// WorklogHandler serves /worklog/items — the dashboard's per-project
// browser over the stop_summaries table.
type WorklogHandler struct {
	db *store.DB
}

// NewWorklogHandler constructs a WorklogHandler.
func NewWorklogHandler(db *store.DB) *WorklogHandler {
	return &WorklogHandler{db: db}
}

// List handles GET /worklog/items.
//
// Returns one "global" list plus one bucket per project that has
// recorded worklog rows. Both visible (recap_visible=1) and suppressed
// (recap_visible=0) rows are included so users can audit suppression
// behavior in the UI.
//
// Optional query params:
//   - project: scope to one project_path (absolute). Omit for all.
func (h *WorklogHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	project := strings.TrimSpace(r.URL.Query().Get("project"))

	rows, err := store.ListWorklogEntries(ctx, h.db, store.ListWorklogEntriesOpts{
		ProjectPath: project,
		Limit:       500,
	})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	resp := groupWorklogByProject(rows)
	writeJSON(w, http.StatusOK, resp)
}

// groupWorklogByProject splits entries into one "global" list
// (project_path == "") and one bucket per distinct project_path.
// Buckets are sorted by the most-recent entry inside them.
func groupWorklogByProject(rows []store.WorklogEntry) api.WorklogResponse {
	global := make([]store.WorklogEntry, 0)
	byPath := map[string][]store.WorklogEntry{}

	for _, e := range rows {
		if e.ProjectPath == "" {
			global = append(global, e)
			continue
		}
		byPath[e.ProjectPath] = append(byPath[e.ProjectPath], e)
	}

	groups := make([]api.WorklogProjectGroup, 0, len(byPath))
	for path, entries := range byPath {
		// Defensive newest-first sort; the store query already orders
		// by ts DESC but we keep the loop independent of that.
		sort.SliceStable(entries, func(i, j int) bool { return entries[i].Ts > entries[j].Ts })
		groups = append(groups, api.WorklogProjectGroup{
			ProjectPath: path,
			Name:        filepath.Base(path),
			Entries:     entries,
			Count:       len(entries),
		})
	}
	// Sort groups by most-recent entry inside them (DESC).
	sort.SliceStable(groups, func(i, j int) bool {
		ai := mostRecentWorklogTs(groups[i].Entries)
		aj := mostRecentWorklogTs(groups[j].Entries)
		if ai != aj {
			return ai > aj
		}
		return groups[i].Name < groups[j].Name
	})

	sort.SliceStable(global, func(i, j int) bool { return global[i].Ts > global[j].Ts })

	return api.WorklogResponse{
		Global:       global,
		ByProject:    groups,
		GlobalCount:  len(global),
		ProjectCount: len(groups),
		Total:        len(rows),
	}
}

func mostRecentWorklogTs(entries []store.WorklogEntry) int64 {
	if len(entries) == 0 {
		return 0
	}
	return entries[0].Ts
}

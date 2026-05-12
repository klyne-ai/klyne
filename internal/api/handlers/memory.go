package handlers

import (
	"errors"
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/store"
)

// MemoryHandler serves the /memory endpoints — the dashboard's
// per-service-grouped view of the decisions table.
type MemoryHandler struct {
	db *store.DB
}

// NewMemoryHandler constructs a MemoryHandler.
func NewMemoryHandler(db *store.DB) *MemoryHandler {
	return &MemoryHandler{db: db}
}

// List handles GET /memory/items.
//
// Returns one "global" list plus one bucket per project that has
// recorded memories. Buckets are sorted by most-recent-activity DESC
// so the dashboard's default view surfaces the services the user is
// actively working in.
//
// Optional query params (all client-side filters — schema is unchanged):
//   - q:   substring filter applied to memory text
//   - tag: single tag filter (e.g. ?tag=runbook)
//
// Pagination is intentionally absent — memories are short, deliberately
// scarce, and the dashboard wants the whole picture at once. The
// underlying ListDecisions limit is generous enough (10,000) that this
// is fine for v1.
func (h *MemoryHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	q := strings.TrimSpace(r.URL.Query().Get("q"))
	tag := strings.TrimSpace(r.URL.Query().Get("tag"))

	// One read fetches every row across every project (and global).
	// Filtering by q / tag is applied client-side so the same fetch
	// can power both the unfiltered grid AND the filtered view.
	rows, err := store.ListDecisions(ctx, h.db, store.DecisionFilter{Limit: 10_000})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	filtered := make([]store.Decision, 0, len(rows))
	for _, d := range rows {
		if q != "" && !strings.Contains(strings.ToLower(d.Text), strings.ToLower(q)) {
			continue
		}
		if tag != "" && !hasTag(d.Tags, tag) {
			continue
		}
		filtered = append(filtered, d)
	}

	resp := groupByProject(filtered)
	writeJSON(w, http.StatusOK, resp)
}

// Delete handles DELETE /memory/items/{id}. Mirrors the CLI's
// `klyne decisions delete <id>`. Returns 404 on no-match.
func (h *MemoryHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if strings.TrimSpace(id) == "" {
		http.Error(w, "id required", http.StatusBadRequest)
		return
	}
	if err := store.DeleteDecision(r.Context(), h.db, id); err != nil {
		if errors.Is(err, errMemoryNotFound) || isSQLNoRows(err) {
			http.Error(w, "memory not found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

// errMemoryNotFound is a sentinel — kept private so callers within
// this package can compare via errors.Is without taking a dependency
// on database/sql here.
var errMemoryNotFound = errors.New("memory not found")

// isSQLNoRows is a tiny shim so the handler doesn't need to import
// database/sql just to recognise the not-found case. `store.DeleteDecision`
// wraps sql.ErrNoRows in a fmt.Errorf with %w.
func isSQLNoRows(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no rows")
}

// hasTag reports whether tags contains exactly the wanted tag.
func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}

// groupByProject splits decisions into one "global" list (project_path
// == "") and one bucket per distinct project_path. Buckets are sorted
// by the most-recent memory inside them, so the project a user just
// touched lands at the top of the dashboard.
func groupByProject(rows []store.Decision) api.MemoryResponse {
	global := make([]store.Decision, 0)
	byPath := map[string][]store.Decision{}

	for _, d := range rows {
		if d.ProjectPath == "" {
			global = append(global, d)
			continue
		}
		byPath[d.ProjectPath] = append(byPath[d.ProjectPath], d)
	}

	groups := make([]api.MemoryProjectGroup, 0, len(byPath))
	for path, mems := range byPath {
		// Sort each project's memories newest-first. ListDecisions
		// already returns them in that order, but be defensive — the
		// query may evolve and this loop is the read path the UI sees.
		sort.SliceStable(mems, func(i, j int) bool { return mems[i].Ts > mems[j].Ts })
		groups = append(groups, api.MemoryProjectGroup{
			ProjectPath: path,
			Name:        filepath.Base(path),
			Memories:    mems,
			Count:       len(mems),
		})
	}
	// Sort groups by most-recent memory inside them (DESC). Falls
	// back to alphabetical when a group has no memories (defensive —
	// the loop above never appends an empty group, but defensive).
	sort.SliceStable(groups, func(i, j int) bool {
		ai := mostRecentTs(groups[i].Memories)
		aj := mostRecentTs(groups[j].Memories)
		if ai != aj {
			return ai > aj
		}
		return groups[i].Name < groups[j].Name
	})

	sort.SliceStable(global, func(i, j int) bool { return global[i].Ts > global[j].Ts })

	return api.MemoryResponse{
		Global:       global,
		ByProject:    groups,
		GlobalCount:  len(global),
		ProjectCount: len(groups),
		Total:        len(global) + len(rows) - len(global),
	}
}

func mostRecentTs(mems []store.Decision) int64 {
	if len(mems) == 0 {
		return 0
	}
	return mems[0].Ts
}

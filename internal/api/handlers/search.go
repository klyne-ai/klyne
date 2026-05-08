package handlers

import (
	"net/http"
	"time"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/store"
)

// SearchHandler handles GET /search.
type SearchHandler struct {
	db *store.DB
}

// NewSearchHandler constructs a SearchHandler.
func NewSearchHandler(db *store.DB) *SearchHandler {
	return &SearchHandler{db: db}
}

// Search handles GET /search.
//
// Query params:
//   - q     string  full-text query (required, min 1 char)
//   - limit int     max results (default 20, max 200)
//   - sort  string  "recent" (default) | "relevance"
func (h *SearchHandler) Search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		http.Error(w, "missing required query param: q", http.StatusBadRequest)
		return
	}

	limit, err := queryInt(r, "limit", 20)
	if err != nil || limit < 1 || limit > 200 {
		http.Error(w, "invalid limit: must be 1–200", http.StatusBadRequest)
		return
	}

	sort := store.SearchSortRecent
	if r.URL.Query().Get("sort") == string(store.SearchSortRelevance) {
		sort = store.SearchSortRelevance
	}

	start := time.Now()
	hits, err := store.Search(r.Context(), h.db, q, limit, sort)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	tookMs := time.Since(start).Milliseconds()

	resp := api.SearchResponse{
		Query: q,
		Hits:  make([]api.SearchHit, 0, len(hits)),
		Took:  tookMs,
	}

	for _, h := range hits {
		resp.Hits = append(resp.Hits, api.SearchHit{
			MessageID:   h.MessageID,
			SessionID:   h.SessionID,
			CLI:         h.CLI,
			ProjectPath: h.ProjectPath,
			Role:        h.Role,
			Snippet:     h.Snippet,
			Score:       h.Rank,
			Ts:          h.TS,
		})
	}

	writeJSON(w, http.StatusOK, resp)
}

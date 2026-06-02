package handlers

import (
	"log"
	"net/http"
	"os"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/connectors/codereviewgraph"
	"github.com/klyne-ai/klyne/internal/store"
)

// CodeReviewContextHandler handles GET /code-review-context.
//
// Stateless: every request re-reads the on-disk
// `.code-review-graph/summary.json` so the caller always sees a fresh
// snapshot. The upstream tool may rewrite the file at any time; no
// caching here keeps the contract simple.
type CodeReviewContextHandler struct {
	db *store.DB
}

// NewCodeReviewContextHandler constructs the handler. The db is used to
// constrain a caller-supplied project_root to the known-project allowlist so
// the endpoint can't be used as a filesystem existence oracle for arbitrary
// paths.
func NewCodeReviewContextHandler(db *store.DB) *CodeReviewContextHandler {
	return &CodeReviewContextHandler{db: db}
}

// Get handles GET /code-review-context.
//
// Query params:
//
//	project_root  (optional) — absolute path to the repo. When provided it
//	                            MUST be in the known-project set (worklog
//	                            rollup); otherwise 403. When empty, the
//	                            daemon's working directory is used.
//
// Status codes:
//
//	200  — directory absent (Detected=false) OR present and parsed
//	403  — project_root supplied but not in the known-project set
//	500  — summary.json malformed (generic message; detail is logged
//	       server-side, never echoed to the client)
func (h *CodeReviewContextHandler) Get(w http.ResponseWriter, r *http.Request) {
	root := r.URL.Query().Get("project_root")
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			log.Printf("code-review-context: resolve cwd: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": "internal error",
			})
			return
		}
		root = wd
	} else {
		// Allowlist: a caller-supplied path must already be a known project.
		// This stops the handler from doubling as a file-existence /
		// directory-probing oracle for arbitrary filesystem paths.
		known, err := isKnownProjectPath(r.Context(), h.db, root)
		if err != nil {
			log.Printf("code-review-context: allowlist lookup: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": "internal error",
			})
			return
		}
		if !known {
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error": "project_root not in known project set",
			})
			return
		}
	}

	resp := api.CodeReviewContextResponse{
		ProjectRoot:       root,
		HighRiskFiles:     []string{},
		RecentBlockers:    []codereviewgraph.Blocker{},
		FrequentReviewers: []string{},
	}

	rg, err := codereviewgraph.Load(root)
	if err != nil {
		// Malformed JSON surfaces as 500. Log the detail server-side; return
		// a generic body so we don't echo err.Error()/path back to the
		// client (info leak / probing aid).
		log.Printf("code-review-context: load %q: %v", root, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "internal error",
		})
		return
	}
	if rg != nil {
		resp.Detected = true
		resp.HighRiskFiles = rg.HighRiskFiles
		resp.RecentBlockers = rg.RecentBlockers
		resp.FrequentReviewers = rg.FrequentReviewers
	}
	writeJSON(w, http.StatusOK, resp)
}

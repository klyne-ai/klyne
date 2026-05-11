package handlers

import (
	"net/http"
	"os"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/connectors/codereviewgraph"
)

// CodeReviewContextHandler handles GET /code-review-context.
//
// Stateless: every request re-reads the on-disk
// `.code-review-graph/summary.json` so the caller always sees a fresh
// snapshot. The upstream tool may rewrite the file at any time; no
// caching here keeps the contract simple.
type CodeReviewContextHandler struct{}

// NewCodeReviewContextHandler constructs the handler. No dependencies
// — the handler is a pure projection over the filesystem.
func NewCodeReviewContextHandler() *CodeReviewContextHandler {
	return &CodeReviewContextHandler{}
}

// Get handles GET /code-review-context.
//
// Query params:
//
//	project_root  (optional) — absolute path to the repo. When empty,
//	                            the daemon's working directory is used.
//
// Status codes:
//
//	200  — directory absent (Detected=false) OR present and parsed
//	500  — summary.json malformed; the body still echoes ProjectRoot
//	       so the operator can locate the offending file.
func (h *CodeReviewContextHandler) Get(w http.ResponseWriter, r *http.Request) {
	root := r.URL.Query().Get("project_root")
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": "resolve cwd: " + err.Error(),
			})
			return
		}
		root = wd
	}

	resp := api.CodeReviewContextResponse{
		ProjectRoot:       root,
		HighRiskFiles:     []string{},
		RecentBlockers:    []codereviewgraph.Blocker{},
		FrequentReviewers: []string{},
	}

	rg, err := codereviewgraph.Load(root)
	if err != nil {
		// Malformed JSON surfaces as 500 — silently returning empty
		// would let bad fixtures rot. The body still echoes the
		// inspected path so the operator can fix it.
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error":        err.Error(),
			"project_root": root,
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

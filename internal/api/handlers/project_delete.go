package handlers

import (
	"context"
	"net/http"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/store"
)

// ProjectDeleteHandler serves DELETE /api/projects?path=<abs>&dry_run=true|false.
// Wipes every project-scoped row across the worklog/decision/runbook/work-span/
// git-snapshot tables in a single transaction. With dry_run=true it only counts
// without mutation so the UI can show a confirmation impact preview.
type ProjectDeleteHandler struct {
	db *store.DB
}

// NewProjectDeleteHandler constructs a ProjectDeleteHandler.
func NewProjectDeleteHandler(db *store.DB) *ProjectDeleteHandler {
	return &ProjectDeleteHandler{db: db}
}

// Delete handles the DELETE call. ?path is required and must be non-empty.
// ?dry_run defaults to false; "true" / "1" enable preview-only mode.
func (h *ProjectDeleteHandler) Delete(w http.ResponseWriter, r *http.Request) {
	projectPath := r.URL.Query().Get("path")
	if projectPath == "" {
		http.Error(w, "missing required ?path=<abs>", http.StatusBadRequest)
		return
	}
	dryRun := isTruthy(r.URL.Query().Get("dry_run"))

	// Allowlist: a real (non-dry-run) delete may only target a path klyne
	// already knows about (it appears in the worklog rollup). dry_run is the
	// confirmation-preview path and stays open so the UI can show impact even
	// for paths on the edge of the known set. An unknown non-dry-run path is
	// rejected with 403 — the endpoint is not a generic row-wipe vector.
	if !dryRun {
		known, err := isKnownProjectPath(r.Context(), h.db, projectPath)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if !known {
			http.Error(w, "path not in known project set", http.StatusForbidden)
			return
		}
	}

	counts, err := store.DeleteProjectScopedRows(r.Context(), h.db, projectPath, dryRun)
	if err != nil {
		http.Error(w, "delete failed", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, api.ProjectDeleteResponse{
		Deleted: !dryRun,
		Counts:  counts,
	})
}

// isKnownProjectPath reports whether projectPath appears in the worklog
// rollup — the same allowlist /worklog/reflect/run uses to gate subprocess
// spawns. Shared by the project-delete and code-review-context handlers so a
// caller-supplied path can never reach a destructive/filesystem-touching code
// path unless klyne already indexed it.
func isKnownProjectPath(ctx context.Context, db *store.DB, projectPath string) (bool, error) {
	rollup, err := store.ListWorklogRollup(ctx, db)
	if err != nil {
		return false, err
	}
	for _, p := range rollup {
		if p.ProjectPath == projectPath {
			return true, nil
		}
	}
	return false, nil
}

func isTruthy(s string) bool {
	switch s {
	case "1", "true", "TRUE", "True", "yes", "on":
		return true
	}
	return false
}

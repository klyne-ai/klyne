package handlers

import (
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

func isTruthy(s string) bool {
	switch s {
	case "1", "true", "TRUE", "True", "yes", "on":
		return true
	}
	return false
}

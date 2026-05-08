// Package handlers implements the HTTP handlers for the agentdeck API.
// Each handler is a thin translation layer between the HTTP contract
// (defined in internal/api/contracts.go) and the store DAOs.
//
// W7-owned. Do not edit from other workstreams.
package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/store"
)

// SessionsHandler handles all /sessions* routes.
type SessionsHandler struct {
	db *store.DB
}

// NewSessionsHandler constructs a SessionsHandler with the given store.
func NewSessionsHandler(db *store.DB) *SessionsHandler {
	return &SessionsHandler{db: db}
}

// List handles GET /sessions.
//
// Query params:
//   - limit  int    (default 50, max 500)
//   - before int64  epoch-ms cursor for pagination (0 = unbounded)
//   - cli    string filter by CLI ("claude"|"codex")
//   - project string filter by project_path
func (h *SessionsHandler) List(w http.ResponseWriter, r *http.Request) {
	limit, err := queryInt(r, "limit", 50)
	if err != nil || limit < 1 || limit > 500 {
		http.Error(w, "invalid limit: must be 1-500", http.StatusBadRequest)
		return
	}

	before, err := queryInt64(r, "before", 0)
	if err != nil || before < 0 {
		http.Error(w, "invalid before: must be a non-negative epoch-ms integer", http.StatusBadRequest)
		return
	}

	filter := store.SessionFilter{
		CLI:         r.URL.Query().Get("cli"),
		ProjectPath: r.URL.Query().Get("project"),
		Limit:       limit,
		Before:      before,
	}

	sessions, err := store.ListSessions(r.Context(), h.db, filter)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	resp := api.SessionListResponse{
		Sessions:   make([]connectors.Session, 0, len(sessions)),
		NextBefore: 0,
	}

	for _, s := range sessions {
		resp.Sessions = append(resp.Sessions, *s)
	}

	// Emit cursor only when the page is full (more may exist).
	if len(sessions) == limit {
		resp.NextBefore = sessions[len(sessions)-1].LastMsgAt
	}

	writeJSON(w, http.StatusOK, resp)
}

// Get handles GET /sessions/{id}.
func (h *SessionsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}

	session, err := store.GetSession(r.Context(), h.db, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, api.SessionResponse{Session: *session})
}

// Messages handles GET /sessions/{id}/messages.
//
// Query params:
//   - limit  int   (default 100, max 500)
//   - before int64 epoch-ms cursor (0 = unbounded)
func (h *SessionsHandler) Messages(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}

	// Verify session exists first so we return 404 rather than an empty list
	// for unknown sessions.
	_, err := store.GetSession(r.Context(), h.db, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	limit, err := queryInt(r, "limit", 100)
	if err != nil || limit < 1 || limit > 500 {
		http.Error(w, "invalid limit: must be 1-500", http.StatusBadRequest)
		return
	}

	before, err := queryInt64(r, "before", 0)
	if err != nil || before < 0 {
		http.Error(w, "invalid before: must be a non-negative epoch-ms integer", http.StatusBadRequest)
		return
	}

	order := r.URL.Query().Get("order") // "" / "asc" → ASC, "desc" → DESC

	// Optional (branch, cwd) filter — used by the cockpit when one
	// session has parallel sub-threads from different worktrees.
	// query.Has() requires Go 1.17+; chi gives us net/url's parsed form.
	// Distinguish "filter by empty string" from "no filter" using the
	// param's presence in the raw query.
	q := r.URL.Query()
	filter := store.MessageFilter{}
	if q.Has("branch") {
		filter.Branch = q.Get("branch")
		filter.BranchSet = true
	}
	if q.Has("cwd") {
		filter.Cwd = q.Get("cwd")
		filter.CwdSet = true
	}

	msgs, err := store.ListMessagesBySessionFiltered(r.Context(), h.db, id, limit, before, order, filter)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	resp := api.MessageListResponse{
		Messages:   make([]connectors.Message, 0, len(msgs)),
		NextBefore: 0,
	}
	for _, m := range msgs {
		resp.Messages = append(resp.Messages, *m)
	}
	if len(msgs) == limit {
		resp.NextBefore = msgs[len(msgs)-1].Ts
	}

	writeJSON(w, http.StatusOK, resp)
}

// Summary handles GET /sessions/{id}/summary.
// Returns the latest rolling summary for the session, or 404 if none exists.
func (h *SessionsHandler) Summary(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}

	// Verify session exists before querying summaries.
	_, err := store.GetSession(r.Context(), h.db, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	summary, err := store.LatestSummary(r.Context(), h.db, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "no summary available for this session", http.StatusNotFound)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, api.SummaryResponse{
		SessionID: summary.SessionID,
		Version:   summary.Version,
		Text:      summary.Text,
		Model:     summary.Model,
		Ts:        summary.TS,
	})
}

// Delete handles DELETE /sessions/{id}.
//
// Removes the session row, all its messages (FK cascade), all FTS5 entries
// (cascade-driven trigger), and any thread/summary children. Returns 204 on
// success, 404 if no row matched, 500 on unexpected error.
//
// Note: this only deletes from the agentdeck DB. The on-disk JSONL files at
// ~/.claude/projects/* and ~/.codex/sessions/* are NOT touched — the connector
// will NOT re-ingest them on next start because warm-up uses the dedup-by-ID
// path on InsertMessage. To prevent re-ingestion on a *fresh* DB rebuild, the
// user should delete the source JSONL file too (out of scope here).
func (h *SessionsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}
	if err := store.DeleteSession(r.Context(), h.db, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- helpers shared across all handlers in this package ---

// queryInt parses a URL query parameter as an integer, returning def when
// the parameter is absent. Returns an error when the value cannot be parsed.
func queryInt(r *http.Request, key string, def int) (int, error) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return def, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, err
	}
	return v, nil
}

// queryInt64 parses a URL query parameter as an int64, returning def when
// the parameter is absent.
func queryInt64(r *http.Request, key string, def int64) (int64, error) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return def, nil
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, err
	}
	return v, nil
}

// writeJSON encodes v as JSON and writes it with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

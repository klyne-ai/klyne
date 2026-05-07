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

	"github.com/mohitpatell/agentdeck/internal/api"
	"github.com/mohitpatell/agentdeck/internal/connectors"
	"github.com/mohitpatell/agentdeck/internal/store"
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

	msgs, err := store.ListMessagesBySession(r.Context(), h.db, id, limit, before)
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

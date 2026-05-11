package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/store"
)

// AdvisoriesHandler serves GET /advisories — every klyne advisor
// message ever ingested into the SQLite index, across every session
// in every project. The data source is the `messages` table: when
// klyne's UserPromptSubmit hook injects an advisory line, Claude
// Code stores it as a type=attachment row whose parsed canonical
// shape lands as role=system with the literal "klyne: …" content.
// The cockpit reads those rows back here.
//
// Newest first. Capped at the URL's `?limit=` (default 200, max
// 1000) so an oversized response can't lock the page on a user
// with thousands of historical advisories.
type AdvisoriesHandler struct {
	db *store.DB
}

// NewAdvisoriesHandler constructs the handler.
func NewAdvisoriesHandler(db *store.DB) *AdvisoriesHandler {
	return &AdvisoriesHandler{db: db}
}

// List handles GET /advisories.
//
// Query params:
//   - limit int   max rows (default 200, max 1000)
//   - kind  str   optional filter — one of stale, acceleration,
//                 hard_ceiling, window_50, window_75, unknown
//
// All advisories are role=system messages whose Content starts
// with "klyne: " — that's klyne's own convention from
// internal/contexthealth/advisor.go. The classifier here is a
// case-insensitive substring match; it's lenient on purpose so a
// future advisor copy refresh doesn't silently drop rows from
// the feed.
func (h *AdvisoriesHandler) List(w http.ResponseWriter, r *http.Request) {
	limit, err := queryInt(r, "limit", 200)
	if err != nil || limit < 1 || limit > 1000 {
		http.Error(w, "invalid limit: must be 1-1000", http.StatusBadRequest)
		return
	}
	kindFilter := strings.TrimSpace(r.URL.Query().Get("kind"))

	const q = `
SELECT m.id, m.session_id, s.cli, COALESCE(s.project_path,''), m.content, m.ts
FROM messages m
LEFT JOIN sessions s ON s.id = m.session_id
WHERE m.role = 'system'
  AND m.content LIKE 'klyne:%'
ORDER BY m.ts DESC
LIMIT ?
`
	rows, err := h.db.Read().Query(q, limit)
	if err != nil {
		http.Error(w, "query advisories: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	out := make([]api.AdvisoryRow, 0, limit)
	for rows.Next() {
		var row api.AdvisoryRow
		if err := rows.Scan(&row.MessageID, &row.SessionID, &row.CLI, &row.ProjectPath, &row.Content, &row.TS); err != nil {
			http.Error(w, "scan advisory: "+err.Error(), http.StatusInternalServerError)
			return
		}
		row.Kind = ClassifyAdvisory(row.Content)
		if kindFilter != "" && string(row.Kind) != kindFilter {
			continue
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		http.Error(w, "iterate advisories: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(api.AdvisoryListResponse{Advisories: out})
}

// ClassifyAdvisory inspects the advisory body and returns the
// matching kind. Exported so tests and future surfaces can reuse
// the same substring rules.
//
// Order matters: each trigger's most distinctive substring is
// checked before the more generic "5-hour window" phrase, because
// the acceleration advisory ALSO mentions "5-hour window" in its
// "you'll burn through it faster" tail. Returns AdvisoryKindUnknown
// on no match — never errors, so the feed stays robust to copy
// refreshes.
func ClassifyAdvisory(content string) api.AdvisoryKind {
	c := strings.ToLower(content)
	switch {
	// Highly distinctive phrases first; each can only appear in
	// one advisor's copy.
	case strings.Contains(c, "per-turn cost has roughly doubled"):
		return api.AdvisoryKindAcceleration
	case strings.Contains(c, "next turn's prefix will keep growing"):
		return api.AdvisoryKindHardCeiling
	// Topic-shift phrase must be checked BEFORE the stale phrase
	// because the topic-shift copy also mentions "stale relative
	// to your new direction" (similar tail). The "shifted topic
	// since the session opened" anchor is unique to topic_shift.
	case strings.Contains(c, "shifted topic since the session opened"):
		return api.AdvisoryKindTopicShift
	case strings.Contains(c, "stale relative to your current direction"):
		return api.AdvisoryKindStale
	// Five-hour patterns last because acceleration's tail also
	// mentions "5-hour window."
	case strings.Contains(c, "cheapest next step is /klyne:handoff"):
		return api.AdvisoryKindFiveHourUrgent
	case strings.Contains(c, "5-hour window") || strings.Contains(c, "dominant consumer"):
		return api.AdvisoryKindFiveHourWarn
	default:
		return api.AdvisoryKindUnknown
	}
}

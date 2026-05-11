package handlers

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"sort"

	"github.com/go-chi/chi/v5"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/contexthealth"
	"github.com/klyne-ai/klyne/internal/store"
	"github.com/klyne-ai/klyne/internal/usage"
)

// AdvisorDetailHandler serves GET /sessions/{id}/advisor-detail.
//
// The cockpit calls this when the user clicks the "ⓘ" button on a
// session tile. The response is everything needed to render the
// per-session advisory modal: the advisories themselves PLUS the
// live proof data that justifies each trigger's verdict (which
// files are stale, the per-turn acceleration trajectory, the
// current context-window fill, etc.).
//
// Read-only. Computes proof data from the DB-ingested messages —
// no JSONL re-scan, no AI calls.
type AdvisorDetailHandler struct {
	db *store.DB
}

// NewAdvisorDetailHandler constructs the handler.
func NewAdvisorDetailHandler(db *store.DB) *AdvisorDetailHandler {
	return &AdvisorDetailHandler{db: db}
}

// Get handles GET /sessions/{id}/advisor-detail.
func (h *AdvisorDetailHandler) Get(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "id")
	if sessionID == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	// Load all messages for this session. Cap at 10000 so a
	// pathological transcript doesn't OOM the daemon; klyne's
	// own classifier only really uses the last few turns for
	// acceleration / topic-shift / fill anyway.
	messages, err := store.ListMessagesBySessionOrdered(ctx, h.db, sessionID, 10000, 0, "asc")
	if err != nil {
		http.Error(w, "list messages: "+err.Error(), http.StatusInternalServerError)
		return
	}

	advisories, err := h.fetchAdvisoriesForSession(sessionID)
	if err != nil {
		http.Error(w, "fetch advisories: "+err.Error(), http.StatusInternalServerError)
		return
	}

	resp := api.AdvisorDetailResponse{
		SessionID:     sessionID,
		Advisories:    advisories,
		Stale:         buildStaleProof(messages),
		Acceleration:  buildAccelerationProof(messages),
		ContextWindow: buildContextWindowProof(messages),
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// fetchAdvisoriesForSession pulls every "klyne: …" system message
// belonging to this session, ordered oldest-first so the modal
// can render them as a chronological list.
func (h *AdvisorDetailHandler) fetchAdvisoriesForSession(sessionID string) ([]api.AdvisoryRow, error) {
	const q = `
SELECT m.id, m.session_id, s.cli, COALESCE(s.project_path,''), m.content, m.ts
FROM messages m
LEFT JOIN sessions s ON s.id = m.session_id
WHERE m.session_id = ?
  AND m.role = 'system'
  AND m.content LIKE 'klyne:%'
ORDER BY m.ts ASC
`
	rows, err := h.db.Read().Query(q, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]api.AdvisoryRow, 0, 8)
	for rows.Next() {
		var row api.AdvisoryRow
		if err := rows.Scan(&row.MessageID, &row.SessionID, &row.CLI, &row.ProjectPath, &row.Content, &row.TS); err != nil {
			return nil, err
		}
		row.Kind = ClassifyAdvisory(row.Content)
		out = append(out, row)
	}
	return out, rows.Err()
}

// buildStaleProof reuses the relevance scorer that drives the
// stale-context advisor. The shape is converted to api.FileRelevanceProof
// for the wire DTO (which adds the basename so the UI doesn't have
// to split the path itself).
//
// Returns an empty StaleProof — not an error — when there is no
// loaded-file context to score (rare but possible on a fresh session).
func buildStaleProof(msgs []*connectors.Message) api.StaleProof {
	v := contexthealth.ScoreFiles(msgs)
	files := make([]api.FileRelevanceProof, 0, len(v.Files))
	for _, f := range v.Files {
		files = append(files, api.FileRelevanceProof{
			Path:     f.Path,
			Basename: filepath.Base(f.Path),
			Bytes:    f.Bytes,
			Score:    f.Score,
			Stale:    f.Stale,
		})
	}
	// Already sorted by bytes desc in ScoreFiles, but re-affirm
	// so the cockpit gets a stable order regardless of future
	// internal-sort refactors.
	sort.Slice(files, func(i, j int) bool {
		if files[i].Bytes != files[j].Bytes {
			return files[i].Bytes > files[j].Bytes
		}
		return files[i].Path < files[j].Path
	})
	return api.StaleProof{
		Files:      files,
		StaleBytes: v.StaleBytes,
		TotalBytes: v.TotalBytes,
		StaleShare: v.StaleShare,
		Threshold:  0.5, // matches staleShareThreshold in contexthealth/relevance.go
	}
}

// buildAccelerationProof surfaces the EvaluateAcceleration verdict
// in DTO form. The UI panel uses RecentMean / PriorMean / Ratio
// directly so the user sees what numbers klyne is reasoning about,
// not just "fired" vs "didn't."
func buildAccelerationProof(msgs []*connectors.Message) api.AccelerationProof {
	v := contexthealth.EvaluateAcceleration(msgs)
	ratio := 0.0
	if v.PriorMean > 0 {
		ratio = v.RecentMean / v.PriorMean
	}
	return api.AccelerationProof{
		RecentMean:      v.RecentMean,
		PriorMean:       v.PriorMean,
		Ratio:           ratio,
		LatestEffective: v.LatestEffectiveInput,
		SampledTurns:    v.SampledTurns,
		WouldFire:       v.ShouldFire,
	}
}

// buildContextWindowProof returns the current fill state. Uses
// the latest assistant message's TokensIn (the "prefix size at
// this point in the conversation") and the model's known context
// window. WouldFire is true when fill >= 75% — matching the
// hard-ceiling threshold inside the classifier.
func buildContextWindowProof(msgs []*connectors.Message) api.ContextWindowProof {
	var latestIn int64
	var model string
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role != connectors.RoleAssistant || m.TokensIn <= 0 {
			continue
		}
		latestIn = m.TokensIn
		model = m.Model
		break
	}
	ctxWindow := usage.ContextWindowForModel(model)
	fillPct := 0.0
	if ctxWindow > 0 {
		fillPct = float64(latestIn) / float64(ctxWindow) * 100
		if fillPct > 100 {
			fillPct = 100
		}
	}
	return api.ContextWindowProof{
		FillPct:       fillPct,
		LatestInput:   latestIn,
		ContextWindow: ctxWindow,
		Model:         model,
		Threshold:     75,
		WouldFire:     fillPct >= 75,
	}
}

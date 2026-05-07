package handlers

import (
	"context"
	"fmt"
	"net/http"

	"github.com/mohitpatell/agentdeck/internal/api"
	"github.com/mohitpatell/agentdeck/internal/cost"
	"github.com/mohitpatell/agentdeck/internal/store"
)

// CostHandler handles GET /cost/summary.
type CostHandler struct {
	db     *store.DB
	engine *cost.Engine
}

// NewCostHandler constructs a CostHandler.
func NewCostHandler(db *store.DB, engine *cost.Engine) *CostHandler {
	return &CostHandler{db: db, engine: engine}
}

// Summary handles GET /cost/summary.
//
// Query params:
//   - group string  one of "session"|"project"|"day"|"model" (default "model")
//   - since int64   epoch-ms lower bound (default 0 = all time)
//   - until int64   epoch-ms upper bound (informational; stored in response)
func (h *CostHandler) Summary(w http.ResponseWriter, r *http.Request) {
	groupStr := r.URL.Query().Get("group")
	if groupStr == "" {
		groupStr = string(api.CostGroupModel)
	}
	group := api.CostGroup(groupStr)
	switch group {
	case api.CostGroupSession, api.CostGroupProject, api.CostGroupDay, api.CostGroupModel:
		// valid
	default:
		http.Error(w, fmt.Sprintf("invalid group %q: must be session|project|day|model", groupStr), http.StatusBadRequest)
		return
	}

	since, err := queryInt64(r, "since", 0)
	if err != nil || since < 0 {
		http.Error(w, "invalid since: must be a non-negative epoch-ms integer", http.StatusBadRequest)
		return
	}

	until, err := queryInt64(r, "until", 0)
	if err != nil || until < 0 {
		http.Error(w, "invalid until: must be a non-negative epoch-ms integer", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	var buckets []api.CostBucket
	var total api.CostBucket
	total.Key = "total"

	switch group {
	case api.CostGroupModel:
		byModel, err := h.engine.RollupByModel(ctx, h.db.Read(), since)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		for model, costVal := range byModel {
			buckets = append(buckets, api.CostBucket{
				Key:     model,
				CostUSD: costVal,
			})
			total.CostUSD += costVal
		}

	case api.CostGroupSession:
		buckets, total, err = h.rollupBySessions(ctx, since)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

	case api.CostGroupProject:
		buckets, total, err = h.rollupByProjects(ctx, since)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

	case api.CostGroupDay:
		buckets, total, err = h.rollupByDays(ctx, since)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	if buckets == nil {
		buckets = []api.CostBucket{}
	}

	writeJSON(w, http.StatusOK, api.CostSummaryResponse{
		Group:   group,
		Since:   since,
		Until:   until,
		Buckets: buckets,
		Total:   total,
	})
}

// rollupBySessions aggregates cost and token data per session_id using
// the pre-computed session aggregate columns.
func (h *CostHandler) rollupBySessions(ctx context.Context, since int64) ([]api.CostBucket, api.CostBucket, error) {
	rows, err := h.db.Read().QueryContext(ctx, `
		SELECT id,
		       COALESCE(tokens_in,  0),
		       COALESCE(tokens_out, 0),
		       COALESCE(cost_usd,   0),
		       COALESCE(msg_count,  0)
		FROM sessions
		WHERE last_msg_at >= ?
		ORDER BY last_msg_at DESC`, since)
	if err != nil {
		return nil, api.CostBucket{}, fmt.Errorf("cost: session rollup query: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var buckets []api.CostBucket
	total := api.CostBucket{Key: "total"}

	for rows.Next() {
		var b api.CostBucket
		if err := rows.Scan(&b.Key, &b.TokensIn, &b.TokensOut, &b.CostUSD, &b.Count); err != nil {
			return nil, api.CostBucket{}, fmt.Errorf("cost: session rollup scan: %w", err)
		}
		total.TokensIn += b.TokensIn
		total.TokensOut += b.TokensOut
		total.CostUSD += b.CostUSD
		total.Count += b.Count
		buckets = append(buckets, b)
	}
	if err := rows.Err(); err != nil {
		return nil, api.CostBucket{}, fmt.Errorf("cost: session rollup rows: %w", err)
	}
	return buckets, total, nil
}

// rollupByProjects aggregates cost and token data per project_path.
func (h *CostHandler) rollupByProjects(ctx context.Context, since int64) ([]api.CostBucket, api.CostBucket, error) {
	rows, err := h.db.Read().QueryContext(ctx, `
		SELECT project_path,
		       SUM(COALESCE(tokens_in,  0)),
		       SUM(COALESCE(tokens_out, 0)),
		       SUM(COALESCE(cost_usd,   0)),
		       SUM(COALESCE(msg_count,  0))
		FROM sessions
		WHERE last_msg_at >= ?
		GROUP BY project_path
		ORDER BY SUM(cost_usd) DESC`, since)
	if err != nil {
		return nil, api.CostBucket{}, fmt.Errorf("cost: project rollup query: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var buckets []api.CostBucket
	total := api.CostBucket{Key: "total"}

	for rows.Next() {
		var b api.CostBucket
		if err := rows.Scan(&b.Key, &b.TokensIn, &b.TokensOut, &b.CostUSD, &b.Count); err != nil {
			return nil, api.CostBucket{}, fmt.Errorf("cost: project rollup scan: %w", err)
		}
		total.TokensIn += b.TokensIn
		total.TokensOut += b.TokensOut
		total.CostUSD += b.CostUSD
		total.Count += b.Count
		buckets = append(buckets, b)
	}
	if err := rows.Err(); err != nil {
		return nil, api.CostBucket{}, fmt.Errorf("cost: project rollup rows: %w", err)
	}
	return buckets, total, nil
}

// rollupByDays aggregates cost per UTC calendar day from messages table.
func (h *CostHandler) rollupByDays(ctx context.Context, since int64) ([]api.CostBucket, api.CostBucket, error) {
	rows, err := h.db.Read().QueryContext(ctx, `
		SELECT date(ts / 1000, 'unixepoch') AS day,
		       SUM(COALESCE(tokens_in,  0)),
		       SUM(COALESCE(tokens_out, 0)),
		       SUM(COALESCE(cost_usd,   0)),
		       COUNT(*)
		FROM messages
		WHERE ts >= ?
		GROUP BY day
		ORDER BY day DESC`, since)
	if err != nil {
		return nil, api.CostBucket{}, fmt.Errorf("cost: day rollup query: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var buckets []api.CostBucket
	total := api.CostBucket{Key: "total"}

	for rows.Next() {
		var b api.CostBucket
		if err := rows.Scan(&b.Key, &b.TokensIn, &b.TokensOut, &b.CostUSD, &b.Count); err != nil {
			return nil, api.CostBucket{}, fmt.Errorf("cost: day rollup scan: %w", err)
		}
		total.TokensIn += b.TokensIn
		total.TokensOut += b.TokensOut
		total.CostUSD += b.CostUSD
		total.Count += b.Count
		buckets = append(buckets, b)
	}
	if err := rows.Err(); err != nil {
		return nil, api.CostBucket{}, fmt.Errorf("cost: day rollup rows: %w", err)
	}
	return buckets, total, nil
}

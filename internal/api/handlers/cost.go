package handlers

import (
	"context"
	"fmt"
	"net/http"

	"github.com/mohitpatell/agentdeck/internal/api"
	"github.com/mohitpatell/agentdeck/internal/store"
)

// CostHandler handles GET /cost/summary.
//
// Despite the historical "cost" name, this endpoint no longer reports dollar
// amounts. Flat-subscription users don't care about provider compute prices
// and the per-row cost recomputation was wasteful on the read path. The
// endpoint now reports activity rollups (tokens + message counts) bucketed
// by session/project/day. The CostUSD field on every bucket is 0 going
// forward, retained only for DTO backward compatibility.
type CostHandler struct {
	db *store.DB
}

// NewCostHandler constructs a CostHandler. The cost.Engine argument is no
// longer needed but kept on the signature for backward compatibility with
// the existing wiring; pass nil if you don't have one.
func NewCostHandler(db *store.DB, _ any) *CostHandler {
	return &CostHandler{db: db}
}

// Summary handles GET /cost/summary.
//
// Query params:
//   - group string  one of "session"|"project"|"day"|"model" (default "day")
//   - since int64   epoch-ms lower bound (default 0 = all time)
//   - until int64   epoch-ms upper bound (informational; stored in response)
//
// The "model" group is preserved for compatibility but only reports message
// counts per model — not dollar costs.
func (h *CostHandler) Summary(w http.ResponseWriter, r *http.Request) {
	groupStr := r.URL.Query().Get("group")
	if groupStr == "" {
		groupStr = string(api.CostGroupDay)
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
	case api.CostGroupSession:
		buckets, total, err = h.rollupBySessions(ctx, since)
	case api.CostGroupProject:
		buckets, total, err = h.rollupByProjects(ctx, since)
	case api.CostGroupDay:
		buckets, total, err = h.rollupByDays(ctx, since)
	case api.CostGroupModel:
		buckets, total, err = h.rollupByModels(ctx, since)
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
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

// rollupBySessions aggregates token + message activity per session.
func (h *CostHandler) rollupBySessions(ctx context.Context, since int64) ([]api.CostBucket, api.CostBucket, error) {
	rows, err := h.db.Read().QueryContext(ctx, `
		SELECT id,
		       COALESCE(tokens_in,  0),
		       COALESCE(tokens_out, 0),
		       COALESCE(msg_count,  0)
		FROM sessions
		WHERE last_msg_at >= ?
		ORDER BY last_msg_at DESC`, since)
	if err != nil {
		return nil, api.CostBucket{}, fmt.Errorf("activity: session rollup query: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var buckets []api.CostBucket
	total := api.CostBucket{Key: "total"}

	for rows.Next() {
		var b api.CostBucket
		if err := rows.Scan(&b.Key, &b.TokensIn, &b.TokensOut, &b.Count); err != nil {
			return nil, api.CostBucket{}, fmt.Errorf("activity: session rollup scan: %w", err)
		}
		total.TokensIn += b.TokensIn
		total.TokensOut += b.TokensOut
		total.Count += b.Count
		buckets = append(buckets, b)
	}
	if err := rows.Err(); err != nil {
		return nil, api.CostBucket{}, fmt.Errorf("activity: session rollup rows: %w", err)
	}
	return buckets, total, nil
}

// rollupByProjects aggregates token + message activity per project_path.
func (h *CostHandler) rollupByProjects(ctx context.Context, since int64) ([]api.CostBucket, api.CostBucket, error) {
	rows, err := h.db.Read().QueryContext(ctx, `
		SELECT project_path,
		       SUM(COALESCE(tokens_in,  0)),
		       SUM(COALESCE(tokens_out, 0)),
		       SUM(COALESCE(msg_count,  0))
		FROM sessions
		WHERE last_msg_at >= ?
		GROUP BY project_path
		ORDER BY SUM(tokens_out) DESC`, since)
	if err != nil {
		return nil, api.CostBucket{}, fmt.Errorf("activity: project rollup query: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var buckets []api.CostBucket
	total := api.CostBucket{Key: "total"}

	for rows.Next() {
		var b api.CostBucket
		if err := rows.Scan(&b.Key, &b.TokensIn, &b.TokensOut, &b.Count); err != nil {
			return nil, api.CostBucket{}, fmt.Errorf("activity: project rollup scan: %w", err)
		}
		total.TokensIn += b.TokensIn
		total.TokensOut += b.TokensOut
		total.Count += b.Count
		buckets = append(buckets, b)
	}
	if err := rows.Err(); err != nil {
		return nil, api.CostBucket{}, fmt.Errorf("activity: project rollup rows: %w", err)
	}
	return buckets, total, nil
}

// rollupByDays aggregates activity per UTC calendar day from the messages
// table. The dashboard sparkline reads this.
func (h *CostHandler) rollupByDays(ctx context.Context, since int64) ([]api.CostBucket, api.CostBucket, error) {
	rows, err := h.db.Read().QueryContext(ctx, `
		SELECT date(ts / 1000, 'unixepoch') AS day,
		       SUM(COALESCE(tokens_in,  0)),
		       SUM(COALESCE(tokens_out, 0)),
		       COUNT(*)
		FROM messages
		WHERE ts >= ?
		GROUP BY day
		ORDER BY day DESC`, since)
	if err != nil {
		return nil, api.CostBucket{}, fmt.Errorf("activity: day rollup query: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var buckets []api.CostBucket
	total := api.CostBucket{Key: "total"}

	for rows.Next() {
		var b api.CostBucket
		if err := rows.Scan(&b.Key, &b.TokensIn, &b.TokensOut, &b.Count); err != nil {
			return nil, api.CostBucket{}, fmt.Errorf("activity: day rollup scan: %w", err)
		}
		total.TokensIn += b.TokensIn
		total.TokensOut += b.TokensOut
		total.Count += b.Count
		buckets = append(buckets, b)
	}
	if err := rows.Err(); err != nil {
		return nil, api.CostBucket{}, fmt.Errorf("activity: day rollup rows: %w", err)
	}
	return buckets, total, nil
}

// rollupByModels aggregates message counts per model.
func (h *CostHandler) rollupByModels(ctx context.Context, since int64) ([]api.CostBucket, api.CostBucket, error) {
	rows, err := h.db.Read().QueryContext(ctx, `
		SELECT COALESCE(model, ''),
		       SUM(COALESCE(tokens_in,  0)),
		       SUM(COALESCE(tokens_out, 0)),
		       COUNT(*)
		FROM messages
		WHERE ts >= ?
		GROUP BY model
		ORDER BY SUM(tokens_out) DESC`, since)
	if err != nil {
		return nil, api.CostBucket{}, fmt.Errorf("activity: model rollup query: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var buckets []api.CostBucket
	total := api.CostBucket{Key: "total"}

	for rows.Next() {
		var b api.CostBucket
		if err := rows.Scan(&b.Key, &b.TokensIn, &b.TokensOut, &b.Count); err != nil {
			return nil, api.CostBucket{}, fmt.Errorf("activity: model rollup scan: %w", err)
		}
		total.TokensIn += b.TokensIn
		total.TokensOut += b.TokensOut
		total.Count += b.Count
		buckets = append(buckets, b)
	}
	if err := rows.Err(); err != nil {
		return nil, api.CostBucket{}, fmt.Errorf("activity: model rollup rows: %w", err)
	}
	return buckets, total, nil
}

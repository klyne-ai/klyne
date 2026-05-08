package handlers

import (
	"net/http"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/store"
)

// CockpitHandler serves the /cockpit/threads endpoint, which buckets
// recent messages by session_id so the cockpit UI shows one tile per
// running session. The most-recent git_branch is echoed as informational
// metadata for the tile label, but it is NOT used for grouping — users
// want a single point of entry per session regardless of which worktree
// it was last touched from.
type CockpitHandler struct {
	db *store.DB
}

// NewCockpitHandler constructs a CockpitHandler.
func NewCockpitHandler(db *store.DB) *CockpitHandler {
	return &CockpitHandler{db: db}
}

// Threads handles GET /cockpit/threads.
//
// Query params:
//   - since int64  epoch-ms lower bound (default 0 = all time, but the UI
//                  typically passes Date.now() - 7*86400_000)
//   - limit int    max threads (default 24, max 100)
//
// Each row in the response represents a (session_id, git_branch, cwd)
// tuple with aggregate timestamps + token totals. Sorted by last_msg_at
// DESC so the cockpit's most-recent-first ordering needs no client
// sort.
func (h *CockpitHandler) Threads(w http.ResponseWriter, r *http.Request) {
	since, err := queryInt64(r, "since", 0)
	if err != nil || since < 0 {
		http.Error(w, "invalid since: must be a non-negative epoch-ms integer", http.StatusBadRequest)
		return
	}

	limit, err := queryInt(r, "limit", 24)
	if err != nil || limit < 1 || limit > 100 {
		http.Error(w, "invalid limit: must be 1-100", http.StatusBadRequest)
		return
	}

	// Bucket by session_id only.
	//
	// We used to split on (session_id, git_branch) so parallel
	// `claude --resume <id>` invocations from different worktrees got
	// separate tiles. In practice users want one entry-point per running
	// session — branch is informational, not a separator. The most-recent
	// branch is surfaced via the correlated subquery so the tile label
	// can still show "where did I last touch this".
	//
	// `cwd` stays empty in the response — the UI uses project_path for
	// the resume command and tile label.
	const q = `
SELECT
    b.session_id,
    s.cli,
    s.project_path,
    COALESCE((
        SELECT git_branch
        FROM messages m2
        WHERE m2.session_id = b.session_id
        ORDER BY m2.ts DESC
        LIMIT 1
    ), '')                 AS git_branch,
    ''                     AS cwd,
    COALESCE(s.model, '')  AS model,
    b.last_msg_at,
    b.msg_count,
    b.tokens_in,
    b.tokens_out
FROM (
    SELECT
        session_id,
        MAX(ts)            AS last_msg_at,
        COUNT(*)           AS msg_count,
        SUM(tokens_in)     AS tokens_in,
        SUM(tokens_out)    AS tokens_out
    FROM messages
    WHERE ts >= ?
    GROUP BY session_id
) b
JOIN sessions s ON s.id = b.session_id
ORDER BY b.last_msg_at DESC
LIMIT ?`

	rows, err := h.db.Read().QueryContext(r.Context(), q, since, limit)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer rows.Close() //nolint:errcheck

	resp := api.CockpitThreadsResponse{Threads: make([]api.CockpitThread, 0, limit)}
	for rows.Next() {
		var t api.CockpitThread
		if err := rows.Scan(
			&t.SessionID,
			&t.CLI,
			&t.ProjectPath,
			&t.GitBranch,
			&t.Cwd,
			&t.Model,
			&t.LastMsgAt,
			&t.MsgCount,
			&t.TokensIn,
			&t.TokensOut,
		); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		resp.Threads = append(resp.Threads, t)
	}
	if err := rows.Err(); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

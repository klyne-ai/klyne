// Package store — messages DAO.
//
// W2 deliverable. Owned path: internal/store/messages.go.
//
// Tool-call storage: Option A (orchestrator-locked). ToolCalls and
// ToolResults slices are JSON-encoded into tool_calls_json and
// tool_results_json columns respectively (migration 004). The legacy
// tool_name column is retained for backward-compat but is not written by
// this code.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/klyne-ai/klyne/internal/connectors"
)

// InsertMessage inserts a canonical message and, in the same transaction,
// updates the parent session's aggregate counters:
//
//	last_msg_at = MAX(current, m.Ts)
//	msg_count   += 1
//	tokens_in   += m.TokensIn
//	tokens_out  += m.TokensOut
//	cost_usd    += m.CostUSD
//
// The session row must already exist (created via UpsertSession). If it does
// not, the INSERT will fail with a foreign-key constraint violation — that is
// the intended contract so callers cannot orphan messages.
//
// Uses the write handle.
func InsertMessage(ctx context.Context, db *DB, m *connectors.Message) error {
	// Marshal tool slices; empty/nil slices become "[]" rather than null so
	// callers always get a valid JSON array back on read.
	toolCallsJSON, err := marshalToolSlice(m.ToolCalls)
	if err != nil {
		return fmt.Errorf("store: marshal tool_calls for msg %q: %w", m.ID, err)
	}
	toolResultsJSON, err := marshalToolSlice(m.ToolResults)
	if err != nil {
		return fmt.Errorf("store: marshal tool_results for msg %q: %w", m.ID, err)
	}

	tx, err := db.Write().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin tx for InsertMessage %q: %w", m.ID, err)
	}

	// INSERT message --------------------------------------------------------
	const insertMsg = `
INSERT INTO messages
    (id, session_id, parent_uuid, role, content,
     tool_calls_json, tool_results_json,
     tokens_in, tokens_out, cached_read_tokens, cached_write_tokens,
     cost_usd, model, ts, git_branch, cwd)
VALUES
    (?, ?, ?, ?, ?,
     ?, ?,
     ?, ?, ?, ?,
     ?, ?, ?, ?, ?)`

	if _, err := tx.ExecContext(ctx, insertMsg,
		m.ID,
		m.SessionID,
		nullableStr(m.ParentUUID),
		string(m.Role),
		m.Content,
		toolCallsJSON,
		toolResultsJSON,
		m.TokensIn,
		m.TokensOut,
		m.CachedReadTokens,
		m.CachedWriteTokens,
		m.CostUSD,
		nullableStr(m.Model),
		m.Ts,
		m.GitBranch,
		m.Cwd,
	); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("store: insert message %q: %w", m.ID, err)
	}

	// UPDATE session counters -----------------------------------------------
	// last_msg_at is kept as MAX so out-of-order ingestion is safe.
	const updateSession = `
UPDATE sessions SET
    last_msg_at         = MAX(last_msg_at, ?),
    msg_count           = msg_count           + 1,
    tokens_in           = tokens_in           + ?,
    tokens_out          = tokens_out          + ?,
    cached_read_tokens  = cached_read_tokens  + ?,
    cached_write_tokens = cached_write_tokens + ?,
    cost_usd            = cost_usd            + ?
WHERE id = ?`

	res, err := tx.ExecContext(ctx, updateSession,
		m.Ts,
		m.TokensIn,
		m.TokensOut,
		m.CachedReadTokens,
		m.CachedWriteTokens,
		m.CostUSD,
		m.SessionID,
	)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("store: update session %q for msg %q: %w", m.SessionID, m.ID, err)
	}

	// If zero rows updated the session doesn't exist.  The FK on messages
	// would have already caught this, but we check explicitly so callers get
	// a clear error rather than a confusing FK message on the INSERT above.
	affected, err := res.RowsAffected()
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("store: rows affected for session %q: %w", m.SessionID, err)
	}
	if affected == 0 {
		_ = tx.Rollback()
		return fmt.Errorf("store: session %q not found — call UpsertSession first: %w",
			m.SessionID, sql.ErrNoRows)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit InsertMessage %q: %w", m.ID, err)
	}
	return nil
}

// ListMessagesBySession returns messages for a session in ts ASC order.
//
//   - limit > 0  — page size; if limit == 0 the default is 100.
//   - before == 0 — no upper bound (returns from the start of the session).
//   - before > 0  — cursor; returns messages with ts < before.
//
// Pagination is cursor-based using the (ts, id) tuple: callers should pass
// the Ts of the last-seen message as `before` in the subsequent page request.
// This is stable across concurrent inserts because messages are ordered by
// their immutable recorded timestamp.
//
// Uses the read handle.
func ListMessagesBySession(
	ctx context.Context,
	db *DB,
	sessionID string,
	limit int,
	before int64,
) ([]*connectors.Message, error) {
	return ListMessagesBySessionOrdered(ctx, db, sessionID, limit, before, "asc")
}

// ListMessagesBySessionOrdered is the explicit-order variant of
// ListMessagesBySession. order="desc" returns the most recent rows first,
// useful when the caller only needs the tail of a long session (cockpit
// previews). Any value other than "desc" is treated as "asc".
func ListMessagesBySessionOrdered(
	ctx context.Context,
	db *DB,
	sessionID string,
	limit int,
	before int64,
	order string,
) ([]*connectors.Message, error) {
	return ListMessagesBySessionFiltered(ctx, db, sessionID, limit, before, order, MessageFilter{})
}

// MessageFilter narrows ListMessagesBySessionFiltered to a sub-thread of a
// session — used by the cockpit to fetch the tail of one (branch, cwd)
// bucket when two parallel terminals share the same sessionId.
//
// Filters are AND-ed; empty values are ignored. Pass UnsetBranch/UnsetCwd
// (just the empty string sentinel) to filter "branch is empty" specifically.
type MessageFilter struct {
	Branch    string
	Cwd       string
	BranchSet bool // distinguishes "filter by empty string" from "no filter"
	CwdSet    bool
}

// ListMessagesBySessionFiltered is the most general read path: ordered,
// paginated, optionally filtered by (git_branch, cwd) tuple.
func ListMessagesBySessionFiltered(
	ctx context.Context,
	db *DB,
	sessionID string,
	limit int,
	before int64,
	order string,
	filter MessageFilter,
) ([]*connectors.Message, error) {
	const defaultMsgLimit = 100
	if limit <= 0 {
		limit = defaultMsgLimit
	}

	dir := "ASC"
	if order == "desc" {
		dir = "DESC"
	}

	q := `
SELECT id, session_id, parent_uuid, role, content,
       tool_calls_json, tool_results_json,
       tokens_in, tokens_out, cached_read_tokens, cached_write_tokens,
       cost_usd, model, ts, git_branch, cwd
FROM messages
WHERE session_id = ?`
	args := []any{sessionID}

	if before > 0 {
		q += " AND ts < ?"
		args = append(args, before)
	}
	if filter.BranchSet {
		q += " AND git_branch = ?"
		args = append(args, filter.Branch)
	}
	if filter.CwdSet {
		q += " AND cwd = ?"
		args = append(args, filter.Cwd)
	}
	q += " ORDER BY ts " + dir + ", id " + dir + " LIMIT ?"
	args = append(args, limit)

	rows, err := db.Read().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list messages session=%q: %w", sessionID, err)
	}
	defer rows.Close() //nolint:errcheck

	var out []*connectors.Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan message row: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: messages rows: %w", err)
	}
	return out, nil
}

// --- internal helpers -------------------------------------------------------

// messageScanner is satisfied by a row from *sql.Rows.
type messageScanner interface {
	Scan(dest ...any) error
}

func scanMessage(r messageScanner) (*connectors.Message, error) {
	var (
		m               connectors.Message
		role            string
		parentUUID      sql.NullString
		model           sql.NullString
		toolCallsJSON   string
		toolResultsJSON string
	)

	err := r.Scan(
		&m.ID,
		&m.SessionID,
		&parentUUID,
		&role,
		&m.Content,
		&toolCallsJSON,
		&toolResultsJSON,
		&m.TokensIn,
		&m.TokensOut,
		&m.CachedReadTokens,
		&m.CachedWriteTokens,
		&m.CostUSD,
		&model,
		&m.Ts,
		&m.GitBranch,
		&m.Cwd,
	)
	if err != nil {
		return nil, err
	}

	m.Role = connectors.Role(role)
	if parentUUID.Valid {
		m.ParentUUID = parentUUID.String
	}
	if model.Valid {
		m.Model = model.String
	}

	// Decode tool slices — tolerate empty string (pre-migration rows).
	if toolCallsJSON != "" && toolCallsJSON != "[]" {
		if err := json.Unmarshal([]byte(toolCallsJSON), &m.ToolCalls); err != nil {
			return nil, fmt.Errorf("unmarshal tool_calls_json for msg %q: %w", m.ID, err)
		}
	}
	if toolResultsJSON != "" && toolResultsJSON != "[]" {
		if err := json.Unmarshal([]byte(toolResultsJSON), &m.ToolResults); err != nil {
			return nil, fmt.Errorf("unmarshal tool_results_json for msg %q: %w", m.ID, err)
		}
	}

	return &m, nil
}

// marshalToolSlice returns "[]" for nil/empty slices, otherwise the JSON
// encoding.  This ensures round-trips are deterministic.
func marshalToolSlice[T any](v []T) (string, error) {
	if len(v) == 0 {
		return "[]", nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// nullableStr converts an empty string to a NULL sql.NullString, preserving
// non-empty strings as valid values.
func nullableStr(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

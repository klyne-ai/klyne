package mcpserver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/store"
)

// get_session tool
// ================
// Fetches one session's metadata + ordered messages directly from
// klyne's SQLite store. Distinct from search_messages (cross-session
// FTS) and from generate_handoff (current-session JSONL re-scan +
// renderer): this is the "I already know the session_id, give me its
// content" surface that bootstrap recipients need.

const (
	getSessionDefaultLimit = 100
	getSessionMaxLimit     = 1000
)

// GetSessionInput is the JSON-Schema input for the get_session tool.
type GetSessionInput struct {
	SessionID string `json:"session_id" jsonschema:"the session id to fetch; required"`
	// Limit caps how many messages to return. Default 100, max 1000.
	Limit int `json:"limit,omitempty" jsonschema:"max messages to return; default 100, max 1000"`
	// Before is an epoch-ms cursor; only messages with ts < before are
	// returned. 0 means "no upper bound". Use the Ts of the last
	// message in a prior page to paginate backwards.
	Before int64 `json:"before,omitempty" jsonschema:"epoch-ms cursor; only messages with ts < before are returned"`
	// Since is an epoch-ms lower bound; only messages with ts >= since
	// are returned. 0 means "no lower bound".
	Since int64 `json:"since,omitempty" jsonschema:"epoch-ms lower bound; only messages with ts >= since are returned"`
	// Order is "asc" (default — oldest first) or "desc" (newest first).
	Order string `json:"order,omitempty" jsonschema:"asc (default) | desc"`
}

// SessionMetaRow is the trimmed session metadata returned alongside
// the messages — same shape the cockpit uses, minus a few columns the
// AI doesn't need (encoded_cwd, raw_path).
type SessionMetaRow struct {
	ID          string  `json:"id"`
	CLI         string  `json:"cli"`
	ProjectPath string  `json:"project_path,omitempty"`
	StartedAt   int64   `json:"started_at"`
	LastMsgAt   int64   `json:"last_msg_at"`
	MsgCount    int     `json:"msg_count"`
	TokensIn    int64   `json:"tokens_in"`
	TokensOut   int64   `json:"tokens_out"`
	CostUSD     float64 `json:"cost_usd"`
	Model       string  `json:"model,omitempty"`
	Status      string  `json:"status,omitempty"`
}

// GetSessionOutput is the structured payload returned by HandleGetSession.
type GetSessionOutput struct {
	SessionID string                `json:"session_id"`
	Found     bool                  `json:"found"`
	Reason    string                `json:"reason,omitempty" jsonschema:"populated when Found is false"`
	Session   *SessionMetaRow       `json:"session,omitempty"`
	Messages  []*connectors.Message `json:"messages,omitempty"`
	Total     int                   `json:"total"`
	Limit     int                   `json:"limit"`
	Order     string                `json:"order"`
}

// HandleGetSession returns metadata + ordered messages for one
// session_id. Pure SQLite reads — no JSONL access.
func HandleGetSession(ctx context.Context, _ *mcp.CallToolRequest, in GetSessionInput) (*mcp.CallToolResult, GetSessionOutput, error) {
	if in.SessionID == "" {
		const reason = "session_id is required"
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: reason}},
		}, GetSessionOutput{Reason: reason}, nil
	}

	limit := in.Limit
	if limit <= 0 {
		limit = getSessionDefaultLimit
	}
	if limit > getSessionMaxLimit {
		limit = getSessionMaxLimit
	}
	order := in.Order
	if order != "desc" {
		order = "asc"
	}

	db, err := store.Open(config.DBPath())
	if err != nil {
		return nil, GetSessionOutput{}, fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	sess, err := store.GetSession(ctx, db, in.SessionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			reason := fmt.Sprintf("session %q not found in klyne store", in.SessionID)
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: reason}},
			}, GetSessionOutput{
				SessionID: in.SessionID,
				Reason:    reason,
				Limit:     limit,
				Order:     order,
			}, nil
		}
		return nil, GetSessionOutput{}, fmt.Errorf("get session: %w", err)
	}

	msgs, err := store.ListMessagesBySessionFiltered(ctx, db, in.SessionID, limit, in.Before, order, store.MessageFilter{})
	if err != nil {
		return nil, GetSessionOutput{}, fmt.Errorf("list messages: %w", err)
	}

	// Client-side `since` filter — the store DAO does not expose a
	// lower bound directly, and folding it in at this layer keeps the
	// helper signature stable.
	if in.Since > 0 {
		filtered := make([]*connectors.Message, 0, len(msgs))
		for _, m := range msgs {
			if m.Ts >= in.Since {
				filtered = append(filtered, m)
			}
		}
		msgs = filtered
	}

	out := GetSessionOutput{
		SessionID: in.SessionID,
		Found:     true,
		Session: &SessionMetaRow{
			ID:          sess.ID,
			CLI:         string(sess.CLI),
			ProjectPath: sess.ProjectPath,
			StartedAt:   sess.StartedAt,
			LastMsgAt:   sess.LastMsgAt,
			MsgCount:    int(sess.MsgCount),
			TokensIn:    sess.TokensIn,
			TokensOut:   sess.TokensOut,
			CostUSD:     sess.CostUSD,
			Model:       sess.Model,
			Status:      string(sess.Status),
		},
		Messages: msgs,
		Total:    len(msgs),
		Limit:    limit,
		Order:    order,
	}
	summary := fmt.Sprintf("get_session %s: returned %d/%d messages", in.SessionID, len(msgs), sess.MsgCount)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: summary}},
	}, out, nil
}

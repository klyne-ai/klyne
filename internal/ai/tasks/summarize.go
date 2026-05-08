// Package tasks implements AI background tasks for klyne: Summarize,
// Title, and the Runner that triggers them from SSE events.
package tasks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/klyne-ai/klyne/internal/ai"
	"github.com/klyne-ai/klyne/internal/store"
)

const summarizeSystemPrompt = "You are a session summarizer. Output a concise rolling summary."

// Summarize produces a rolling per-session summary covering the last `window`
// messages. If a prior summary exists for the session, it is included in the
// prompt so the new summary is incremental.
//
// Steps:
//  1. Fetch last `window` messages via store.ListMessagesBySession.
//  2. Fetch prior summary via store.LatestSummary (ignore sql.ErrNoRows).
//  3. Build a prompt combining the prior summary and the flattened messages.
//  4. Call provider.Chat.
//  5. Persist the result via store.InsertSummary.
//  6. Return the persisted *store.Summary.
func Summarize(
	ctx context.Context,
	db *store.DB,
	provider ai.Provider,
	model string,
	sessionID string,
	window int,
) (*store.Summary, error) {
	// 1. Fetch last `window` messages.
	msgs, err := store.ListMessagesBySession(ctx, db, sessionID, window, 0)
	if err != nil {
		return nil, fmt.Errorf("tasks/summarize: list messages: %w", err)
	}

	// 2. Fetch prior summary (best-effort).
	prior, err := store.LatestSummary(ctx, db, sessionID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("tasks/summarize: fetch prior summary: %w", err)
	}
	// err == sql.ErrNoRows → prior == nil; that's fine.

	// 3. Build prompt messages.
	var promptMsgs []ai.Message
	if prior != nil {
		promptMsgs = append(promptMsgs, ai.Message{
			Role:    "user",
			Content: fmt.Sprintf("[Previous summary]\n%s", prior.Text),
		})
	}

	if len(msgs) > 0 {
		var sb strings.Builder
		sb.WriteString("[Recent messages]\n")
		for _, m := range msgs {
			sb.WriteString(fmt.Sprintf("[%s] %s\n", m.Role, m.Content))
		}
		promptMsgs = append(promptMsgs, ai.Message{
			Role:    "user",
			Content: sb.String(),
		})
	}

	// Ensure there's at least a minimal prompt even if session has no messages.
	if len(promptMsgs) == 0 {
		promptMsgs = append(promptMsgs, ai.Message{
			Role:    "user",
			Content: "Summarize this session (no messages yet).",
		})
	}

	// 4. Call provider.
	resp, err := provider.Chat(ctx, ai.ChatRequest{
		Model:        model,
		SystemPrompt: summarizeSystemPrompt,
		Messages:     promptMsgs,
	})
	if err != nil {
		return nil, fmt.Errorf("tasks/summarize: provider chat: %w", err)
	}

	// 5. Persist.
	s := &store.Summary{
		SessionID: sessionID,
		Text:      resp.Text,
		Model:     model,
		TS:        time.Now().UnixMilli(),
	}
	if err := store.InsertSummary(ctx, db, s); err != nil {
		return nil, fmt.Errorf("tasks/summarize: insert summary: %w", err)
	}

	// 6. Return persisted summary (Version is populated by InsertSummary).
	return s, nil
}

package tasks

import (
	"context"
	"fmt"
	"strings"

	"github.com/klyne-ai/klyne/internal/ai"
	"github.com/klyne-ai/klyne/internal/store"
)

const (
	titleSystemPrompt = "You are a session title generator. Output a concise title of 3 to 7 words that captures the main topic of this session. Reply with only the title, no punctuation at the end."
	titleMessageLimit = 5
)

// Title generates a 3-7 word title for a session based on its first few
// messages. It returns the title string; the caller is responsible for
// persisting it (e.g. via store.UpsertSession).
//
// Steps:
//  1. Fetch the first `titleMessageLimit` messages for the session.
//  2. Build a prompt from those messages.
//  3. Call provider.Chat.
//  4. Return the trimmed response text.
func Title(
	ctx context.Context,
	db *store.DB,
	provider ai.Provider,
	model string,
	sessionID string,
) (string, error) {
	// 1. Fetch the first few messages.
	msgs, err := store.ListMessagesBySession(ctx, db, sessionID, titleMessageLimit, 0)
	if err != nil {
		return "", fmt.Errorf("tasks/title: list messages: %w", err)
	}

	// 2. Build prompt.
	var promptMsgs []ai.Message
	if len(msgs) > 0 {
		var sb strings.Builder
		sb.WriteString("[Session messages]\n")
		for _, m := range msgs {
			sb.WriteString(fmt.Sprintf("[%s] %s\n", m.Role, m.Content))
		}
		promptMsgs = append(promptMsgs, ai.Message{
			Role:    "user",
			Content: sb.String(),
		})
	} else {
		promptMsgs = append(promptMsgs, ai.Message{
			Role:    "user",
			Content: "Generate a title for a new, empty session.",
		})
	}

	// 3. Call provider.
	resp, err := provider.Chat(ctx, ai.ChatRequest{
		Model:        model,
		SystemPrompt: titleSystemPrompt,
		Messages:     promptMsgs,
		MaxTokens:    20, // titles are short
	})
	if err != nil {
		return "", fmt.Errorf("tasks/title: provider chat: %w", err)
	}

	// 4. Return trimmed title.
	return strings.TrimSpace(resp.Text), nil
}

package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// PreCompactInput is the input schema for get_pre_compact_context.
type PreCompactInput struct {
	SessionID string `json:"session_id,omitempty" jsonschema:"explicit Claude Code session id; defaults to latest session in current working directory"`
	CWD       string `json:"cwd,omitempty" jsonschema:"override the working directory used to resolve the latest session"`
	// Limit caps how many pre-compact messages to return. Defaults to
	// 30 (the most common useful slice); hard ceiling 100.
	Limit int `json:"limit,omitempty" jsonschema:"how many of the most recent pre-compact messages to return; default 30, max 100"`
}

// PreCompactMessageRow is the typed shape of one pre-compact message
// in the structured output. Trimmed to the fields an AI actually
// needs to reconstruct context — full canonical Message would carry
// noise like tool_results' giant payloads.
type PreCompactMessageRow struct {
	Role    string `json:"role" jsonschema:"user, assistant, tool, or system"`
	Content string `json:"content" jsonschema:"the text body of the message; truncated"`
	Model   string `json:"model,omitempty" jsonschema:"model id, when role is assistant"`
	TS      int64  `json:"ts" jsonschema:"epoch-millisecond timestamp of the message"`
}

// PreCompactOutput is the structured result of get_pre_compact_context.
type PreCompactOutput struct {
	SessionID         string                  `json:"session_id,omitempty" jsonschema:"the session id whose pre-compact context is reported"`
	Path              string                  `json:"path,omitempty" jsonschema:"absolute path of the source transcript"`
	FoundCompact      bool                    `json:"found_compact" jsonschema:"true when the session has at least one /compact event"`
	Trigger           string                  `json:"trigger,omitempty" jsonschema:"manual or auto when compactMetadata is present"`
	PreTokens         int64                   `json:"pre_tokens,omitempty" jsonschema:"cache-aware token count immediately before the compact"`
	CompactTimestamp  string                  `json:"compact_timestamp,omitempty" jsonschema:"RFC3339 timestamp of the last compact event"`
	Messages          []PreCompactMessageRow  `json:"messages,omitempty" jsonschema:"messages immediately preceding the last compact, oldest first"`
	Ambiguous         bool                    `json:"ambiguous,omitempty" jsonschema:"true when multiple sessions in this cwd require explicit session_id disambiguation"`
	Candidates        []CandidateRow          `json:"candidates,omitempty" jsonschema:"sessions to choose from when ambiguous"`
}

// preCompactRowPreviewBytes caps per-message body length in the
// structured output so a single huge tool_result does not blow the
// payload. Mirrors the handoff per-message cap intentionally.
const preCompactRowPreviewBytes = 600

// HandleGetPreCompactContext recovers the messages immediately before
// the last /compact event in a session. This is the only tool in the
// v1 surface that exposes information the AI literally cannot get on
// its own — once /compact runs, the original messages are gone from
// the AI's context, and only the JSONL still has them.
func HandleGetPreCompactContext(_ context.Context, _ *mcp.CallToolRequest, in PreCompactInput) (*mcp.CallToolResult, PreCompactOutput, error) {
	path, ambiguous, cands, err := resolveSession(GetContextHealthInput{
		SessionID: in.SessionID,
		CWD:       in.CWD,
	})
	if err != nil {
		return nil, PreCompactOutput{}, err
	}
	if ambiguous {
		rows := make([]CandidateRow, 0, len(cands))
		for _, c := range cands {
			rows = append(rows, CandidateRow{
				SessionID: c.SessionID,
				Preview:   c.Preview,
				IsActive:  c.IsActive,
				ModTime:   c.ModTime.UTC().Format(timeRFC3339),
				MsgCount:  c.MsgCount,
			})
		}
		const reason = "Multiple Claude Code sessions in this project. Pick one and call get_pre_compact_context again with session_id."
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: reason}},
		}, PreCompactOutput{Ambiguous: true, Candidates: rows}, nil
	}
	if path == "" {
		const reason = "No Claude Code session found for this working directory."
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: reason}},
		}, PreCompactOutput{}, nil
	}

	bundle, err := LoadPreCompactMessages(path, in.Limit)
	if err != nil {
		return nil, PreCompactOutput{}, fmt.Errorf("load pre-compact messages: %w", err)
	}
	if !bundle.FoundCompact {
		const reason = "This session has not been /compact'd yet — nothing to recover."
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: reason}},
		}, PreCompactOutput{Path: path, FoundCompact: false}, nil
	}

	rows := make([]PreCompactMessageRow, 0, len(bundle.Messages))
	for _, m := range bundle.Messages {
		body := strings.TrimSpace(m.Content)
		if len(body) > preCompactRowPreviewBytes {
			body = body[:preCompactRowPreviewBytes] + "…"
		}
		rows = append(rows, PreCompactMessageRow{
			Role:    string(m.Role),
			Content: body,
			Model:   m.Model,
			TS:      m.Ts,
		})
	}

	out := PreCompactOutput{
		Path:             path,
		FoundCompact:     true,
		Trigger:          bundle.Trigger,
		PreTokens:        bundle.PreTokens,
		CompactTimestamp: bundle.CompactTimestamp,
		Messages:         rows,
	}
	if len(bundle.Messages) > 0 && bundle.Messages[0].SessionID != "" {
		out.SessionID = bundle.Messages[0].SessionID
	}

	summary := fmt.Sprintf(
		"Recovered %d messages from before the last /compact event (trigger=%s, pre-compact size %d tokens).",
		len(rows), defaultStr(bundle.Trigger, "unknown"), bundle.PreTokens,
	)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: summary}},
	}, out, nil
}

// defaultStr returns s when non-empty, otherwise fallback. Tiny
// helper to keep the summary line readable.
func defaultStr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

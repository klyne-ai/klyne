package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// HandleGenerateHandoff resolves the session, loads the snapshot,
// and renders a deterministic Markdown handoff prompt the user can
// paste into a fresh Claude Code session to continue work without
// re-explaining the project.
//
// No AI dependency. No call to any provider. The handoff is built
// entirely from JSONL ground truth so it works in air-gapped
// environments and in fresh klyne installs that have no BYOK key.
func HandleGenerateHandoff(_ context.Context, _ *mcp.CallToolRequest, in HandoffInput) (*mcp.CallToolResult, HandoffOutput, error) {
	path, ambiguous, cands, err := resolveSession(GetContextHealthInput{
		SessionID: in.SessionID,
		CWD:       in.CWD,
	})
	if err != nil {
		return nil, HandoffOutput{}, err
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
		const reason = "Multiple Claude Code sessions in this project. Pick one and call generate_handoff again with session_id."
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: reason}},
		}, HandoffOutput{Ambiguous: true, Candidates: rows}, nil
	}
	if path == "" {
		const reason = "No Claude Code session found for this working directory."
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: reason}},
		}, HandoffOutput{}, nil
	}

	snap, err := LoadSnapshot(path)
	if err != nil {
		return nil, HandoffOutput{}, fmt.Errorf("load snapshot: %w", err)
	}
	md := renderHandoff(snap, parseHandoffScope(in.Scope))

	out := HandoffOutput{
		SessionID:   snap.SessionID,
		Path:        snap.Path,
		ProjectPath: projectPathFromMessages(snap.Messages),
		Markdown:    md,
	}
	// The Content text is what the AI reads first; surface a one-liner
	// pointing at the structured Markdown so the AI knows to copy it.
	summary := fmt.Sprintf(
		"Generated handoff for session %s (~%d messages). The Markdown is in the structured output under `markdown`.",
		short(snap.SessionID), snap.MsgCount,
	)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: summary}},
	}, out, nil
}

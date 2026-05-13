package mcpserver

import (
	"context"
	"fmt"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ListSessionsInput is the input schema for list_sessions.
type ListSessionsInput struct {
	// CWD overrides os.Getwd() for project resolution. Useful when
	// the AI knows the project root differs from where the MCP
	// subprocess was spawned.
	CWD string `json:"cwd,omitempty" jsonschema:"override the working directory used to find sessions; defaults to current process cwd"`
}

// ListSessionsOutput is the structured output of list_sessions.
type ListSessionsOutput struct {
	// CWD is the directory the resolver actually consulted (after
	// the optional override). Useful for the AI to confirm it asked
	// about the right project.
	CWD string `json:"cwd" jsonschema:"the working directory that was searched"`
	// Candidates is every session in the project, sorted by
	// most-recently-modified first.
	Candidates []CandidateRow `json:"candidates" jsonschema:"sessions found in this project, newest first"`
	// Markdown is the slash-prompt-ready rendering, produced
	// server-side so /klyne:sessions can echo verbatim without the
	// host LLM re-rendering structured rows itself.
	Markdown string `json:"markdown" jsonschema:"slash-prompt-ready markdown rendering (verbatim-echo target)"`
}

// HandleListSessions enumerates every Claude Code session in the
// cwd's project tree. Designed to be called proactively by the AI
// when it wants to disambiguate before running other tools.
func HandleListSessions(_ context.Context, _ *mcp.CallToolRequest, in ListSessionsInput) (*mcp.CallToolResult, ListSessionsOutput, error) {
	cwd := in.CWD
	if cwd == "" {
		w, err := os.Getwd()
		if err != nil {
			return nil, ListSessionsOutput{}, fmt.Errorf("resolve cwd: %w", err)
		}
		cwd = w
	}
	cands, err := ListSessionsForCWD(cwd)
	if err != nil {
		return nil, ListSessionsOutput{}, err
	}
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
	out := ListSessionsOutput{CWD: cwd, Candidates: rows}
	out.Markdown = formatSessionsAsMarkdown(out)

	var summary string
	switch len(rows) {
	case 0:
		summary = fmt.Sprintf("No Claude Code sessions found under %s.", cwd)
	case 1:
		summary = fmt.Sprintf("Found 1 session in %s.", cwd)
	default:
		summary = fmt.Sprintf("Found %d sessions in %s.", len(rows), cwd)
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: summary}},
	}, out, nil
}

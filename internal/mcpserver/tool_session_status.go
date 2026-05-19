package mcpserver

import (
	"context"
	"errors"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/klyne-ai/klyne/internal/contexthealth"
)

// tool_session_status.go — get_session_status MCP tool.
//
// Composes the verdict from contexthealth.Classify with the
// trajectory + table from contexthealth.ComputeTimeline into a
// single Markdown blob. Backs the /klyne:status slashcommand.
//
// Resolves the session and loads the JSONL snapshot once, then
// runs both classifiers against that single snapshot — avoids the
// duplicate disk read that would happen if the slashcommand called
// get_context_health + get_token_timeline separately.

// GetSessionStatusInput mirrors the GetContextHealthInput / TokenTimelineInput
// shape so the AI can call this tool with the same disambiguation /
// override pattern it already uses for the underlying tools.
type GetSessionStatusInput struct {
	SessionID string `json:"session_id,omitempty" jsonschema:"explicit Claude Code session id; defaults to latest session in current working directory"`
	CWD       string `json:"cwd,omitempty" jsonschema:"override the working directory used to resolve the latest session"`
}

// GetSessionStatusOutput exposes both the verdict fields (so machine
// consumers can read State/Action without re-parsing the Markdown)
// and the markdown rendering that the slashcommand echoes verbatim.
type GetSessionStatusOutput struct {
	SessionID      string                        `json:"session_id,omitempty" jsonschema:"the session id whose status is reported"`
	Path           string                        `json:"path,omitempty" jsonschema:"absolute path of the analysed transcript"`
	Model          string                        `json:"model,omitempty" jsonschema:"model id of the most recent assistant turn"`
	State          string                        `json:"state,omitempty" jsonschema:"context-health state classification (healthy / drifting / risky / rescue_now)"`
	Action         string                        `json:"action,omitempty" jsonschema:"recommended next action"`
	Reason         string                        `json:"reason,omitempty" jsonschema:"one-sentence rationale for the verdict"`
	ContextFillPct float64                       `json:"context_fill_pct,omitempty" jsonschema:"percent of context window consumed by the next turn"`
	MsgCount       int                           `json:"msg_count,omitempty" jsonschema:"total message count in the transcript"`
	Bloat          []contexthealth.BloatRow      `json:"bloat,omitempty" jsonschema:"top tool-output sources contributing to bloat"`
	Points         []contexthealth.TimelinePoint `json:"points,omitempty" jsonschema:"per-assistant-turn token rows in chronological order"`
	LatestInput    int64                         `json:"latest_input,omitempty" jsonschema:"most recent qualifying turn's TokensIn — current prefix size"`
	PeakInput      int64                         `json:"peak_input,omitempty" jsonschema:"largest single-turn TokensIn in the window"`
	FirstInput     int64                         `json:"first_input,omitempty" jsonschema:"oldest qualifying turn's TokensIn"`
	ContextWindow  int64                         `json:"context_window,omitempty" jsonschema:"model's context window in tokens; 0 when unknown"`
	WindowStartMs  int64                         `json:"window_start_ms,omitempty" jsonschema:"left edge of the displayed window (epoch ms)"`
	WindowEndMs    int64                         `json:"window_end_ms,omitempty" jsonschema:"right edge (epoch ms)"`
	Ambiguous      bool                          `json:"ambiguous,omitempty" jsonschema:"true when multiple sessions in this cwd require explicit session_id"`
	Candidates     []CandidateRow                `json:"candidates,omitempty" jsonschema:"sessions to choose from when ambiguous"`
	Markdown       string                        `json:"markdown" jsonschema:"slash-prompt-ready markdown rendering (verbatim-echo target)"`
}

// HandleGetSessionStatus is the MCP entry point. Stub — Task 5 wires
// the real composition. Returns "not implemented" so the package
// compiles before the renderer lands.
func HandleGetSessionStatus(_ context.Context, _ *mcp.CallToolRequest, _ GetSessionStatusInput) (*mcp.CallToolResult, GetSessionStatusOutput, error) {
	return nil, GetSessionStatusOutput{}, errors.New("not implemented")
}

// formatSessionStatusAsMarkdown — stub for Task 4. Returns empty
// string so the package compiles before the renderer lands.
func formatSessionStatusAsMarkdown(_ GetSessionStatusOutput, _ *time.Location, _ string) string {
	return ""
}

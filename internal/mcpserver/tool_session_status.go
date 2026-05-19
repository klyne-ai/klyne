package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

// formatSessionStatusAsMarkdown renders the unified status output.
// Verdict on top, reason next, then the token trajectory + table,
// then the bloat-sources list. Returns a single string the slash
// command echoes byte-for-byte.
func formatSessionStatusAsMarkdown(out GetSessionStatusOutput, loc *time.Location, tzName string) string {
	if out.Ambiguous {
		return formatAmbiguousAsMarkdown("get_session_status", out.Candidates)
	}
	// No-session path: the resolver populated Reason and left State empty.
	if out.State == "" && len(out.Points) == 0 {
		if out.Reason != "" {
			return out.Reason
		}
		return "No Claude Code session found for this working directory."
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Session status — `%s`\n\n", short(out.SessionID))
	fmt.Fprintf(&b, "**State:** `%s` · **Action:** `%s` · **Context fill:** %.1f%%\n\n",
		out.State, out.Action, out.ContextFillPct)
	if out.Reason != "" {
		fmt.Fprintf(&b, "> %s\n\n", out.Reason)
	}

	// --- Tokens section ----------------------------------------------
	b.WriteString("## Tokens\n\n")
	if len(out.Points) == 0 {
		b.WriteString("_no token-timeline rows yet — session is too short for a trajectory._\n\n")
	} else {
		// Reuse the same trajectory + sparkline shape as /klyne:tokens.
		b.WriteString(renderTrajectorySentence(TokenTimelineOutput{
			ContextWindow: out.ContextWindow,
			FirstInput:    out.FirstInput,
			PeakInput:     out.PeakInput,
			LatestInput:   out.LatestInput,
			PctOfContext:  pctOfContext(out.LatestInput, out.ContextWindow),
		}))
		b.WriteString("\n\n```\n")
		values := totalInputSeries(out.Points)
		b.WriteString(renderSparkline(values))
		b.WriteString("\n")
		b.WriteString(renderTimeAxis(out.WindowStartMs, out.WindowEndMs, sparklineCols))
		b.WriteString("\n")
		fmt.Fprintf(&b, "first ~%s · peak ~%s · now ~%s\n",
			humanTokens(out.FirstInput), humanTokens(out.PeakInput), humanTokens(out.LatestInput))
		b.WriteString("```\n\n")

		// Per-turn table — same column shape as /klyne:tokens. 10 rows
		// evenly sampled across the timeline.
		const tableRows = 10
		samples := evenlySamplePoints(out.Points, tableRows)
		if out.ContextWindow > 0 {
			b.WriteString("| time     | input tokens | % of context | cached | uncached |\n")
			b.WriteString("|----------|-------------:|-------------:|-------:|---------:|\n")
			for _, p := range samples {
				when := time.UnixMilli(p.TsMs).In(loc).Format("15:04:05")
				pct := float64(p.TotalInput) / float64(out.ContextWindow) * 100
				if pct > 100 {
					pct = 100
				}
				fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n",
					when, humanTokens(p.TotalInput), formatPct(pct),
					humanTokens(p.CachedReadTokens), humanTokens(p.EffectiveInput))
			}
		} else {
			b.WriteString("| time     | input tokens | cached | uncached |\n")
			b.WriteString("|----------|-------------:|-------:|---------:|\n")
			for _, p := range samples {
				when := time.UnixMilli(p.TsMs).In(loc).Format("15:04:05")
				fmt.Fprintf(&b, "| %s | %s | %s | %s |\n",
					when, humanTokens(p.TotalInput),
					humanTokens(p.CachedReadTokens), humanTokens(p.EffectiveInput))
			}
		}
		b.WriteString("\n")
	}

	// --- Bloat sources (top 3) ---------------------------------------
	if len(out.Bloat) > 0 {
		b.WriteString("## Top context-bloat sources\n\n")
		max := 3
		if len(out.Bloat) < max {
			max = len(out.Bloat)
		}
		for i := 0; i < max; i++ {
			row := out.Bloat[i]
			fmt.Fprintf(&b, "%d. **%s** — %.1f%% of tool output\n", i+1, row.Label, row.SharePct)
		}
	}

	return b.String()
}

// pctOfContext returns latest/window * 100 capped at 100. Returns 0
// when window is 0 (model unknown).
func pctOfContext(latest, window int64) float64 {
	if window <= 0 {
		return 0
	}
	pct := float64(latest) / float64(window) * 100
	if pct > 100 {
		pct = 100
	}
	return pct
}

package mcpserver

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/store"
)

// Bootstrap tool
// ==============
// Serena-inspired session-bootstrap brief. Synthesises klyne's existing
// per-project data — recent sessions, project + global memories, and the
// most-recent session's context-health verdict — into ONE deterministic
// briefing the agent can fetch at session start. Pure JSONL + SQLite
// reads; no AI calls, no network.
//
// Auto-invoked by the `klyne-bootstrap` skill when the agent enters a
// project it has no prior context for, or when the user asks "what was
// I working on?" / "where did I leave off?". Also surfaced as a slash
// command (`/klyne:bootstrap`) for explicit user invocation.

// bootstrapRecentSessions caps how many sessions appear in the brief.
// Three is enough for "the user picked up roughly where they left off
// yesterday" recall without blowing the agent's pre-task budget. The
// candidate list is already sorted newest-first by ListSessionsForCWD.
const bootstrapRecentSessions = 3

// bootstrapProjectMemoryLimit and bootstrapGlobalMemoryPreview cap the
// memory rows surfaced in the brief. Project memories are the more
// immediately useful set; globals get a preview + a count so the agent
// knows the rest exists and can `recall` for them on demand.
const (
	bootstrapProjectMemoryLimit  = 5
	bootstrapGlobalMemoryPreview = 3
)

// BootstrapInput is the JSON-Schema input for the bootstrap MCP tool.
// All fields optional; sensible defaults make the tool callable with
// `{}`.
type BootstrapInput struct {
	// CWD overrides os.Getwd() for project resolution. Useful when the
	// AI knows the project root differs from where it spawned the MCP
	// subprocess.
	CWD string `json:"cwd,omitempty" jsonschema:"override the working directory; defaults to current process cwd"`
}

// BootstrapHealthSummary is the trimmed-down view of the latest
// session's context-health verdict surfaced in the brief. Keeps the
// payload small — the agent can call `get_context_health` directly for
// the full bloat report.
type BootstrapHealthSummary struct {
	SessionID      string `json:"session_id" jsonschema:"the session id whose health is reported"`
	State          string `json:"state" jsonschema:"context-health state classification (healthy / drifting / risky / rescue_now)"`
	Action         string `json:"action" jsonschema:"recommended next action (continue / consider-handoff / handoff-now / compact-pending)"`
	ContextFillPct int    `json:"context_fill_pct" jsonschema:"percent of context window consumed by the next turn (0..100)"`
}

// BootstrapOutput is the structured payload returned by HandleBootstrap.
type BootstrapOutput struct {
	CWD                    string                  `json:"cwd" jsonschema:"the working directory that was searched"`
	Sessions               []CandidateRow          `json:"sessions" jsonschema:"up to 3 most-recent sessions in this project, newest first"`
	ProjectMemories        []store.Decision        `json:"project_memories" jsonschema:"up to 5 most-recent project-scoped klyne (SQLite) memories"`
	GlobalMemoryCount      int                     `json:"global_memory_count" jsonschema:"total global klyne memory count (project_path = \"\")"`
	GlobalMemoriesPreview  []store.Decision        `json:"global_memories_preview" jsonschema:"up to 3 most-recent global klyne (SQLite) memories"`
	ClaudeAutoMemory       ClaudeAutoMemory        `json:"claude_auto_memory" jsonschema:"on-disk Claude auto-memory for this project (~/.claude/projects/<encoded-cwd>/memory/) — separate store, separate writer"`
	LatestHealth           *BootstrapHealthSummary `json:"latest_health,omitempty" jsonschema:"context-health verdict for the most-recently modified session, when one exists"`
	Markdown               string                  `json:"markdown" jsonschema:"slash-prompt-ready markdown rendering (verbatim-echo target)"`
}

// HandleBootstrap synthesises the Day-1 briefing. Read-only across all
// data sources:
//
//  1. Sessions via ListSessionsForCWD (JSONL scan under ~/.claude or
//     ~/.codex).
//  2. Memories via store.ListDecisions against klyne's SQLite store.
//  3. Latest health via a delegated HandleGetContextHealth call against
//     the newest-modified session.
//
// Never errors on missing data — empty sections render as `_(none)_`
// rather than failing the call. The agent's output stays stable
// whether the project is brand-new or has years of history.
func HandleBootstrap(ctx context.Context, _ *mcp.CallToolRequest, in BootstrapInput) (*mcp.CallToolResult, BootstrapOutput, error) {
	cwd := in.CWD
	if cwd == "" {
		w, err := os.Getwd()
		if err != nil {
			return nil, BootstrapOutput{}, fmt.Errorf("resolve cwd: %w", err)
		}
		cwd = w
	}

	out := BootstrapOutput{CWD: cwd}

	// --- sessions: take up to 3, newest-first -----------------------
	cands, err := ListSessionsForCWDCtx(ctx, cwd)
	if err != nil {
		return nil, BootstrapOutput{}, fmt.Errorf("list sessions: %w", err)
	}
	limit := bootstrapRecentSessions
	if len(cands) < limit {
		limit = len(cands)
	}
	rows := make([]CandidateRow, 0, limit)
	for i := 0; i < limit; i++ {
		c := cands[i]
		rows = append(rows, CandidateRow{
			SessionID: c.SessionID,
			Preview:   c.Preview,
			IsActive:  c.IsActive,
			ModTime:   c.ModTime.UTC().Format(timeRFC3339),
			MsgCount:  c.MsgCount,
		})
	}
	out.Sessions = rows

	// --- memories: open SQLite, list project + globals --------------
	// The store is opened once and closed on exit so the two queries
	// share a connection. Errors are surfaced rather than silently
	// swallowed — a missing DB at this point usually means klyne has
	// never run before, in which case the migrations would have
	// created an empty file on first Open.
	db, err := store.Open(config.DBPath())
	if err != nil {
		return nil, BootstrapOutput{}, fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	projectMemories, err := store.ListDecisions(ctx, db, store.DecisionFilter{
		ProjectPath: cwd,
		Limit:       bootstrapProjectMemoryLimit,
	})
	if err != nil {
		return nil, BootstrapOutput{}, fmt.Errorf("list project memories: %w", err)
	}
	out.ProjectMemories = projectMemories

	// Globals: store filter on ProjectPath="" is treated as "no
	// filter", so we fetch a generous slice and partition client-side.
	// Mirrors the HandleRecallMemory precedent.
	allRows, err := store.ListDecisions(ctx, db, store.DecisionFilter{Limit: 500})
	if err != nil {
		return nil, BootstrapOutput{}, fmt.Errorf("list globals: %w", err)
	}
	globals := make([]store.Decision, 0)
	for _, d := range allRows {
		if d.ProjectPath == "" {
			globals = append(globals, d)
		}
	}
	out.GlobalMemoryCount = len(globals)
	preview := bootstrapGlobalMemoryPreview
	if len(globals) < preview {
		preview = len(globals)
	}
	out.GlobalMemoriesPreview = globals[:preview]

	// --- Claude auto-memory (on-disk, written by Claude itself) ----
	// This is a SECOND memory system distinct from klyne's SQLite
	// store: Claude Code maintains it via its own "auto memory" system
	// prompt and writes .md files with YAML frontmatter under
	// ~/.claude/projects/<encoded-cwd>/memory/. Bootstrap shows both
	// so the agent has a complete picture without conflating sources.
	if home, herr := os.UserHomeDir(); herr == nil {
		if auto, aerr := ReadClaudeAutoMemory(home, cwd); aerr == nil {
			out.ClaudeAutoMemory = auto
		}
		// Silent on aerr: auto-memory is optional context and an
		// unreadable file should not fail the bootstrap call.
	}

	// --- latest health: delegate to the existing handler ------------
	// We pick the newest-modified session (cands[0]) so the verdict
	// reflects whichever session the agent is most likely resuming.
	// If the delegated call errors or returns an ambiguous /
	// no-session result, we leave LatestHealth nil — the agent will
	// see the absence and can decide whether to fetch directly.
	if len(cands) > 0 {
		_, hOut, hErr := HandleGetContextHealth(ctx, nil, GetContextHealthInput{
			CWD:       cwd,
			SessionID: cands[0].SessionID,
		})
		if hErr == nil && !hOut.Ambiguous && hOut.State != "" {
			out.LatestHealth = &BootstrapHealthSummary{
				SessionID:      hOut.SessionID,
				State:          hOut.State,
				Action:         hOut.Action,
				ContextFillPct: int(hOut.ContextFillPct + 0.5),
			}
		}
	}

	out.Markdown = formatBootstrapAsMarkdown(out)

	summary := fmt.Sprintf(
		"bootstrap: %d session(s), %d project memo(s), %d global(s)",
		len(out.Sessions), len(out.ProjectMemories), out.GlobalMemoryCount,
	)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: summary}},
	}, out, nil
}

// formatBootstrapAsMarkdown renders the brief as the slash-prompt-ready
// Markdown. Every section is always emitted — empty sections print
// `_(none)_` so the agent's output is stable regardless of what data
// happens to exist for this project. Visible lines stay under 80 chars
// to keep the rendering readable in narrow terminals.
func formatBootstrapAsMarkdown(out BootstrapOutput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# klyne bootstrap\n\n")
	fmt.Fprintf(&b, "Project: `%s`\n\n", out.CWD)

	// --- sessions ---------------------------------------------------
	b.WriteString("## Recent sessions\n\n")
	if len(out.Sessions) == 0 {
		b.WriteString("_(none)_\n\n")
	} else {
		for _, c := range out.Sessions {
			active := ""
			if c.IsActive {
				active = " · active"
			}
			preview := cleanSessionPreview(c.Preview)
			fmt.Fprintf(&b, "- `%s`%s — %s · %d msgs\n",
				short(c.SessionID), active, c.ModTime, c.MsgCount)
			if preview != "" {
				fmt.Fprintf(&b, "  - %s\n", oneLine(truncatePreview(preview)))
			}
		}
		b.WriteString("\n")
	}

	// --- klyne memory (SQLite store) -------------------------------
	b.WriteString("## klyne memory (SQLite store)\n\n")
	b.WriteString("### Project-scoped\n\n")
	if len(out.ProjectMemories) == 0 {
		b.WriteString("_(none)_\n\n")
	} else {
		for _, d := range out.ProjectMemories {
			fmt.Fprintf(&b, "- %s\n", oneLine(d.Text))
		}
		b.WriteString("\n")
	}
	b.WriteString("### Global\n\n")
	if out.GlobalMemoryCount == 0 {
		b.WriteString("_(none)_\n\n")
	} else {
		for _, d := range out.GlobalMemoriesPreview {
			fmt.Fprintf(&b, "- %s\n", oneLine(d.Text))
		}
		remaining := out.GlobalMemoryCount - len(out.GlobalMemoriesPreview)
		if remaining > 0 {
			fmt.Fprintf(&b, "- _(+%d more — call `recall` to see them)_\n", remaining)
		}
		b.WriteString("\n")
	}

	// --- Claude auto-memory (files on disk) ------------------------
	b.WriteString("## Claude auto-memory (files on disk)\n\n")
	if out.ClaudeAutoMemory.Dir != "" {
		fmt.Fprintf(&b, "_Source: `%s`_\n\n", out.ClaudeAutoMemory.Dir)
	}
	b.WriteString(RenderClaudeAutoMemoryAsMarkdown(out.ClaudeAutoMemory))

	// --- latest health (only when populated) -----------------------
	if out.LatestHealth != nil {
		b.WriteString("## Current session health\n\n")
		fmt.Fprintf(&b, "- Session: `%s`\n", short(out.LatestHealth.SessionID))
		fmt.Fprintf(&b, "- State: `%s`\n", out.LatestHealth.State)
		fmt.Fprintf(&b, "- Action: `%s`\n", out.LatestHealth.Action)
		fmt.Fprintf(&b, "- Context fill: %d%%\n", out.LatestHealth.ContextFillPct)
	}

	return b.String()
}

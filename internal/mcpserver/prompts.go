package mcpserver

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Slash-command surface (live prompts)
// ====================================
// Tools are auto-invoked by the AI based on context. Prompts surface
// in the user's `/` menu — Claude Code formats MCP prompts as
// `/mcp__<server-name>__<prompt-name>`. These four prompts let the
// user pull each tool's data on demand without waiting for the AI to
// decide it's relevant.
//
// All four are "live": the handler runs the same business logic the
// underlying tool does and returns the result formatted as a single
// user-message — no AI roundtrip needed for the fetch. The AI then
// reads that injected message and responds normally.
//
// All four accept an optional `cwd` argument that overrides the MCP
// subprocess's process cwd. Callers (Claude Code) typically don't
// pass it; the prompt resolves the working dir from os.Getwd().

// promptCWD pulls cwd from the prompt request arguments, falling back
// to os.Getwd() when absent. Centralised so each prompt handler stays
// short. Returns ("", error) only when both the override is empty AND
// os.Getwd() fails.
func promptCWD(req *mcp.GetPromptRequest) (string, error) {
	if req != nil && req.Params != nil {
		if v, ok := req.Params.Arguments["cwd"]; ok && v != "" {
			return v, nil
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve cwd: %w", err)
	}
	return cwd, nil
}

// userPromptResult wraps text in a single user-role PromptMessage,
// the shape Claude Code expects for slash-injected content.
func userPromptResult(description, text string) *mcp.GetPromptResult {
	return &mcp.GetPromptResult{
		Description: description,
		Messages: []*mcp.PromptMessage{
			{Role: "user", Content: &mcp.TextContent{Text: text}},
		},
	}
}

// PromptHealthHandler implements the /mcp__klyne__health prompt.
// Live: invokes get_context_health and returns its verdict + bloat
// scorecard as Markdown. Equivalent to the AI calling the tool but
// triggered explicitly by the user via the slash menu.
func PromptHealthHandler(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	cwd, err := promptCWD(req)
	if err != nil {
		return nil, err
	}
	_, out, err := HandleGetContextHealth(ctx, nil, GetContextHealthInput{CWD: cwd})
	if err != nil {
		return nil, err
	}
	return userPromptResult(
		"Context-health snapshot for the current session",
		formatHealthAsMarkdown(out),
	), nil
}

// PromptSessionsHandler implements /mcp__klyne__sessions. Live:
// lists every Claude + Codex session in cwd's project so the user can
// see what's discoverable and pick one for the next call.
func PromptSessionsHandler(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	cwd, err := promptCWD(req)
	if err != nil {
		return nil, err
	}
	_, out, err := HandleListSessions(ctx, nil, ListSessionsInput{CWD: cwd})
	if err != nil {
		return nil, err
	}
	return userPromptResult(
		"Sessions in this project (Claude + Codex)",
		formatSessionsAsMarkdown(out),
	), nil
}

// PromptHandoffHandler implements /mcp__klyne__handoff. Live:
// renders the deterministic handoff Markdown the user can paste into
// a fresh session.
func PromptHandoffHandler(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	cwd, err := promptCWD(req)
	if err != nil {
		return nil, err
	}
	_, out, err := HandleGenerateHandoff(ctx, nil, HandoffInput{CWD: cwd})
	if err != nil {
		return nil, err
	}
	if out.Markdown == "" {
		// Ambiguous or no-session — surface the underlying reason text
		// (already populated by the tool handler). Fall back to a
		// generic explanation when even that is empty.
		text := strings.TrimSpace(out.Markdown)
		if text == "" {
			text = "No handoff available for this working directory. Run `/mcp__klyne__sessions` to see candidates."
		}
		return userPromptResult("Handoff (no session resolved)", text), nil
	}
	return userPromptResult(
		"Handoff prompt — paste into a fresh session",
		out.Markdown,
	), nil
}

// PromptSearchHandler implements /mcp__klyne__search. Live:
// invokes search_messages and returns the hits as Markdown so the
// user (and Claude) can pick a session_id to drill into.
//
// Required argument: `query`. Optional: `limit`, `sort`, `project_path`.
func PromptSearchHandler(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	args := map[string]string{}
	if req != nil && req.Params != nil {
		args = req.Params.Arguments
	}
	in := SearchInput{
		Query:       args["query"],
		Sort:        args["sort"],
		ProjectPath: args["project_path"],
	}
	if v := strings.TrimSpace(args["limit"]); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			in.Limit = n
		}
	}
	_, out, err := HandleSearchMessages(ctx, nil, in)
	if err != nil {
		return nil, err
	}
	return userPromptResult(
		"Search results across indexed sessions",
		formatSearchAsMarkdown(out),
	), nil
}

// formatSearchAsMarkdown renders a SearchOutput as a hit list. Each
// row carries enough info (session_id, project_path, role, ts) for
// the AI to call get_pre_compact_context or generate_handoff on the
// right session next.
func formatSearchAsMarkdown(out SearchOutput) string {
	if out.DaemonDown {
		return out.Reason
	}
	if len(out.Hits) == 0 {
		if out.Reason != "" {
			return out.Reason
		}
		return fmt.Sprintf("No matches for %q.", out.Query)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Search results for %q\n\n", out.Query)
	fmt.Fprintf(&b, "%d hit(s)", out.Total)
	if out.TookMs > 0 {
		fmt.Fprintf(&b, " · %dms", out.TookMs)
	}
	b.WriteString("\n\n")
	for i, h := range out.Hits {
		when := time.UnixMilli(h.TS).UTC().Format("2006-01-02 15:04 UTC")
		fmt.Fprintf(&b, "## %d. `%s` (%s)\n\n", i+1, short(h.SessionID), h.CLI)
		fmt.Fprintf(&b, "- Project: `%s`\n", h.ProjectPath)
		fmt.Fprintf(&b, "- Role: %s · %s\n", h.Role, when)
		fmt.Fprintf(&b, "- Snippet: %s\n\n", oneLine(h.Snippet))
	}
	return b.String()
}

// PromptTokenTimelineHandler implements /mcp__klyne__tokens.
// Live: invokes get_token_timeline and renders the ASCII sparkline
// + recent-turns table inline so the user can eyeball the trend
// without leaving Claude Code's chat.
func PromptTokenTimelineHandler(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	cwd, err := promptCWD(req)
	if err != nil {
		return nil, err
	}
	in := TokenTimelineInput{CWD: cwd}
	if req != nil && req.Params != nil {
		// Prefer the explicit duration string ("30m", "2h"); fall
		// back to the integer-hours convenience field.
		if v := strings.TrimSpace(req.Params.Arguments["window"]); v != "" {
			in.Window = v
		}
		if v := strings.TrimSpace(req.Params.Arguments["window_hours"]); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				in.WindowHours = n
			}
		}
	}
	_, out, err := HandleGetTokenTimeline(ctx, nil, in)
	if err != nil {
		return nil, err
	}
	return userPromptResult(
		"Token usage timeline for the active session",
		formatTokenTimelineAsMarkdown(out),
	), nil
}

// PromptPreCompactHandler implements /mcp__klyne__precompact.
// Live: recovers messages preceding the last /compact event (Claude)
// or replacement_history (Codex). When no compact has happened, the
// text explains why nothing was returned.
func PromptPreCompactHandler(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	cwd, err := promptCWD(req)
	if err != nil {
		return nil, err
	}
	_, out, err := HandleGetPreCompactContext(ctx, nil, PreCompactInput{CWD: cwd})
	if err != nil {
		return nil, err
	}
	return userPromptResult(
		"Recovered context from before the last /compact event",
		formatPreCompactAsMarkdown(out),
	), nil
}

// formatHealthAsMarkdown turns a GetContextHealthOutput into a human
// (and AI) readable block. Includes the headline verdict, the
// recommended action, and the top 3 bloat rows when present.
func formatHealthAsMarkdown(out GetContextHealthOutput) string {
	if out.Ambiguous {
		return formatAmbiguousAsMarkdown("get_context_health", out.Candidates)
	}
	if out.State == "" {
		// "No session found" path; the tool already populated Reason.
		return out.Reason
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Context health: %s\n\n", out.State)
	fmt.Fprintf(&b, "**Recommended action:** `%s`\n\n", out.Action)
	fmt.Fprintf(&b, "%s\n\n", out.Reason)
	fmt.Fprintf(&b, "- Session: `%s`\n", short(out.SessionID))
	fmt.Fprintf(&b, "- Model: `%s`\n", out.Model)
	fmt.Fprintf(&b, "- Context fill: %.1f%%\n", out.ContextFillPct)
	fmt.Fprintf(&b, "- Messages: %d\n\n", out.MsgCount)
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

// formatSessionsAsMarkdown renders a ListSessionsOutput as a list of
// sessions with CLI / activity / last-modified per row.
func formatSessionsAsMarkdown(out ListSessionsOutput) string {
	if len(out.Candidates) == 0 {
		return fmt.Sprintf("No klyne-discoverable sessions in `%s`.", out.CWD)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Sessions in `%s`\n\n", out.CWD)
	for _, c := range out.Candidates {
		active := ""
		if c.IsActive {
			active = " · **active**"
		}
		// CandidateRow doesn't expose CLI directly today; the AI can
		// infer it from the session-id format, but we include CLI in
		// the rendered text by prefix-matching the SessionID against
		// known shapes. Cleaner: extend CandidateRow with CLI in a
		// follow-up; left as-is for this slice to avoid a JSON-shape
		// change without need.
		fmt.Fprintf(&b, "- `%s`%s — %s · %d msgs · %q\n",
			c.SessionID, active, c.ModTime, c.MsgCount, c.Preview)
	}
	return b.String()
}

// formatPreCompactAsMarkdown renders the recovered messages (or the
// "no compact yet" status) as a readable block.
func formatPreCompactAsMarkdown(out PreCompactOutput) string {
	if out.Ambiguous {
		return formatAmbiguousAsMarkdown("get_pre_compact_context", out.Candidates)
	}
	if !out.FoundCompact {
		if out.Path == "" {
			return "No session found for this working directory."
		}
		return "This session has not been /compact'd yet — nothing to recover."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Pre-compact recovery\n\n")
	fmt.Fprintf(&b, "Recovered %d messages from before the last /compact event.\n\n", len(out.Messages))
	if out.Trigger != "" {
		fmt.Fprintf(&b, "- Trigger: `%s`\n", out.Trigger)
	}
	if out.PreTokens > 0 {
		fmt.Fprintf(&b, "- Pre-compact size: %d tokens\n", out.PreTokens)
	}
	if out.CompactTimestamp != "" {
		fmt.Fprintf(&b, "- Compact timestamp: `%s`\n", out.CompactTimestamp)
	}
	b.WriteString("\n## Messages\n\n")
	for _, m := range out.Messages {
		fmt.Fprintf(&b, "**%s** — %s\n\n", m.Role, oneLine(m.Content))
	}
	return b.String()
}

// formatAmbiguousAsMarkdown surfaces the candidate list when the
// resolver can't pick a unique active session. Same content shape the
// underlying tools return, in human-readable form for the slash UX.
func formatAmbiguousAsMarkdown(toolName string, cands []CandidateRow) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Multiple sessions in this project. Re-call `%s` with an explicit `session_id`:\n\n", toolName)
	for _, c := range cands {
		active := ""
		if c.IsActive {
			active = " · active"
		}
		fmt.Fprintf(&b, "- `%s`%s — %s · %q\n", c.SessionID, active, c.ModTime, c.Preview)
	}
	return b.String()
}

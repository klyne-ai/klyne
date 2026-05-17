package mcpserver

import (
	"fmt"
	"strings"
	"time"
)

// Tool-output formatters.
// =======================
// Each handler in the tools/* layer produces both a structured output
// and a `markdown` field meant to be rendered verbatim by the host.
// These helpers turn the structured outputs into that markdown. They
// previously lived in prompts.go alongside the live slash-prompt
// handlers, but the prompt surface was removed (it duplicated the
// static slash commands under ~/.claude/commands/klyne/). The
// formatters stayed because the tools themselves still emit the
// markdown body.

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
// sessions with CLI / activity / last-modified / preview per row.
//
// The directive prefix asks the AI host to render the section
// verbatim. Without it, hosts tend to condense the list to "the
// active session is f876eadd" — losing the previews and timestamps
// the user actually invoked /klyne:sessions to see.
//
// The preview cleanup drops Claude Code's `<environment_context>…`
// boilerplate that appears as the literal first user message of
// most sessions. That string is structurally meaningless to a
// human scanning a session list; the second non-trivial line is
// what they actually want.
func formatSessionsAsMarkdown(out ListSessionsOutput) string {
	if len(out.Candidates) == 0 {
		return fmt.Sprintf("No klyne-discoverable sessions in `%s`.", out.CWD)
	}
	var b strings.Builder
	b.WriteString("The user invoked `/klyne:sessions`. Render the list below VERBATIM, every row preserved. Do not collapse to a single 'the active session is X' line. After the list you may add at most one short sentence of context.\n\n")
	fmt.Fprintf(&b, "# Sessions in `%s`\n\n", out.CWD)
	for _, c := range out.Candidates {
		active := ""
		if c.IsActive {
			active = " · **active**"
		}
		preview := cleanSessionPreview(c.Preview)
		fmt.Fprintf(&b, "- `%s`%s — %s · %d msgs · %q\n",
			c.SessionID, active, c.ModTime, c.MsgCount, preview)
	}
	return b.String()
}

// cleanSessionPreview drops Claude Code's `<environment_context>`
// boilerplate that often appears as the first user message of a
// resumed session. When the original preview is just that envelope,
// returns a placeholder so the row stays readable rather than printing
// 200 chars of context-tag XML.
func cleanSessionPreview(preview string) string {
	trimmed := strings.TrimSpace(preview)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "<environment_context>") ||
		strings.HasPrefix(trimmed, "<command-message>") ||
		strings.HasPrefix(trimmed, "<command-name>") {
		return "(session-resume metadata — no human prompt yet)"
	}
	return trimmed
}

// formatPreCompactAsMarkdown renders the recovered messages (or the
// "no compact yet" status) as a readable block. The directive prefix
// asks the AI host to render the section verbatim — without it,
// hosts tend to summarise the recovered messages back into a single
// "we did X then Y" line, defeating the purpose of recovery.
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
	b.WriteString("The user invoked `/klyne:precompact`. Render the report below VERBATIM, including every message row with its timestamp and role exactly as written. Do not summarise; do not omit rows. After the report you may add at most one short sentence of context.\n\n")
	fmt.Fprintf(&b, "# Pre-compact recovery\n\n")
	fmt.Fprintf(&b, "Recovered %d messages from immediately before the LAST `/compact` event in this session.\n\n", len(out.Messages))
	b.WriteString("> Note: this returns only the slice of conversation that the last `/compact` ate. If you ran `/compact` early in a long session, only those early turns are recovered here; later turns are still in the live transcript and visible via `klyne tokens` or by scrolling the chat.\n\n")
	if out.Trigger != "" {
		fmt.Fprintf(&b, "- Trigger: `%s`\n", out.Trigger)
	}
	if out.PreTokens > 0 {
		fmt.Fprintf(&b, "- Pre-compact size: %d tokens\n", out.PreTokens)
	}
	if out.CompactTimestamp != "" {
		fmt.Fprintf(&b, "- Compact timestamp: `%s`\n", out.CompactTimestamp)
	}
	b.WriteString("\n## Messages (oldest first)\n\n")
	loc := time.Local
	for _, m := range out.Messages {
		body := oneLine(m.Content)
		if body == "" {
			continue
		}
		when := ""
		if m.TS > 0 {
			when = time.UnixMilli(m.TS).In(loc).Format("15:04:05") + " "
		}
		fmt.Fprintf(&b, "**%s%s** — %s\n\n", when, m.Role, body)
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

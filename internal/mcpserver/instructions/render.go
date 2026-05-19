// Package instructions builds the MCP server's Instructions string
// for klyne. The string carries a directive telling the model when
// to call mcp__klyne__recall plus a titled inventory of the user's
// project + global runbooks. Computed once at server New(); empty
// string when no runbooks exist (instructions field omitted from
// MCP initialize).
package instructions

import (
	"fmt"
	"strings"

	"github.com/klyne-ai/klyne/internal/store"
)

// deriveTitle returns a one-line display title for a runbook body.
// First non-empty line, trimmed, truncated to 60 runes (NOT bytes)
// with "…" appended when cut. Mirrors deriveMemoryName in
// internal/mcpserver/tool_memory_crud.go — kept local to avoid a
// circular package dependency (instructions is a sub-package of
// mcpserver, and mcpserver imports instructions).
func deriveTitle(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	var firstLine string
	for _, line := range strings.Split(trimmed, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			firstLine = s
			break
		}
	}
	if firstLine == "" {
		return ""
	}
	const maxRunes = 60
	runes := []rune(firstLine)
	if len(runes) <= maxRunes {
		return firstLine
	}
	return string(runes[:maxRunes]) + "…"
}

// renderFooter formats the "more runbooks exist" footer. Empty
// string when there is nothing to hint at (Build passes the count
// of rows that were cut from the inventory).
func renderFooter(hiddenCount int) string {
	if hiddenCount <= 0 {
		return ""
	}
	return fmt.Sprintf("+%d more, call mcp__klyne__recall to see all", hiddenCount)
}

// renderInventory formats one scope's runbook list under a heading.
// Returns "" when items is empty so the caller can omit empty
// sections cleanly. hiddenCount > 0 appends the "+N more" footer
// on its own line below the list.
func renderInventory(items []store.Decision, heading string, hiddenCount int) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", heading)
	for _, d := range items {
		title := deriveTitle(d.Text)
		if title == "" {
			// Defensive: a body that derives no title would render as a
			// dangling id; skip it rather than emit a malformed row.
			continue
		}
		fmt.Fprintf(&b, "  - %s  %s\n", d.ID, title)
	}
	if footer := renderFooter(hiddenCount); footer != "" {
		fmt.Fprintf(&b, "  %s\n", footer)
	}
	return b.String()
}

// renderDirective returns the static behavioural prompt that tells
// the model when to call mcp__klyne__recall. Two trigger rules:
//   (1) any operational shell command (deploys, secrets, scripts,
//       "add X for service Y", etc.)
//   (2) topical match against the inventory shown below the directive
// The wording is load-bearing — changes here flow into every session
// for every klyne user. Treat it like a public API.
func renderDirective() string {
	return strings.TrimSpace(`
klyne tracks runbooks for this project. Before acting on a user request,
check whether one applies:

  - Operational asks (deploys, secrets, migrations, scripts under
    ./scripts/, "add X for service Y", etc.) → ALWAYS call
    mcp__klyne__recall first, then follow any matching runbook verbatim
    with variables substituted from the user's request.

  - Topical match against the inventory below → call mcp__klyne__recall
    to fetch the full runbook body before investigating.

If a runbook matches, echo the substituted commands in a fenced block
and confirm BEFORE executing.
`)
}

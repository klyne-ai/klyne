package instructions

import (
	"context"
	"fmt"
	"strings"

	"github.com/klyne-ai/klyne/internal/store"
)

// inventoryLimit is the max number of runbooks listed per scope in
// the instructions string. Build fetches a wider window than this
// so the "+N more" footer can show an accurate cut count without a
// second COUNT query.
const inventoryLimit = 20

// fetchWindow is the cap on rows pulled per scope. We surface
// inventoryLimit rows; anything between inventoryLimit and
// fetchWindow contributes to the "+N more" footer count.
// Beyond fetchWindow the footer undercounts — acceptable per spec
// (recall still works live).
const fetchWindow = inventoryLimit * 4

// Build returns the MCP server Instructions string for the project
// rooted at cwd. Empty string means "omit the instructions field
// from MCP initialize" — used for new users with no runbooks AND
// for any failure path (DB unreachable, query error, etc.). The
// caller passes the result straight to ServerOptions.Instructions.
//
// cwd MUST already be canonicalised (projectpath.Canonical) by the
// caller. Build does not call Canonical itself — the same string is
// used in the rendered "path: <cwd>" heading and as the query key,
// so the caller is responsible for picking one canonical form.
//
// Build is best-effort enrichment. It MUST NOT panic or return an
// error: instructions are nice-to-have and a server that fails to
// start because the runbook list could not be rendered is worse than
// a server that starts without runbooks.
func Build(ctx context.Context, cwd string, db *store.DB) string {
	if db == nil {
		return ""
	}

	project, projectHidden := fetchProject(ctx, db, cwd)
	global, globalHidden := fetchGlobal(ctx, db)

	if len(project) == 0 && len(global) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(renderDirective())
	b.WriteString("\n\n")

	if len(project) > 0 {
		heading := fmt.Sprintf("Project runbooks (path: %s)", cwd)
		b.WriteString(renderInventory(project, heading, projectHidden))
		b.WriteString("\n")
	}
	if len(global) > 0 {
		const globalHeading = "Global runbooks"
		b.WriteString(renderInventory(global, globalHeading, globalHidden))
	}

	return strings.TrimRight(b.String(), "\n")
}

// fetchProject returns up to inventoryLimit project-scoped runbooks
// plus the number that were cut from the cap. cwd="" yields no
// project runbooks because ListDecisions treats empty ProjectPath as
// "no filter" — see fetchGlobal for the client-side filter that
// handles globals.
func fetchProject(ctx context.Context, db *store.DB, cwd string) ([]store.Decision, int) {
	if cwd == "" {
		return nil, 0
	}
	rows, err := store.ListDecisions(ctx, db, store.DecisionFilter{
		ProjectPath: cwd,
		Limit:       fetchWindow,
	})
	if err != nil {
		// Best-effort: log to stderr would be nice but stderr is
		// the MCP host's diagnostic channel — leave that to the
		// caller. Silent skip is acceptable here per the spec's
		// error-handling table.
		return nil, 0
	}
	return trimToLimit(rows)
}

// fetchGlobal returns up to inventoryLimit global runbooks
// (project_path == "") plus the cut count. ListDecisions with
// ProjectPath="" returns ALL decisions across all projects (not
// just globals), so we have to fetch a larger window and filter
// client-side — same pattern as HandleRecallMemory at
// internal/mcpserver/tool_memory.go:243-257.
//
// Window size: fetchWindow. Cheap and almost certainly covers the
// global slice in real installs (most users have <5 globals). If a
// power user manages to have so many non-global decisions that
// globals get pushed past the window, the worst case is the
// instructions surface fewer globals than expected — recall still
// works live.
func fetchGlobal(ctx context.Context, db *store.DB) ([]store.Decision, int) {
	all, err := store.ListDecisions(ctx, db, store.DecisionFilter{
		Limit: fetchWindow,
	})
	if err != nil {
		return nil, 0
	}
	globals := make([]store.Decision, 0, fetchWindow)
	for _, d := range all {
		if d.ProjectPath == "" {
			globals = append(globals, d)
		}
	}
	return trimToLimit(globals)
}

// trimToLimit trims rows down to inventoryLimit and returns
// (kept, hidden) where hidden is the count cut. Callers fetch up
// to fetchWindow rows so hidden is an accurate cut count for the
// "+N more" footer (capped at fetchWindow - inventoryLimit).
func trimToLimit(rows []store.Decision) ([]store.Decision, int) {
	if len(rows) <= inventoryLimit {
		return rows, 0
	}
	return rows[:inventoryLimit], len(rows) - inventoryLimit
}

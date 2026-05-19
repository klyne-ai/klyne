package instructions

import (
	"context"
	"fmt"
	"strings"

	"github.com/klyne-ai/klyne/internal/store"
)

// inventoryLimit caps how many runbooks per scope appear in the
// rendered inventory. fetchWindow caps how many rows we pull per
// scope; the gap between inventoryLimit and fetchWindow feeds the
// "+N more" footer count. Beyond fetchWindow the footer undercounts
// — acceptable per spec (recall still works live).
const (
	inventoryLimit = 20
	fetchWindow    = inventoryLimit * 4
)

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
		b.WriteString(renderInventory(global, "Global runbooks", globalHidden))
	}

	return strings.TrimRight(b.String(), "\n")
}

// fetchProject returns up to inventoryLimit project-scoped runbooks
// plus the number that were cut from the cap. cwd="" yields no
// project runbooks because ListDecisions treats empty ProjectPath as
// "no filter"; globals are fetched separately via fetchGlobal.
func fetchProject(ctx context.Context, db *store.DB, cwd string) ([]store.Decision, int) {
	if cwd == "" {
		return nil, 0
	}
	rows, err := store.ListDecisions(ctx, db, store.DecisionFilter{
		ProjectPath: cwd,
		Limit:       fetchWindow,
	})
	if err != nil {
		// Best-effort per spec — silent skip.
		return nil, 0
	}
	return trimToLimit(rows)
}

// fetchGlobal returns up to inventoryLimit global runbooks
// (project_path == "") plus the cut count. Uses the store's
// ListGlobalDecisions primitive — fetches with fetchWindow so
// the "+N more" footer count is accurate up to fetchWindow -
// inventoryLimit hidden rows. Beyond that, the footer
// undercounts (recall still works live).
func fetchGlobal(ctx context.Context, db *store.DB) ([]store.Decision, int) {
	rows, err := store.ListGlobalDecisions(ctx, db, fetchWindow)
	if err != nil {
		return nil, 0
	}
	return trimToLimit(rows)
}

// trimToLimit trims rows down to inventoryLimit and returns
// (kept, hidden) where kept ≤ inventoryLimit and hidden ≥ 0 is the
// accurate cut count (capped at fetchWindow - inventoryLimit by the
// fetch step). Callers fetch fetchWindow rows so hidden tracks the
// real overage up to that bound.
func trimToLimit(rows []store.Decision) ([]store.Decision, int) {
	if len(rows) <= inventoryLimit {
		return rows, 0
	}
	return rows[:inventoryLimit], len(rows) - inventoryLimit
}

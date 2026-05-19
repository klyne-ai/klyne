# Runbook Auto-Recall Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire MCP server-level `Instructions` into klyne's MCP server so the model sees a directive + titled runbook inventory on every message of every session — for both Claude Code and Codex — without the user having to reference runbooks manually.

**Architecture:** New `internal/mcpserver/instructions/` package with one pure builder (`Build`) and pure renderers. `mcpserver.New()` calls `Build` once at server boot, passes the result via `mcp.ServerOptions.Instructions`. The string contains a behavioural directive followed by the 20 newest project + 20 newest global runbook titles (with `+N more` footer when capped). Empty string when no runbooks exist (zero context cost for new users). Instructions are best-effort enrichment — DB or filesystem errors degrade silently and never block server startup.

**Tech Stack:** Go 1.25, `github.com/modelcontextprotocol/go-sdk/mcp` v1.6.0 (`ServerOptions.Instructions` is in `mcp/server.go:62` of the SDK), `internal/store` for `ListDecisions`, `internal/projectpath` for `Canonical`, standard `testing` package.

**Spec:** [`docs/superpowers/specs/2026-05-19-runbook-auto-recall-design.md`](../specs/2026-05-19-runbook-auto-recall-design.md)

---

## File Structure

| File | Action | Responsibility |
|---|---|---|
| `internal/mcpserver/instructions/render.go` | **create** | Pure formatting helpers: `renderDirective`, `renderInventory`, `renderFooter`, `deriveTitle`. No DB access. |
| `internal/mcpserver/instructions/render_test.go` | **create** | Unit tests for the four pure helpers. |
| `internal/mcpserver/instructions/build.go` | **create** | `Build(ctx, cwd, db) string` — queries store, formats, returns empty string on any failure or empty inventory. |
| `internal/mcpserver/instructions/build_test.go` | **create** | Table-driven tests for empty DB, project+global, and overflow-footer cases against a real `store.DB`. |
| `internal/mcpserver/server.go:27` | **edit** | Bump `version` constant from `v0.7.0` to `v0.7.1`. Wire-visible behaviour changes (instructions field now present). |
| `internal/mcpserver/server.go:49-53` | **edit** | Compute instructions string at `New()` and pass via `&mcp.ServerOptions{Instructions: instr}` instead of `nil`. |
| `internal/mcpserver/tool_memory_crud_test.go` | **edit** | Add `TestInstructions_PostRemember_ShowsRunbookTitle` — end-to-end wiring check: insert via `HandleRememberMemory`, call `instructions.Build`, assert title shows. |

---

## Background reading the engineer must do BEFORE Task 1

These three reads ground all the patterns the plan reuses. Skim them in order:

1. **`internal/mcpserver/tool_memory.go:194-272`** — `HandleRecallMemory`. Shows the "globals must be filtered client-side" gotcha you must replicate in `Build`. Specifically lines 243-257: because `store.ListDecisions` with `ProjectPath=""` returns ALL decisions across all projects (not just globals), you have to iterate the result and keep only rows where `d.ProjectPath == ""`.
2. **`internal/mcpserver/tool_memory_crud.go:267-300`** — `wrapNamed` + `deriveMemoryName`. The title-derivation logic (first non-empty line, trimmed, truncated to 60 runes with `…`). You'll reimplement this in the `instructions` package as `deriveTitle` because `deriveMemoryName` is unexported and a sub-package can't import its parent without a circular dependency.
3. **`internal/store/decisions.go:61-90`** — `ListDecisions` signature: `(ctx, db, DecisionFilter{ProjectPath, SessionID, Tag, Limit}) ([]Decision, error)`. Returns rows sorted `ts DESC`. Default limit 100 when `Limit <= 0`.

---

## Task 1: Renderer primitives (pure functions, TDD)

**Files:**
- Create: `internal/mcpserver/instructions/render.go`
- Create: `internal/mcpserver/instructions/render_test.go`

Pure helpers with no DB or filesystem access. Doing these first means Task 2's `Build` is just glue.

### Step 1.1: Create the test file with `deriveTitle` cases

- [ ] **Step 1.1: Write failing tests for `deriveTitle`**

Create `internal/mcpserver/instructions/render_test.go`:

```go
package instructions

import (
	"strings"
	"testing"
)

func TestDeriveTitle_Empty(t *testing.T) {
	if got := deriveTitle(""); got != "" {
		t.Errorf("deriveTitle(\"\") = %q, want \"\"", got)
	}
	if got := deriveTitle("   \n\n   "); got != "" {
		t.Errorf("deriveTitle(whitespace) = %q, want \"\"", got)
	}
}

func TestDeriveTitle_SingleLine(t *testing.T) {
	got := deriveTitle("rotate api keys monthly")
	if got != "rotate api keys monthly" {
		t.Errorf("got %q", got)
	}
}

func TestDeriveTitle_MultilineFirstLine(t *testing.T) {
	in := "for labstack changes we have four working dirs\nsteps:\n1. ..."
	got := deriveTitle(in)
	want := "for labstack changes we have four working dirs"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDeriveTitle_TruncatesAt60Runes(t *testing.T) {
	long := strings.Repeat("x", 80)
	got := deriveTitle(long)
	runes := []rune(got)
	if len(runes) != 61 {
		t.Fatalf("len = %d, want 61 (60 + ellipsis)", len(runes))
	}
	if runes[60] != '…' {
		t.Errorf("expected ellipsis suffix, got %q", string(runes[60]))
	}
}

func TestDeriveTitle_SkipsBlankFirstLine(t *testing.T) {
	got := deriveTitle("\n\nactual title\nbody")
	if got != "actual title" {
		t.Errorf("got %q", got)
	}
}
```

- [ ] **Step 1.2: Run tests to verify they fail**

Run: `go test ./internal/mcpserver/instructions/ -run TestDeriveTitle -v`
Expected: FAIL with build error `package internal/mcpserver/instructions: no Go files in ...` or `undefined: deriveTitle`.

- [ ] **Step 1.3: Create `render.go` with `deriveTitle`**

Create `internal/mcpserver/instructions/render.go`:

```go
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
```

- [ ] **Step 1.4: Run tests to verify they pass**

Run: `go test ./internal/mcpserver/instructions/ -run TestDeriveTitle -v`
Expected: PASS — all 5 subtests pass.

### Step 1.5–1.9: `renderFooter`

- [ ] **Step 1.5: Append failing footer tests to `render_test.go`**

```go
func TestRenderFooter_Zero(t *testing.T) {
	if got := renderFooter(0); got != "" {
		t.Errorf("renderFooter(0) = %q, want \"\"", got)
	}
}

func TestRenderFooter_Negative(t *testing.T) {
	if got := renderFooter(-3); got != "" {
		t.Errorf("renderFooter(-3) = %q, want \"\"", got)
	}
}

func TestRenderFooter_Positive(t *testing.T) {
	got := renderFooter(5)
	want := "+5 more, call mcp__klyne__recall to see all"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
```

- [ ] **Step 1.6: Run to verify they fail**

Run: `go test ./internal/mcpserver/instructions/ -run TestRenderFooter -v`
Expected: FAIL with `undefined: renderFooter`.

- [ ] **Step 1.7: Implement `renderFooter`**

Append to `render.go`:

```go
// renderFooter formats the "more runbooks exist" footer. Empty
// string when there is nothing to hint at (Build passes the count
// of rows that were cut from the inventory).
func renderFooter(hiddenCount int) string {
	if hiddenCount <= 0 {
		return ""
	}
	return fmt.Sprintf("+%d more, call mcp__klyne__recall to see all", hiddenCount)
}
```

- [ ] **Step 1.8: Run to verify pass**

Run: `go test ./internal/mcpserver/instructions/ -run TestRenderFooter -v`
Expected: PASS.

### Step 1.9–1.13: `renderInventory`

- [ ] **Step 1.9: Append failing tests for `renderInventory`**

```go
func TestRenderInventory_Empty(t *testing.T) {
	if got := renderInventory(nil, "Project runbooks (path: /repo)", 0); got != "" {
		t.Errorf("nil slice should yield empty string, got %q", got)
	}
	if got := renderInventory([]store.Decision{}, "Project runbooks", 0); got != "" {
		t.Errorf("empty slice should yield empty string, got %q", got)
	}
}

func TestRenderInventory_TwoRowsNoFooter(t *testing.T) {
	rows := []store.Decision{
		{ID: "d-abc", Text: "rotate api keys monthly"},
		{ID: "d-def", Text: "labstack worktree branch\nstep 1\nstep 2"},
	}
	got := renderInventory(rows, "Project runbooks (path: /repo)", 0)

	if !strings.Contains(got, "# Project runbooks (path: /repo)") {
		t.Errorf("missing heading; got:\n%s", got)
	}
	if !strings.Contains(got, "  - d-abc  rotate api keys monthly") {
		t.Errorf("missing first row; got:\n%s", got)
	}
	if !strings.Contains(got, "  - d-def  labstack worktree branch") {
		t.Errorf("missing second row (first line only); got:\n%s", got)
	}
	if strings.Contains(got, "more, call mcp__klyne__recall") {
		t.Errorf("should not include footer when hiddenCount=0; got:\n%s", got)
	}
}

func TestRenderInventory_WithFooter(t *testing.T) {
	rows := []store.Decision{{ID: "d-1", Text: "title"}}
	got := renderInventory(rows, "Global runbooks", 7)
	if !strings.Contains(got, "+7 more, call mcp__klyne__recall to see all") {
		t.Errorf("missing footer; got:\n%s", got)
	}
}
```

- [ ] **Step 1.10: Run to verify they fail**

Run: `go test ./internal/mcpserver/instructions/ -run TestRenderInventory -v`
Expected: FAIL with `undefined: renderInventory`.

- [ ] **Step 1.11: Implement `renderInventory`**

Append to `render.go`:

```go
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
```

- [ ] **Step 1.12: Run to verify pass**

Run: `go test ./internal/mcpserver/instructions/ -run TestRenderInventory -v`
Expected: PASS (3 subtests).

### Step 1.13–1.16: `renderDirective`

- [ ] **Step 1.13: Append failing test for `renderDirective`**

```go
func TestRenderDirective_ContainsKeyPhrases(t *testing.T) {
	got := renderDirective()
	for _, want := range []string{
		"klyne tracks runbooks for this project",
		"mcp__klyne__recall",
		"Operational asks",
		"Topical match",
		"confirm BEFORE executing",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("directive missing %q\nfull text:\n%s", want, got)
		}
	}
}
```

- [ ] **Step 1.14: Run to verify it fails**

Run: `go test ./internal/mcpserver/instructions/ -run TestRenderDirective -v`
Expected: FAIL with `undefined: renderDirective`.

- [ ] **Step 1.15: Implement `renderDirective`**

Append to `render.go`:

```go
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
```

- [ ] **Step 1.16: Run to verify pass**

Run: `go test ./internal/mcpserver/instructions/ -run TestRenderDirective -v`
Expected: PASS.

### Step 1.17: Run all Task 1 tests and commit

- [ ] **Step 1.17: Run the full renderer test file**

Run: `go test ./internal/mcpserver/instructions/ -v`
Expected: PASS — all 11 subtests (3 deriveTitle + 3 renderFooter + 3 renderInventory + 1 renderDirective + 1 empty-cases) pass.

- [ ] **Step 1.18: Commit Task 1**

```bash
git add internal/mcpserver/instructions/render.go internal/mcpserver/instructions/render_test.go
git commit -m "$(cat <<'EOF'
mcp: add instructions package renderer primitives

Pure helpers (deriveTitle, renderFooter, renderInventory,
renderDirective) for the upcoming MCP server Instructions string.
No DB access; tested in isolation. Build.go (Task 2) and the
server.New() wiring (Task 3) follow.

Spec: docs/superpowers/specs/2026-05-19-runbook-auto-recall-design.md
EOF
)"
```

---

## Task 2: `Build` function (DB-touching, TDD)

**Files:**
- Create: `internal/mcpserver/instructions/build.go`
- Create: `internal/mcpserver/instructions/build_test.go`

`Build` queries the store and composes the final string. Returns `""` on any failure or empty inventory so the caller can pass it directly to `mcp.ServerOptions.Instructions` without conditional logic.

### Step 2.1: Test helper for opening a temp DB

- [ ] **Step 2.1: Create `build_test.go` with helper and empty-DB test**

Create `internal/mcpserver/instructions/build_test.go`:

```go
package instructions

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/store"
)

// openTempDB opens a fresh store DB in a tempdir, runs migrations,
// and returns the handle. Cleaned up via t.Cleanup.
func openTempDB(t *testing.T) *store.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "klyne.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// seed inserts one decision row with the given project_path and
// returns the id so tests can assert ordering / membership.
func seed(t *testing.T, db *store.DB, id, projectPath, text string) {
	t.Helper()
	d := &store.Decision{
		ID:          id,
		Ts:          time.Now().UnixMilli(),
		ProjectPath: projectPath,
		Text:        text,
	}
	if err := store.InsertDecision(context.Background(), db, d); err != nil {
		t.Fatalf("InsertDecision %s: %v", id, err)
	}
	// 1ms spacing so ts DESC ordering is deterministic across rows.
	time.Sleep(time.Millisecond)
}

func TestBuild_EmptyDB(t *testing.T) {
	db := openTempDB(t)
	got := Build(context.Background(), "/some/project", db)
	if got != "" {
		t.Errorf("expected empty string for empty DB, got:\n%s", got)
	}
}
```

- [ ] **Step 2.2: Run to verify it fails**

Run: `go test ./internal/mcpserver/instructions/ -run TestBuild_EmptyDB -v`
Expected: FAIL with `undefined: Build`.

- [ ] **Step 2.3: Create `build.go` with minimal `Build`**

Create `internal/mcpserver/instructions/build.go`:

```go
package instructions

import (
	"context"
	"fmt"
	"strings"

	"github.com/klyne-ai/klyne/internal/store"
)

// inventoryLimit is the max number of runbooks listed per scope in
// the instructions string. Build fetches limit+1 rows so it can
// detect overflow without a second COUNT query.
const inventoryLimit = 20

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
	// Stub — Step 2.5 expands this.
	_ = ctx
	_ = cwd
	_ = db
	return ""
}

// fmt / strings imports will be exercised in later steps.
var _ = fmt.Sprintf
var _ = strings.Builder{}
```

- [ ] **Step 2.4: Run to verify pass**

Run: `go test ./internal/mcpserver/instructions/ -run TestBuild_EmptyDB -v`
Expected: PASS.

### Step 2.5: Project + global runbook case

- [ ] **Step 2.5: Append failing test for populated DB**

Append to `build_test.go`:

```go
func TestBuild_ProjectAndGlobal(t *testing.T) {
	db := openTempDB(t)
	cwd := "/repo/svc-a"

	// Two project + two global runbooks, varied bodies.
	seed(t, db, "d-p1", cwd, "rotate api keys monthly")
	seed(t, db, "d-p2", cwd, "for labstack changes we have four working dirs\nstep 1\nstep 2")
	seed(t, db, "d-g1", "", "RUNBOOK: rotate vendor api key via ops vault")
	seed(t, db, "d-g2", "", "globally true: always confirm before destructive shell")

	// Decoy: a runbook from a DIFFERENT project must not leak in.
	seed(t, db, "d-other", "/repo/svc-b", "irrelevant to svc-a")

	got := Build(context.Background(), cwd, db)
	if got == "" {
		t.Fatal("expected non-empty instructions, got empty string")
	}

	// Directive present.
	if !strings.Contains(got, "klyne tracks runbooks for this project") {
		t.Errorf("missing directive header; got:\n%s", got)
	}

	// Both section headings present, scoped to cwd.
	if !strings.Contains(got, "# Project runbooks (path: /repo/svc-a)") {
		t.Errorf("missing project heading; got:\n%s", got)
	}
	if !strings.Contains(got, "# Global runbooks") {
		t.Errorf("missing global heading; got:\n%s", got)
	}

	// Each runbook's id + first-line title is present.
	for _, want := range []string{
		"d-p1  rotate api keys monthly",
		"d-p2  for labstack changes we have four working dirs",
		"d-g1  RUNBOOK: rotate vendor api key via ops vault",
		"d-g2  globally true: always confirm before destructive shell",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing row %q; got:\n%s", want, got)
		}
	}

	// Decoy from a different project must NOT appear.
	if strings.Contains(got, "d-other") || strings.Contains(got, "irrelevant to svc-a") {
		t.Errorf("decoy row from /repo/svc-b leaked into project section; got:\n%s", got)
	}

	// No footer when under cap.
	if strings.Contains(got, "more, call mcp__klyne__recall") {
		t.Errorf("unexpected overflow footer; got:\n%s", got)
	}
}
```

- [ ] **Step 2.6: Run to verify it fails**

Run: `go test ./internal/mcpserver/instructions/ -run TestBuild_ProjectAndGlobal -v`
Expected: FAIL — Build currently returns `""` unconditionally.

- [ ] **Step 2.7: Implement Build's query + render logic**

Replace the `Build` function body and helpers in `build.go`:

```go
// Build returns the MCP server Instructions string for the project
// rooted at cwd. (See full doc on previous version.)
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
// "no filter" — see fetchGlobal for the client-side filter that
// handles globals.
func fetchProject(ctx context.Context, db *store.DB, cwd string) ([]store.Decision, int) {
	if cwd == "" {
		return nil, 0
	}
	rows, err := store.ListDecisions(ctx, db, store.DecisionFilter{
		ProjectPath: cwd,
		Limit:       inventoryLimit + 1,
	})
	if err != nil {
		// Best-effort: log to stderr would be nice but stderr is
		// the MCP host's diagnostic channel — leave that to the
		// caller. Silent skip is acceptable here per the spec's
		// error-handling table.
		return nil, 0
	}
	return cap(rows)
}

// fetchGlobal returns up to inventoryLimit global runbooks
// (project_path == "") plus the cut count. ListDecisions with
// ProjectPath="" returns ALL decisions across all projects (not
// just globals), so we have to fetch a larger window and filter
// client-side — same pattern as HandleRecallMemory at
// internal/mcpserver/tool_memory.go:243-257.
//
// Window size: inventoryLimit*4. Cheap and almost certainly covers
// the global slice in real installs (most users have <5 globals).
// If a power user manages to have so many non-global decisions that
// globals get pushed past the window, the worst case is the
// instructions surface fewer globals than expected — recall still
// works live.
func fetchGlobal(ctx context.Context, db *store.DB) ([]store.Decision, int) {
	const window = inventoryLimit * 4
	all, err := store.ListDecisions(ctx, db, store.DecisionFilter{
		Limit: window,
	})
	if err != nil {
		return nil, 0
	}
	globals := make([]store.Decision, 0, inventoryLimit+1)
	for _, d := range all {
		if d.ProjectPath == "" {
			globals = append(globals, d)
			if len(globals) > inventoryLimit {
				break
			}
		}
	}
	return cap(globals)
}

// cap trims rows down to inventoryLimit and returns (kept, hidden)
// where hidden is the count cut. Callers fetch limit+1 specifically
// so a single extra row marks overflow without an exact count.
func cap(rows []store.Decision) ([]store.Decision, int) {
	if len(rows) <= inventoryLimit {
		return rows, 0
	}
	return rows[:inventoryLimit], len(rows) - inventoryLimit
}
```

Also delete the two `var _ = ...` lines from Step 2.3 — they were placeholders to keep the imports valid in the stub; the real implementation uses both packages.

Note on the name `cap`: Go's builtin `cap()` returns slice capacity. Shadowing the builtin inside one file is legal and the helper is unexported, but the engineer should not export it. If they prefer, rename to `trimToLimit` — the test doesn't care.

- [ ] **Step 2.8: Run to verify pass**

Run: `go test ./internal/mcpserver/instructions/ -run TestBuild_ProjectAndGlobal -v`
Expected: PASS.

### Step 2.9: Overflow footer

- [ ] **Step 2.9: Append failing overflow test**

Append to `build_test.go`:

```go
func TestBuild_OverflowProjectFooter(t *testing.T) {
	db := openTempDB(t)
	cwd := "/repo/svc-big"

	// 25 project runbooks → expect 20 shown + footer "+5 more".
	for i := 0; i < 25; i++ {
		seed(t, db, fmt.Sprintf("d-p%02d", i), cwd, fmt.Sprintf("project runbook %02d", i))
	}
	// 5 globals → expect 5 shown, no footer.
	for i := 0; i < 5; i++ {
		seed(t, db, fmt.Sprintf("d-g%02d", i), "", fmt.Sprintf("global runbook %02d", i))
	}

	got := Build(context.Background(), cwd, db)

	// Project footer present, global footer absent.
	if !strings.Contains(got, "+5 more, call mcp__klyne__recall to see all") {
		t.Errorf("missing project overflow footer; got:\n%s", got)
	}

	// Newest-first: seed inserts ts in increasing order, so d-p24 is
	// newest. The first 20 shown must be d-p24 down to d-p05.
	if !strings.Contains(got, "d-p24") {
		t.Errorf("newest project row d-p24 missing; got:\n%s", got)
	}
	if strings.Contains(got, "d-p04") {
		t.Errorf("oldest project row d-p04 should have been trimmed; got:\n%s", got)
	}

	// Globals are under cap — no global footer.
	// Count how many times the footer phrase appears: must be exactly 1
	// (the project one).
	if n := strings.Count(got, "more, call mcp__klyne__recall"); n != 1 {
		t.Errorf("expected exactly 1 footer line, found %d; got:\n%s", n, got)
	}
}
```

Add `"fmt"` to the imports if not already present.

- [ ] **Step 2.10: Run to verify it fails**

Run: `go test ./internal/mcpserver/instructions/ -run TestBuild_OverflowProjectFooter -v`
Expected: PASS — if Step 2.7 is correct, this should pass on first run because the `cap` helper already trims and the footer is wired through. If it FAILS, fix the bug in Step 2.7 and re-run.

The whole point of writing this test AFTER the implementation is to verify edge-case behaviour (newest-first ordering, footer count). It is NOT another red-green cycle; treat it as a regression guard.

### Step 2.11: Cwd="" edge case

- [ ] **Step 2.11: Append failing test for empty-cwd graceful path**

```go
func TestBuild_EmptyCwd_GlobalsOnly(t *testing.T) {
	db := openTempDB(t)

	seed(t, db, "d-g1", "", "globally true: always confirm")
	seed(t, db, "d-other", "/some/project", "should not appear")

	got := Build(context.Background(), "", db)

	if !strings.Contains(got, "d-g1") {
		t.Errorf("global row missing under empty cwd; got:\n%s", got)
	}
	if strings.Contains(got, "# Project runbooks") {
		t.Errorf("project section should be omitted under empty cwd; got:\n%s", got)
	}
	if strings.Contains(got, "d-other") {
		t.Errorf("project-scoped row leaked under empty cwd; got:\n%s", got)
	}
}
```

- [ ] **Step 2.12: Run to verify pass**

Run: `go test ./internal/mcpserver/instructions/ -run TestBuild_EmptyCwd_GlobalsOnly -v`
Expected: PASS — `fetchProject` returns early on empty cwd, and `fetchGlobal` filters client-side.

### Step 2.13: Run all Task 2 tests + lint, then commit

- [ ] **Step 2.13: Run the full instructions test suite**

Run: `go test ./internal/mcpserver/instructions/ -v -race`
Expected: PASS — 15 subtests (11 from Task 1 + 4 new in Task 2).

- [ ] **Step 2.14: Run go vet on the new package**

Run: `go vet ./internal/mcpserver/instructions/`
Expected: no output (clean).

- [ ] **Step 2.15: Commit Task 2**

```bash
git add internal/mcpserver/instructions/build.go internal/mcpserver/instructions/build_test.go
git commit -m "$(cat <<'EOF'
mcp: add instructions.Build for MCP server Instructions string

Queries project + global runbooks via store.ListDecisions, caps at
20 per scope with "+N more" footer, returns empty string on any
failure or empty inventory. Replicates the globals-must-be-filtered-
client-side pattern from HandleRecallMemory so empty ProjectPath
yields globals-only instead of all-projects.

Wiring into mcpserver.New() lands in Task 3.

Spec: docs/superpowers/specs/2026-05-19-runbook-auto-recall-design.md
EOF
)"
```

---

## Task 3: Wire Instructions into `mcp.NewServer` + integration test

**Files:**
- Modify: `internal/mcpserver/server.go:27` (version bump)
- Modify: `internal/mcpserver/server.go:49-53` (Instructions wiring)
- Modify: `internal/mcpserver/tool_memory_crud_test.go` (end-to-end test)

This task makes the new package actually do something. The integration test runs the real `HandleRememberMemory` (which canonicalises cwd) and then `instructions.Build` against the same DB, asserting the new runbook surfaces.

### Step 3.1: End-to-end integration test (write FIRST)

- [ ] **Step 3.1: Append failing integration test**

Append to `internal/mcpserver/tool_memory_crud_test.go` (at the bottom of the file):

```go
// TestInstructions_PostRemember_ShowsRunbookTitle is the end-to-end
// wiring check: storing a runbook via HandleRememberMemory and then
// calling instructions.Build must surface the new runbook's first
// line. Protects three things at once: HandleRememberMemory's
// canonical-path resolution, store persistence, and the
// instructions package's read path.
func TestInstructions_PostRemember_ShowsRunbookTitle(t *testing.T) {
	withTempStore(t) // existing helper that points config.DBPath() at a tempdir

	cwd := t.TempDir()
	_, _, err := HandleRememberMemory(context.Background(), nil, RememberMemoryInput{
		Text: "for labstack changes we have four working dirs",
		CWD:  cwd,
	})
	if err != nil {
		t.Fatalf("HandleRememberMemory: %v", err)
	}

	db, err := store.Open(context.Background(), config.DBPath())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer db.Close()

	canonical := projectpath.Canonical(cwd)
	got := instructions.Build(context.Background(), canonical, db)

	if !strings.Contains(got, "for labstack changes we have four working dirs") {
		t.Errorf("instructions missing remembered runbook title; got:\n%s", got)
	}
}
```

Add these imports to the top of the file if not already present:

```go
import (
	// ... existing imports ...
	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/mcpserver/instructions"
	"github.com/klyne-ai/klyne/internal/projectpath"
	"github.com/klyne-ai/klyne/internal/store"
)
```

A helper named `withTempStore` does NOT exist in `internal/mcpserver/` (verified at plan time). Other test files in the codebase point `config.DBPath()` at a tempdir by swapping the package-level `config.HomeDir` var — see `internal/app/lifecycle_test.go:18-19` for the canonical pattern. Add this helper to `tool_memory_crud_test.go` (or wherever the existing test setup lives in that file):

```go
// withTempStore points config.DBPath() at a tempdir so tests don't
// touch the user's real ~/.klyne/klyne.db. Mirrors the pattern used
// across internal/app/*_test.go and internal/api/handlers/*_test.go.
func withTempStore(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	orig := config.HomeDir
	config.HomeDir = func() (string, error) { return tmp, nil }
	t.Cleanup(func() { config.HomeDir = orig })
}
```

Note: `config.HomeDir` is a package-level `var` declared in `internal/config/paths.go:14` precisely for this swap pattern. The cleanup restores it so other tests in the same package run aren't polluted.

- [ ] **Step 3.2: Run to verify it fails or passes**

Run: `go test ./internal/mcpserver/ -run TestInstructions_PostRemember -v`

Expected outcome depends on whether the package compiles:
- If FAIL with `undefined: instructions.Build` → impossible, Task 2 created it. Re-check imports.
- If PASS → great, the wiring already works end-to-end via the in-memory store.
- If FAIL with assertion failure → diagnose. Most likely cause: `HandleRememberMemory` canonicalised the path differently than your test expected. Print `canonical` and the project_paths in the DB to diagnose.

If it passes, move on. If it fails, fix in `build.go` (NOT in the test — the test encodes the contract the spec promised).

### Step 3.3: Bump version constant

- [ ] **Step 3.3: Bump `version` in `server.go`**

Edit `internal/mcpserver/server.go` line 27. Change:

```go
const version = "v0.7.0"
```

to:

```go
const version = "v0.7.1"
```

Also add a one-line entry to the version-history comment block at lines 13-26. After the last existing line, insert:

```go
// v0.7.1 — slice 9: MCP server Instructions now carries a directive
// + titled inventory of project + global runbooks (≤ 20 per scope)
// so the model auto-surfaces runbooks for every message without the
// user having to call recall manually. See
// docs/superpowers/specs/2026-05-19-runbook-auto-recall-design.md.
```

### Step 3.4: Wire Build into NewServer

- [ ] **Step 3.4: Edit `New()` in `server.go`**

Edit `internal/mcpserver/server.go`. The current code at lines 49-53 is:

```go
func New() *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "klyne",
		Version: version,
	}, nil)
```

Replace with:

```go
func New() *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "klyne",
		Version: version,
	}, &mcp.ServerOptions{
		Instructions: buildServerInstructions(),
	})
```

Then add the `buildServerInstructions` helper at the bottom of `server.go` (after `Run`):

```go
// buildServerInstructions assembles the MCP-level Instructions string
// surfaced in the initialize response. Best-effort: any failure
// (cwd lookup, DB open, query) collapses to "" so the server still
// starts and the instructions field is simply omitted from the
// handshake. See internal/mcpserver/instructions/build.go for the
// content rules.
//
// Called once per server boot. Subprocess-per-session means each
// klyne MCP session computes its own instructions — newly remembered
// runbooks land in the next session's instructions.
func buildServerInstructions() string {
	cwd, err := os.Getwd()
	if err != nil {
		// Fall through with empty cwd: instructions.Build will
		// return globals-only (still useful).
		cwd = ""
	}
	canonical := cwd
	if cwd != "" {
		canonical = projectpath.Canonical(cwd)
	}

	ctx := context.Background()
	db, err := store.Open(ctx, config.DBPath())
	if err != nil {
		return ""
	}
	defer db.Close() //nolint:errcheck

	return instructions.Build(ctx, canonical, db)
}
```

Add the required imports at the top of `server.go`:

```go
import (
	"context"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/mcpserver/instructions"
	"github.com/klyne-ai/klyne/internal/projectpath"
	"github.com/klyne-ai/klyne/internal/store"
)
```

Sanity check: `context` may already be imported (the existing `Run` uses `ctx context.Context`). If so, just add the other four.

- [ ] **Step 3.5: Build the binary**

Run: `go build ./...`
Expected: clean build, no errors. If there is a circular import, the most likely cause is `internal/mcpserver/instructions/` accidentally importing `internal/mcpserver/` — open the file and remove the import. The instructions package must only depend on `internal/store`.

- [ ] **Step 3.6: Run the full mcpserver test suite**

Run: `go test ./internal/mcpserver/... -v -race`
Expected: PASS — including the new integration test and all preexisting tests. If any preexisting test fails, the most likely cause is `withTempStore` was added with the wrong env var and now real-DB-touching tests in the package see a stale state. Diagnose by inspecting which test fails.

- [ ] **Step 3.7: Run go vet on the whole module**

Run: `go vet ./...`
Expected: no output.

### Step 3.8: Manual smoke test

- [ ] **Step 3.8: Verify the instructions field appears in MCP initialize**

The klyne MCP server speaks JSON-RPC over stdio. Smoke-test the handshake manually:

```bash
cd $(mktemp -d)
go install ./cmd/klyne  # from the klyne checkout
klyne mcp install       # if needed to install slashcommands; harmless if already installed
# Drive a minimal initialize round-trip:
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"smoke","version":"1"}}}' \
  | klyne mcp serve | head -c 4096
```

Expected: the response JSON contains an `"instructions"` field, OR (if you have no runbooks for the cwd) does NOT contain that field. Either is correct given the spec's empty-case rule. To get a non-empty case, first `cd` into your klyne checkout (which has runbooks per your existing usage) or seed one via `klyne remember "smoke test runbook"` and re-run.

If the field is missing when it should be present:
- Verify `go install` actually rebuilt the binary (`which klyne`, then check its mtime).
- Verify `config.DBPath()` resolves to the same DB the seed runbook was written to (run `klyne list-memories` or similar).
- Verify `mcp.ServerOptions{Instructions: ...}` is actually being passed (re-read `server.go`).

### Step 3.9: Commit Task 3

- [ ] **Step 3.9: Commit**

```bash
git add internal/mcpserver/server.go internal/mcpserver/tool_memory_crud_test.go
git commit -m "$(cat <<'EOF'
mcp: wire Instructions into NewServer for auto-recall of runbooks

mcpserver.New() now computes a per-session instructions string via
instructions.Build and passes it through ServerOptions.Instructions.
Both Claude Code and Codex hosts surface this in the initialize
response, so the model sees the directive + titled inventory on
every message without the user having to invoke recall manually.

Best-effort: any failure (getcwd, DB open, query) collapses to ""
so server startup is never blocked.

Version bumped to v0.7.1 since the wire-visible MCP handshake now
carries the instructions field.

Spec: docs/superpowers/specs/2026-05-19-runbook-auto-recall-design.md
EOF
)"
```

---

## Self-review (run before declaring the plan done)

Checked by the plan author. The implementing engineer does not need to redo this.

### Spec coverage

| Spec section | Implemented in |
|---|---|
| Problem / Goals (4 goals) | All four met: (1) directive + inventory visible per message, (2) covers operational + topical, (3) one mechanism for Claude + Codex via standard MCP, (4) empty-DB → empty string. |
| Decisions table — payload shape: directive + titled inventory | Task 1 (renderDirective, renderInventory), Task 2 (Build composes both). |
| Decisions table — trigger rule: operational OR topical | renderDirective text (Step 1.15) contains both rules verbatim. |
| Decisions table — inventory cap 20/scope + footer | inventoryLimit=20 + cap helper + renderFooter (Tasks 1 + 2). |
| Decisions table — mechanism: ServerOptions.Instructions | Task 3 Step 3.4. |
| Decisions table — empty case → "" | TestBuild_EmptyDB (Step 2.1) + integration smoke (Step 3.8). |
| Architecture diagram | Tasks 2 + 3 implement every box. |
| Components: render.go, build.go, server.go wiring | Tasks 1, 2, 3 respectively. |
| Instructions text wording | renderDirective + renderInventory format match the spec's example block exactly. |
| Error handling table (getcwd / DB / ListDecisions / empty) | buildServerInstructions handles getcwd + DB-open; fetchProject/fetchGlobal handle ListDecisions errors; Build short-circuits on empty. |
| Testing: 3 build cases + 4 render cases + 1 integration | 4 Build cases (added EmptyCwd), 4+ render cases (added Empty), 1 integration. Exceeds spec. |

No gaps.

### Placeholder scan

Searched for: TBD, TODO, "fill in", "similar to", "appropriate error handling". None present.

### Type consistency

- `Build(ctx, cwd, db) string` — used the same signature in Tasks 2 and 3.
- `inventoryLimit = 20` — used as `inventoryLimit + 1` in `fetchProject`/`fetchGlobal` and as cap threshold in `cap` helper. Consistent.
- `renderInventory(items, heading, hiddenCount)` — three args wherever invoked. Heading is a full string ("Project runbooks (path: ...)") not a prefix.
- `deriveTitle` (instructions package) intentionally diverges from `deriveMemoryName` (mcpserver package) by name only, with a comment explaining why (circular import). Bodies are identical.

### Scope check

Single feature, ~150 LOC across 4 files, 3 tasks, ~30 steps. Fits one plan cleanly.

---

## Execution

**Plan complete and saved to `docs/superpowers/plans/2026-05-19-runbook-auto-recall.md`. Two execution options:**

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

**Which approach?**

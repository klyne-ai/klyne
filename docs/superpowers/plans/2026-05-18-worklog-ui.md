# Worklog UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a new `/worklog` route in the klyne cockpit UI that browses `stop_summaries` rows project-scoped, showing both visible (`recap_visible=1`) and suppressed (`recap_visible=0`) entries by default so users can audit worklog behavior in one glance.

**Architecture:** End-to-end mirror of the existing `/memory` pipeline. Backend: new `RouteWorklog = "/worklog/items"` constant + `WorklogHandler.List` reading via a new `store.ListWorklogEntries` helper. Frontend: new `ui/src/routes/worklog/+page.svelte` cribbed from the memory page, new `fetchWorklog` in `api.ts`, new TypeScript response types, and a "Worklog" tab added to `TopNav.svelte`.

**Tech Stack:** Go 1.22, chi router, SQLite via `internal/store/*`. SvelteKit 2 (Svelte 5 runes mode), TypeScript, adapter-static.

**Spec:** `docs/superpowers/specs/2026-05-18-worklog-ui-design.md`

---

## File Structure

```
internal/api/contracts.go           # MODIFY — add RouteWorklog constant + 3 response types
internal/api/handlers/mounter.go    # MODIFY — register new handler
internal/api/handlers/worklog.go    # CREATE — WorklogHandler.List
internal/api/handlers/worklog_test.go  # CREATE — handler tests
internal/store/stop_summaries.go    # MODIFY — add ListWorklogEntries helper
internal/store/stop_summaries_test.go  # MODIFY — tests for new helper

ui/src/lib/types.ts                 # MODIFY — add WorklogEntry/Group/Response interfaces
ui/src/lib/api.ts                   # MODIFY — add fetchWorklog
ui/src/routes/worklog/+page.svelte  # CREATE — the new page
ui/src/lib/ui/TopNav.svelte         # MODIFY — add "Worklog" tab + route detection
```

---

### Task 1: Backend store — `ListWorklogEntries`

**Files:**
- Modify: `internal/store/stop_summaries.go`
- Modify: `internal/store/stop_summaries_test.go`

- [ ] **Step 1: Add the new struct + helper to `stop_summaries.go`**

Append at the bottom of `internal/store/stop_summaries.go`:

```go
// WorklogEntry is a stop_summaries row including the migration-015
// worklog columns. Returned by ListWorklogEntries for the /worklog
// UI — distinct from StopSummary which is the deterministic core
// the Stop hook writes.
type WorklogEntry struct {
	SessionID    string   `json:"session_id"`
	Ts           int64    `json:"ts"`
	ProjectPath  string   `json:"project_path"`
	CLI          string   `json:"cli"`
	Summary      string   `json:"summary"`
	LastUser     string   `json:"last_user"`
	LastBash     string   `json:"last_bash"`
	Files        []string `json:"files"`
	RecapVisible int      `json:"recap_visible"`
	RecapTopic   string   `json:"recap_topic"`
	Importance   int      `json:"importance"`
	Signature    string   `json:"signature"`
}

// ListWorklogEntriesOpts scopes ListWorklogEntries queries.
type ListWorklogEntriesOpts struct {
	// ProjectPath narrows to one project. Empty returns all projects.
	ProjectPath string
	// Limit caps the result set. Defaults to 500 when zero.
	Limit int
}

// ListWorklogEntries returns stop_summaries rows (with worklog metadata)
// ordered by ts DESC. Includes both visible and suppressed rows.
//
// Uses idx_stop_summaries_project_ts when ProjectPath != "", otherwise
// idx_stop_summaries_ts.
func ListWorklogEntries(ctx context.Context, db *DB, opts ListWorklogEntriesOpts) ([]WorklogEntry, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 500
	}
	q := `
SELECT session_id, ts, project_path, cli, summary, last_user, last_bash,
       files_json, recap_visible, COALESCE(recap_topic,''), importance,
       COALESCE(signature,'')
  FROM stop_summaries
 WHERE 1=1`
	args := make([]any, 0, 2)
	if opts.ProjectPath != "" {
		q += ` AND project_path = ?`
		args = append(args, opts.ProjectPath)
	}
	q += ` ORDER BY ts DESC LIMIT ?`
	args = append(args, limit)

	rows, err := db.Read().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list worklog entries: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	out := make([]WorklogEntry, 0)
	for rows.Next() {
		var e WorklogEntry
		var filesJSON string
		if err := rows.Scan(
			&e.SessionID, &e.Ts, &e.ProjectPath, &e.CLI, &e.Summary,
			&e.LastUser, &e.LastBash, &filesJSON,
			&e.RecapVisible, &e.RecapTopic, &e.Importance, &e.Signature,
		); err != nil {
			return nil, fmt.Errorf("store: scan worklog entry: %w", err)
		}
		if filesJSON != "" {
			if err := json.Unmarshal([]byte(filesJSON), &e.Files); err != nil {
				// Don't fail the whole query on one bad row — return empty files
				// list. This matches the defensive read pattern in StopSummary.
				e.Files = nil
			}
		}
		if e.Files == nil {
			e.Files = []string{}
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate worklog entries: %w", err)
	}
	return out, nil
}
```

- [ ] **Step 2: Add the test**

Append at the bottom of `internal/store/stop_summaries_test.go`:

```go
func TestListWorklogEntries_ReturnsBothVisibleAndSuppressed(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UnixMilli()

	rows := []struct {
		row store.StopSummary
		w   store.WorklogColumns
	}{
		// Visible meaningful session in project A (most recent).
		{
			row: store.StopSummary{
				SessionID: "s-a-visible", Ts: now, ProjectPath: "/proj/a",
				CLI: "claude", Summary: "## did stuff", LastUser: "create hello.js",
				Files: []string{"hello.js"},
			},
			w: store.WorklogColumns{RecapVisible: 1, Importance: 7, RecapTopic: "feature", DraftState: "accepted"},
		},
		// Suppressed trivial session in project A (older).
		{
			row: store.StopSummary{
				SessionID: "s-a-suppressed", Ts: now - 5_000, ProjectPath: "/proj/a",
				CLI: "claude", Summary: "## what is 2+2", LastUser: "what is 2+2",
			},
			w: store.WorklogColumns{RecapVisible: 0, Importance: 2, DraftState: "proposed"},
		},
		// Visible session in a different project.
		{
			row: store.StopSummary{
				SessionID: "s-b-visible", Ts: now - 2_000, ProjectPath: "/proj/b",
				CLI: "codex", Summary: "## codex did stuff", LastUser: "fix bug",
				Files: []string{"main.go"},
			},
			w: store.WorklogColumns{RecapVisible: 1, Importance: 5, DraftState: "accepted"},
		},
	}
	for _, r := range rows {
		if err := store.UpsertStopSummaryWithWorklog(ctx, db, r.row, r.w); err != nil {
			t.Fatalf("seed %s: %v", r.row.SessionID, err)
		}
	}

	t.Run("all projects, newest first", func(t *testing.T) {
		got, err := store.ListWorklogEntries(ctx, db, store.ListWorklogEntriesOpts{})
		if err != nil {
			t.Fatalf("list all: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("len=%d, want 3", len(got))
		}
		if got[0].SessionID != "s-a-visible" {
			t.Errorf("got[0]=%s, want s-a-visible (newest)", got[0].SessionID)
		}
		if got[2].SessionID != "s-a-suppressed" {
			t.Errorf("got[2]=%s, want s-a-suppressed (oldest)", got[2].SessionID)
		}
	})

	t.Run("includes suppressed rows", func(t *testing.T) {
		got, err := store.ListWorklogEntries(ctx, db, store.ListWorklogEntriesOpts{})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		var anySuppressed bool
		for _, e := range got {
			if e.RecapVisible == 0 {
				anySuppressed = true
				break
			}
		}
		if !anySuppressed {
			t.Error("expected at least one suppressed row in result")
		}
	})

	t.Run("scoped by project_path", func(t *testing.T) {
		got, err := store.ListWorklogEntries(ctx, db, store.ListWorklogEntriesOpts{ProjectPath: "/proj/a"})
		if err != nil {
			t.Fatalf("list /proj/a: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("len=%d, want 2 for /proj/a", len(got))
		}
		for _, e := range got {
			if e.ProjectPath != "/proj/a" {
				t.Errorf("project_path=%s, want /proj/a", e.ProjectPath)
			}
		}
	})

	t.Run("populates files from files_json", func(t *testing.T) {
		got, err := store.ListWorklogEntries(ctx, db, store.ListWorklogEntriesOpts{ProjectPath: "/proj/a"})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		// Newest first = s-a-visible which had Files=["hello.js"].
		if len(got) == 0 || len(got[0].Files) != 1 || got[0].Files[0] != "hello.js" {
			t.Errorf("got[0].Files=%v, want [hello.js]", got[0].Files)
		}
	})
}
```

- [ ] **Step 3: Run test, expect FAIL (function not defined)**

```
go test ./internal/store/ -run TestListWorklogEntries -v
```
Expected: compile error for missing `ListWorklogEntries`.

- [ ] **Step 4: Re-run after Step 1 in place**

```
go test ./internal/store/ -run TestListWorklogEntries -v
```
Expected: PASS all four subtests.

- [ ] **Step 5: Commit**

```bash
git add internal/store/stop_summaries.go internal/store/stop_summaries_test.go
git commit -m "feat(store): ListWorklogEntries — read stop_summaries with worklog cols"
```

---

### Task 2: Backend types + route constant

**Files:**
- Modify: `internal/api/contracts.go`
- Modify: `internal/api/contracts_test.go`

- [ ] **Step 1: Add route constant**

In `internal/api/contracts.go`, in the route-constant block, add after `RouteMemoryItem`:

```go
	// Worklog: SPA page lives at `/worklog`, so the API uses a
	// /worklog/items subpath to avoid colliding with the SPA route
	// (same pattern as /memory/items).
	RouteWorklog              = "/worklog/items"
```

In the same file, add `RouteWorklog` to the `AllRoutes()` slice (alphabetically after `RouteUsageStats` is fine, or place it next to `RouteMemoryItem` for grouping — match the existing style).

- [ ] **Step 2: Add response types**

In `internal/api/contracts.go`, append at the bottom (after the /memory block):

```go
// ---------------------------------------------------------------------------
// /worklog  — project-scoped browser over the stop_summaries table
// ---------------------------------------------------------------------------

// WorklogProjectGroup is one bucket in WorklogResponse.ByProject —
// every worklog entry under the same project_path, grouped for the
// dashboard's per-service view.
type WorklogProjectGroup struct {
	ProjectPath string                `json:"project_path"`
	Name        string                `json:"name"`
	Entries     []store.WorklogEntry  `json:"entries"`
	Count       int                   `json:"count"`
}

// WorklogResponse is GET /worklog/items. Splits worklog rows into one
// "global" list (project_path == "") and one group per project, newest
// first within each. Both visible (recap_visible=1) and suppressed
// (recap_visible=0) rows are returned so users can audit suppression.
type WorklogResponse struct {
	Global       []store.WorklogEntry  `json:"global"`
	ByProject    []WorklogProjectGroup `json:"by_project"`
	GlobalCount  int                   `json:"global_count"`
	ProjectCount int                   `json:"project_count"`
	Total        int                   `json:"total"`
}
```

- [ ] **Step 3: Update contracts_test.go**

In `internal/api/contracts_test.go`, find the slice of routes used by `TestAllRoutes_Unique` (or similar). Add `RouteWorklog` to the expected list. The test exists to keep `AllRoutes()` in sync — adding the constant alone isn't enough.

- [ ] **Step 4: Run contracts tests**

```
go test ./internal/api/ -run TestAllRoutes -v
go build ./...
```
Expected: PASS + clean build.

- [ ] **Step 5: Commit**

```bash
git add internal/api/contracts.go internal/api/contracts_test.go
git commit -m "feat(api): RouteWorklog + WorklogResponse types"
```

---

### Task 3: Backend handler + tests + registration

**Files:**
- Create: `internal/api/handlers/worklog.go`
- Create: `internal/api/handlers/worklog_test.go`
- Modify: `internal/api/handlers/mounter.go`

- [ ] **Step 1: Write the handler**

Create `internal/api/handlers/worklog.go`:

```go
package handlers

import (
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/store"
)

// WorklogHandler serves /worklog/items — the dashboard's per-project
// browser over the stop_summaries table.
type WorklogHandler struct {
	db *store.DB
}

// NewWorklogHandler constructs a WorklogHandler.
func NewWorklogHandler(db *store.DB) *WorklogHandler {
	return &WorklogHandler{db: db}
}

// List handles GET /worklog/items.
//
// Returns one "global" list plus one bucket per project that has
// recorded worklog rows. Both visible (recap_visible=1) and suppressed
// (recap_visible=0) rows are included so users can audit suppression
// behavior in the UI.
//
// Optional query params:
//   - project: scope to one project_path (absolute). Omit for all.
func (h *WorklogHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	project := strings.TrimSpace(r.URL.Query().Get("project"))

	rows, err := store.ListWorklogEntries(ctx, h.db, store.ListWorklogEntriesOpts{
		ProjectPath: project,
		Limit:       500,
	})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	resp := groupWorklogByProject(rows)
	writeJSON(w, http.StatusOK, resp)
}

// groupWorklogByProject splits entries into one "global" list
// (project_path == "") and one bucket per distinct project_path.
// Buckets are sorted by the most-recent entry inside them.
func groupWorklogByProject(rows []store.WorklogEntry) api.WorklogResponse {
	global := make([]store.WorklogEntry, 0)
	byPath := map[string][]store.WorklogEntry{}

	for _, e := range rows {
		if e.ProjectPath == "" {
			global = append(global, e)
			continue
		}
		byPath[e.ProjectPath] = append(byPath[e.ProjectPath], e)
	}

	groups := make([]api.WorklogProjectGroup, 0, len(byPath))
	for path, entries := range byPath {
		// Defensive newest-first sort; the store query already orders
		// by ts DESC but we keep the loop independent of that.
		sort.SliceStable(entries, func(i, j int) bool { return entries[i].Ts > entries[j].Ts })
		groups = append(groups, api.WorklogProjectGroup{
			ProjectPath: path,
			Name:        filepath.Base(path),
			Entries:     entries,
			Count:       len(entries),
		})
	}
	// Sort groups by most-recent entry inside them (DESC).
	sort.SliceStable(groups, func(i, j int) bool {
		ai := mostRecentWorklogTs(groups[i].Entries)
		aj := mostRecentWorklogTs(groups[j].Entries)
		if ai != aj {
			return ai > aj
		}
		return groups[i].Name < groups[j].Name
	})

	sort.SliceStable(global, func(i, j int) bool { return global[i].Ts > global[j].Ts })

	return api.WorklogResponse{
		Global:       global,
		ByProject:    groups,
		GlobalCount:  len(global),
		ProjectCount: len(groups),
		Total:        len(rows),
	}
}

func mostRecentWorklogTs(entries []store.WorklogEntry) int64 {
	if len(entries) == 0 {
		return 0
	}
	return entries[0].Ts
}
```

- [ ] **Step 2: Write the handler test**

Create `internal/api/handlers/worklog_test.go`:

```go
package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/api/handlers"
	"github.com/klyne-ai/klyne/internal/store"
)

func newWorklogRouter(t *testing.T, db *store.DB) http.Handler {
	t.Helper()
	r := chi.NewRouter()
	h := handlers.NewWorklogHandler(db)
	r.Get(api.RouteWorklog, h.List)
	return r
}

func seedWorklogRows(t *testing.T, db *store.DB) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UnixMilli()

	rows := []struct {
		row store.StopSummary
		w   store.WorklogColumns
	}{
		{
			row: store.StopSummary{SessionID: "wl-a-visible", Ts: now, ProjectPath: "/proj/a", CLI: "claude", Summary: "## did stuff", LastUser: "create hello.js", Files: []string{"hello.js"}},
			w:   store.WorklogColumns{RecapVisible: 1, Importance: 7, RecapTopic: "feature", DraftState: "accepted"},
		},
		{
			row: store.StopSummary{SessionID: "wl-a-suppressed", Ts: now - 5_000, ProjectPath: "/proj/a", CLI: "claude", Summary: "## trivia", LastUser: "what is 2+2"},
			w:   store.WorklogColumns{RecapVisible: 0, Importance: 2, DraftState: "proposed"},
		},
		{
			row: store.StopSummary{SessionID: "wl-b-visible", Ts: now - 2_000, ProjectPath: "/proj/b", CLI: "codex", Summary: "## fix", LastUser: "fix bug"},
			w:   store.WorklogColumns{RecapVisible: 1, Importance: 5, DraftState: "accepted"},
		},
	}
	for _, r := range rows {
		if err := store.UpsertStopSummaryWithWorklog(ctx, db, r.row, r.w); err != nil {
			t.Fatalf("seed %s: %v", r.row.SessionID, err)
		}
	}
}

func TestWorklog_List_GroupsByProject(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	seedWorklogRows(t, db)

	srv := httptest.NewServer(newWorklogRouter(t, db))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/worklog/items")
	if err != nil {
		t.Fatalf("GET /worklog/items: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body api.WorklogResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if body.GlobalCount != 0 {
		t.Errorf("GlobalCount = %d, want 0 (no project_path='' rows seeded)", body.GlobalCount)
	}
	if body.ProjectCount != 2 {
		t.Errorf("ProjectCount = %d, want 2 (/proj/a + /proj/b)", body.ProjectCount)
	}
	if body.Total != 3 {
		t.Errorf("Total = %d, want 3", body.Total)
	}
	// /proj/a should sort first (has the newest entry).
	if len(body.ByProject) == 0 || body.ByProject[0].ProjectPath != "/proj/a" {
		t.Errorf("ByProject[0]=%v, want /proj/a first", body.ByProject)
	}
}

func TestWorklog_List_IncludesSuppressedRows(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	seedWorklogRows(t, db)

	srv := httptest.NewServer(newWorklogRouter(t, db))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/worklog/items")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	var body api.WorklogResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	var anySuppressed bool
	for _, g := range body.ByProject {
		for _, e := range g.Entries {
			if e.RecapVisible == 0 {
				anySuppressed = true
			}
		}
	}
	if !anySuppressed {
		t.Error("expected suppressed rows in response; got none")
	}
}

func TestWorklog_List_ScopedByProject(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	seedWorklogRows(t, db)

	srv := httptest.NewServer(newWorklogRouter(t, db))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/worklog/items?project=/proj/a")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	var body api.WorklogResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if body.ProjectCount != 1 {
		t.Errorf("ProjectCount = %d, want 1 (only /proj/a)", body.ProjectCount)
	}
	if body.Total != 2 {
		t.Errorf("Total = %d, want 2 (/proj/a has 2 rows)", body.Total)
	}
}
```

- [ ] **Step 3: Wire the handler into the mounter**

In `internal/api/handlers/mounter.go`, after the `/memory` block (around line 119), add:

```go
	// /worklog — project-scoped browser over the stop_summaries table.
	// Read-only; both visible and suppressed rows returned so users can
	// audit suppression behavior.
	hWorklog := NewWorklogHandler(m.deps.DB)
	r.Get(api.RouteWorklog, hWorklog.List)
```

- [ ] **Step 4: Run the new tests + a smoke build**

```
go test ./internal/api/handlers/ -run TestWorklog -v
go build ./...
```
Expected: all three subtests PASS; clean build.

- [ ] **Step 5: Commit**

```bash
git add internal/api/handlers/worklog.go internal/api/handlers/worklog_test.go internal/api/handlers/mounter.go
git commit -m "feat(api): /worklog/items handler — project-grouped stop_summaries"
```

---

### Task 4: Frontend types

**Files:**
- Modify: `ui/src/lib/types.ts`

- [ ] **Step 1: Add the interfaces**

Append to `ui/src/lib/types.ts`, right after the `MemoryResponse` interface block:

```typescript
// --- /worklog/items ---

/** WorklogEntry mirrors store.WorklogEntry — one stop_summaries row with worklog metadata. */
export interface WorklogEntry {
  session_id: string;
  ts: number;
  project_path: string;
  cli: string;
  summary: string;
  last_user: string;
  last_bash: string;
  files: string[];
  recap_visible: number;       // 0 = suppressed, 1 = visible
  recap_topic: string;
  importance: number;          // 1..10
  signature: string;
}

/** WorklogProjectGroup is one bucket of entries under the same project. */
export interface WorklogProjectGroup {
  project_path: string;
  name: string;
  entries: WorklogEntry[];
  count: number;
}

/** WorklogResponse is GET /worklog/items. */
export interface WorklogResponse {
  global: WorklogEntry[];
  by_project: WorklogProjectGroup[];
  global_count: number;
  project_count: number;
  total: number;
}
```

- [ ] **Step 2: Type-check**

```
cd ui && npx tsc --noEmit
```
Expected: clean (or only pre-existing errors unrelated to our additions).

- [ ] **Step 3: Commit**

```bash
git add ui/src/lib/types.ts
git commit -m "feat(ui): WorklogResponse / WorklogProjectGroup / WorklogEntry types"
```

---

### Task 5: Frontend api wrapper

**Files:**
- Modify: `ui/src/lib/api.ts`

- [ ] **Step 1: Import the new types**

In the named-import block at the top of `ui/src/lib/api.ts` (currently importing `MemoryResponse` and others), add `WorklogResponse`:

```typescript
import type {
  ...,
  MemoryResponse,
  WorklogResponse,  // NEW — keep alphabetical with siblings
  ...
} from './types.js';
```

- [ ] **Step 2: Add fetch wrapper**

After the `/memory/items` section (the `fetchMemory` / `deleteMemory` block), add:

```typescript
// ---------------------------------------------------------------------------
// /worklog/items
// ---------------------------------------------------------------------------

/**
 * GET /worklog/items — every worklog entry grouped by global vs project.
 * Returns both visible (recap_visible=1) and suppressed (recap_visible=0).
 *
 * @param project Optional absolute project path. Omit for all projects.
 */
export async function fetchWorklog(project?: string): Promise<WorklogResponse> {
  const qs = project ? `?project=${encodeURIComponent(project)}` : '';
  return get<WorklogResponse>(`/worklog/items${qs}`);
}
```

- [ ] **Step 3: Type-check**

```
cd ui && npx tsc --noEmit
```
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add ui/src/lib/api.ts
git commit -m "feat(ui): fetchWorklog wrapper over GET /worklog/items"
```

---

### Task 6: Frontend page — `/worklog`

**Files:**
- Create: `ui/src/routes/worklog/+page.svelte`

- [ ] **Step 1: Write the page**

Create `ui/src/routes/worklog/+page.svelte`:

```svelte
<!--
  Worklog view — project-scoped browser over the /worklog/items endpoint.
  Card grid showing every stop_summaries row including suppressed (recap_visible=0)
  ones, so users can audit klyne's worklog signal-vs-noise behavior in one glance.

  v1 scope: project filter dropdown only. No tag chips, group-by, search, or delete.
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/stores';
  import { fetchWorklog } from '$lib/api.js';
  import type { WorklogEntry, WorklogProjectGroup, WorklogResponse } from '$lib/types.js';
  import { relTime } from '$lib/format.js';

  let resp = $state<WorklogResponse | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let selectedProject = $state<string>(''); // '' = all
  let expanded = $state<Record<string, boolean>>({});

  async function load(): Promise<void> {
    loading = true;
    try {
      resp = await fetchWorklog(selectedProject || undefined);
      error = null;
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'failed to load worklog';
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    selectedProject = $page.url.searchParams.get('project') ?? '';
    void load();
  });

  function onSelectProject(next: string): void {
    selectedProject = next;
    const url = new URL(window.location.href);
    if (next) url.searchParams.set('project', next);
    else url.searchParams.delete('project');
    window.history.replaceState({}, '', url.toString());
    void load();
  }

  function entryKey(e: WorklogEntry): string {
    return `${e.session_id}:${e.ts}`;
  }

  function toggle(e: WorklogEntry): void {
    const k = entryKey(e);
    expanded[k] = !expanded[k];
  }

  function title(e: WorklogEntry): string {
    const t = (e.last_user || e.summary || '(no prompt)').trim();
    return t.length > 120 ? t.slice(0, 117) + '…' : t;
  }

  // Flatten by_project + global into a single list of [groupName, entries] pairs
  // so the template stays simple. Global comes first if present.
  function buckets(r: WorklogResponse): WorklogProjectGroup[] {
    const out: WorklogProjectGroup[] = [];
    if (r.global.length > 0) {
      out.push({ project_path: '', name: 'Global', entries: r.global, count: r.global.length });
    }
    for (const g of r.by_project) out.push(g);
    return out;
  }

  // Project options for the dropdown — current response groups + an "All" choice.
  function projectOptions(r: WorklogResponse): { value: string; label: string }[] {
    return [
      { value: '', label: 'All projects' },
      ...r.by_project.map((g) => ({ value: g.project_path, label: g.name })),
    ];
  }
</script>

<div class="worklog-page">
  <header class="worklog-header">
    <h1>Worklog</h1>
    <p class="muted">
      Everything klyne's Stop hook captured. Suppressed rows are shown so you can
      see what signal vs noise the suppression rules filter.
    </p>
  </header>

  {#if loading && !resp}
    <p class="muted">Loading…</p>
  {:else if error}
    <p class="error">⚠ {error}</p>
    <p class="muted">Is the klyne daemon running?</p>
  {:else if resp}
    <div class="filter-bar">
      <label>
        Project:
        <select value={selectedProject} onchange={(e) => onSelectProject((e.currentTarget as HTMLSelectElement).value)}>
          {#each projectOptions(resp) as opt}
            <option value={opt.value}>{opt.label}</option>
          {/each}
        </select>
      </label>
      <span class="muted">{resp.total} {resp.total === 1 ? 'entry' : 'entries'}</span>
    </div>

    {#if resp.total === 0}
      <p class="muted empty">No worklog entries yet for this scope.</p>
    {:else}
      {#each buckets(resp) as group (group.project_path || '__global__')}
        <section class="group">
          <h2>{group.name} <span class="count">({group.count})</span></h2>
          <div class="grid">
            {#each group.entries as entry (entryKey(entry))}
              <article class="card" class:suppressed={entry.recap_visible === 0}>
                <header class="card-head">
                  <span class="badge cli">[{entry.cli}]</span>
                  <span class="muted">{relTime(entry.ts)}</span>
                  <span class="dot">·</span>
                  <span class="muted">imp {entry.importance}</span>
                  {#if entry.recap_visible === 1}
                    <span class="pill visible">★ visible</span>
                  {:else}
                    <span class="pill suppressed">✗ suppressed</span>
                  {/if}
                </header>
                <p class="title">{title(entry)}</p>
                <button class="expand" onclick={() => toggle(entry)} aria-expanded={!!expanded[entryKey(entry)]}>
                  {expanded[entryKey(entry)] ? '▾ Hide' : '▸ Expand'}
                </button>
                {#if expanded[entryKey(entry)]}
                  <div class="card-detail">
                    {#if entry.last_bash}
                      <p class="muted small">Last bash:</p>
                      <pre>{entry.last_bash}</pre>
                    {/if}
                    {#if entry.files.length > 0}
                      <p class="muted small">Files touched:</p>
                      <ul class="files">
                        {#each entry.files as f}<li><code>{f}</code></li>{/each}
                      </ul>
                    {/if}
                    <p class="muted small">Summary:</p>
                    <pre class="summary">{entry.summary}</pre>
                    <p class="muted small footer">
                      <code>{entry.session_id}</code>
                      {#if entry.signature}· sig <code>{entry.signature}</code>{/if}
                    </p>
                  </div>
                {/if}
              </article>
            {/each}
          </div>
        </section>
      {/each}
    {/if}
  {/if}
</div>

<style>
  .worklog-page { padding: 1.5rem; max-width: 1400px; margin: 0 auto; }
  .worklog-header h1 { margin: 0 0 0.25rem; }
  .muted { color: var(--text-muted, #888); }
  .small { font-size: 0.85em; }
  .error { color: var(--text-error, #c33); }
  .filter-bar {
    display: flex; align-items: center; gap: 1rem;
    padding: 0.75rem 0; border-bottom: 1px solid var(--border, #2a2a2a); margin-bottom: 1rem;
  }
  .filter-bar select {
    padding: 0.25rem 0.5rem; background: var(--surface, #1a1a1a);
    color: var(--text, #eee); border: 1px solid var(--border, #2a2a2a);
    border-radius: 4px;
  }
  .group { margin-bottom: 2rem; }
  .group h2 { margin: 0 0 0.75rem; font-size: 1.1rem; }
  .count { color: var(--text-muted, #888); font-weight: normal; font-size: 0.9em; }
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
    gap: 1rem;
  }
  .card {
    background: var(--surface, #1a1a1a);
    border: 1px solid var(--border, #2a2a2a);
    border-radius: 6px;
    padding: 0.75rem;
  }
  .card.suppressed { opacity: 0.65; }
  .card-head {
    display: flex; gap: 0.5rem; align-items: center; flex-wrap: wrap;
    font-size: 0.85em; margin-bottom: 0.5rem;
  }
  .badge.cli {
    padding: 0.1rem 0.4rem; border-radius: 3px;
    background: var(--surface-2, #222); font-family: monospace;
  }
  .dot { color: var(--text-muted, #666); }
  .pill {
    padding: 0.1rem 0.4rem; border-radius: 999px; font-size: 0.75em;
  }
  .pill.visible { background: #1e3a1e; color: #8ec98e; }
  .pill.suppressed { background: #3a1e1e; color: #c98e8e; }
  .title {
    margin: 0.25rem 0;
    font-weight: 500;
    line-height: 1.4;
  }
  .expand {
    background: none; border: none; color: var(--text-muted, #888);
    padding: 0.25rem 0; cursor: pointer; font-size: 0.85em;
  }
  .expand:hover { color: var(--text, #eee); }
  .card-detail {
    border-top: 1px solid var(--border, #2a2a2a);
    margin-top: 0.5rem; padding-top: 0.5rem;
  }
  pre {
    white-space: pre-wrap; word-break: break-word;
    background: var(--surface-2, #0d0d0d);
    padding: 0.5rem; border-radius: 4px; font-size: 0.85em;
    margin: 0.25rem 0 0.5rem;
  }
  .summary { max-height: 300px; overflow: auto; }
  .files { margin: 0.25rem 0 0.5rem 1rem; padding: 0; }
  .files li { list-style: disc; }
  .footer { margin-top: 0.5rem; }
  .empty { text-align: center; padding: 2rem; }
</style>
```

- [ ] **Step 2: Type-check + sanity build**

```
cd ui && npx tsc --noEmit
cd ui && npm run build
```
Expected: clean tsc; build succeeds (will generate `build/` output).

- [ ] **Step 3: Commit**

```bash
git add ui/src/routes/worklog/+page.svelte
git commit -m "feat(ui): /worklog page — project-grouped card grid"
```

---

### Task 7: Nav link in TopNav

**Files:**
- Modify: `ui/src/lib/ui/TopNav.svelte`

- [ ] **Step 1: Add the tab + route detection**

In `ui/src/lib/ui/TopNav.svelte`, change the `tabs` array to add Worklog between Memory and Insights:

```typescript
  const tabs = [
    { id: 'work',     label: 'Work',     path: '/' },
    { id: 'memory',   label: 'Memory',   path: '/memory' },
    { id: 'worklog',  label: 'Worklog',  path: '/worklog' },
    { id: 'insights', label: 'Insights', path: '/insights' }
  ] as const;
```

And in `currentRoute()`, add a clause so `/worklog` lights up the right tab:

```typescript
  function currentRoute(): string {
    const p = $page.url.pathname;
    if (p === '/' || p.startsWith('/work') || p.startsWith('/cockpit') || p.startsWith('/projects') || p.startsWith('/sessions')) return 'work';
    if (p.startsWith('/memory')) return 'memory';
    if (p.startsWith('/worklog')) return 'worklog';
    if (p.startsWith('/insights') || p.startsWith('/stats')) return 'insights';
    return '';
  }
```

- [ ] **Step 2: Update the comment at the top of TopNav so future readers know the 4th tab is intentional**

Replace the leading comment:

```svelte
<!--
  TopNav — 4-tab shell.

  Tabs:
    Work     (replaces Dashboard + Cockpit + Projects)
    Memory   (decisions / runbooks review)
    Worklog  (stop_summaries audit — signal vs suppressed)
    Insights (project-centric subscription-aware metrics)

  Search lives behind the `/` overlay, not as a dedicated route.
-->
```

- [ ] **Step 3: Sanity build**

```
cd ui && npm run build
```
Expected: build succeeds.

- [ ] **Step 4: Commit**

```bash
git add ui/src/lib/ui/TopNav.svelte
git commit -m "feat(ui): add Worklog tab to TopNav"
```

---

### Task 8: Manual smoke verification

**Files:** none — runtime check.

- [ ] **Step 1: Build klyne binary**

```
make build
# or: go build -o bin/klyne ./cmd/klyne
```
Expected: `bin/klyne` exists.

- [ ] **Step 2: Build UI**

```
cd ui && npm run build
```
Expected: `ui/build/` populated.

- [ ] **Step 3: Start the daemon (background)**

```
./bin/klyne start &
```
Expected: daemon listening (default port — check `klyne config get` or daemon logs for the port).

- [ ] **Step 4: Hit the API directly**

```
curl -s http://localhost:<PORT>/worklog/items | jq '.total, .project_count'
```
Expected: numeric response, no error. (Port comes from `klyne config get` or the daemon log line "listening on ...").

- [ ] **Step 5: Open the UI in a browser**

Navigate to `http://localhost:<PORT>/worklog`. Expected:
- Page renders, "Worklog" tab is highlighted in the top nav.
- Cards appear, grouped by project, newest first.
- Both ★ visible and ✗ suppressed pills are present somewhere (assuming the DB has both kinds).
- Project dropdown filters the view.
- Expand on a card reveals summary, files, bash, session_id.

- [ ] **Step 6: Stop the daemon**

```
./bin/klyne stop
```

- [ ] **Step 7: No commit needed for smoke step.**

---

## Self-Review Notes

**Spec coverage:**
- ✓ Backend route + handler — Tasks 2, 3
- ✓ `stop_summaries` reader — Task 1
- ✓ Frontend types + fetch + page + nav — Tasks 4, 5, 6, 7
- ✓ Show both visible + suppressed by default — handler returns all rows; UI shows both with distinct pills
- ✓ Project filter dropdown — implemented in page (Task 6)
- ✓ 500 row cap — hardcoded in handler (Task 3)
- ✓ Tests at store + handler layers — Tasks 1, 3
- ✓ Spec's out-of-scope items genuinely deferred

**Placeholder scan:** none — every code block is concrete.

**Type consistency:** `WorklogEntry` defined once in Go store, once in TS types, fields match snake_case 1:1. `WorklogResponse` shape identical in both languages.

**One risk:** the spec mentioned `signature` in WorklogEntry but `signature` may be NULL in the DB. The store helper wraps with `COALESCE(signature,'')` so empty string is returned. Handler + UI handle empty string gracefully (UI only renders the `sig` line if non-empty).

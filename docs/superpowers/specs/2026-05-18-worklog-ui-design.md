# Worklog UI — project-scoped browser

**Status:** Approved (brainstorming complete, ready for implementation plan)
**Date:** 2026-05-18
**Branch:** `worktree-worklog-ui`

## Why

Today there is no way for a user to browse what klyne's worklog has actually captured. They have to trust that the Stop-hook pipeline is firing, the suppression rules are doing the right thing, and the visible/suppressed split matches their intent. The first question we want a klyne user to be able to answer in one glance is *"is the worklog really working, and what did it keep for this project?"*

A small UI page modeled after the existing `/memory` route closes that gap with minimum new surface area.

## Goal (v1)

A new `/worklog` page in the cockpit UI that lists every `stop_summaries` row in the local SQLite store, project-scoped, with both `recap_visible=1` (kept) and `recap_visible=0` (suppressed) rows visible by default so the user can audit suppression behavior directly.

Out of scope for v1: tag chips, group-by controls, reflection synthesis panel, search box, delete affordance, "visible only" toggle. Listed in §Future so they aren't forgotten.

## Architecture

End-to-end mirror of the existing `/memory` pipeline. No new packages, no new patterns.

| Layer | Existing (`/memory`) | New (`/worklog`) |
|---|---|---|
| Route constant | `RouteMemory = "/memory/items"` in `internal/api/contracts.go` | `RouteWorklog = "/worklog/items"` |
| Handler | `internal/api/handlers/memory.go` (`List`) | `internal/api/handlers/worklog.go` (`List`) |
| Data source | `decisions` table via `store.ListDecisions(...)` | `stop_summaries` table via a new `store.ListWorklogEntries(...)` |
| Response type | `MemoryResponse` in `ui/src/lib/types.ts` | `WorklogResponse` in `ui/src/lib/types.ts` |
| Client fetch | `fetchMemory()` in `ui/src/lib/api.ts` | `fetchWorklog()` |
| Page | `ui/src/routes/memory/+page.svelte` | `ui/src/routes/worklog/+page.svelte` |
| Nav link | existing sidebar entry "Memory" | new sidebar entry "Worklog" beside it |

## Data shape

### Server response (`/worklog/items` GET)

```json
{
  "items": [WorklogEntry],
  "total": 0,
  "projects": ["/abs/path1", "/abs/path2"]
}
```

Field `projects` is the distinct sorted list of `project_path` values present in the result set — used by the UI's project filter dropdown without an extra round-trip.

### `WorklogEntry`

| Field | Type | Source |
|---|---|---|
| `session_id` | string | `stop_summaries.session_id` |
| `project_path` | string | `stop_summaries.project_path` |
| `project_name` | string | basename of `project_path`, derived server-side |
| `cli` | `"claude"` \| `"codex"` | `stop_summaries.cli` |
| `ts` | int64 (epoch ms) | `stop_summaries.ts` |
| `recap_visible` | 0 \| 1 | `stop_summaries.recap_visible` |
| `recap_topic` | string | `stop_summaries.recap_topic` |
| `importance` | int (1–10) | `stop_summaries.importance` |
| `signature` | string | `stop_summaries.signature` |
| `files` | string[] | parsed JSON from `stop_summaries.files_json` |
| `summary` | string | `stop_summaries.summary` (markdown) |
| `last_user` | string | `stop_summaries.last_user` |
| `last_bash` | string | `stop_summaries.last_bash` |

Server-side cap: **500 rows**, newest first, no pagination in v1.

### Query params (v1)

- `project=<abs-path>` — filter to one project. Omit for all.

## UI

Card grid, three columns at desktop width, one at mobile. Order: newest first.

### Filter bar (top of page)

- Project dropdown — populated from `WorklogResponse.projects`. First option `"All projects"`.
- That's it for v1. No tag chips, no scope toggle, no search.

### Card layout

```
┌──────────────────────────────────────────────────┐
│ [claude]  2 min ago  ·  oms-service  ·  imp 5    │
│ ★ visible   (or)   ✗ suppressed                  │
├──────────────────────────────────────────────────┤
│ "Refactor the createOrder handler to validate…"  │
│                                                  │
│ ▸ Expand                                         │
└──────────────────────────────────────────────────┘
```

On expand the card shows:

- Full `summary` markdown (rendered)
- `last_bash` if non-empty (in `<code>` block)
- File list (`files`)
- `signature` and `session_id` in a small muted footer

### Empty states

- No rows for this project: *"No worklog entries yet. Run a session in this project and come back."*
- API error: *"Couldn't reach klyne daemon at `<URL>`. Is it running?"* (same wording as `/memory`)

## Backend implementation notes

`store.ListWorklogEntries(ctx, opts)` opts:

```go
type ListWorklogOpts struct {
    ProjectPath string // "" = all
    Limit       int    // default 500
}
```

Returns `[]WorklogEntry` ordered by `ts DESC`. Uses the existing `idx_stop_summaries_project_ts` index when `ProjectPath != ""`; otherwise `idx_stop_summaries_ts`.

The handler `List`:
1. Parse `project` query param.
2. Call `store.ListWorklogEntries`.
3. Compute distinct projects for the dropdown (single `SELECT DISTINCT project_path FROM stop_summaries ORDER BY project_path`).
4. Render JSON.

## Error handling

- Bad `project` value (non-string): 400 with message body. Server doesn't validate path existence — user may want to view rows for a since-deleted dir.
- Store error: 500, log to klyne daemon logs.
- UI: same error-banner component the `/memory` page uses (`ErrorBanner.svelte` or inline equivalent — match what `memory/+page.svelte` does).

## Testing

- `internal/store/stop_summaries_test.go`: add `TestListWorklogEntries_*` covering: all projects, filtered by project, limit cap, ordering, mixed visible/suppressed both returned.
- `internal/api/handlers/worklog_test.go`: mirror `handlers/memory_test.go`. Seed rows via `store`, hit handler with `httptest`, assert response shape and field values.
- No UI tests v1. Manual smoke: open `/worklog`, switch project, expand a card.

## Out of scope (v2 backlog)

- Tag chip filtering (`recap_topic`)
- Group-by (scope / project / topic / none)
- "Visible only" toggle
- Search box (full-text over `summary`/`last_user`)
- Reflections section (`worklog_reflections` table)
- Pagination beyond 500
- Delete affordance (worklog rows are auto-managed — no v1 use case)
- Cost: per-row cost field if/when klyne starts capturing it
- Cross-CLI badge ribbons for visual diff between claude/codex sessions
- Linking a worklog row to its source session JSONL viewer

## Open questions

None for v1. All resolved in brainstorming.

## Risks / non-risks

- **No risk** to existing memory feature — separate route, separate handler, separate store function.
- **Schema is already stable** — `stop_summaries` migration 015 has been live for a while; no new migrations needed.
- **Daemon must be running** for the UI to fetch — same prerequisite as `/memory`. No new operational requirement.

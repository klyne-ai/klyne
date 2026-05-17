# Work view simplify + Insights → Project open — Design Brief

**Date:** 2026-05-17
**Status:** Draft for review
**Owner:** klyne UI
**Branch:** `init`

## Pain

The Work view at `/` is a 3-column layout: `ProjectRail` (left), terminal grid (center), `Inspector` (right). The two sidebars duplicate information that already lives in the Insights and Projects pages, and they steal horizontal space from the terminal grid — which is the only thing a user actually watches while sessions stream. Both rails default-open via `localStorage`, so first-load lands on a cluttered screen.

The Insights page surfaces the right metrics (cache hit, trend, /compact rate, tokens-per-message) and an inline-expanded per-project block with "top sessions" — but there is no obvious entry point from a project row into the full per-project session list at `/projects/[name]`. Users have to leave Insights, open the Work view, click the Projects tab (now removed), and start over.

## Goals

1. Work view becomes a terminal-grid-only canvas. Strip the two sidebars and their toggle UI; the terminal grid expands edge-to-edge.
2. Insights gains an explicit per-project entry point that routes to the existing `/projects/[name]` page (no new page built).

## Non-goals

- Building a new per-project detail view inside Insights. `/projects/[name]` already exists and is mature (session list, day-grouping, CLI filter, deletion, previews). Reuse it.
- Changing terminal-grid behavior, fullscreen, focus modal, advisor modal, or the per-session `Terminal.svelte` component.
- Changing Insights' KPIs, daily-activity chart, agent-mix donut, sort controls, or window selector.
- Removing the `ProjectRail` / `Inspector` component files — only their use inside `/` is removed. They remain in `$lib/ui/` in case a future view wants them. (See "Open question" below.)

## Scope

### Work view (`ui/src/routes/+page.svelte`)

**Remove:**
- `<ProjectRail …/>` element and its import
- `{#if inspectorOpen && selectedProject}<Inspector …/>{/if}` block and its import
- Header buttons: `toggleRail` ("‹"/"›"), `toggleInspector` ("hide inspector ›")
- State: `selectedPath`, `railOpen`, `inspectorOpen`, `savedRailOpen`, `savedInspectorOpen`
- localStorage keys: `LS_RAIL = 'klyne.work.rail'`, `LS_INSP = 'klyne.work.inspector'`. The `loadBool` / `saveBool` helpers have no other callers after this change — delete them. `loadCols` / `saveCols` use `localStorage` directly and stay.
- `selectProject(path)` function (callback was only used by `ProjectRail`)
- `selectedProject` derived (only consumed by `Inspector`)
- `projects` derived + `projectsStore` import (only consumed by the rail/inspector flow)
- Wrapper `<div class="work" class:inspector-collapsed class:rail-collapsed …>` becomes `<div class="work" class:work-fullscreen={fullscreen}>` — the two collapse classes go away

**Keep verbatim:**
- Terminal grid + `cols` selector (1/2/3) and `LS_COLS`
- Fullscreen toggle (`toggleFullscreen`, `onFullscreenChange`, `klyne-fullscreen` body class) and its `f` / `Escape` keybindings
- Live-count chip and `jumpToLive()`
- `focusThread` focus modal
- `AdvisorModal`
- All SSE wiring (`subscribe`, `onMsgNew`, `loadThreads`, `tickHandle`, `refreshHandle`)
- `sessionOrder` insertion-order rule (newest at top, never re-sort while visible)

**Behavior after change:**
- First load: terminal grid fills the viewport. No sidebars exist at all.
- Column-count selector and fullscreen button remain in the right of the header.
- Empty state ("No active sessions") renders unchanged inside the now-full-width grid.

### Insights (`ui/src/routes/insights/+page.svelte`)

**Add:** an explicit "Open project →" affordance per ranked project row. Routes to `/projects/[name]` using `encodeURIComponent(p.name)`.

Two placements (do both):

1. **Row header** — append a small button at the end of the existing 8-column grid row (which currently ends with `/compact` count). The simplest move: extend the grid template by one column (`60px`) and put an "open →" ghost button there. Click must `e.stopPropagation()` so it doesn't toggle the inline expansion.
2. **Inline-expanded right column** — beneath the existing "Top sessions" list, add an `Open full project →` primary CTA. This is where a user who already drilled in is most likely to want the full list.

Existing per-session "open →" buttons in the expanded view (which route to `/sessions/[id]`) stay.

**Keep verbatim:** every KPI tile, both charts, all sort/window/unit selectors, the inline expansion behavior, daily sparkline, agent breakdown.

### Insights — minor consistency

The Projects tab no longer exists in `TopNav` (`Work` / `Memory` / `Insights`). The Insights "Open project →" link is the **only** in-app path to `/projects/[name]` once this lands. The route still resolves and `/projects/[name]` is unchanged.

## Out of scope (deferred — capture as follow-ups)

- Whether to delete `ProjectRail.svelte` and `Inspector.svelte` outright. They have no remaining consumers after this change. Decision deferred so this PR stays focused on user-visible behavior; a follow-up sweep can delete them once we're sure no plan or branch is using them.
- A "back to Insights" breadcrumb on `/projects/[name]`. Today the page's back button routes to `/projects` (which is no longer in TopNav but still exists). Out of scope here.
- Whether `/projects` (the route, not the tab) should be removed or redirected to Insights. Out of scope.

## Files touched

| File | Change |
|---|---|
| `ui/src/routes/+page.svelte` | Remove rail + inspector, related state, toggles, imports |
| `ui/src/routes/insights/+page.svelte` | Add "Open project →" to row + expanded view; extend grid template by one column |
| `ui/src/app.css` | Grep for `.rail-collapsed`, `.inspector-collapsed`, and `.work > .rail` / `.work > .inspector` selectors; delete any that remain. They have no remaining consumers after the wrapper classes are dropped. |

No backend changes. No new routes. No new API calls.

## Validation

1. `cd ui && npm run check` — typecheck must pass (removed `selectedPath`, `selectedProject`, `railOpen`, `inspectorOpen` must have zero remaining references).
2. `cd ui && npm run lint` — must pass.
3. `cd ui && npm run test` — existing tests must pass (no test touches the Work view layout directly today; UI unit tests are component-scoped).
4. Manual browser check on dev server (`cd ui && npm run dev`):
   - Load `/`: terminal grid spans full width; no rail; no inspector; fullscreen + col-count + live chip still work.
   - Load `/insights`: per-row "open →" routes to `/projects/<name>`; clicking it does not toggle the inline expansion.
   - Expand a project row in Insights: "Open full project →" CTA appears under Top sessions and routes correctly.
   - `/projects/<name>` page itself is unchanged — all sessions visible, day-grouped, CLI filter works.

## Risk

Low. Pure UI restructure. No data model changes. The deleted state was all client-side `localStorage`; deleted keys silently become dead entries in users' browsers (no migration needed).

Only behavioral surprise: users who had the Inspector open and were using it to glance at project metadata while watching terminals lose that affordance. Insights is one click away in TopNav and gives them the same info plus more. Acceptable.

# Self-contained Insights drill-in

**Date:** 2026-05-19
**Status:** Design — awaiting user review

## Problem

From the **Insights** tab, clicking "open project" (insights/+page.svelte:286), "Open full project →" (:341), or a top-session "open →" (:326) calls `goto('/projects/[name]')` or `goto('/sessions/[id]')`. The TopNav active-tab classifier (`TopNav.svelte:40`) buckets any path matching `startsWith('/projects')` or `startsWith('/sessions')` under the **Work** tab. So the moment you drill into a project from Insights, the Work tab lights up and the Insights tab loses its highlight — the user is yanked out of the Insights context they were working in.

The user's requirement, verbatim: *"in insight only we will have all navigation and all data will be there only"* — drilling into a project or session must keep you in the Insights tab, with the full project and session data reachable without leaving Insights.

## Goals

1. Clicking into a project or session from Insights keeps the **Insights** tab active.
2. The full project sessions list and per-session detail are reachable from within Insights.
3. The drill chain (Insights → project → session → back) stays in the Insights tab end to end, and is deep-linkable / back-button friendly.
4. The existing Work-tab `/projects/[name]` and `/sessions/[id]` routes keep working **unchanged** (the projects-list page and any other links into them are untouched).

## Non-goals

- No redesign of the Insights ranked-list or expanded-row content (agent breakdown / sparkline / top-sessions stay as-is).
- No change to the TopNav classifier logic (verified unnecessary — see "Key findings").
- No backend / API changes. Both detail views already fetch client-side from existing endpoints.
- No drawer/modal/inline-accordion drill-in (explicitly rejected by the user in favour of sub-routes).

## Key findings (verified in code)

- `routes/projects/[name]/+page.svelte` (272 lines) and `routes/sessions/[id]/+page.svelte` (379 lines) are **self-contained client components**: each reads its route param from `$page.params`, fetches via `$lib/api.js`, and renders. No SvelteKit `load` functions, no `+page.ts`, no layout.
- TopNav classifier (`TopNav.svelte:36-42`) checks `startsWith('/worklog')`, then a `work` bucket (`'/' | /work | /cockpit | /projects | /sessions`), then `startsWith('/insights') || startsWith('/stats')` → `insights`. A path of `/insights/projects/foo` does **not** match `startsWith('/projects')` (it starts with `/insights`), so it correctly falls through to the `insights` branch. **No classifier change is needed.**
- Existing back-navigation:
  - `projects/[name]:129` — "‹ projects" → `goto('/projects')`.
  - `sessions/[id]:36-37, 175` — back/after-delete → `/projects/[name]` (or `/projects` when project unknown).
  These targets must become context-aware so the chain stays in Insights.

## Decisions made during brainstorming

| Decision | Value | Rationale |
|---|---|---|
| Drill-in model | Sub-routes under `/insights/` | User picked it over drawer / inline accordion. Deep-linkable, back-button works, reuses existing components. |
| Code reuse | Extract shared components | Avoids duplicating ~650 lines across the Work and Insights route trees. |
| Context propagation | `basePath` + `backHref` props | A single prop drives every internal link so the same component renders correctly under both route trees. |
| Old routes | Keep working unchanged | Work tab project/session drill-in and the projects-list page still depend on them. Confirmed by user ("Approved — proceed"). |
| TopNav | No change | Classifier already routes `/insights/*` to the Insights tab. |

## Architecture

```
BEFORE
  routes/projects/[name]/+page.svelte   ── all logic + markup (272 LOC)
  routes/sessions/[id]/+page.svelte     ── all logic + markup (379 LOC)
  insights/+page.svelte                 ── goto('/projects/..'|'/sessions/..')  ← bounces to Work

AFTER
  lib/ui/ProjectDetail.svelte           ── all project logic + markup; props {projectName, basePath, backHref}
  lib/ui/SessionDetail.svelte           ── all session logic + markup; props {sessionId, basePath}

  routes/projects/[name]/+page.svelte            → <ProjectDetail projectName basePath=""        backHref="/projects" />
  routes/sessions/[id]/+page.svelte              → <SessionDetail sessionId  basePath=""        />
  routes/insights/projects/[name]/+page.svelte   → <ProjectDetail projectName basePath="/insights" backHref="/insights" />   (NEW)
  routes/insights/sessions/[id]/+page.svelte     → <SessionDetail sessionId  basePath="/insights" />                          (NEW)

  insights/+page.svelte  → goto('/insights/projects/..' | '/insights/sessions/..')   ← stays in Insights
```

`basePath` is `''` in Work context and `'/insights'` in Insights context. Every forward link a component builds is `${basePath}/projects/${name}` or `${basePath}/sessions/${id}`. This single prop is what keeps the user inside the Insights tab through the whole chain.

`backHref` exists because `ProjectDetail`'s "back" target differs by context and is **not** just `${basePath}/projects`:
- Work context → `/projects` (the projects-list index page).
- Insights context → `/insights` (the ranked list lives on the Insights page itself; there is no `/insights/projects` index).

`SessionDetail`'s back target is `${basePath}/projects/${projectName}` (or `${basePath}` when the project is unknown — Work: `/projects`-fallback retained; Insights: `/insights`). The Insights project route `/insights/projects/[name]` exists, so this needs no extra prop beyond `basePath`, with the unknown-project fallback being `/projects` for Work and `/insights` for Insights — derived from `basePath` (empty → `/projects`, `/insights` → `/insights`).

## Components

### `lib/ui/ProjectDetail.svelte`
- **Props:** `projectName: string`, `basePath: string` (default `''`), `backHref: string` (default `'/projects'`).
- **Responsibility:** everything currently in `routes/projects/[name]/+page.svelte` lines 1–272 (sessions fetch, CLI filter tabs, day grouping, previews, delete) — moved verbatim except:
  - param no longer read from `$page.params`; it comes from the `projectName` prop.
  - `goto('/sessions/${id}')` (lines 236, 237) → `goto(\`${basePath}/sessions/${encodeURIComponent(s.id)}\`)`.
  - "‹ projects" back button (line 129) → `goto(backHref)`.
- **Depends on:** `$lib/api.js`, `$lib/projects.svelte.js`, `$lib/stores.svelte.js`, `$lib/format.js`, `CliBadge`, `StatusBadge`.

### `lib/ui/SessionDetail.svelte`
- **Props:** `sessionId: string`, `basePath: string` (default `''`).
- **Responsibility:** everything in `routes/sessions/[id]/+page.svelte` lines 1–379 — moved verbatim except:
  - param from `sessionId` prop, not `$page.params`.
  - back / after-delete targets (lines 36–37, 175): `projectName ? \`${basePath}/projects/${encodeURIComponent(projectName)}\` : (basePath || '/projects')`.
- **Depends on:** `$lib/api.js`, plus whatever it already imports.

### Route wrappers (4 files, ~8–12 lines each)
Each reads its param from `$page.params` and renders the matching component:
- `routes/projects/[name]/+page.svelte` → `<ProjectDetail projectName={name} basePath="" backHref="/projects" />`
- `routes/sessions/[id]/+page.svelte` → `<SessionDetail sessionId={id} basePath="" />`
- `routes/insights/projects/[name]/+page.svelte` *(new)* → `<ProjectDetail projectName={name} basePath="/insights" backHref="/insights" />`
- `routes/insights/sessions/[id]/+page.svelte` *(new)* → `<SessionDetail sessionId={id} basePath="/insights" />`

### `insights/+page.svelte` link changes (3 sites)
- `:286` `goto(\`/projects/${encodeURIComponent(p.name)}\`)` → `goto(\`/insights/projects/${encodeURIComponent(p.name)}\`)`
- `:341` same change as `:286`
- `:326` `goto(\`/sessions/${encodeURIComponent(s.session_id)}\`)` → `goto(\`/insights/sessions/${encodeURIComponent(s.session_id)}\`)`

## Data flow

Unchanged. Components fetch the same endpoints (`fetchSessions`, `fetchMessages`, `fetchSession`, `fetchRestore`, `deleteSession`) with the same arguments. The only difference is the identifier arrives via a prop instead of `$page.params`, and built links carry `basePath`.

## Error handling

No new failure modes. The moved code keeps its existing `loadError` / try-catch handling verbatim. Invalid `/insights/projects/<unknown>` behaves exactly like the existing `/projects/<unknown>` (the component shows its existing "Loading project…" / "No sessions found" states). SvelteKit's default 404 covers genuinely malformed routes as it does today.

## Testing

- **Unit (new):** a focused test per component asserting link construction honours `basePath`/`backHref` — i.e. given `basePath="/insights"`, the session row href is `/insights/sessions/...` and the back target is the Insights value; given `basePath=""`, links are the Work values. (Existing `ui/src/lib/*.test.ts` cover stores/format/filters only; this adds the first component-level link-context test.)
- **Manual (required, per UI guidance):** in a browser, walk Insights → "open project" → a session → back → back. Assert the **Insights** tab stays highlighted at every step, the URL is `/insights/...` throughout, the browser back button retraces the chain, and a hard refresh on `/insights/projects/<name>` and `/insights/sessions/<id>` renders correctly. Regression-check the Work tab: `/projects/<name>` and `/sessions/<id>` behave exactly as before and keep the Work tab active.

## Out of scope

- Removing or redirecting the old `/projects` & `/sessions` routes.
- Any change to insights ranked-list/expanded-row content or to TopNav.
- Sharing scroll position or fetched data between the Work and Insights instances of a detail view (each route instance fetches independently, as today).

# Dashboard Redesign — Unified 7-Surface Shell

_Authored 2026-05-23 · branch `feat/dashboard-redesign`_

## 1. Why

Today klyne ships 15 SvelteKit routes (`/`, `/cockpit`, `/projects`, `/projects/[name]`,
`/insights`, `/insights/projects/[name]`, `/insights/sessions/[id]`, `/sessions/[id]`,
`/runbooks`, `/search`, `/stats`, `/worklog`, `/worklog/project`, `/advisors`,
`/productivity`). Each grew bottom-up. They feel like "a local project someone
published" — same data shown three different ways, no consistent chrome, no glanceable
entry point. The Productivity page is the one screen that landed; the rest do not match
its quality.

The redesign brings every surface under one chrome (sidebar + topbar + global ⌘K
palette + slide-over session drawer) and collapses the 15 routes into 7 surfaces —
without removing any feature. The Productivity page is embedded verbatim because it is
already approved.

## 2. Aesthetic

Reuse the Productivity page's token system as the single source of truth:

- `--font-sans: Geist`, `--font-mono: Geist Mono`
- Dark warm-neutral surfaces (`oklch(0.155 0.005 80)` bg, `oklch(0.195 ..)` card,
  `oklch(0.225 ..)` card-2, `oklch(0.13 ..)` inset).
- Foreground tiers: `--fg`, `--fg-soft`, `--fg-muted`, `--fg-dim`.
- Status hues at chroma ≈ 0.13: `--ok` (green 150°), `--warn` (gold 75°),
  `--alert` (red 27°), `--info` (blue 230°), `--accent` (gold 80°).
- No gradients, no decorative SVG, no emoji. Colour is reserved for status only.

`tokens.css` is moved up to `ui/src/lib/styles/tokens.css` and imported once from
`+layout.svelte`. The existing Productivity components keep working unchanged because
the variable names are identical.

## 3. Information architecture

### 3.1 Seven surfaces

| Surface | Replaces | Job |
|---|---|---|
| **Live** | `/` + `/cockpit` | Situational awareness across every active AI terminal. 3-col tile grid. Sidebar auto-collapses to icon-rail on this page. |
| **Productivity** | `/productivity` | Standup digest. Embedded verbatim from today's V4 layout. |
| **Projects** | `/projects` + `/projects/[name]` + `/insights/projects/[name]` + per-project worklog | Master/detail. Left list + right panel with tabs: Overview · Sessions · Worklog · Files. |
| **Insights** | `/insights` + `/stats` | Cross-session economics. 6 always-visible KPIs + tabs: Overview · Activity · Models · Daily · Projects. |
| **Runbooks** | `/runbooks` | Pre-execution memory. Left scope/tag/project sidebar + grouped card grid. |
| **Worklog** | `/worklog` + `/worklog/project` | Per-project reflection rollup (stale/cold/fresh) with copy + run-reflect. Cards link to the ISO-week drill-in. |
| **Advisors** | `/advisors` | Inbox of every advisory klyne fired. Stays separate from Live (per chat5 decision). |

Plus two cross-cutting overlays:

- **SessionDrawer** — slides over any surface when a session is clicked. Replaces
  `/sessions/[id]` and `/insights/sessions/[id]`. Carries the **full** session
  experience: stats, context %, token-over-time chart, KLYNE_SUMMARY, advisors fired
  in this session, full message stream with tool-call breakdown, Restore-Context modal
  for compacted sessions, resume command.
- **SearchPalette** — ⌘K (and `/`) from anywhere. FTS5 hits + project/session/runbook
  jump targets. Replaces the `/search` full page.

### 3.2 Routing

SvelteKit routes after the cutover:

```
/                 → Live
/productivity     → Productivity (unchanged)
/projects         → Projects (default selection = first project)
/projects/[name]  → Projects with that project selected (deep-link)
/insights         → Insights
/runbooks         → Runbooks
/worklog          → Worklog
/worklog/project  → kept, ISO-week drill-in (reached from Worklog card)
/advisors         → Advisors
```

Removed routes (logic preserved, surface merged): `/cockpit`, `/search`, `/stats`,
`/sessions/[id]`, `/insights/projects/[name]`, `/insights/sessions/[id]`. Each removal
is replaced by either a tab within a remaining surface or a drawer/palette.

Sessions are no longer a route. Clicking a session anywhere → `SessionDrawer`, which
pushes `?session=<id>` into the URL so deep-links still work.

### 3.3 Shell chrome

```
┌────────────┬──────────────────────────────────────────────┐
│ sidebar    │ topbar (crumbs · daemon status pill · ⌘K)    │
│ (220 / 64) ├──────────────────────────────────────────────┤
│   Live    ●│                                              │
│   Product. │              <page content>                  │
│   Projects │                                              │
│   Insights │                                              │
│   Runbooks │                                              │
│ ─Capture─  │                                              │
│   Advisors!│                                              │
│   Worklog  │                                              │
│ ─────────  │                                              │
│ M · 127…   │                                              │
└────────────┴──────────────────────────────────────────────┘
```

- Sidebar: brand mark + `>K`, ⌘K search affordance, Workspace section
  (Live/Productivity/Projects/Insights/Runbooks), Capture section
  (Advisors/Worklog), foot with user avatar + daemon status dot.
- Collapse: 220 px ↔ 64 px (icon-only). Auto-collapse on Live; manual toggle
  via the half-moon button on the sidebar edge persists across pages once the user
  has touched it.
- Topbar: breadcrumbs (Workspace · Live), right-aligned daemon status pill, page-
  specific right-rail (e.g. Live shows `4 live · streaming`).
- ⌘K and `/` both open the palette. Esc closes.

### 3.4 Visual reference

Source-of-truth prototype lives in the design bundle at
`docs/design/2026-05-23-dashboard/`. The prototype is React; we port behaviour, not
structure. Visuals must match pixel-for-pixel except where Svelte idioms shorten the
code (e.g. component composition for tabs/cards).

## 4. Locked decisions

From the brainstorming questions:

- **Live tile content:** Adopt design as-is — last 3 tail messages per tile. Full
  streaming chat lives in `SessionDrawer`.
- **Search:** ⌘K palette + keep `/` shortcut. The 315-line `/search` route is removed;
  its filters move into the palette.
- **Preserve list — all four selected:**
  - SessionDrawer renders the full message stream + tool-call breakdown + Restore-
    Context modal for compacted sessions.
  - Insights gains a **Daily** subtab (per-day token+cost rows).
  - `/worklog/project?path=` kept with ISO-week grouping, reached from Worklog cards
    and from Projects → Worklog tab "see full →".
  - Projects → Overview tab gets the agent-mix donut and the hidden-session (eye-off)
    toggle on session rows.
  - Live keeps today's fullscreen tile-focus modal in addition to the Drawer (long
    press / `F`).
- **Worktree:** `.worktrees/dashboard-redesign` off `init` on branch
  `feat/dashboard-redesign`.

## 5. Per-page design notes

### 5.1 Live

- 3-col `auto-fill, minmax(300px, 1fr)` grid; collapses to 2 below 720 px main
  width, 1 below 480 px.
- Sidebar auto-collapses on entry to maximise screen real estate.
- Tile: header (status dot · project · short id · CLI pill · last-ago) + body
  (tail-of-3 messages with role kicker, line-clamp 3, 2 px left accent rail) +
  foot (`streaming` / `idle · last msg Ns ago` · `open ›`).
- Filters: project/branch/session text filter, CLI segmented (all / claude / codex),
  `show idle` checkbox. Default = show idle ON (matches today's `/`).
- Empty state: "No sessions match these filters." in a card.
- Fullscreen focus modal preserved from today's `/`. `F` toggles fullscreen on the
  hovered tile; Esc closes. Reuses today's `Terminal.svelte` for the focused view.
- Hidden-session control (eye-off) on each tile.
- Live count badge in sidebar Workspace › Live shows the count; topbar right rail
  shows `N live · streaming`.

### 5.2 Productivity

- Embedded verbatim. The new shell wraps `routes/productivity/+page.svelte` without
  layout changes. The wrapper provides only the page-head row (title · lede · loaded-
  ago · refresh).
- Components untouched: `DaySummary`, `ProofOfWork`, `RiskPanel`, `RangeBar`,
  `SummaryBar`, `ServiceCard`, `SessionTimeline`, `TimeBarChart`,
  `ConcurrencyTimeline`.

### 5.3 Projects

- Two-column: 280–320 px list · flex panel.
- List row: state dot (fresh/stale/cold) · project id · last-ago · CLI pills · session
  count · `tokensIn` · stale/cold tag pill.
- Panel header: state dot · project name · CLI pills · `Open in CLI` button · path
  (mono).
- Tabs: Overview · Sessions · Worklog · Files.
  - **Overview:** 4 stat tiles (Sessions / Messages / Tokens out / Cost) + Daily
    activity 16-day bar chart + Recent sessions (3 rows) + **Agent-mix donut**
    (new — preserved from `/insights/projects/[name]`).
  - **Sessions:** Day-grouped (Today / Yesterday / explicit date). Slim row with CLI
    pill, title (ellipsised), short id, msg count, ↓ tokens, state pill, eye-off
    toggle, chevron. Click row → SessionDrawer.
  - **Worklog:** Latest reflection inline. Stale/cold banner with copy + run-reflect
    (reuses `/worklog` reflect logic). "See full →" → `/worklog/project?path=`.
  - **Files:** Top files touched + tools used bar chart (reuses today's data).
- Toolbar: filter / CLI / sort (recent · sessions · messages · name) + state legend.
- Hidden-session toggle visible on every row.

### 5.4 Insights

- Header: window selector (1h · 3h · 6h · 1d primary, 7d/30d/90d in dropdown), CLI
  filter (both/claude/codex). Default window = 1d.
- 6 KPI strip: Spend · ↑ Input · ↓ Output · Sessions · Messages · Projects.
- Tabs: Overview · Activity · Models · Daily · Projects.
  - **Overview:** Tokens-per-day bar chart (claude over codex stacked) + Agent-mix
    donut.
  - **Activity:** GitHub-style heatmap (14 weeks × 7 days) + Streak / Longest /
    Favourite model / Peak hours stats.
  - **Models:** Per-model table (Model · Share bar · Sessions · Input · Output ·
    Cache hit · Cost · %). Reuses `ModelRow` types from today.
  - **Daily (new):** Per-day rows table from today's `/stats` `DailyRow[]`:
    date · sessions · turns · input · output · cache read · cache write · cost. Sortable
    by date desc by default.
  - **Projects:** Project rank table (Project · Tokens bar · Tokens · Trend ·
    Cache hit · Tok/msg · /compact · open →). Row click → Projects with that project
    selected.

### 5.5 Runbooks

- Two-column: 220 px sidebar · flex grid.
- Sidebar: Scope (all / global / project — buttons with counts) · Project select ·
  Tags (multi-toggle pill cloud).
- Top toolbar: search input + count.
- Grouped grid (`auto-fill, minmax(360px, 1fr)`): Global section, then per-project
  sections.
- Card: scope/project pill · id · ago · title · pre-formatted body box · tag pills ·
  delete (alert) + open (chevron).
- Actions: `+ new runbook`, `Export`.

### 5.6 Worklog

- Filter chips: all · stale · cold · fresh (counts shown).
- One card per project, ordered stale → cold → fresh.
- Card: 3 px left rail in state colour. Header (project · state pill · reflected-ago ·
  last-activity-ago · path). Latest reflection block (bullets · shipped · open loops)
  if any. Run-strip (copy command + `▶ run` / `✖ stop`) only on stale/cold.
- "See full →" link in card foot → `/worklog/project?path=<path>` for ISO-week
  drill-in (preserved).
- Reflect runner reuses today's `runReflect` API + `AbortController` + elapsed timer.

### 5.7 Advisors

- Pinned to the Capture section of the sidebar (kept separate from Live per chat5).
- Filter chips: all · acceleration · topic shift · stale context · hard ceiling.
- Card row: 3 px left rail in level colour (warn/alert/info), kind kicker, CLI, short
  session id, project, ago, body (line-clamp). Click → SessionDrawer at that session.
- Sidebar `Advisors` meta shows the live unread count (alert dot in icon-rail mode).

### 5.8 SessionDrawer (overlay)

- Slide-in from right, 92 vw max, 820 px target. Backdrop scrim.
- Header: dot · `Session` kicker · full uuid · CLI pill · project · started · last-msg
  · model · close.
- Body sections, top → bottom:
  1. 4 stat tiles: Messages · ↑ Tokens in · ↓ Tokens out · Cost.
  2. Context % bar (alert/warn/ok colour).
  3. Token usage over time chart (reuses `TokenTimelineChart.svelte`).
  4. KLYNE_SUMMARY card (with importance score).
  5. Advisors fired in this session (filtered from advisor stream).
  6. **Full message stream** + tool-call breakdown (reuses today's `SessionDetail.svelte`
     interior — `MessageBubble`, `ToolCallBlock`).
  7. **Restore-Context** banner + modal for compacted sessions (reuses today's
     `RestoreContext.svelte`).
  8. Resume command card (`claude --resume <id>` · copy button).
- Open mechanism: `onOpenSession(id)` from any list. URL: `?session=<id>` so the
  drawer survives reload and can be deep-linked.

### 5.9 SearchPalette (overlay)

- Triggered by ⌘K (or `/` when no input is focused). Esc closes.
- Centered modal, 640 px max, 12 vh from top.
- Input + groups:
  - Empty input: "Try" suggestions (current `searchSuggest`) + "Jump to" (Productivity
    today · Projects: <name> · Runbooks).
  - With input: FTS5 hits grouped by kind (msg / session / runbook). Each row:
    icon · highlighted snippet · `project · short_id · ago`.
- Picking a hit: jumps to surface OR opens SessionDrawer (for session hits).
- All filters from today's `/search` (cli, project, kind) live inline beneath the
  input as compact chips.

## 6. Data flow

Existing API surface is unchanged. Every page binds to the same endpoints today's
routes use:

- `fetchCockpitThreads` (Live + topbar live count)
- `fetchAdvisories` (Advisors + per-session drawer)
- `fetchUsageStats` (Insights — Overview / Models / Daily / Activity)
- `fetchProjectInsights` (Insights → Projects tab and Projects → Overview)
- `fetchWorklog`, `fetchWorklogProject`, `runReflect` (Worklog + Projects → Worklog)
- `fetchMessages`, session detail endpoints (SessionDrawer)
- FTS5 search endpoint (SearchPalette)
- SSE subscription via `subscribe({ onMsgNew, onSessionUpdate })` (Live + topbar live
  count + Advisors badge + Projects badge counts)

No daemon-side changes. No new endpoints. No API/Go change beyond cosmetic.

## 7. Migration

### 7.1 Strategy

Single-shot route replacement, page-by-page commits. Worktree isolates risk. A
short-lived `PUBLIC_DASHBOARD_V2` env toggle exists only as a cutover scaffold so
pages 2–9 can land independently while the old shell stays default; it is deleted
in step 10. Not a long-lived feature flag.

Commit cadence (one PR-sized commit per row; #2–#9 may land in any order):

1. `chore(ui): introduce dashboard shell + tokens.css move`
   - Adds `src/lib/dashboard/{Shell,Sidebar,Topbar,SearchPalette,SessionDrawer}.svelte`.
   - Moves `tokens.css` to `lib/styles/`.
   - Wires `+layout.svelte` to choose new vs. old shell via `PUBLIC_DASHBOARD_V2`.
   - Does NOT touch existing routes. Default OFF.
2. `feat(ui): Live page replaces / + /cockpit`
3. `feat(ui): Projects merges /projects, /projects/[name], /insights/projects/[name]`
4. `feat(ui): Insights merges /insights + /stats; adds Daily tab`
5. `feat(ui): Worklog rebuilt; /worklog/project drill-in retained`
6. `feat(ui): Runbooks rebuilt`
7. `feat(ui): Advisors moves to capture section`
8. `feat(ui): SessionDrawer replaces /sessions/[id] + /insights/sessions/[id]`
9. `feat(ui): SearchPalette replaces /search`
10. `chore(ui): flip PUBLIC_DASHBOARD_V2 to default on; delete dead routes`

After 10, the old route files are deleted. The feature env is removed in a follow-up
once we're past the next release cut.

### 7.2 Component inventory

Keep (reused as-is):
- All `lib/components/productivity/*` (DaySummary, ProofOfWork, RiskPanel, etc.)
- `MessageBubble`, `ToolCallBlock`, `RestoreContext`, `TokenTimelineChart`, `Terminal`
- `AgentMixDonut`, `DailyActivityChart`, `SmoothSparkline`, `Sparkline`
- `CliBadge`, `StatusBadge`, `Kbd`, `Inspector`
- `hidden-sessions.svelte.ts` store

Retire (logic absorbed elsewhere):
- `TopNav.svelte` → `Sidebar` + `Topbar`
- `SearchOverlay.svelte` → `SearchPalette.svelte`
- `SessionList.svelte`, `SessionView.svelte` → replaced by Live tile + Projects →
  Sessions tab + SessionDrawer
- `ProjectRail.svelte`, `ProjectDetail.svelte`, `SessionDetail.svelte` wrappers →
  retired; their interior pieces (message stream, tool-call block, restore-context,
  token timeline chart) move under `SessionDrawer` / Projects panel and keep working
  as standalone components.

### 7.3 URL compatibility

Before deleting old routes, add redirects in `+layout.svelte`:

| Old URL | New URL |
|---|---|
| `/cockpit` | `/` |
| `/search` | `/?palette=1` (opens palette on landing) |
| `/sessions/<id>` | `/projects?session=<id>` (drawer opens) |
| `/insights/sessions/<id>` | `/insights?session=<id>` |
| `/insights/projects/<name>` | `/projects/<name>` |
| `/stats` | `/insights?tab=daily` |
| `/worklog/project?path=…` | unchanged (kept) |

Redirects stay for one release after deletion, then removed.

## 8. Testing

- Unit: `navlinks.ts` updated, plus new helpers for drawer URL state (`?session=`,
  `?palette`, `?tab=`). Test patterns mirror today's `navlinks.test.ts`.
- Component (Vitest): Sidebar collapse persistence, SearchPalette filter chips,
  SessionDrawer URL roundtrip, ProjectsList sorting.
- Integration: `messageFilters`, `format`, `sse` test suites must remain green.
- E2E (Playwright if present): Skip — Playwright MCP is available on demand but no
  E2E suite exists in-repo. Use the screenshot tool for visual verification at end
  of each milestone instead.
- Manual checklist (recorded in the implementation plan): sidebar collapse on Live,
  ⌘K palette opens from every surface, drawer URL deep-link survives reload,
  Productivity untouched visually, `/worklog/project?path=` still reachable.

## 9. Out of scope

- Backend changes (no new endpoints, no schema changes).
- Mobile / responsive < 640 px (no mobile target today; punt).
- Theming (light mode) — explicitly not requested.
- Auth / multi-user — single-user local app.
- New chart types beyond what today's components render.
- Marketing or onboarding pages.

## 10. Open items

None blocking. Decisions captured in §4. The implementation plan (writing-plans
deliverable) will sequence §7.1 into concrete steps with verification gates.

# Web cockpit — 4-tab shell at `http://127.0.0.1:7878`

> Status: shipped. Local-only HTTP server bound to `127.0.0.1`. No remote calls. Started by `klyne start` (or just `klyne`).

The web cockpit is the browser-side surface on the same local engine that powers the MCP tools. A SvelteKit single-page app served from the klyne daemon.

## Shell

Four-tab top nav. Search lives behind the `/` keyboard overlay, not as a route.

| Tab | Primary view | Deep-link surfaces |
|---|---|---|
| **Work** | `/` — live operational view: running sessions, projects, recent activity | `/cockpit` (SSE tile grid), `/projects` · `/projects/[name]`, `/sessions/[id]`, `/advisors` |
| **Runbooks** | `/runbooks` — project + global runbooks grouped by service (pre-execution-recall surface) | — |
| **Worklog** | `/worklog` — per-session reflections | — |
| **Insights** | `/insights` — project-centric, subscription-aware metrics | `/stats` (Overview / Models / Daily / Stats tabs, GitHub-style heatmap, models-by-cost, streaks) |

## Surfaces by route

| Route | What you see |
|---|---|
| `/` | Work overview — top nav, recent sessions, projects |
| `/cockpit` | Live SSE-streaming grid; one tile per active session, latest 10 messages tail |
| `/projects` · `/projects/[name]` | Project hub + per-project drill-down (sessions, files, decisions) |
| `/sessions/[id]` | Full session detail — every message, every tool call, token timeline |
| `/runbooks` | Project + global runbooks grouped by service ([runbooks.md](./runbooks.md)) |
| `/insights` | Project-centric subscription-aware metrics |
| `/stats` | Tokens-per-day, models-by-cost, activity heatmap ([stats-dashboard.md](./stats-dashboard.md)) |
| `/advisors` | Per-session advisor state — which triggers fired and when ([proactive-session-advisor.md](./proactive-session-advisor.md)) |

## Keyboard

| Key | Action |
|---|---|
| `/` | Open the global search overlay ([search.md](./search.md)) |
| `Esc` | Close the search overlay |

## Implementation

- `ui/` — SvelteKit app (Svelte 5 runes)
- `internal/api/` — daemon HTTP handlers backing every route
- `internal/store/` — SQLite-backed data layer
- SSE feed from `internal/api/handlers/sse.go` powers the `/cockpit` live tiles
- All routes resolve via deep-link even when not in the top nav — legacy URLs continue to work

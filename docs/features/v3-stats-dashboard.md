# v3 — `/stats` dashboard + per-session heatmap

> Status: implemented on `worktree-v3-tokscale-stats-daily-overview`.
> Adds a new SvelteKit page, a new backend endpoint, and a Unicode
> hour-by-hour heatmap to the existing `klyne tokens` CLI surface.
> Inspired by [junhoyeo/tokscale](https://github.com/junhoyeo/tokscale).

## Web dashboard — `/stats`

Lives at `http://127.0.0.1:7878/stats`. Tabs:

| Tab | What it shows |
|---|---|
| **Overview** | Tokens-per-day bar chart over the selected window + Models-by-cost bars with % share |
| **Models** | One row per model: Input / Output / C.Read / C.Write / Sessions / Cost |
| **Daily** | One row per day: Messages / Input / Output / C.Read / C.Write / Total / Cost |
| **Stats** | GitHub-style activity heatmap (12 weeks) + Favorite model · Current streak · Longest streak · Active days · Peak hour · Window |

Top-of-page strip is always visible: Total spend · Input (with cache %) · Output · Sessions · Messages.

Filters: CLI (claude / codex / both), window (7d / 30d / 90d / 1y).

### Backend

- `GET /usage/stats?cli=claude&days=30&heatmap_weeks=12` → JSON `UsageStatsResponse`
- Reuses `internal/cost.Engine` so totals stay aligned with `klyne audit-sessions`.
- New package `internal/usagestats` owns the aggregation (daily rollups,
  per-model sums, peak hour, streaks, GitHub-style heatmap with quantile
  intensity bucketing).
- 7 unit tests + 3 HTTP handler tests cover the path.

### Frontend

- `ui/src/routes/stats/+page.svelte` — single-file page using Svelte 5
  runes (`$state`, `$derived`, `$effect`).
- New top-nav entry between **Cockpit** and **Projects**.
- Reuses existing `kfmt` / `costFmt` / `ad-card` / `ad-mono` helpers so
  the visual language matches the rest of the app.

## CLI — enhanced `klyne tokens`

`klyne tokens` already rendered a per-turn table + ASCII sparkline. v3
appends a per-session activity heatmap:

```
Activity heatmap (IST)
```
```
        00          06          12          18        23
May 11  ··············································▓▓
May 12  ▒▒············░░██······························
```
peak hour May 12 8:00 — 139 turn(s), 1.1M effective tokens · 4 active hour(s) total
```

- Single-day sessions collapse to one 24-cell row.
- Multi-day sessions get one row per day.
- Intensity buckets (`··` / `░░` / `▒▒` / `▓▓` / `██`) derived from the
  session's own quantiles — the scale tracks the user's data, not
  absolute tokens.
- Skipped automatically for sessions with fewer than 6 turns.

Implementation: `internal/mcpserver/token_timeline_heatmap.go` +
`token_timeline_heatmap_test.go`. The renderer is appended inside the
existing `formatTokenTimelineAsMarkdown` so both `klyne tokens` and
`/klyne:tokens` (the MCP slash prompt) pick it up.

## What we intentionally did NOT add

| Idea | Why skipped |
|---|---|
| Standalone `klyne daily` CLI | Same data is now in the `/stats` Daily tab — adding a CLI would duplicate it. |
| Cross-CLI ingestion (Cursor / Gemini / etc.) | User explicitly scoped this to Claude + Codex. |
| Persistent client-side filters | Filter state stays in-page for now; refresh resets to defaults. |
| AI-generated weekly summary | Violates "no AI calls in core flow". |

# Analytics commands — `top`, `patterns`, `roast`

> Status: v1 implementation landed on the `worktree-claudestat-inspired-features`
> branch. All commands ship as CLI surfaces only; no MCP wiring or daemon
> changes — the local SQLite store is the data source.

This doc describes three read-only CLI subcommands inspired by
[claudestat](https://github.com/DeibyGS/claudestat) but designed to fit
klyne's existing contracts:

- **local-first** — every byte read comes from `~/.klyne/klyne.db` (the
  daemon's SQLite store).
- **deterministic** — same input always produces the same output. There
  is no AI call in the core flow.
- **read-only** — no schema migrations, no writes back to the store, no
  changes to source JSONL files.
- **no telemetry** — nothing is uploaded, proxied, or logged elsewhere.

## Why we did it

claudestat's most-cited features include pattern recognition (loops, Bash
overuse) and shareable analytics. klyne already has a populated SQLite
store but was missing the ergonomic CLI surface that turns that data
into a glanceable answer.

These three commands close that gap without changing klyne's identity
(session-rescue first; analytics second).

## Surfaces

### `klyne top`

Tool-call rankings across sessions.

```bash
klyne top                          # top 20 tools by call count
klyne top --project=<path>         # scope to one project
klyne top --since=24h --json       # recent calls only, JSON
```

Each row carries: call count, share of total, error count (when the
matching `tool_result` was flagged `is_error`), and number of distinct
sessions the tool appeared in.

### `klyne patterns`

Deterministic inefficiency detection. Three rules ship in v1:

| Kind | Trigger | Default threshold |
|---|---|---|
| `tight_loop` | N+ consecutive same-tool calls within one session | 5 calls |
| `bash_overuse` | `bash_calls / total_tool_calls` in a session | ≥ 40 % (min 10 calls) |
| `low_cache_reuse` | `cached_read / tokens_in` in a session | < 30 % (min 50k tokens) |

Each finding has severity (`info` | `warn` | `alert`) and carries both
the metric value and the threshold so the reader can re-derive the
verdict.

```bash
klyne patterns                       # everything
klyne patterns --kind=tight_loop     # filter by kind
klyne patterns --since=168h --json   # recent only, JSON
```

### `klyne roast`

Sardonic, deterministic, templated insights. No AI calls — every line
is a templated string interpolated with real numbers.

Categories: small-sample, spend, cache reuse, Bash share, tight loop,
tool monoculture, project monoculture, and a fall-through "clean" line.

```bash
klyne roast            # up to 5 zingers
klyne roast --max=8    # up to 8 zingers
klyne roast --json     # structured form for downstream piping
```

## Architecture

- `internal/insights/` — analytics library used by all three commands.
  Three files:
  - `insights.go` — session walking, `SessionStats`, `AggregateTools`.
  - `patterns.go` — `DetectPatterns` over `SessionStats`.
  - `roast.go` — templated insights over a `RoastInput`.
- `cmd/klyne/top.go`, `patterns.go`, `roast.go` — use `internal/insights`.
- `cmd/klyne/format.go` — shared rendering helpers (`shortID`, `shortPath`,
  `fmtCount`).

Test coverage:

- `internal/insights/insights_test.go` — unit tests for aggregation and
  pattern detection.
- `internal/insights/integration_test.go` — end-to-end tests against a
  real SQLite DB seeded via the store API.
- `cmd/klyne/analytics_e2e_test.go` — cobra-level tests for `top` and
  `roast`.

## What we explicitly skipped from claudestat

These features didn't fit klyne's contracts and were not implemented:

| claudestat surface | Why skipped |
|---|---|
| Live `watch` tail | Requires hook-driven write-side ingestion; klyne is read-only |
| Quota kill switch | klyne does not block sessions — that's the user's call |
| Desktop notifications | Out of scope for an MCP/CLI surface |
| Weekly AI-generated reports | klyne explicitly avoids AI calls in the core flow |
| Shareable session cards | Privacy-sensitive in a local-first product |

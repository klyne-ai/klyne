# Cost-Per-Outcome with Waste Digest — Design Brief

**Date:** 2026-05-15
**Status:** Draft for review
**Owner:** klyne core

## Pain

["The Unplanned $100 Month"](https://medium.com/@tararoutray/claude-code-token-burn-the-unplanned-100-month-reality-48587c6a92ce), GH [#41866](https://github.com/anthropics/claude-code/issues/41866), [#17693](https://github.com/anthropics/claude-code/issues/17693), [#10784 OAuth retry storm wasted 3.75M tokens](https://github.com/anthropics/claude-code/issues/10784), [#30016 Playwright SQL loop](https://github.com/anthropics/claude-code/issues/30016). Surveyed 8 observability tools (claude-code-otel, ccusage, codeburn, token-dashboard, hoangsonww monitor, phuryn, niski84, Maciek) — **all stop at per-project; none reach per-outcome.** Open lane.

## What klyne already has

The pivot is **technically free** — klyne already has every column needed:

- Migration 004 (`messages_tool_columns`): `tool_calls_json` — parse `git commit`, `gh pr create`.
- Migration 005 (`cached_tokens`): `cached_*_tokens` — split fresh vs cache for honest math.
- Migration 008 (`message_branch_cwd`): `git_branch + cwd` per message.
- Migration 009 (`decisions`): `project_path + tags + session_id + ts`.
- `internal/cost/pricing.go` + `pricing_schema.go` + `refresh.go` — rate engine ready.
- `internal/audit/runner_test.go` + `groundtruth_codex.go` — audit pipeline scaffolding.
- Migration 007 (`zero_costs`) — historical signal: USD removed from hot path because subscriptions made it misleading. **Bring back as display layer, not stored fact.**
- `internal/api/handlers/cost.go` — HTTP cost handler in cockpit.
- `internal/insights/` — natural home for waste detectors.

## What's net-new

1. **`work_spans` table** — `(commit_sha | pr_number | exploration_id) → {tokens_fresh, tokens_cache, usd, waste_classes[], decision_ids[], session_ids[]}`.
2. **Batch attribution job** — nightly stream over `messages` joined to `git log`, state machine keyed by `(git_branch, cwd)`, opens span at session start or last commit, closes on `git commit` / `gh pr create` tool calls.
3. **Waste-class detectors** — `WASTE_LOOP, WASTE_REVERT, WASTE_DEADEND, WASTE_MISALIGNED, WASTE_COMPACTED`.
4. **PR footer wrapper** — `klyne gh-pr-create` shim wraps `gh pr create` and appends a footer line. Same distribution flywheel as `Co-Authored-By: Claude`.
5. **Weekly digest CLI** — `klyne cost week` prints work-spans grouped by waste class with $$$.
6. **Cockpit panel** — extend `internal/api/handlers/cost.go` with `/api/cost/spans` endpoint feeding a new UI tile.

## Architecture

| Component | File | Status |
|---|---|---|
| `work_spans` table | migration `014_work_spans.sql` + `internal/store/work_spans.go` | NEW |
| Batch attribution | `internal/cost/attribution/runner.go` (new package) | NEW |
| Waste detectors | `internal/cost/attribution/waste.go` | NEW |
| PR footer wrapper | `cmd/klyne/gh_wrap.go` | NEW |
| Weekly digest CLI | `cmd/klyne/cost_week.go` | NEW |
| Cockpit endpoint | extend `internal/api/handlers/cost.go` with `/api/cost/spans` | EXTEND |
| Cockpit UI tile | `ui/src/...` (TBD by UI workstream) | NEW |

## Data flow

Nightly cron (or `klyne cost rebuild`):

1. Stream `messages WHERE ts > last_run` ordered by `(session_id, ts)`.
2. State machine per `(git_branch, cwd)` opens a `work_span` at session start or after last close event.
3. Accumulate input/output/cache tokens; multiply via `pricing.go` rate table → USD (display only, NOT stored as fact in `work_spans`).
4. Detect close events from `tool_calls_json`:
   - `Bash(git commit ...)` → parse `git rev-parse HEAD` from result stdout → `commit_sha`.
   - `Bash(gh pr create ...)` → parse PR URL from stdout → `pr_number`.
   - Session end without close → `bucket=exploration`, autogen `exploration_id`.
5. Attach `decision_ids` from `decisions WHERE session_id IN spans AND ts BETWEEN open AND close`.
6. Run waste pass (see algorithm below).
7. Insert/update span row.

## Algorithm — span close detection

```go
for msg := range stream {
    span := state[key(msg.GitBranch, msg.Cwd)]
    span.AccumulateTokens(msg)
    for _, tc := range msg.ToolCalls() {
        cmd, ok := tc.BashCommand()
        if !ok { continue }
        switch {
        case isGitCommit(cmd):
            span.Close(parseCommitSha(tc.Result), CloseCommit)
        case isPRCreate(cmd):
            span.Close(parsePRNumber(tc.Result), ClosePR)
        }
    }
}
// At end of stream: any open spans → bucket=exploration with autogen id.
```

## Algorithm — waste detectors

| Class | Rule |
|---|---|
| `WASTE_LOOP` | Same `tool_calls_json` SHA-256 hash repeats >5× within 10-turn window. |
| `WASTE_REVERT` | File `F` edited then content at next commit equals content at prior commit. |
| `WASTE_DEADEND` | Branch in `messages.git_branch` never reached `main` AND no open PR (via `gh pr list`). |
| `WASTE_MISALIGNED` | ≥20 consecutive assistant turns followed by user message containing reversal markers (`no`, `stop`, `actually`, `wrong`, `back up`). |
| `WASTE_COMPACTED` | Turns within N of `/compact` whose content didn't survive into post-compact summary. |

Each tagged span carries one or more waste classes. Weekly digest sums tokens × rate per class.

## Testing

- Unit per detector: golden message fixtures → expected waste tags.
- Property: span tokens always ≥ 0; close events monotonic per span.
- Integration: feed `audit/groundtruth_codex_test.go` data through attribution runner, assert known commits get attributed.
- E2E: recorded "OAuth retry storm" fixture from issue #10784 → expect `WASTE_LOOP` flagged with ~3.75M tokens.
- Snapshot: `klyne cost week` output for a fixture week.

## Rollout

- **v0** ships **batch attribution + weekly digest CLI only** (no PR footer, no cockpit). Personal mode default — data stays local.
- **v1** adds PR footer wrapper opt-in (`klyne gh-pr-create` aliased in user shell).
- **v2** adds cockpit panel + team mode (aggregated, no per-person attribution upstream).
- README hero copy: "Prices every PR, catches the waste."

## Today-shippable v0

`work_spans` migration + minimal attribution runner that closes on `git commit` only (no `gh pr` parsing) + the `WASTE_LOOP` detector + `klyne cost week` CLI. Skip the other 4 waste classes for v0 — `WASTE_LOOP` alone caught the 3.75M-token OAuth storm from issue #10784, which is the demo.

**Estimate: 6–8 hours.**

## Time estimate

- **v0 today** (commit-close, `WASTE_LOOP` only, week CLI): 6–8h
- **v1** (gh pr close, all 5 waste detectors, footer wrapper): +3 days
- **v2** (cockpit tile + team mode): +3 days

## Risks + mitigations

| Risk | Mitigation |
|---|---|
| Tokens are meaningless on flat plans → guilt-trip | Reframe unit as "budget burned against your 5h cap"; lead digest with `WASTE`, not `SPEND` |
| Attribution heuristics get edge cases wrong | Confidence score per span; hide low-confidence from headlines, show in drill-down |
| Telemetry creep | Personal mode default; team mode opt-in + aggregated only; never per-person upstream |
| Squash-commits bundle multiple work units | v1 detect squash via `git log --merges` and split heuristically; v0 accept noise |
| `gh pr create` parsing fragile | v0 skips PR-close entirely; v1 parses URL with regex + falls back to `gh pr view --json` |

## Devil's-advocate counter

Subscribers don't pay per token — the $ is fictional. Counter: the **waste** number isn't fictional — it's the headroom you burned against your 5-hour window cap, which IS a real constraint subscribers feel. Migration 007 already removed `cost_usd` from the hot path for this reason; bring it back **only as a display layer** in the digest, never persisted.

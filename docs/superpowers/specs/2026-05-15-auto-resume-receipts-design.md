# Auto-Resume with Receipts — Design Brief

**Date:** 2026-05-15
**Status:** Draft for review
**Owner:** klyne core

## Pain

Anthropic's `--continue` and `--resume` famously fail to actually restore context. GitHub [#43696](https://github.com/anthropics/claude-code/issues/43696), [#40286](https://github.com/anthropics/claude-code/issues/40286), [#39663](https://github.com/anthropics/claude-code/issues/39663), [#15837](https://github.com/anthropics/claude-code/issues/15837). Top-shared post of the quarter: ["Claude Code Lost My 4-Hour Session"](https://dev.to/gonewx/claude-code-lost-my-4-hour-session-heres-the-0-fix-that-actually-works-24h6). Multi-step debugging often requires 4–5 restarts.

## What klyne already has

- `internal/resume/builder.go` — resume builder exists. **The hard part is largely done.**
- `internal/mcpserver/tool_bootstrap.go` (commit 054dcb2 "Serena-inspired bootstrap brief") — Day-1 brief invoked as tool by the agent.
- `internal/store/decisions.go` (migration 009) with `project_path + tags + session_id + ts`.
- `internal/store/sessions.go` with `last_msg_at, project_path, msg_count`.
- `internal/store/messages.go` (migrations 008 git_branch+cwd, 004 tool_calls_json).
- `internal/store/search.go` + FTS5 index for topic similarity.
- `internal/mcpserver/handoff.go` + `tool_generate_handoff.go` — handoff payload generation.

## What's net-new

1. **SessionStart hook handler** — currently `tool_bootstrap` is invoked AS A TOOL by the agent on demand; we need a hook that fires automatically at session-start and surfaces the resume offer in the system message stream.
2. **Per-session relevance scorer** — score = 0.35 × recency + 0.25 × cwd_match + 0.25 × git_file_jaccard + 0.15 × topic_sim.
3. **Token-cost estimator** — estimate the size of each resume variant (full / decisions-only) before injection. Reuses existing `internal/usage/calc.go` token math.
4. **`/klyne:resume` slash command** — accepts `<rank>` and optional flags `--decisions-only`, `--last-N`, `--budget=8K`.
5. **Anti-bloat trimmer** — when payload exceeds budget, cut in order: pre-decision turns → big tool_results → file contents → assistant prose → decisions only.

## Architecture

| Component | File | Status |
|---|---|---|
| SessionStart hook handler | `internal/mcpserver/hook_session_start.go` | NEW |
| Relevance scorer | `internal/resume/scorer.go` | NEW |
| Token-cost estimator | extend `internal/resume/builder.go` with `EstimateTokens(variant)` | EXTEND |
| Slash command | extend `internal/mcpserver/slashcommands.go` to register `/klyne:resume` | EXTEND |
| Hook installer | extend `internal/mcpserver/install.go` for SessionStart | EXTEND/AUDIT |
| Trimmer | `internal/resume/trim.go` — pure function over variant + budget | NEW |

## Data flow

1. SessionStart event fires → klyne hook handler invoked.
2. Handler reads cwd from event payload + queries `sessions WHERE project_path=cwd ORDER BY last_msg_at DESC LIMIT 20`.
3. For each candidate, compute score (cheap; all from SQLite + 1 git diff call).
4. Top-3 with score ≥ 0.55 surfaced as a system message:
   > klyne: yesterday's session on `auth_middleware.go` — 3 decisions, 12 files, 14h ago. Resume? `/klyne:resume 1` (~6K tokens) · `--decisions-only` (~800)
5. User invokes `/klyne:resume 1`.
6. Resume builder hydrates: decisions for that session_id, last 8 turns, open-file context. Trimmer enforces 8K budget.
7. Inject as `additionalContext` on the user's next prompt.

## Algorithm — scoring

```
score = 0.35 * exp(-hours_since_last_msg / 48)            // half-life ~33h
      + 0.25 * cwd_match                                   // 1.0 exact, 0.6 parent, 0.3 sibling
      + 0.25 * |touched ∩ git_recent| / |touched ∪ git_recent|
      + 0.15 * cosine(session_summary_embed, recent_user_prompts_embed)
```

`git_recent = git diff --name-only HEAD~5 ∪ uncommitted`.

In v0 the topic-embedding term is dropped (weight redistributed) since `summaries` table embeddings may not be populated for all sessions. Threshold: surface candidates with `score ≥ 0.55`, cap at top-3.

## Algorithm — trimmer cut order

When payload exceeds budget, drop in this order:

1. Drop turns older than the most recent decision timestamp (decisions are the compressed signal).
2. Drop tool_result payloads > 500 tokens; keep tool_use args.
3. Drop file contents; keep file paths + line ranges.
4. Drop assistant prose; keep user asks + decisions verbatim.
5. Last resort: decisions only (~800 tokens), with `/klyne:recall` pointer for the rest.

## Testing

- Unit: scorer truth table (8+ cases covering recency decay, cwd hierarchy, git overlap edge cases, empty git_recent).
- Unit: trimmer cut-order property tests (budget always respected, decisions never cut first).
- Golden: `internal/resume/testdata/scenarios/` with 3 fixture sessions; assert top-3 ranking matches expected.
- Integration: hook handler happy-path (3 candidates) + no-candidates path (cold project) + degenerate path (1 weak candidate, score < 0.55).

## Rollout

- Behind `KLYNE_AUTORESUME=1` env var on first ship; default off.
- `/klyne:resume --dry-run <rank>` shows what would be injected without injecting.
- `klyne resume list` CLI prints ranked candidates without invoking the agent.
- README hero copy: "Resume any session, on any machine — with receipts."

## Today-shippable v0

**Skip topic embedding** (drop the 0.15 term, redistribute to recency + cwd). **Skip the SessionStart hook auto-surface** — instead expose `/klyne:resume` slash command + a manual `klyne resume list` CLI that prints ranked candidates. User invokes it; not auto-fired. Removes hook plumbing risk and lets us validate the scorer against real usage before automating.

**Estimate: 5–7 hours.**

## Time estimate

- **v0 today** (manual `/klyne:resume` command + `klyne resume list` CLI, no SessionStart hook): 5–7h
- **v1** (SessionStart auto-surface + topic embedding term): +2 days
- **v2** (cross-machine sync via existing SQLite + reconcile): +1 day

## Risks + mitigations

| Risk | Mitigation |
|---|---|
| Score thresholds wrong → annoying offers | v0 manual-only sidesteps; v1 calibrate against fixture set + telemetry |
| Trimmer drops something important | Cut order ranks decisions last; user can override with `--last-N` |
| Duplicate work vs. claude-mem (75k stars) | Differentiator is the **receipt** + scoping; never silent inject |
| `--budget=` UX is confusing | Default 8K; use natural-language sizes ("small=2K, medium=8K, large=16K") |

## Devil's-advocate counter

claude-mem already won on memory framing. Counter: don't compete on memory — compete on **honesty + scoping**. The receipt (token-cost shown before injection) is the moat. If klyne also ships silent auto-inject like claude-mem, it loses.

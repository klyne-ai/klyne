# Context X-ray — Design Brief

**Date:** 2026-05-15
**Status:** Draft for review
**Owner:** klyne core

## Pain

"Lost in the middle" + The Register: ["Claude Code has become dumber, lazier"](https://www.theregister.com/software/2026/04/06/claude-code-has-become-dumber-lazier-amd-director/5228799) — AMD director's telemetry over 6,852 sessions: 73% collapse in median thinking length, up to 80× more retries. Plus GH [#29971 "MCP skill injection wastes ~25K tokens per call"](https://github.com/anthropics/claude-code/issues/29971), [#50062 "MCP connectors silent 100K bloat"](https://github.com/anthropics/claude-code/issues/50062), Roborhythms ["Most Claude Code Skills Are Garbage"](https://www.roborhythms.com/best-claude-code-skills-2026/).

## What klyne already has

**Most of this is already built.** Auditing what exists:

- `internal/contexthealth/contexthealth.go` — full `Classify(Input) Result` with:
  - 4 states: `Healthy`, `Drifting`, `Risky`, `RescueNow`.
  - 3 actions: `Continue`, `Compact`, `StartFresh`.
  - `BloatRow[]` with 5 kinds: `file_read`, `file_edit`, `file_mixed`, `command`, `tool_result`.
  - `Signals` struct: `HiddenRatio`, `TopRepeatedFile`, `TopFailedCommand`, `TopicShifted`.
  - Documented eval suite under `docs/eval/context-health/` with thresholds in `docs/marketing/context-rescue-eval.md`.
- `internal/contexthealth/bloat_test.go` + `classifier_test.go` — coverage exists.
- `internal/usage/context_window.go` + `calc.go` — fill computation.
- `internal/mcpserver/tool_context_health.go` + `tool_token_timeline.go` — MCP tools.
- `docs/marketing/context-rescue-eval.md` — eval doc + thresholds.

## What's net-new

The current classifier surfaces a composite verdict (Healthy/Drifting/Risky/RescueNow). Per yesterday's anti-snake-oil verdict, we should **also** ship cheap honest single signals AND extend bloat attribution to MCP servers / skills / hooks (not just file_read / command).

1. **Single-signal advisor surfacing** — when `Signals.HiddenRatio > 3.0` OR cache-hit-rate trajectory drops sharply, surface a **one-cause** message instead of (or alongside) the composite verdict. Reads: "Cache hit 84%→47% over 5 turns — context churning." Less noisy than the composite; falsifiable per-signal.
2. **MCP / Skill / Hook source attribution** — `BloatRow` currently captures file_read / command / tool_result. Add `BloatKindMCPInject`, `BloatKindSkillInject`, `BloatKindHookInject` for SessionStart-injected payloads. Requires reading SessionStart system messages from the JSONL and attributing tokens by source name.
3. **RULER-curve grounding** — fill_pct currently treats the model's advertised window as the denominator. RULER (NVIDIA, 2024) shows effective context degrades past ~25% of advertised on multi-hop reasoning. Add `EffectiveFillPct = ContextFillPct / RULERFactor(model)` and surface in `Signals`.
4. **Cache-hit-rate trajectory** — rolling 5-turn cache hit rate from `cached_*_tokens` columns. Surface as a separate signal.
5. **`klyne audit` CLI** — single command that prints the bloat scorecard for the active session (or by `--session-id`). The screenshot people share.

## Architecture

| Component | File | Status |
|---|---|---|
| Cache-hit trajectory | `internal/contexthealth/cache_trajectory.go` | NEW |
| RULER curve | `internal/contexthealth/ruler.go` + JSON table per model | NEW |
| MCP / Skill / Hook bloat attribution | extend `internal/contexthealth/bloat.go` to read source from SessionStart system messages | EXTEND |
| Single-signal mode | extend `Classify` to optionally return `[]SingleSignal` alongside composite | EXTEND |
| `klyne audit` CLI | `cmd/klyne/audit.go` | NEW |
| MCP tool extension | extend `tool_context_health.go` with `mode=signals\|composite\|both` | EXTEND |
| Eval calibration | extend `docs/eval/context-health/` with per-signal expected outputs | EXTEND |

## Data flow

`klyne audit` invoked:

1. Read active session JSONL via existing connectors (`connectors/claude/watch.go` or `connectors/codex/watch.go`).
2. Compute composite verdict via existing `contexthealth.Classify`.
3. **NEW:** walk SessionStart-region messages (system role, before first user turn), attribute system-prompt tokens to source by parsing JSONL metadata (`mcp_server=serena: 18K`, `skill=superpowers: 8K`, `hook=klyne: 7K`).
4. **NEW:** compute cache trajectory over last 5 user→assistant pairs.
5. **NEW:** compute `EffectiveFillPct` via RULER curve.
6. Render scorecard:

```
klyne audit — session 7c9e (Claude, 2.4h)

Composite:    Risky — context fill 71%, hidden ratio 4.2
Effective:    79% (RULER-adjusted for sonnet-4.6)
Cache:        84% → 47% over last 5 turns ↓ (churning)

Pre-prompt context: 47K tokens
  serena MCP        18K  (last useful call: 3d ago)
  github MCP        12K  (never called this session)
  superpowers       8K
  klyne hooks       7K
  anthropic hooks   2K

Top repetition: src/auth.ts read 6× (likely lost prior content)
```

## Algorithm — RULER curve

A small JSON shipped with klyne mapping `model → effective_factor`:

```json
{
  "claude-sonnet-4-6": 0.45,
  "claude-opus-4-7": 0.55,
  "gpt-4o": 0.40,
  "gpt-5": 0.50,
  "gemini-2.5-pro": 0.45,
  "default": 0.45
}
```

`EffectiveFillPct = min(100, ContextFillPct / factor)`. Backed by RULER paper + replication (NoLiMa, LongBench v2). Documented in this spec with citations.

## Testing

- Existing classifier tests stay green (extension is additive).
- Unit: cache trajectory truth table (rising / flat / falling / churning).
- Unit: source attribution from SessionStart tool/system messages (golden fixture from a real session JSONL).
- Eval: extend `docs/eval/context-health/` cases to label expected `single_signal_top` per scenario.
- Smoke: `klyne audit --session-id <fixture>` snapshot test of full output.

## Rollout

- v0 ships behind `KLYNE_AUDIT_CLI=1`; **single-signal mode is opt-in** via flag (`--signals`); composite stays default.
- After 1 week of internal use → flip single-signal default ON; user can `--composite` to revert.
- README hero copy: "Tells you the 80K tokens your MCP servers ate before you typed."

## Today-shippable v0

`klyne audit` CLI that prints the existing composite + bloat scorecard PLUS new MCP/skill/hook source attribution + cache trajectory. Skip the RULER curve and single-signal mode for v0 — both add bug surface; ship next week. Most of the v0 work is plumbing existing data into a CLI surface.

**Estimate: 4–6 hours.**

## Time estimate

- **v0 today** (CLI surfacing existing classifier + new MCP/skill/hook attribution + cache trajectory): 4–6h
- **v1** (RULER curve + single-signal mode + advisor integration): +2 days
- **v2** (cockpit visualization + opt-in per-source pruning suggestions): +2 days

## Risks + mitigations

| Risk | Mitigation |
|---|---|
| Single-signal advisor causes alarm fatigue | Threshold-based on trajectory slope, not absolute level; user can mute per-signal |
| RULER factors are model-specific and rot | Versioned JSON; ship update path via klyne self-update; default fallback factor 0.45 |
| MCP/skill source not always identifiable from JSONL | Best-effort parse; unattributable bytes go to "unknown" bucket and are still surfaced |
| Bloat audit becomes shame-tool that breaks trust with skill authors | Report only; no auto-disable; users decide what to prune |
| Composite + signals together = noisy | v0 default composite-only; v1 single-signal-only; both available via `--mode` |

## Devil's-advocate counter

Composite drift scores risk being snake oil. Counter: ship the single signals **standalone with cited methodology** (RULER curve, cache-hit math, hidden-ratio formula) and hold the composite "Drift Score" as a v2 feature only after backtesting against klyne's own FTS5 archive. The audit CLI is the honest deliverable today.

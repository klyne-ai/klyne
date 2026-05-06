# 00 · Executive Summary

The v1 14-day plan in §15 of the spec is written as a single-developer sequence. To **parallelize across multiple AI agents**, this plan restructures it as a **DAG of 19 workstreams** running in **5 waves**.

- **Wave 0** is a single bootstrap agent that produces the contracts every other workstream depends on.
- **Waves 1–4** fan out to up to **7 agents in parallel**.
- The **critical path** is `W0 → W1 → W2 → W3 → W11 → W15 → W12 → W16 → W17 → W18` (~12.5 effective single-agent days). Everything else fits inside that window.

## The single most important rule

> **Don't fan out before W0 lands.**
>
> Wave 0 produces every cross-stream contract: `Connector` interface, `Message` struct, SQLite schema, HTTP/SSE shapes, pricing JSON, config TOML, repo skeleton, CI. A 30-minute review of those by you (the human) saves days of rework later.

## How parallelism is enforced

- **Single-writer rule.** Each workstream owns disjoint files/directories. No two agents touch the same file at the same time.
- **W0-owned contracts.** Every type that crosses workstream boundaries lives in exactly one W0 file. Modifying these requires a labeled `contract-change` PR with human review — not a silent edit.
- **Worktrees, not branches in shared trees.** Each agent works in its own `git worktree` at `~/agentdeck-worktrees/W{N}-{slug}`.
- **Coverage + lint + type-check gates** in CI on every PR.

## Workstream count by wave

| Wave | Days | Concurrent workstreams | Max agents |
|---|---|---|---|
| 0 | 1–2 | W0 | 1 |
| 1 | 2–4 | W1, W4, W5, W6, W9, W10, W13 | **7** |
| 2 | 4–7 | W2, W3, W7, W8, W11, W14 | **6** |
| 3 | 8–10 | W12, W15 | 2 |
| 4 | 11–13 | W16, W17 | 2 |
| 5 | 14 | W18 | 1 + ad-hoc |

## Three biggest risks (full register in [`05-risk-register.md`](./05-risk-register.md))

1. **Contract drift** — two agents redefine the same type with subtle differences. Mitigated by W0 owning every cross-stream type and CI label-gating contract changes.
2. **TS/Go type drift** — the SvelteKit frontend types diverge from the Go DTOs. Mitigated by `ui/scripts/check-contracts.ts` running in CI (built in W13).
3. **Wave 3 integration finds a contract bug** that ripples to all upstream workstreams. Mitigated by reserving a "contract revision" slot in Wave 3 and treating it as a hard dependency for affected workstreams.

## Success definition for this plan

On Day 14, all four user flows in spec §6 (install, daily, `/compact` recovery, find old thread) work end-to-end on a fresh machine, every spec §12 performance budget is green, and a `v1.0.0` tag is pushed with multi-arch artifacts.

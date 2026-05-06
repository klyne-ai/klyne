# agentdeck — Multi-Agent Build Plan

> **Companion to:** [`compass_artifact_wf-d189e421-ff1d-443c-95b8-19b52fcd59b4_text_markdown.md`](../../compass_artifact_wf-d189e421-ff1d-443c-95b8-19b52fcd59b4_text_markdown.md) (the v1 shipping spec).
> **Purpose:** Decompose the 14-day v1 plan into independent workstreams that AI agents can execute in parallel without colliding.

---

## How to use this folder

### If you are a **human** kicking off the project
Read in this order:
1. [`00-executive-summary.md`](./00-executive-summary.md) — what this plan is, in one screen.
2. [`02-dependency-graph.md`](./02-dependency-graph.md) — see the DAG, critical path, parallel layers.
3. [`03-execution-waves.md`](./03-execution-waves.md) — the 5 waves and merge points.
4. [`06-kickoff-sequence.md`](./06-kickoff-sequence.md) — the 5 commands to run today.

### If you are an **AI agent** dispatched to build a workstream
1. Open the **single file** matching your workstream ID under [`workstreams/`](./workstreams/) — it is self-contained and paste-ready.
2. Re-read [`04-shared-contracts.md`](./04-shared-contracts.md) before touching code that crosses module boundaries.
3. Honor the **hard boundaries** section in your workstream file. Single-writer rule applies.

---

## Folder layout

```
docs/plan/
├── README.md                       ← you are here
├── 00-executive-summary.md         ← TL;DR
├── 01-workstreams-overview.md      ← one-table view of all 19 workstreams
├── 02-dependency-graph.md          ← DAG + critical path
├── 03-execution-waves.md           ← Waves 0–5 with merge points
├── 04-shared-contracts.md          ← W0 deliverables (frozen)
├── 05-risk-register.md             ← multi-agent execution risks
├── 06-kickoff-sequence.md          ← do-this-today checklist
├── _raw_plan.md                    ← original Plan-agent output (archive)
└── workstreams/                    ← paste-ready brief per workstream
    ├── W00-bootstrap.md
    ├── W01-store-db.md
    ├── W02-store-daos.md
    ├── W03-store-search.md
    ├── W04-connector-claude.md
    ├── W05-connector-codex.md
    ├── W06-config.md
    ├── W07-api-handlers.md
    ├── W08-sse-hub.md
    ├── W09-cost-engine.md
    ├── W10-ai-providers.md
    ├── W11-selector-summarize.md
    ├── W12-app-wiring.md
    ├── W13-ui-shell.md
    ├── W14-ui-components.md
    ├── W15-compact-wizard.md
    ├── W16-perf-bench.md
    ├── W17-release.md
    └── W18-alpha.md
```

---

## Universal rules for every workstream

These apply to every agent dispatched against this plan. Each workstream file repeats them, but they live here as the source of truth.

1. **Spec is law.** §18 decisions are LOCKED. Do not propose Rust, React, Postgres, WebSockets, OAuth re-use, or any other replaced choice.
2. **TDD-first.** Write tests before implementation. ≥80% coverage on changed lines (CI enforces).
3. **Single-writer rule.** Touch only files listed under your workstream's "Owned paths". Need to change something else? Open a PR against the relevant W0-owned contract file (see [`04-shared-contracts.md`](./04-shared-contracts.md)).
4. **Run the quality gate** after any change >30 lines: `make ci` (set up by W0). If your environment can't run it, ask the human to.
5. **Use the `superpowers:verification-before-completion` skill** before claiming done.
6. **Worktree isolation.** Each agent works in its own git worktree at `~/agentdeck-worktrees/W{N}-{slug}` on branch `wave{X}/W{N}-{slug}`. No two agents share a working tree.
7. **Performance budgets** in spec §12 are hard requirements, not aspirations.

---

## Decision log (locked — do not relitigate)

| Decision | Source |
|---|---|
| Backend: Go 1.23+ | spec §5, §18 |
| SQLite via `modernc.org/sqlite` v1.44.3 (pure Go, CGO-free) | spec §5, §10, §18 |
| Frontend: SvelteKit 5 + Tailwind 4 + adapter-static | spec §5, §10, §18 |
| Real-time: SSE (no WebSockets) | spec §5, §18 |
| Search: FTS5 BM25 (sqlite-vec opt-in v1.1) | spec §5, §18 |
| v1 connectors: Claude Code + Codex CLI only | spec §9, §17, §18 |
| **No OAuth re-use, ever** (Anthropic ToS, fully enforced 2026-04-04) | spec §8, §17, §18 |
| Telemetry: off in v1 | spec §18 |
| Single repo; UI in `ui/` embedded into Go binary via `embed.FS` | spec §11, §18 |
| Distribution: brew + curl-bash + winget on Day 14 | spec §15, §18 |

---

## Status board (fill in as you go)

| Wave | Workstream | Owner / agent | Branch | Status |
|---|---|---|---|---|
| 0 | W0  | Opus Lead + 2 Sonnet helpers | `main` | 🟢 merged (see [W0-INTEGRATION-NOTES.md](./W0-INTEGRATION-NOTES.md)) |
| 1 | W1  | _unassigned_ | — | ⬜ not started |
| 1 | W4  | _unassigned_ | — | ⬜ not started |
| 1 | W5  | _unassigned_ | — | ⬜ not started |
| 1 | W6  | _unassigned_ | — | ⬜ not started |
| 1 | W9  | _unassigned_ | — | ⬜ not started |
| 1 | W10 | _unassigned_ | — | ⬜ not started |
| 1 | W13 | _unassigned_ | — | ⬜ not started |
| 2 | W2  | _unassigned_ | — | ⬜ not started |
| 2 | W3  | _unassigned_ | — | ⬜ not started |
| 2 | W7  | _unassigned_ | — | ⬜ not started |
| 2 | W8  | _unassigned_ | — | ⬜ not started |
| 2 | W11 | _unassigned_ | — | ⬜ not started |
| 2 | W14 | _unassigned_ | — | ⬜ not started |
| 3 | W12 | _unassigned_ | — | ⬜ not started |
| 3 | W15 | _unassigned_ | — | ⬜ not started |
| 4 | W16 | _unassigned_ | — | ⬜ not started |
| 4 | W17 | _unassigned_ | — | ⬜ not started |
| 5 | W18 | _unassigned_ | — | ⬜ not started |

Legend: ⬜ not started · 🟡 in progress · 🟢 merged · 🔴 blocked

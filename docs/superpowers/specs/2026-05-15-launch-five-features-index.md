# klyne — Five-Feature Launch Index (2026-05-15)

This directory holds five design briefs for the launch features identified in yesterday's research session (parallel-agent sentiment scan + competitive analysis — see conversation history). Each brief is independently implementable; none have hard cross-feature dependencies.

## Briefs

| # | Brief | Pain (validated) | Existing modules → status | v0 estimate |
|---|---|---|---|---|
| 1 | [Compact Shield](2026-05-15-compact-shield-design.md) | Auto-compact destroys context (5/5) | `precompact.go` READ exists; need `PreCompact` HOOK + snapshot table | 6–8h |
| 2 | [Auto-Resume with Receipts](2026-05-15-auto-resume-receipts-design.md) | `--resume` famously broken (5/5) | `resume/builder.go` + `tool_bootstrap.go` exist; need scorer + slash command | 5–7h |
| 3 | [Pre-Action Safety Net](2026-05-15-pre-action-safety-net-design.md) | Agent runs `git reset --hard` (5/5, **highest viral**) | greenfield (hook plumbing exists) | 5–7h |
| 4 | [Cost-Per-Outcome + Waste Digest](2026-05-15-cost-per-outcome-design.md) | $100/month surprises (4/5) | every column exists (migrations 004/005/008/009); need `work_spans` + attribution | 6–8h |
| 5 | [Context X-ray](2026-05-15-context-xray-design.md) | MCP/skill bloat + drift (5/5 + 4/5) | full `contexthealth` classifier exists; need CLI + source attribution | 4–6h |

**Total v0 effort across all 5: 26–36 hours.** Realistic to ship 2 today; queue the remaining 3 for the week.

## Recommended order to ship today

1. **Pre-Action Safety Net (5–7h)** — greenfield + viral demo + safety shouldn't wait. Ship first.
2. **Context X-ray v0 (4–6h)** — CLI command surfacing existing classifier data + new MCP/skill source attribution. Cheapest, highest screenshot yield, builds confidence.

That's ~10 hours of focused work for two shippable features today, both with strong demos. Compact Shield, Auto-Resume, and Cost-Per-Outcome go this week.

## Cross-cutting design principle

Every feature respects klyne's existing brand: **"surface what current-Claude cannot see; never decide for the user."**

- Compact Shield asks before clearing.
- Auto-Resume asks before injecting (with a token receipt).
- Safety Net snapshots-then-allows (or asks before blocking).
- Cost-Per-Outcome shows the bill, doesn't hide it.
- X-ray reports, doesn't auto-prune.

The consistency turns 5 features into 1 product. The opposite of claude-mem's "trust us, we'll inject the right stuff."

## Existing modules touched

| Module | Touched by briefs |
|---|---|
| `internal/mcpserver/precompact.go` | 1 |
| `internal/mcpserver/hook_install.go` + `install.go` | 1, 2, 3 |
| `internal/mcpserver/tool_bootstrap.go` | 2 |
| `internal/mcpserver/slashcommands.go` | 2 |
| `internal/store/decisions.go` + `sessions.go` + `messages.go` | 1, 2, 4 |
| `internal/resume/builder.go` | 2 |
| `internal/contexthealth/contexthealth.go` + `bloat.go` | 1, 5 |
| `internal/cost/pricing.go` + `refresh.go` | 4 |
| `internal/audit/groundtruth_codex.go` | 4 (pattern reuse) |
| `internal/api/handlers/cost.go` | 4 |

## New migrations

The working tree currently has uncommitted `010_runbook_dismissals.sql` and `011_stop_summaries.sql`. New migrations land after them:

| # | Purpose | Brief |
|---|---|---|
| 012 | `shield_snapshots` | 1 |
| 013 | `safety_snapshots` | 3 |
| 014 | `work_spans` | 4 |

## New env-var feature flags

| Flag | Default | Brief |
|---|---|---|
| `KLYNE_SHIELD` | `0` (off) | 1 |
| `KLYNE_AUTORESUME` | `0` (off) | 2 |
| `KLYNE_SAFETY_NET` | `1` (**on**) | 3 |
| `KLYNE_COST_ATTRIBUTION` | `0` (off) | 4 |
| `KLYNE_AUDIT_CLI` | `0` (off) | 5 |

Safety Net defaults ON because data-loss prevention shouldn't be opt-in.

## Naming + branding caveat

These are working names. Before public launch, run a naming pass:

- "Compact Shield" / "Intelligence Cliff" — pick one for the README
- "Receipts" — clear; keep
- "Safety Net" — clear; keep
- "Waste Digest" — pairs with "Cost-Per-Outcome"; keep both
- "X-ray" — strong visual; keep

The README launch copy from yesterday's research is the one to test against.

## What we explicitly skipped (and why)

From yesterday's sentiment scan, several validated pains do **not** make this launch:

- **5-hour cap dashboard** — Anthropic doubled limits in March 2026; existing token timeline is enough.
- **Cross-machine sync as a flagship** — falls out for free as a side effect of Auto-Resume (SQLite is syncable via git/dropbox/icloud).
- **Hook config doctor** — niche power-users only.
- **Multi-agent orchestration** — ruflo / claude-flow at 50k stars own this lane.
- **Skills authoring** — Superpowers and ECC own this lane; klyne is the layer underneath.
- **Composite Drift Score** — held until backtested against klyne's own FTS5 archive (per Quality Decay agent's anti-snake-oil verdict).

## Process notes

- These briefs are the brainstorming output per [`superpowers:brainstorming`](https://github.com/obra/superpowers/blob/main/skills/brainstorming/SKILL.md). Implementation plans (file-level checklists, test cases, schema DDL) come next via [`superpowers:writing-plans`](https://github.com/obra/superpowers/blob/main/skills/writing-plans/SKILL.md), one plan per brief at the moment we commit to building it.
- Each brief is intentionally lightweight (~700–900 words) so all 5 can be read in 30 minutes and decisions made about ship order. Depth-first plans are produced one at a time on commitment.

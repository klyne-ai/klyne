# Showcase Harness — Overnight Build Handoff

**Branch:** `worktree-showcase-harness` (worktree at `.claude/worktrees/showcase-harness/`)
**Date:** 2026-05-18
**No PR opened, no push.** Review locally first.

## What got built

A Node test harness at `test/scenarios/` that drives real `claude -p` sessions
through multi-turn flows and asserts klyne's features work end-to-end.
Pure Node (no npm deps), uses `sqlite3` CLI for DB inspection.

```
test/scenarios/
├── package.json          # type:module, no deps
├── README.md             # full usage + scenario table
├── runner.js             # discovers + runs scenarios, writes report
├── lib/
│   ├── claude.js         # spawn claude -p, parse JSON, capture cost
│   ├── klyne.js          # SQLite query/cleanup helpers via sqlite3 CLI
│   ├── tmpdir.js         # mktemp + git init + seed klyne hook
│   ├── assert.js         # rich-error assertions
│   └── report.js         # markdown report renderer
├── scenarios/
│   ├── 01-worklog-signal-not-noise.js   # trivial vs meaningful → suppression behavior
│   ├── 02-worklog-suppression-rules.js  # read-only rule
│   ├── 03-memory-cross-session.js       # remember in A, recall in B
│   ├── 04-reflection-synthesis.js       # /klyne:reflect citation invariant
│   └── 05-runbooks-recurring-commands.js  # /klyne:runbooks (treated as "planning")
└── reports/              # generated per run (gitignored)
```

Plan doc: `docs/superpowers/plans/2026-05-18-claude-scenario-harness.md`.

## How to run

```bash
cd test/scenarios
npm run test:smoke       # one cheap scenario (~$0.25)
npm test                 # full sweep (~$2-3)
npm run test:dry         # no claude calls, just print intent
npm run test:keep        # keep tmp dirs for debugging
```

Reports land in `test/scenarios/reports/<ISO>.md`. Exit 0 = all PASS, 1 = any non-PASS.

## What I tested overnight

Ran Scenario 01 (worklog signal-not-noise) three times with iterations.
Total spend: ~$0.85.

**Confirmed working at the DB level:**
- klyne Stop hook fires for `claude -p` invocations when seeded via project-level
  `.claude/settings.json` (the harness auto-seeds this in every tmp project)
- Suppression rules behave correctly: trivial sessions produce `recap_visible=0`
  rows or no rows at all; sessions that produce a `git commit` produce
  `recap_visible=1` rows with importance≥5
- Example real row from a harness run:
  `klyne-showcase-meaningful-V8wfNL · recap_visible=1 · importance=5`

**Three real findings:**

1. **Project-level hooks need `--settings <file>` in `-p` mode.** The default
   `claude -p` does not load project `.claude/settings.json` hooks unless you
   pass `--settings <path>` and `--setting-sources user,project,local`. The harness
   does both. Without this, klyne's worklog never captures `-p` sessions in
   ephemeral dirs. Worth verifying whether real users hitting `-p` get hook
   coverage in their normal flow.
2. **Suppression rules are stricter than "files were edited".** A meaningful-looking
   session that edits 2 files but doesn't commit can still get suppressed (no
   qualifying signal tag). Worth confirming this matches your intent — it may
   surprise users.
3. **Hook latency vs harness polling.** Stop hooks fire *asynchronously* after
   `claude -p` exits. Rows can take 5–60s to appear in SQLite. Scenario 01's poll
   was bumped to 90s with 1s intervals; even so I observed runs where the row
   appeared just outside the window. Added a `finalRows` diagnostic in the
   report so you can distinguish "hook never fired" from "hook fired late".

## Open question for you

**Requirements said "planning" was one of three features shipped, but no
`planning` surface exists in klyne** (verified via grep across the repo,
slash commands, MCP tools, and skill listings). Scenario 05 currently tests
**`/klyne:runbooks`** as the closest match (recurring command capture).
If that's wrong, tell me what "planning" was supposed to mean and I'll add the
right scenario.

## Status per scenario

| #  | Scenario               | Status                          | Notes |
|----|------------------------|---------------------------------|-------|
| 01 | worklog signal/noise   | Built; smoke run inconclusive   | DB shows correct rows after run; harness poll sometimes misses them. See finding 3 above. |
| 02 | suppression rules      | Built; not smoke-tested yet     | Cheap to run (~$0.05); minimal risk. |
| 03 | memory cross-session   | Built; not smoke-tested yet     | Will SKIP if klyne MCP not registered for the harness's `claude -p` runs — diagnostic message included. |
| 04 | reflection synthesis   | Built; not smoke-tested yet     | Costliest scenario (3 seed sessions + reflect = ~$0.80). Run only when ready. |
| 05 | runbooks ("planning")  | Built; not smoke-tested yet     | First step is a SKIP that surfaces the planning-vs-runbooks ambiguity. |

## What I'd do next (when you wake)

1. **Confirm the "planning" feature** so Scenario 05 can be corrected.
2. **Run `npm run test:smoke`** yourself — verify the finalRows diagnostic surfaces
   the meaningful row even when the poll window misses it. If it does, we can
   either bump the poll window further or switch to "wait until cleanup" semantics.
3. **Run full sweep** (`npm test`) for the first complete pass. Budget ~$2-3.
4. **Decide on long-term home for the harness** — currently at `test/scenarios/`.
   Could move to `examples/scenarios/` if you prefer.

## Files I touched

Created:
- `docs/superpowers/plans/2026-05-18-claude-scenario-harness.md`
- `test/scenarios/**` (12 files)
- `HANDOFF.md` (this file)

No existing files were modified. The pre-existing UI changes on `init` were
not touched — they're on the original branch, this work is on
`worktree-showcase-harness`.

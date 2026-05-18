# klyne scenario harness

End-to-end functional verification of klyne's worklog, memory, reflection, and
runbooks features by driving real `claude -p` sessions and asserting against
klyne's SQLite state.

**What this is:** test/proof scaffolding. Each scenario is a real multi-turn
flow that exercises one feature and asserts the expected outcome.

**What this is NOT:** marketing demos. Output is PASS/FAIL with evidence, not
hand-curated examples.

## Prerequisites

- Node 20+
- `claude` CLI on PATH, authenticated
- `klyne` binary on PATH (`klyne --version` works)
- `~/.klyne/klyne.db` exists (klyne has run at least once on this machine)
- klyne MCP server / hooks installed (verify by checking that recent
  `~/.claude/projects/<encoded>/<id>.jsonl` files contain `"command":".../klyne session-end"`)

## Run

```bash
# One cheap scenario, ~$0.10
npm run test:smoke

# Full sweep, ~$2-3 of API spend
npm test

# Dry run (no claude invocations, just print intent)
npm run test:dry

# Keep tmp project dirs for debugging
npm run test:keep
```

Flags:

- `--smoke` — run only Scenario 01 (worklog signal-not-noise), cheapest
- `--only=<glob>` — run scenarios matching glob (e.g. `--only=04-*`)
- `--keep-tmp` — leave tmp project dirs in place for inspection
- `--no-claude` — skip `claude -p` invocations, print intent only (no cost)

## Reports

Each run writes `reports/<ISO>.md` with per-scenario results, including:

- PASS / FAIL / SKIP per assertion step
- Captured `claude -p` stdout snippets
- Relevant SQL rows from `~/.klyne/klyne.db`
- Cost estimate per scenario

Exit code is 0 if all scenarios PASS, 1 otherwise.

## Scenarios

| #  | Feature                  | What it proves                                                                                          |
|----|--------------------------|---------------------------------------------------------------------------------------------------------|
| 01 | Worklog signal-not-noise | Trivial sessions produce no visible worklog entry; meaningful sessions do.                              |
| 02 | Suppression rules        | Specific rules from `internal/worklog/suppress.go` fire correctly (read-only skip, sub-90s trivia, …).  |
| 03 | Memory cross-session     | Fact stored via klyne memory in session A is retrievable in session B (different session, same project).|
| 04 | Reflection synthesis     | `/klyne:reflect` over pending entries produces a `worklog_reflections` row honoring citation invariant. |
| 05 | Runbooks (planning?)     | `/klyne:runbooks` surfaces recurring command patterns from past sessions.                               |

> **Open question:** the requirements named "planning" as one of three features
> shipped, but no `planning` surface exists in the codebase. Closest match is
> `/klyne:runbooks` (recurring Bash detection). Scenario 05 tests runbooks and
> flags this assumption.

## How isolation works

Each scenario:

1. Creates a fresh tmp project at `/tmp/klyne-showcase-<scenario>-<random>/`
2. `git init`s it
3. Runs `claude -p` with that dir as cwd
4. Queries klyne's real `~/.klyne/klyne.db`, filtered to the tmp `project_path`
5. Cleans up: deletes its rows from klyne.db, removes tmp dir

This means scenarios share the user's real klyne install but cannot
collide with real work since `project_path` is unique per run.

## Cost guard

`runClaude()` invokes `claude -p --max-budget-usd 0.50` per turn. If a runaway
prompt would exceed that, the run aborts before billing more.

## Caveats

- These tests touch the user's real `~/.klyne/klyne.db`. Cleanup is best-effort
  but if a scenario crashes mid-run, you may have leftover rows scoped to the
  tmp project_path (harmless — query for `project_path LIKE '/tmp/klyne-showcase-%'`
  and delete if you want).
- `claude -p` is non-deterministic. Assertions are intentionally loose
  (substring matches, count thresholds) rather than exact-text equality.

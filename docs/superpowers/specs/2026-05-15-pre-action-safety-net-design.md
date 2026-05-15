# Pre-Action Safety Net — Design Brief

**Date:** 2026-05-15
**Status:** Draft for review
**Owner:** klyne core

## Pain

Top emotional pain in the sentiment scan, **highest viral upside if solved**. GitHub [#7232 git reset --hard data destruction](https://github.com/anthropics/claude-code/issues/7232), [#34327 destroyed uncommitted work twice](https://github.com/anthropics/claude-code/issues/34327), [#46444 worktree auto-cleanup deleted 10 days of work](https://github.com/anthropics/claude-code/issues/46444), [#23913 agent deleted 2,229 untracked files](https://github.com/anthropics/claude-code/issues/23913). Kelsey Piper's ["I Can't Stop Yelling at Claude Code"](https://www.theargumentmag.com/p/i-cant-stop-yelling-at-claude-code) went viral on this exact pain. **Zero competitors solve this.**

## What klyne already has

- `internal/mcpserver/hook_install.go` + `internal/mcpserver/install.go` — hook install plumbing.
- `internal/connectors/claude/watch.go` — knows tool-call shape from JSONL.
- `internal/store/sessions.go` — sessions table (snapshots can be keyed here).
- `internal/audit/groundtruth_codex.go` — pattern of parsing Bash tool calls already exists.
- Working tree has `internal/store/runbook_dismissals.go` and `010_runbook_dismissals.sql` — established pattern for "user-acknowledged warnings."

## What's net-new

1. **PreToolUse hook handler** — Claude Code emits a `PreToolUse` hook event for every tool call. Klyne registers this and inspects the payload.
2. **Risky-command pattern matcher** — versioned `policy/risky_commands.json` shipped with klyne, user-overridable in `.claude/klyne-policy.json`.
3. **Shadow-worktree snapshot** — `git stash create --include-untracked` (lightweight, returns SHA) for git repos; `cp -r` to `.klyne/snapshots/<ts>/` fallback for non-git dirs.
4. **`klyne restore <id>` CLI** — list snapshots + restore one.
5. **Auto-prune** — drop snapshots > 30 days (cron-style on klyne daemon tick).

## Architecture

| Component | File | Status |
|---|---|---|
| PreToolUse hook handler | `internal/mcpserver/hook_pretool.go` | NEW |
| Risky-command policy + loader | `policy/risky_commands.json` + `internal/policy/policy.go` | NEW |
| Snapshot writer | `internal/safety/snapshot.go` | NEW |
| Snapshot store | `internal/store/safety_snapshots.go` + migration `013_safety_snapshots.sql` | NEW |
| Restore CLI | `cmd/klyne/restore.go` (new sub-command) | NEW |
| Hook installer | extend `internal/mcpserver/install.go` to register `PreToolUse` | EXTEND |

## Data flow

1. Agent emits `Bash(git reset --hard HEAD~3)`.
2. PreToolUse hook fires with `{tool: "Bash", input: {command: "git reset --hard HEAD~3"}}`.
3. Handler runs pattern match against policy JSON. Match → `risky=true, severity=high, snapshot=true`.
4. Handler runs `git stash create --include-untracked` → returns `stash_sha`. Records `{ts, session_id, command, stash_sha, cwd}` to `safety_snapshots` table.
5. Handler emits `systemMessage`:
   > ⚠️ klyne: snapshotted 14 files (uncommitted diff + 3 untracked) before `git reset --hard`. Run `klyne restore 7` to undo.
6. Handler returns empty (allows the tool call). Agent proceeds.
7. For `severity=critical` (e.g. `git push --force` to main without `--force-with-lease`), handler returns `{decision:"block","reason":"add --confirm to override"}`.
8. User runs `klyne restore 7` → reads snapshot row → `git stash apply <stash_sha>` (or `cp -r` for non-git fallback).

## Algorithm — pattern policy

```json
{
  "version": 1,
  "patterns": [
    {"id": "git-reset-hard",     "regex": "^git\\s+reset\\s+--hard",             "severity": "high",     "snapshot": true},
    {"id": "git-clean-fd",       "regex": "^git\\s+clean\\s+-fd",                "severity": "high",     "snapshot": true},
    {"id": "git-checkout-dot",   "regex": "^git\\s+checkout\\s+--\\s*\\.",        "severity": "high",     "snapshot": true},
    {"id": "git-restore-dot",    "regex": "^git\\s+restore\\s+(\\.|--source)",    "severity": "high",     "snapshot": true},
    {"id": "git-worktree-rm",    "regex": "^git\\s+worktree\\s+remove",           "severity": "medium",   "snapshot": true},
    {"id": "git-branch-D",       "regex": "^git\\s+branch\\s+-D\\s",              "severity": "medium",   "snapshot": true},
    {"id": "rm-rf-broad",        "regex": "^rm\\s+-rf\\s+(/|~|\\$HOME|\\.\\.)",   "severity": "critical", "snapshot": true, "block_unless_confirm": true},
    {"id": "git-push-force-main","regex": "git\\s+push.*--force(?!-with-lease).*\\b(main|master|develop)\\b", "severity": "critical", "block_unless_confirm": true},
    {"id": "schema-drop",        "regex": "(DROP\\s+(TABLE|DATABASE|SCHEMA))",     "severity": "high",     "snapshot": true},
    {"id": "migration-rollback", "regex": "(rollback|down)\\s+(--all|--all-the-way|all)", "severity": "high", "snapshot": true}
  ]
}
```

This 10-pattern v0 list covers ~90% of the "I lost my work" tweets. Users append via `.claude/klyne-policy.json`.

## Testing

- Unit: pattern matcher truth table — for each pattern, 1 positive case + 2 false-positive guards (e.g., `git reset --soft` must NOT match `git-reset-hard`).
- Unit: snapshot writer + `restore` round-trip on a temp git repo using `t.TempDir()`.
- Integration: PreToolUse hook handler with golden hook event JSON.
- E2E smoke: shell script that runs `claude` with klyne installed, agent issues `git reset --hard`, assert snapshot row exists + restore succeeds.

## Rollout

- **Default ON.** This is the safety feature; opt-out feels backwards.
- `KLYNE_SAFETY_NET=0` to disable.
- v0 ships in **snapshot-only mode** (no blocking even on critical patterns) — users get reversibility without changed agent behavior.
- v1 enables `block_unless_confirm` for `severity=critical`.
- README hero copy: "Catches your agent before `git reset --hard`."

## Today-shippable v0

Snapshot-only mode (no blocking) with the 10-pattern policy. `klyne restore` CLI sub-command. No snapshot pruning yet (manual cleanup OK for v0). No user-override policy file (only the shipped JSON loads in v0).

**Estimate: 5–7 hours.**

## Time estimate

- **v0 today** (snapshot-only, 10 patterns, manual restore): 5–7h
- **v1** (block-unless-confirm for critical, prune cron, user policy override loader): +2 days
- **v2** (snapshot diff viewer in UI cockpit, snapshot replay across sessions): +2 days

## Risks + mitigations

| Risk | Mitigation |
|---|---|
| Pattern false-positive blocks legitimate ops | v0 snapshots without blocking — false positives only cost a stash entry, not a workflow halt |
| `git stash create` fails (e.g. detached HEAD with no changes) | Fallback to `cp -r` for the affected paths; log error and allow the call |
| Snapshot fills disk over time | v1 prune cron @ 30 days; surface size in `klyne restore --list` |
| Pattern list rots as users get creative | Versioned policy + user-extensible JSON in v1; track unmatched destructive ops in telemetry to grow list |
| Performance — every Bash call hits the matcher | Compiled regex set, ~µs per call; snapshot only on match |

## Devil's-advocate counter

Pattern lists are inherently incomplete; AST-aware bash parsing is hard. Counter: you don't need 100% coverage — you need to catch the 10 patterns that account for 90% of the "I lost my work" tweets. Ship those, accept the long tail. The user-extensible policy is the relief valve for the long tail.

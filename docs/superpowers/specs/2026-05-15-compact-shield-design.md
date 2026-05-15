# Compact Shield — Design Brief

**Date:** 2026-05-15
**Status:** Draft for review
**Owner:** klyne core

## Pain

Auto-compact silently destroys session intelligence. GitHub [#13112 "Auto compact is the worst"](https://github.com/anthropics/claude-code/issues/13112), [#20051 plan-mode 100% hallucination after compact](https://github.com/anthropics/claude-code/issues/20051), AMD-director telemetry on 6,852 sessions: 73% collapse in median thinking length post-compact, up to 80× more retries. Mentioned in 60–70% of recent r/ClaudeCode "this sucks" threads. **Zero of the 8 surveyed competitors exploit the fact that PreCompact is a *blocking* hook.**

## What klyne already has

- `internal/mcpserver/precompact.go` — full Claude+Codex `LoadPreCompactMessages` two-pass JSONL scanner. **Read path is solved.** This is recovery (post-compact lookup), not active prevention.
- `internal/store/compact_events.go` — events table, populated by `connectors/claude/compact_test.go` and the `insights/compact_boundary` parser (commit 9eec7e8).
- `internal/mcpserver/tool_get_pre_compact_context.go` — MCP read tool the agent calls after compact.
- `internal/mcpserver/hook_install.go` + `stop_hook_install_test.go` — hook install plumbing for `settings.json` exists.
- `internal/contexthealth/contexthealth.go` — `Classify(Input) Result` already returns `StateRescueNow` + `ActionStartFresh` when fill is critical.
- `docs/features/pre-compact-recovery.md` — existing feature doc to extend.

## What's net-new

1. **PreCompact hook handler binary path** — currently klyne installs hooks for UserPromptSubmit and Stop; PreCompact is not wired. Need a small Go entrypoint (extend `cmd/` or reuse the existing hook binary) that the hook command line calls and returns `{"decision":"block","reason":"klyne re-context"}` or empty (lets native run).
2. **Snapshot table** — capture `{decisions_window, open_files, last_N_turns, in_flight_tool_chain_id, pre_tokens, fill_pct}` keyed by session_id + ts. Cheap append-only.
3. **Scoped re-inject on UserPromptSubmit** — when the immediately preceding event in the JSONL is a klyne-blocked compact, inject a tight context payload selected by keyword match against the user's new prompt.
4. **Hard-ceiling fold logic** — at fill ≥92% klyne refuses to block (graceful hand-off to native). "klyne knows when to fold" is a feature.

## Architecture

| Component | File | Status |
|---|---|---|
| PreCompact hook handler | `internal/mcpserver/hook_precompact.go` | NEW |
| Snapshot writer + reader | `internal/store/shield_snapshots.go` + migration `012_shield_snapshots.sql` | NEW |
| Scoped re-inject | extend `internal/mcpserver/hook_install.go` UserPromptSubmit path | EXTEND |
| Hook installer | extend `internal/mcpserver/install.go` to register `PreCompact` | EXTEND |
| Advisor armed-state | extend `contexthealth.Classify` with `StateShieldArmed` when fill ≥70% AND klyne snapshot present | EXTEND |

## Data flow

1. `PostToolUse` (existing) appends to `messages` + `tool_calls_json`.
2. `UserPromptSubmit` (existing advisor) checks fill; at ≥70% calls `WriteSnapshot(session_id, decisions, open_files, last_N_turns, tool_chain_id)`.
3. `PreCompact` hook fires → handler reads latest snapshot, runs the confidence check.
4. If confident: emit `{"decision":"block","systemMessage":"klyne shielded compact (snapshot=<id>)"}`. Otherwise empty (native runs).
5. Next `UserPromptSubmit`: detect "preceding compact was klyne-blocked," fetch snapshot, run keyword-similarity vs. new prompt over `decisions + last_N_turns`, inject top-K within 4K-token budget as `additionalContext`.
6. At fill ≥92%, advisor stops arming + handler refuses to block.

## Algorithm — confidence check

```go
func ShouldBlock(s Snapshot, fill float64) (bool, reason string) {
    if fill >= 0.92 { return false, "fold" }                       // graceful
    if time.Since(s.WrittenAt) > 60*time.Second { return false, "stale" }
    if s.ToolChainCoverage < 0.80 { return false, "incomplete" }
    if estimateReinjectTokens(s) > nativeSummarySize(fill) { return false, "lossy" }
    return true, "shield"
}
```

## Testing

- Golden JSONL fixtures under `internal/mcpserver/testdata/shield/` (auto-compact + manual-compact); reuse fixtures from `precompact_codex_test.go`.
- Unit: `ShouldBlock` truth table — cases for `fold`, `stale`, `incomplete`, `lossy`, `shield`.
- Integration: spin a fake Claude Code hook caller that posts the PreCompact JSON event and asserts klyne's stdout JSON.
- E2E proof recipe: `docs/proof/04-compact-shield/claim.md` mirroring the `pre-compact-recovery` proof shape.

## Rollout

- Behind `KLYNE_SHIELD=1` env var on first ship; default off for one release.
- `klyne shield status` CLI surfaces current arming state + last decision + counter (blocked / folded / stale).
- README hero copy: "Stops the intelligence cliff."
- Add proof page `docs/proof/04-compact-shield/` with split-screen demo recipe.

## Today-shippable v0

The narrowest viable: snapshot writer + PreCompact hook handler that **always blocks at fill 70–92%** and surfaces scoped re-inject on next UserPromptSubmit. Skip the per-block confidence checks (`stale` / `incomplete` / `lossy`) — accept some false-positive blocks in v0; refine in v1. The fill-ceiling fold check stays in v0.

**Estimate: 6–8 hours focused work.**

## Time estimate

- **v0 today** (snapshot + always-block in band + ceiling fold): 6–8h
- **v1** (full confidence checks + advisor `StateShieldArmed`): +2 days
- **v2** (proof eval suite + UI cockpit panel): +1 day

## Risks + mitigations

| Risk | Mitigation |
|---|---|
| False-positive blocks degrade trust | v0 ceiling fold + v1 confidence checks; `klyne shield status` shows hit rate |
| Snapshot includes secrets from tool stdout | Reuse audit pkg redaction; snapshots stay in local SQLite, never network |
| Anthropic ships native `claude_action` (issue #43733) | The substrate (FTS5 + decisions + snapshots) is the moat, not the hook trick |
| Race with manual `/clear` (no PreCompact event) | v1: hook on `SessionEnd(reason=clear)` — known-viable per who96 implementation |

## Devil's-advocate counter

claude-mem (75k stars) solves cross-session memory; Shield does not compete on that axis. Shield's verb is **uptime within a session**. They can coexist; klyne could even read claude-mem's SQLite if a user runs both.

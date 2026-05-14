---
name: klyne-health
description: Use when the current session feels long, when the user asks about context fill / token usage / bloat / "how long can I keep going", when a klyne `UserPromptSubmit` advisory mentions context fill or drift or acceleration or the 5-hour window, after any `/compact` event in recent history, or before loading a large file (>5K tokens) into context. Returns a deterministic context-health verdict plus the top context-bloat sources so the agent can decide whether to continue, scope-handoff, or stop.
---

# klyne-health

Surfaces the deterministic context-health verdict for the active Claude Code or Codex session: token fill against the model's window, classifier state (`healthy / drifting / risky / rescue_now`), and the top bloat sources (which files, tools, or past turns are eating the budget). Reads klyne's local SQLite + JSONL — no AI calls, no network.

This skill exists because Claude has no accurate self-view of its own token usage and no visibility into the 5-hour rate-limit window. Without it the user only finds out a session is in trouble when the next turn stalls or compact bites.

## When to invoke

Invoke when **any** of:

- A klyne `UserPromptSubmit` advisory mentions context fill, drift, acceleration, the 5-hour window, or recommends `/klyne:handoff`. Treat the advisory as a direct instruction to call this skill.
- The user asks about context, token usage, bloat, fill, how much budget is left, or whether to keep going in this session.
- A `/compact` event happened in the recent message history (the session has been compacted at least once).
- You are about to load a file >5K tokens, or run a tool that historically returns >10K tokens of output, and you have already done significant work in this session.
- You notice you are referring to earlier context that you cannot recall in detail — that's a drift signal even without an explicit advisory.

Do **not** re-invoke within the same session unless one of:
- The previous verdict was `healthy` AND at least 10 user turns have passed since.
- A new advisory has fired since the last invocation.
- The user explicitly asks again.

Repeated invocation on a healthy short session wastes tokens. The skill is for inflection points, not status-check polling.

## How to invoke

Call `mcp__klyne__get_context_health` with no arguments. It auto-resolves the active session from the current working directory.

If the response sets `ambiguous: true`, list the candidate session ids and stop — let the user re-run with an explicit `session_id`. Do not guess.

## How to report

Render exactly this. No editorializing. No paraphrasing the reason field. No invented bloat sources.

```
# Context health: <state>

**Recommended action:** `<action>`

<reason>

- Session: `<short session_id>`
- Model: `<model>`
- Context fill: <context_fill_pct>%
- Messages: <msg_count>
```

Then, **only if** the response includes bloat rows:

```
## Top context-bloat sources

1. **<label>** — <share_pct>% of tool output
2. **<label>** — <share_pct>% of tool output
3. **<label>** — <share_pct>% of tool output
```

Top 3 bloat rows only. Skip this section entirely when the response carries no bloat rows.

## What to do after reporting

State the recommended action verbatim from the tool's `action` field. Possible values and how to follow up:

- `continue` — say one sentence acknowledging health is acceptable, then return to the user's original task.
- `consider-handoff` — suggest `/klyne:handoff scope=current` before the user starts the next major task. Do not invoke handoff yourself.
- `handoff-now` — strongly recommend `/klyne:handoff scope=current` and pause for the user's confirmation before doing any further work in this session.
- `compact-pending` — a compact is imminent. Recommend the user capture any critical state (file paths, decisions, runbooks) into `klyne remember this …` before compact lands.

If `action` is empty or unknown, recommend nothing — just report and stop.

## When to stop the skill

After reporting and stating the action. Do not chain into other klyne tools (handoff, search, sessions) — wait for the user to confirm direction. The user is in control.

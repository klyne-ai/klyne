---
name: klyne-bootstrap
description: Use at the very start of a session, when the agent has no prior context in this project, when the user asks "what was I working on?" / "where did I leave off?" / "what's the state of this project?", or before taking the first major action in an unfamiliar codebase. Returns a deterministic briefing of recent sessions, project + global memories, and the most-recent session's context-health verdict — all from klyne's local store with no AI calls. Don't re-invoke within the same session unless the user explicitly asks again.
---

# klyne-bootstrap

Synthesises klyne's existing per-project data into ONE Day-1 briefing the agent can fetch at session start: the last 3 sessions in this project, the 5 most recent project memories, a preview of any global memories, and the most-recent session's context-health verdict. Reads klyne's local SQLite + JSONL — no AI calls, no network.

This skill exists because a fresh Claude Code or Codex session has no idea what the user was working on yesterday. Without it, the agent either asks ("what's this project?") or guesses (and risks contradicting prior decisions). Bootstrap fixes that with a deterministic briefing the agent can read in one round-trip.

## When to invoke

Invoke when **any** of:

- This is the very first agent turn in a project the agent has no recorded context for.
- The user asks "what was I working on?", "where did I leave off?", "what's in this project?", "what's the state of things?", or any variant of "give me a brief".
- You are about to take a non-trivial first action (writing code, running a migration, deploying) in a codebase you have not explored in this session.
- A klyne hook advisory recommends bootstrapping context.

Do **not** re-invoke within the same session unless the user explicitly asks again. The brief is meant for inflection points (session start, "catch me up"), not status-check polling — repeated invocation wastes tokens on data you already saw.

## How to invoke

Call `mcp__klyne__bootstrap` with no arguments. It auto-resolves the project from the current working directory.

The response is structured (`sessions`, `project_memories`, `global_memories_preview`, `latest_health`) plus a pre-rendered `markdown` field for verbatim display.

## How to report

Render the `markdown` field VERBATIM. The server has already produced the Markdown with all four sections — recent sessions, project memories, global memories, and (when populated) current session health — under 80 chars per line, with `_(none)_` placeholders for empty sections so the shape is stable.

No editorializing. No paraphrasing. No re-rendering the structured rows yourself. Output the `markdown` field byte-for-byte.

If the response carries a `latest_health.action` of `handoff-now` or `consider-handoff`, mention it once after the brief — that's the only commentary allowed.

## What to do after reporting

Stop. The brief is context for the agent's NEXT turn — wait for the user to direct the conversation. Do not chain into other klyne tools (recall, health, search, handoff) unless the user explicitly asks. The point of the brief is to give the agent enough context to respond intelligently to whatever the user says next, not to pre-fetch every possible follow-up.

If `latest_health.action` is `handoff-now`, recommend `/klyne:handoff scope=current` and stop. If `action` is `consider-handoff`, suggest it gently. Otherwise say nothing further and wait.

## When to stop the skill

After printing the markdown field (and at most one short health-action sentence). The user is in control from here.

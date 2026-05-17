---
name: klyne-runbooks
description: Use when the user asks "what should I save as a runbook?" / "what runbooks do you recommend?" / "what do I keep redoing?" / "what's repetitive in my workflow?" — OR proactively, when the agent is about to run a sequence it has seen the user run multiple times before. Calls klyne's deterministic pattern detector, returns recurring Bash command sequences from the user's recent sessions in this project (>=3 occurrences across >=2 distinct sessions), and offers them as candidate runbooks the user can accept, dismiss, or edit. No AI in the detection loop.
---

# klyne-runbooks

Surfaces recurring shell-command sequences from the user's recent Claude Code and Codex sessions as candidate *runbooks* — multi-step workflows worth saving to memory so future sessions execute them with one phrase instead of re-typing every step.

This skill exists because the user can't see across their own sessions; klyne can. A workflow done 5 times across 3 sessions is a runbook waiting to happen, but neither the user nor the AI sees the recurrence from inside any single session. klyne walks the JSONL on disk, normalises commands (paths, UUIDs, IPs, timestamps replaced with placeholders), and indexes N-grams that recur across sessions.

## When to invoke

Invoke when **any** of:

- The user asks "what should I save as a runbook?" / "what do I keep redoing?" / "what runbooks do you recommend?" / "what's repetitive?".
- The user has just finished a task and the assistant noticed it looked similar to a sequence run earlier in the conversation OR earlier today.
- You are about to run a multi-step shell sequence (3+ commands) in service of a user request, and you have not yet checked whether klyne has a memory or candidate for it. Call this skill BEFORE running the sequence — it's cheap, deterministic, and the candidate may already be in memory under `recall`.
- The user expresses fatigue ("ugh I always do this", "every single time...", "yet again I'm running...") about a task. Treat as an invitation.

Do **not** re-invoke within the same session unless the user explicitly asks again. The candidate list changes slowly (it depends on whether new occurrences crossed the recurrence threshold). Repeated invocation on the same session wastes tokens on data you already saw.

## How to invoke

Call `mcp__klyne__propose_runbooks` with no arguments. The tool auto-resolves the project from the current working directory and walks the user's recent sessions (default last 90 days, capped at 500 sessions).

The response is structured (`candidates`, `sessions_walked`, `project_path`) plus a pre-rendered `markdown` field for verbatim display.

If `sessions_walked == 0`, klyne has not yet ingested data for this project. Tell the user to run `klyne start` (the daemon) and come back later.

## How to report

Render the `markdown` field VERBATIM. The server has already produced Markdown with all candidates, their occurrence counts, distinct-session counts, last-seen timestamps, scores, and the most-recent observed steps. Each candidate has a stable 12-character id and a deterministic signature you can quote.

No editorialising. No paraphrasing the candidate descriptions. No re-rendering the structured rows yourself.

After printing the markdown, ask the user **one** of these follow-ups:

- "Would you like to save any of these as runbooks? (Reply with the candidate id, or 'none'.)"
- "Should I dismiss any of these so klyne stops suggesting them?"

Pick the question that matches the user's framing. Don't ask both.

## How to accept a candidate

When the user picks a candidate id, call `mcp__klyne__accept_runbook` with:

- `signature`: the exact signature string from the candidate (NOT the id — the id is for display; the tool needs the signature so it can dedupe against the index).
- Optional `name`: only override the auto-suggested name when the user gave you a clearer phrase.
- Optional `body`: only set this when the user explicitly described the runbook in their own words. Otherwise let the tool render a numbered command list.

Confirm the saved `memory_id` and `title` back to the user. The runbook is now retrievable via `recall` (tag filter `runbook`) and `list_memories`.

## How to dismiss a candidate

When the user rejects a candidate, call `mcp__klyne__dismiss_runbook` with:

- `signature`: the candidate's signature.
- `scope`: `"project"` (default) — usually right. Use `"global"` only when the user said something like "stop suggesting this anywhere".
- Optional `reason`: include the user's own words ("just one-off debugging", "this is for an old service we deprecated", etc.).

The signature is added to the project's dismiss list; `propose_runbooks` will never re-surface it for this project (or anywhere, if scope was global).

## What NOT to do

- Do not invent candidate ids. They come from the tool response or not at all.
- Do not auto-accept candidates without user confirmation. The candidate list is a *proposal* — accepting writes durable memory the agent will follow next time.
- Do not chain into other klyne tools (handoff, search, sessions) without the user asking. Surface the proposals, ask one follow-up, and stop.
- Do not normalise or transform the candidate's command list yourself. The tool already shows the most recent un-normalised example for human readability.

## When to stop the skill

After printing the markdown and asking one follow-up, stop and wait for the user's reply. If they pick a candidate, take the accept or dismiss action. If they ignore the question and ask something else, drop the topic — they'll come back if they want to.

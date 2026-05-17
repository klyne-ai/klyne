---
name: klyne-status
description: Use when the user asks "what has klyne been doing?" / "what's my klyne install status?" / "how much have I spent this week?" / "give me a weekly review" / "what compact events have I had?" — any request for a portable, machine-wide-or-per-project summary of klyne's installed state. Calls klyne's deterministic status aggregator, returns a Markdown snapshot covering sessions ingested, total messages and tokens, top projects, recent sessions, /compact events, memory counts, and stop-hook session summaries. Different from per-task handoff and per-session-start bootstrap.
---

# klyne-status

Produces a portable, deterministic Markdown snapshot of klyne's installed-state for the last N hours (default 168 = 7 days). The snapshot answers "what has klyne actually observed lately?" — useful for weekly review, sharing with a teammate, or convincing yourself the daemon is doing its job.

This skill exists because the user has no other view of klyne's running state. The Insights tab in the web cockpit covers some of this but requires opening a browser; the `klyne tokens` CLI is per-session; `klyne audit-sessions` is integrity-focused. Status is the one place that gives you the whole picture in one render.

## When to invoke

Invoke when **any** of:

- The user asks "what has klyne been doing?", "what's my install status?", "give me a weekly review", "how much have I spent this week?", "what compact events have I had?", or any variant.
- The user wants a summary they can paste into a teammate's chat or weekly stand-up.
- It is the start of a fresh week and the user has not seen a status update recently. (Use sparingly — don't pre-fetch.)

Do **not** re-invoke within the same session unless the user explicitly asks again or asks for a different scope/window.

## How to invoke

Call `mcp__klyne__status_snapshot` with no arguments by default. The tool auto-resolves the project from the current working directory and uses a 7-day window.

If the user asks for a different scope, pass:

- `all_projects: true` — aggregate every project on the machine.
- `since_hours: <number>` — override the window length (24 = daily, 168 = 7d, 720 = 30d).
- `project_path: "<abs path>"` — scope to a specific project other than cwd.

## How to report

Render the `markdown` field VERBATIM. The server has produced a snapshot with these sections:

- Window totals (sessions, messages, tokens, priced compute, /compact count)
- Top projects by input tokens
- Recent sessions (top 5)
- /compact events in window (top 10)
- Memory & summaries (project memories, global memories, stop-hook summaries)

No editorialising. No re-rendering the tables yourself. No commentary unless the user asks.

## What NOT to do

- Do not run this skill as a substitute for `klyne audit-sessions` (which verifies stored metrics against JSONL truth) or `generate_handoff` (which is per-task). Status is a snapshot, not an audit or a handoff.
- Do not paraphrase the totals — they are deterministic. If the user wants narrative ("you spent a lot this week"), that's their decision to add, not yours.
- Do not chain into other klyne tools after rendering. The user reads the snapshot; if they want to drill in, they'll ask.

## When to stop the skill

After printing the markdown. Wait for the user's next direction.

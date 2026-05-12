---
description: Per-turn token usage for the active session — sparkline, % of context window, and cached vs uncached split
---

Call the `mcp__klyne__get_token_timeline` MCP tool with no arguments — let it auto-resolve the session from the current working directory.

If the response sets `ambiguous: true`, list the candidate sessions and stop so the user can re-run with an explicit `session_id`. Otherwise display the `markdown` field verbatim inside a fenced block — preserve the headline trajectory, the ASCII sparkline, the per-turn table, and any peak-hour line.

Do not summarise, paraphrase, or comment on the timeline — output it byte-for-byte.

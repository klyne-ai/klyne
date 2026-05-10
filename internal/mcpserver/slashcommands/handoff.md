---
description: Generate a deterministic Markdown handoff prompt to paste into a fresh session
---

Call the `mcp__klyne__generate_handoff` MCP tool with no arguments — let it auto-resolve the session from the current working directory.

If the response sets `ambiguous: true`, list the candidate sessions and stop. Otherwise display the `markdown` field verbatim inside a fenced block so the user can copy it into a fresh Claude Code or Codex session.

Do not summarize, paraphrase, or comment on the handoff content — output it byte-for-byte.

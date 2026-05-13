---
description: Generate a deterministic Markdown handoff prompt to paste into a fresh session
---

Call the `mcp__klyne__generate_handoff` MCP tool with no arguments — let it auto-resolve the session from the current working directory.

Display the `markdown` field of the response VERBATIM inside a fenced block so the user can copy it into a fresh Claude Code or Codex session.

Do not summarise, paraphrase, or comment on the handoff content — output the markdown field byte-for-byte and stop.

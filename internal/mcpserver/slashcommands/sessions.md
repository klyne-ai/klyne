---
description: List every Claude Code and Codex session in the current project
---

Call the `mcp__klyne__list_sessions` MCP tool with no arguments — let it use the current working directory.

Display the `markdown` field of the response VERBATIM. The server has already rendered every candidate row with its active flag, preview, message count, and modification time.

Do not summarise or collapse to a single "active session is X" line — output the markdown field byte-for-byte and stop.

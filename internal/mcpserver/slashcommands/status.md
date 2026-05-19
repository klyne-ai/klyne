---
description: Unified context check — health verdict + recommended action + token timeline + top bloat sources for the active session
---

Call the `mcp__klyne__get_session_status` MCP tool with no arguments — let it auto-resolve the session from the current working directory.

Display the `markdown` field of the response VERBATIM. The server has already rendered the verdict header, the reason, the trajectory headline, the ASCII sparkline, the per-turn table, and the top bloat sources.

Do not summarise, paraphrase, reformat, or add commentary — output the markdown field byte-for-byte and stop.

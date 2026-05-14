---
description: Full-text search across every klyne-indexed Claude + Codex session
---

Call the `mcp__klyne__search_messages` MCP tool with `query` set to: $ARGUMENTS

If $ARGUMENTS is empty, ask the user what to search for and stop.

Otherwise display the `markdown` field of the response VERBATIM. The server has already rendered the daemon-down hint, the empty-result note, or the hit list with session ids, timestamps, and snippets as appropriate.

Do not summarise or reformat — output the markdown field byte-for-byte and stop.

---
description: Day-1 session brief — recent sessions, runbooks, Claude auto-memory, reflections, worklog entries
---

Call the `mcp__klyne__bootstrap` MCP tool with no arguments — let it auto-resolve the project from the current working directory.

Display the `markdown` field of the response VERBATIM. The server has already rendered every section (recent sessions, klyne runbooks, Claude auto-memory, recent reflections, cross-AI worklog entries) with `_(none)_` placeholders for empty ones so the shape is stable.

Do not summarise, paraphrase, or editorialize — output the markdown field byte-for-byte and stop.

For the live context verdict + token timeline, the user runs `/klyne:status`.

---
description: Portable Markdown snapshot of klyne's installation state for this project
---

Call the `mcp__klyne__status_snapshot` MCP tool with no arguments — let it auto-resolve the project from the current working directory.

Display the `markdown` field of the response VERBATIM. The server has already rendered the window totals, top projects, recent sessions, /compact events, and memory + summary counts.

Do not summarise, paraphrase, or editorialize — output the markdown field byte-for-byte and stop.

If the user asks for a wider window or a machine-wide view, call the tool again with `all_projects=true` or a custom `since_hours` value.

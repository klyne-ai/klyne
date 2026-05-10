---
description: Full-text search across every klyne-indexed Claude + Codex session
---

Call the `mcp__klyne__search_messages` MCP tool with `query` set to: $ARGUMENTS

If $ARGUMENTS is empty, ask the user what to search for and stop.

If the response sets `daemon_down: true`, surface the `reason` field so the user knows to start the klyne daemon (`klyne start`).

Otherwise render the hits as Markdown:

```
# Search results for "<query>"

<N> hit(s) · <took_ms>ms

## 1. `<short session_id>` (<cli>)
- Project: `<project_path>`
- Role: <role> · <UTC timestamp>
- Snippet: <one-line snippet>

## 2. ...
```

Show every hit returned. No commentary.

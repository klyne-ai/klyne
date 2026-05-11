---
description: Classify the current session's context health and list the top bloat sources
---

Call the `mcp__klyne__get_context_health` MCP tool with no arguments — let it auto-resolve the session from the current working directory.

If the response sets `ambiguous: true`, list the candidate sessions and stop so the user can re-run with an explicit `session_id`. Otherwise render exactly:

```
# Context health: <state>

**Recommended action:** `<action>`

<reason>

- Session: `<short session_id>`
- Model: `<model>`
- Context fill: <context_fill_pct>%
- Messages: <msg_count>

## Top context-bloat sources

1. **<label>** — <share_pct>% of tool output
2. ...
3. ...
```

Show the top 3 bloat rows only. Do not editorialize — display the data and stop.

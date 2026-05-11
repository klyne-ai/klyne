---
description: List every Claude Code and Codex session in the current project
---

Call the `mcp__klyne__list_sessions` MCP tool with no arguments — let it use the current working directory.

Render the response as Markdown:

```
# Sessions in `<cwd>`

- `<session_id>` [active] — <ModTime> · <MsgCount> msgs · "<Preview>"
- ...
```

Mark sessions with `is_active: true` as **active**. Show every candidate row in the order returned. No commentary — display the data and stop.

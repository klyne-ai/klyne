---
description: Recover the messages immediately preceding the last /compact event
---

Call the `mcp__klyne__get_pre_compact_context` MCP tool with no arguments — let it auto-resolve the session from the current working directory.

If the response sets `ambiguous: true`, list the candidate sessions and stop.

If `found_compact: false`, tell the user this session has not been `/compact`'d yet — nothing to recover — and stop.

Otherwise render:

```
# Pre-compact recovery

Recovered <N> messages from before the last /compact event.

- Trigger: `<trigger>`        # omit if empty (Codex)
- Pre-compact size: <pre_tokens> tokens   # omit if 0 (Codex)
- Compact timestamp: `<compact_timestamp>`

## Messages

**<role>** — <one-line content>

**<role>** — <one-line content>
```

Show every recovered message in order. No commentary.

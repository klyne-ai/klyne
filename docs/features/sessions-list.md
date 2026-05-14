# `/klyne:sessions` — list sessions in the current project

> Status: shipped. MCP tool `list_sessions`. Read-only walk of the JSONL roots.

List every Claude Code + Codex session that belongs to the current working directory, newest first.

## Trigger

```text
/klyne:sessions
```

## What's returned

One row per session:

| Column | Notes |
|---|---|
| `session_id` | Short form, marked `[active]` when the session was modified in the last 30 minutes |
| `modified` | Last-touched timestamp |
| `msg_count` | Total messages in the JSONL |
| `preview` | One-line tail of the most recent assistant message |

The host LLM echoes the rendered markdown table verbatim — no host-side filtering or collapsing.

## When it's useful

- Picking a `session_id` to pass to another klyne tool after an `ambiguous: true` response
- Confirming a stale chat tab is the right resume target before opening Claude Code
- Auditing how many parallel sessions live under one project

## Implementation

- `internal/mcpserver/tool_list_sessions.go` — MCP handler
- `internal/connectors/` — JSONL discovery + session metadata extraction
- `internal/mcpserver/slashcommands/sessions.md` — slash-prompt definition

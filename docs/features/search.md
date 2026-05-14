# `/klyne:search` — full-text search across every session

> Status: shipped. MCP tool `search_messages`. SQLite FTS5 over every locally-indexed message. Typical query: ~4 ms.

Full-text search across every Claude Code + Codex session klyne has ingested. Designed for the case where you remember a bug, not the session where it happened.

## Triggers

| Surface | Invocation |
|---|---|
| Slash command | `/klyne:search jointLedgerController` |
| MCP tool (direct) | `search_messages` with `query` argument |
| Web overlay | Press `/` anywhere in the web cockpit |

## What's returned

Each hit carries:

- `session_id` (short form)
- `cli` (claude / codex)
- `project_path`
- `role` (user / assistant) + RFC3339 timestamp
- `snippet` — one-line context with the match highlighted

## Why it's fast

The store maintains a single FTS5 virtual table over message bodies, updated incrementally as the daemon ingests JSONL. No vector embeddings, no AI calls — straight inverted-index lookup.

## Daemon-down behaviour

If the daemon isn't running, the tool returns `daemon_down: true` and the slash command surfaces:

> Could not reach the klyne daemon at http://127.0.0.1:7878 — start it with `klyne` and try again.

The user knows to restart, not to reformulate the query.

## Implementation

- `internal/mcpserver/tool_search.go` — MCP handler; HTTP call to the daemon
- `internal/store/search.go` — FTS5 query path
- `internal/api/handlers/search.go` — daemon endpoint backing the call
- `internal/mcpserver/slashcommands/search.md` — slash-prompt definition

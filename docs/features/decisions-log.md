# `klyne decisions` — pinned project facts (CLI + MCP)

> Status: shipped. CLI + 3 MCP tools. Read/append only — no in-place updates by design.

The smallest persistent-context surface: short, immutable notes pinned
to a project (and optionally a session). Inspired by
[`mkreyman/mcp-memory-keeper`](https://github.com/mkreyman/mcp-memory-keeper)
but built natively so users don't have to install a second MCP server.

> See [memory.md](./memory.md) for the friendlier chat-driven layer on top of the same storage — `remember` / `recall` use this same table with a different verb pair.

## CLI

```bash
klyne decisions add "We picked Postgres over SQLite — team already runs PG" --tags=db,infra
klyne decisions list                      # current project
klyne decisions list --all --tag=db       # cross-project, single tag
klyne decisions search "postgres"
klyne decisions delete d-6d1ac677627cfd46
```

## MCP tools

Auto-registered when `klyne mcp install` runs.

| Tool | Purpose |
|---|---|
| `record_decision` | The AI pins a fact ("we decided X because Y") when the user states one mid-conversation |
| `list_decisions` | The AI recalls prior decisions when starting a related task |
| `search_decisions` | Keyword recall — "did we decide anything about Y?" |

## Schema

Migration 009. One immutable row per decision:

| Column | Notes |
|---|---|
| `id` | `d-<16 hex>` content hash |
| `ts` | RFC3339 timestamp |
| `project_path` | Resolved project root; empty for "global" decisions |
| `session_id` | Optional — pinning to a specific session |
| `text` | Free-form |
| `tags_json` | JSON-encoded `[]string` |

No update path — amendments are delete-then-add by design.

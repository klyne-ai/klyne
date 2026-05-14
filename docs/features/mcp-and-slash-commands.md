# MCP integration & slash commands

> Status: shipped. The core rescue-layer surface — every klyne capability the AI can call mid-session.

`klyne mcp install` wires klyne as an MCP server in Claude Code and Codex CLI, drops six markdown slash commands into `~/.claude/commands/klyne/`, and registers the `UserPromptSubmit` advisor hook. Idempotent.

```bash
klyne mcp install            # install MCP server + slash commands + advisor hook
klyne mcp install --dry-run  # show what would change without writing
```

## Slash commands

Each slash command calls one MCP tool and prints the tool's `markdown` field byte-for-byte. The server renders the prompt-ready markdown so the host LLM never re-templates structured fields. See [the markdown verbatim contract](#markdown-verbatim-contract) below.

| Slash command | MCP tool | What it does | Detail |
|---|---|---|---|
| `/klyne:handoff` | `generate_handoff` | Deterministic handoff prompt for a fresh session | [handoff.md](./handoff.md) |
| `/klyne:health` | `get_context_health` | Verdict (healthy / drifting / risky / rescue_now) + top bloat sources | [context-health.md](./context-health.md) |
| `/klyne:precompact` | `get_pre_compact_context` | Messages from immediately before the last `/compact` | [pre-compact-recovery.md](./pre-compact-recovery.md) |
| `/klyne:search <query>` | `search_messages` | FTS5 search across every indexed session | [search.md](./search.md) |
| `/klyne:sessions` | `list_sessions` | Every Claude Code + Codex session in the current project | [sessions-list.md](./sessions-list.md) |
| `/klyne:tokens` | `get_token_timeline` | Per-turn token usage with sparkline + heatmap | [token-timeline.md](./token-timeline.md) |

## MCP tools

In addition to the six tools backing the slash commands above, the following MCP tools are registered and callable directly by the AI without a slash command:

| Tool | Purpose | Detail |
|---|---|---|
| `remember` / `recall` | Persistent project/global memory | [memory.md](./memory.md) |
| `record_decision` / `list_decisions` / `search_decisions` | Pinned project facts | [decisions-log.md](./decisions-log.md) |
| `code_review_context` | Surfaces decisions, hot files, and recent activity scoped to a code-review prompt | — |

## Markdown verbatim contract

Every klyne MCP tool output carries a `markdown` field that is the slash-prompt-ready rendering of the result — verdict + bloat report, timeline + sparkline + table, sessions list, handoff prompt, pre-compact recovery, search hits. The matching slash-command prompt is one line: *display the `markdown` field byte-for-byte*. This eliminates two classes of drift:

1. The host LLM paraphrasing / summarising / reformatting instead of echoing.
2. The slash-prompt template drifting from the tool's actual output fields.

When the tool has nothing to show (no session, daemon down, ambiguous candidates), the `markdown` field carries that message too — the slash command always has something safe to echo.

## Auto-resolution

All slash-command-backed tools auto-resolve the session from the host's current working directory. The user typing `/klyne:health` doesn't have to remember a session ID. If multiple sessions live in the same cwd, the tool returns `ambiguous: true` with a candidate list and the user retries with `session_id=...`.

## Implementation

- `internal/mcpserver/` — MCP server, tool handlers, markdown formatters
- `internal/mcpserver/slashcommands/` — the six slash-command markdown files installed into the host
- `cmd/klyne/mcp.go` — `mcp install` + `mcp serve` CLI surfaces
- Server transport: stdio JSON-RPC per the MCP spec

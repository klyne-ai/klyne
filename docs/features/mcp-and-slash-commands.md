# MCP integration & slash commands

> Status: shipped. The core rescue-layer surface — every klyne capability the AI can call mid-session.

`klyne mcp install` wires klyne as an MCP server in Claude Code and Codex CLI, drops six markdown slash commands into `~/.claude/commands/klyne/`, registers the `UserPromptSubmit` advisor hook, and unpacks the agent-driven Skill bundles into `~/.claude/skills/`. Idempotent.

```bash
klyne mcp install            # install MCP server + slash commands + advisor hook
klyne mcp install --dry-run  # show what would change without writing
```

## Slash commands

Each slash command calls one MCP tool and prints the tool's `markdown` field byte-for-byte. The server renders the prompt-ready markdown so the host LLM never re-templates structured fields. See [the markdown verbatim contract](#markdown-verbatim-contract) below.

| Slash command | MCP tool | What it does | Detail |
|---|---|---|---|
| `/klyne:bootstrap` | `bootstrap` | Day-1 session brief: recent sessions, klyne runbooks, Claude auto-memory, recent reflections, cross-AI worklog entries, current session health | [bootstrap.md](./bootstrap.md) |
| `/klyne:handoff` | `generate_handoff` | Deterministic handoff prompt for a fresh session | [handoff.md](./handoff.md) |
| `/klyne:health` | `get_context_health` | Verdict (healthy / drifting / risky / rescue_now) + top bloat sources | [context-health.md](./context-health.md) |
| `/klyne:precompact` | `get_pre_compact_context` | Messages from immediately before the last `/compact` | [pre-compact-recovery.md](./pre-compact-recovery.md) |
| `/klyne:sessions` | `list_sessions` | Every Claude Code + Codex session in the current project | [sessions-list.md](./sessions-list.md) |
| `/klyne:tokens` | `get_token_timeline` | Per-turn token usage with sparkline + heatmap | [token-timeline.md](./token-timeline.md) |

## Skills — the agent-invoked path

Slash commands are user-typed (`/klyne:health`). Skills are Claude-invoked — the agent reads each bundled `SKILL.md` description and auto-invokes when the description matches the current situation. No `/klyne:` typing required.

Skill bundles live under `~/.claude/skills/<skill-name>/SKILL.md` (Claude Code's standard skill directory format). `klyne mcp install` unpacks them; new versions overwrite on re-install (idempotent).

| Skill | Wraps MCP tool | Auto-invokes when |
|---|---|---|
| `klyne-bootstrap` | `bootstrap` | Session start in a project the agent has no prior context for, the user asks "what was I working on?" / "where did I leave off?", or before the agent's first major action in an unfamiliar codebase |
| `klyne-health` | `get_context_health` | An advisory mentions context fill / drift / acceleration / 5-hour window, user asks about token usage, after a `/compact` event, or before loading a >5K-token file |

Why both surfaces: slash commands are deterministic and user-controlled — the right path when you know what you want. Skills close the gap when you *don't* know — they let the agent reach for the rescue tool before the user notices the session is degrading.

The skill body itself defers all heuristics to the deterministic klyne advisor and the MCP tool's `action` field; the agent never invents thresholds. Suppression / cooldown rules live in the skill body for v1 — a `healthy` verdict followed by re-invocation within 10 turns is treated as poll-spam and skipped.

## MCP tools

In addition to the six tools backing the slash commands above, the following MCP tools are registered and callable directly by the AI without a slash command:

| Tool | Purpose | Detail |
|---|---|---|
| `remember` / `recall` | Persistent project/global memory | [memory.md](./memory.md) |
| `update_memory` / `delete_memory` / `list_memories` | Memory CRUD parity: edit, delete, browse by id with derived display names | [memory.md](./memory.md#mcp-tools-new) |
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

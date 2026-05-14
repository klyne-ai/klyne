# Session bootstrap brief — `bootstrap` / `klyne-bootstrap` / `/klyne:bootstrap`

> Status: shipped 2026-05-15. Serena-inspired Day-1 onboarding for the agent. Same JSONL + SQLite reads as the rest of klyne — no AI calls, no schema migration.

## The problem

When a fresh Claude Code or Codex session spawns, the agent has no idea what happened in this project before. It can call `list_sessions` to enumerate sessions, `recall` to fetch memories, and `get_context_health` to read the latest verdict — but that's three calls, and the agent typically only reaches for them when the user asks. The first turn of a new session burns context re-asking "what would you like to work on?".

Serena solves the equivalent on the code side with `initial_instructions` + `check_onboarding_performed` + `read_me`: one auto-invoked surface that briefs the agent before any work starts. klyne's `bootstrap` ports that pattern to klyne's data — recent sessions, project + global memories, the latest active session's health verdict — all in one MCP call, auto-invoked via a Skill.

## What it returns

```jsonc
{
  "cwd": "/Users/.../klyne",
  "sessions": [
    /* up to 3 most-recent sessions in this project, newest first */
    {"session_id": "…", "is_active": true,  "mod_time": "…", "msg_count": 159, "preview": "…"},
    {"session_id": "…", "is_active": false, "mod_time": "…", "msg_count": 1266, "preview": "…"},
    {"session_id": "…", "is_active": false, "mod_time": "…", "msg_count":  402, "preview": "…"}
  ],
  "project_memories":        [/* up to 5 project-scoped memories, ts DESC */],
  "global_memory_count":     7,
  "global_memories_preview": [/* up to 3 most-recent globals */],
  "latest_health": {
    "session_id":       "…",
    "state":            "healthy",            // healthy / drifting / risky / rescue_now
    "action":           "continue",           // continue / consider-handoff / handoff-now / compact-pending
    "context_fill_pct": 15
  },
  "markdown": "# klyne bootstrap\n\nProject: `/Users/.../klyne`\n\n## Recent sessions\n…"
}
```

- **Sessions** — reuses `ListSessionsForCWDCtx` (the same source `list_sessions` uses); takes the first 3 candidates which are already sorted newest-first.
- **Project memories** — `store.ListDecisions` with `ProjectPath = cwd` and limit 5.
- **Global memories** — fetch a generous slice of decisions and filter to `project_path == ""` client-side; count the full result, preview the first 3.
- **Latest health** — delegates to `HandleGetContextHealth` against the most-recently-modified session. Omitted when the call errors, returns ambiguous, or there are no sessions.
- **Markdown** — verbatim-render target. Empty sections print `_(none)_` so the agent's output is stable regardless of project age.

## How the agent picks this up

Two surfaces unpack `klyne mcp install`:

1. **Skill bundle** at `~/.claude/skills/klyne-bootstrap/SKILL.md`. The frontmatter description tells Claude Code to auto-invoke when:
   - The agent enters a project it has no prior context for (first turn of a fresh session).
   - The user asks "what was I working on?" / "where did I leave off?" / "what's the state of this project?".
   - The agent is about to take its first major action in an unfamiliar codebase.

   Cool-down rule (same shape as `klyne-health`): don't re-invoke within the same session unless the user explicitly asks again.

2. **Slash command** at `~/.claude/commands/klyne/bootstrap.md`. User-driven: typing `/klyne:bootstrap` calls the tool with no args and prints the `markdown` field byte-for-byte.

Both surfaces invoke the same `bootstrap` MCP tool. The skill is the auto-engaging path; the slash command is the explicit fallback.

## Why this fits klyne's positioning

The [`docs/QUESTIONS.md`](../QUESTIONS.md) per-command framework splits klyne's value into **prevention / recovery / audit**. `bootstrap` is the audit/cross-session surface bundled for Day-1: every piece of data it returns is already exposed by another klyne tool — sessions via `list_sessions`, memories via `recall`, health via `get_context_health`. Bundling them eliminates the *three-call-chain* the agent would otherwise have to make to get the same picture.

Claude can't do this from its own context. It has no JSONL access (sessions table is unavailable), no persistent memory store (memories live in `~/.klyne/klyne.db`), and no accurate token telemetry (health needs the bloat scorecard klyne computes). Bootstrap turns those into one call the agent gets for free at session start.

## What this is not

- **Not a code-summarization tool.** klyne is read-only over JSONL and SQLite — `bootstrap` doesn't read any source file, doesn't call any LSP, doesn't invoke any AI model.
- **Not a substitute for `generate_handoff`.** `bootstrap` is a project-scope briefing; `handoff` is a single-session continuation prompt. After a `/compact` event, the right tool is `precompact`. After a 5-hour cap, the right tool is `handoff`. Bootstrap is for "I just opened Claude Code in this directory — what's the state?".

## Files

- `internal/mcpserver/tool_bootstrap.go` — handler + structured types + markdown renderer
- `internal/mcpserver/tool_bootstrap_test.go` — empty project, 5 sessions → 3 most-recent, 8 project memories + 2 globals
- `internal/mcpserver/skills/klyne-bootstrap/SKILL.md` — agent-invoked skill bundle
- `internal/mcpserver/slashcommands/bootstrap.md` — `/klyne:bootstrap` wrapper
- `internal/mcpserver/server.go` — tool registration + `bootstrap` MCP prompt
- `internal/mcpserver/prompts.go` — `PromptBootstrapHandler`

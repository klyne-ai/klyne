# `/klyne:handoff` — deterministic session handoff

> Status: shipped. MCP tool `generate_handoff`. No AI calls. Byte-identical output for the same input — covered by [`docs/proof/02-handoff-equivalence/`](../proof/02-handoff-equivalence/).

Generate a Markdown handoff prompt that captures everything a fresh Claude Code or Codex session needs to continue where the current one left off: touched files, commands run, failures, recent context.

## Trigger

```text
/klyne:handoff
```

The host LLM auto-resolves the session from the current working directory and prints the rendered markdown verbatim — paste it into a fresh session.

## What the prompt contains

- **Objective** — inferred from the most recent user-stated goal in the session
- **Files touched** — every file that received a Read/Edit/Write/MultiEdit tool call
- **Commands** — `Bash` invocations and their exit status
- **Failures** — tool calls flagged `is_error`, plus the surrounding context
- **Recent context** — the last N assistant messages, capped at a sane token budget
- **Resume hint** — explicit next-step prompt for the fresh session to pick up

## Ambiguous cwds

If multiple sessions in the same project directory could be the source, the tool returns:

```json
{
  "ambiguous": true,
  "candidates": [
    {"session_id": "...", "preview": "...", "msg_count": 412, "modified": "..."},
    ...
  ],
  "markdown": "Multiple Claude Code sessions in this project. Pick one and call generate_handoff again with session_id."
}
```

The user picks one and the host LLM retries with `session_id=<id>`.

## Determinism

The same session at the same assistant turn produces the same handoff bytes. The [handoff equivalence proof](../proof/02-handoff-equivalence/) hashes the output across two runs and fails red if they diverge.

## Implementation

- `internal/mcpserver/tool_generate_handoff.go` — MCP handler
- `internal/mcpserver/handoff.go` — snapshot loading, content selection, markdown renderer
- `internal/mcpserver/slashcommands/handoff.md` — slash-prompt definition (one line: echo the `markdown` field)

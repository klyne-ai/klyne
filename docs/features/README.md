# klyne features

This directory documents user-facing features. Each page covers what ships, the user problem it solves, and the implementation logic.

Features are grouped by where the user reaches them — MCP / slash commands, CLI, and the local web cockpit. Most features ship on more than one surface; pages link across the groups so a reader can follow a single feature end-to-end.

## MCP server & slash commands

The rescue-layer surface — every klyne capability the AI can call mid-session. Wired up via `klyne mcp install`.

- [mcp-and-slash-commands.md](./mcp-and-slash-commands.md) — installation, the slash commands, and the markdown-verbatim contract
- [handoff.md](./handoff.md) — `/klyne:handoff` · deterministic prompt to continue work in a fresh session
- [context-health.md](./context-health.md) — `get_context_health` MCP tool · verdict + top bloat sources (slash entry point: `/klyne:status`)
- [pre-compact-recovery.md](./pre-compact-recovery.md) — `/klyne:precompact` · messages from before the last `/compact`
- [sessions-list.md](./sessions-list.md) — `/klyne:sessions` · every Claude Code + Codex session in this project
- [token-timeline.md](./token-timeline.md) — `get_token_timeline` MCP tool + `klyne tokens` CLI + web · per-turn token usage with sparkline (slash entry point: `/klyne:status`)

## Proactive surfaces

Features that push information at the user before they have to ask.

- [proactive-session-advisor.md](./proactive-session-advisor.md) — `UserPromptSubmit` hook with four deterministic triggers
- [statusline.md](./statusline.md) — single-line `klyne statusline` for Claude Code's status bar

## Persistent memory

Long-lived facts that survive across sessions.

- [memory.md](./memory.md) — chat-driven `remember` / `recall` with project & global scopes
- [decisions-log.md](./decisions-log.md) — pinned project facts via CLI and MCP (same storage as memory, different verbs)

## Analytics & operational tooling

CLI surfaces that turn klyne's local SQLite store into glanceable answers.

- [analytics-commands.md](./analytics-commands.md) — `klyne top` · `klyne patterns` · `klyne roast`
- [file-heatmap.md](./file-heatmap.md) — `klyne files` per-file Read/Edit/Write rollup
- [subagent-attribution.md](./subagent-attribution.md) — `klyne subagents` surfaces Task-tool spend hidden from parent-session cost
- [audit-sessions.md](./audit-sessions.md) — `klyne audit-sessions` verifies stored stats against raw JSONL
- [otel-exporter.md](./otel-exporter.md) — `klyne otel emit` OTel-shaped JSONL exporter

## Web cockpit

Browser-side surface served by the local daemon at `http://127.0.0.1:7878`.

- [web-cockpit.md](./web-cockpit.md) — the 3-tab shell (Work / Memory / Insights) and every deep-link surface
- [stats-dashboard.md](./stats-dashboard.md) — `/stats` page (Overview / Models / Daily / Stats) + per-session activity heatmap

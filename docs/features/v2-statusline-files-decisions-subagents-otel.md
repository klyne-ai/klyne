# v2 surfaces — statusline, files, decisions, subagents, otel

> Status: implemented on the `worktree-v2-files-decisions-subagents-statusline-otel`
> branch. Read-only by default. No AI calls in the core flow. The OTel
> exporter is the only surface that produces data intended for off-host
> consumption — it ships explicit (opt-in) commands only, never an
> always-on push.

This is the second batch of CLI/MCP additions, picking up where v1's
analytics surfaces (`cost`, `top`, `patterns`, `roast`, `export`) left off.
Each feature was chosen by scanning the ecosystem (ccusage, ccsession,
token-dashboard, mcp-memory-keeper, claude-code-otel, etc.) for ideas
that fit klyne's contracts.

## 1. `klyne statusline`

A single-line summary suitable for Claude Code's `statusLine` settings
hook. Wire it in:

```json
{
  "statusLine": {
    "type": "command",
    "command": "klyne statusline"
  }
}
```

Three formats:

| Flag | Output | Use case |
|---|---|---|
| `--format=short` (default) | `klyne ▸ 38% ctx · 62k/160k · 5h 1%` | Most terminals |
| `--format=mini` | `38% · 1%` | Tight statuslines |
| `--format=plain` | `klyne 38% ctx · 62k/160k · 5h 1%` | No-unicode setups |

Exits 0 even when no session is found ("klyne ▸ idle") so it never
breaks the user's prompt.

## 2. `klyne files`

Per-file heatmap. Walks every locally-stored tool call, extracts the
`file_path` / `path` / `notebook_path` argument, and aggregates by file.

```
| Rank | File | Reads | Edits | Writes | Sessions | Last touched |
|---:|---|---:|---:|---:|---:|---|
| 1 | …services/discountEngineService.js | 51 | 60 | 0 | 8 | 13d ago |
```

Inspired by token-dashboard's "hotspot" view. The CLI carries an
optional `--mutated-only` switch to drop files that were only Read.

Implementation: `internal/fileheat/`. The path extractor uses a small
per-tool dispatch table (`Read`, `Edit`, `Write`, `Glob`, `Grep`,
`MultiEdit`, `NotebookEdit`, `apply_patch`) and falls back to a generic
"looks like a path" heuristic for unknown MCP tools.

## 3. `klyne decisions` (CLI + MCP)

The smallest persistent-context surface: short, immutable notes pinned
to a project (and optionally a session). Inspired by
`mkreyman/mcp-memory-keeper` but built natively into klyne so users
don't have to install a second MCP server.

CLI:

```bash
klyne decisions add "We picked Postgres over SQLite — team already runs PG" --tags=db,infra
klyne decisions list                      # current project
klyne decisions list --all --tag=db       # cross-project, single tag
klyne decisions search "postgres"
klyne decisions delete d-6d1ac677627cfd46
```

MCP tools (auto-registered via `klyne mcp install`):

| Tool | Purpose |
|---|---|
| `record_decision` | The AI pins a fact ("we decided X because Y") when the user states one mid-conversation |
| `list_decisions` | The AI recalls prior decisions when starting a related task |
| `search_decisions` | Keyword recall — "did we decide anything about Y?" |

Schema (migration 009): one immutable row per decision, with
`id, ts, project_path, session_id, text, tags_json`. No update path —
amendments are delete-then-add by design.

## 4. `klyne subagents`

Subagent attribution. Claude Code writes Task-tool subagent transcripts
under `~/.claude/projects/<project>/<parent-session-id>/subagents/agent-XXX.jsonl`.
The parent session's `cost_usd` does **not** include this spend — the
cost engine only sees the final Task tool result, not the subagent's
full conversation.

`klyne subagents` reads those JSONLs directly and rolls them up per
parent session:

```
| Parent session | Project | Subagents | Tokens in | Tokens out | Cache % | Last activity |
|---|---|---:|---:|---:|---:|---|
| ccf1c911 | …/Private/project-redacted | 31 | 201.1M | 882k | 96% | 4d ago |
```

A real run on this maintainer's transcripts: **134 subagents across
5 parent sessions, 555M tokens** — most of which was hidden from the
headline cost number until now.

Implementation: `internal/subagent/`. No DB writes — the command reads
JSONL files directly so it works even before the daemon has ingested
them.

## 5. `klyne otel emit`

OTel-shaped JSON Lines exporter, gated to explicit invocation. One span
per assistant message turn, with `gen_ai.*` attributes per the OTel
GenAI working group draft plus `klyne.*` resource fields so consumers
can join back to klyne's local DB.

```bash
klyne otel emit --since=24h --out spans.jsonl
# then upload spans.jsonl to your collector of choice
```

Sample span:

```json
{
  "trace_id": "acb1f71f0f3ac571721c49f0c5c4357c",
  "span_id": "390091ad23781d3e",
  "name": "gen_ai.completion",
  "kind": "SPAN_KIND_INTERNAL",
  "start_time": "2026-05-12T02:21:21.489Z",
  "end_time": "2026-05-12T02:21:29.030Z",
  "attributes": {
    "gen_ai.system": "anthropic",
    "gen_ai.request.model": "claude-opus-4-7",
    "gen_ai.usage.input_tokens": 228687,
    "gen_ai.usage.output_tokens": 1234,
    "gen_ai.cost.usd": 4.36784625,
    "klyne.session_id": "...",
    "klyne.project_path": "..."
  },
  "resource": {"service.name": "klyne", "service.namespace": "ai-coding-cli"}
}
```

Inspired by `ColeMurray/claude-code-otel`, but kept dependency-free:
no OpenTelemetry SDK, no network in the default path. The user owns
when (and whether) data leaves the machine.

Implementation: `internal/otelexport/`. Trace ID + span ID are
deterministic FNV-64a hashes of session/message IDs — re-exporting
the same data produces identical IDs (useful when streaming spans into
a backing store you don't fully control).

## What we explicitly did NOT add

| Idea | Why skipped |
|---|---|
| ML burn-rate predictions | klyne's advisor explicitly disallows "you'll run out in N turns"-style predictive copy |
| AI compression of past sessions | violates "no AI calls in core flow" |
| Cross-CLI ingestion (Cursor / Aider / Gemini) | different transcript schemas; defer to v3 |
| Always-on OTLP push | crosses local-first contract |
| Global usage leaderboard | privacy boundary |

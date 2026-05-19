# `/klyne:status` — unified context surface

**Date:** 2026-05-19
**Status:** Design — awaiting user review
**Predecessor:** [[project_klyne_followup_tokens_health_merge]] (2026-05-19 follow-up memory)
**Prereq landed:** [2026-05-19-handoff-hybrid-redesign-design.md](./2026-05-19-handoff-hybrid-redesign-design.md) shipped (commits `6e0310d`, `04d7d36`, `0b2747e`, `e7ea2f1`).

## Problem

The user invokes `/klyne:tokens` and `/klyne:health` as one mental concept — "how is my context doing right now?" — and the two-command split feels artificial. Separately, `/klyne:bootstrap` ends with a "Current session health" tail that duplicates `/klyne:health`'s surface, and its `## klyne memory (SQLite store)` section header is stale after the 2026-05-19 runbooks reposition (commit `eac01bf`) which only updated UI/docs.

## Goals

1. One slashcommand answers "how is my context doing right now?"
2. Bootstrap stops carrying the health-verdict tail (single source of truth for that data).
3. Bootstrap copy reflects the runbooks reposition.

## Non-goals

- No changes to the underlying MCP tools `get_context_health` or `get_token_timeline`. They keep their structured outputs because the web cockpit (`TokenTimeline.Points` → session detail chart) and other internal callers depend on them.
- No JSON wire-format changes, no SQLite schema changes, no MCP tool ID renames.
- No aliases or back-compat shims for the deleted `/klyne:tokens` and `/klyne:health` slashcommands — clean replacement.

## Decisions made during brainstorming

| Decision | Value | Rationale |
|---|---|---|
| Back-compat | Replace `/klyne:tokens` + `/klyne:health` outright | User picked "Replace both". Muscle-memory cost is a one-time miss. |
| Command name | `/klyne:status` | Open-ended enough to absorb future additions without renaming again. |
| Output ordering | Verdict on top, full token detail below | Lead with the call to action; raw data underneath. |
| Architecture | New MCP tool `get_session_status` | Matches the dumb-echo pattern used by all 7 existing klyne slashcommands. One round-trip, server controls layout. |
| Bootstrap merge scope | Move only the health tail | Bootstrap remains the cross-session briefing; `/klyne:status` owns "right now". |
| Bootstrap memory copy | Rename "klyne memory (SQLite store)" → "klyne runbooks" | Match the 2026-05-19 reposition; leave "Claude auto-memory" untouched (separate Claude-Code-owned concept). |
| Heatmap | Drop from the merged output | Table + sparkline + trajectory headline are the high-signal pieces. Heatmap can re-enter later if missed. |
| Token table | Preserve verbatim | Columns: `time | input tokens | % of context | cached | uncached` (5-col when context window known, 4-col when not). |

## Architecture

```
/klyne:status                            (slashcommand, ~10 lines markdown)
   └── mcp__klyne__get_session_status    (new MCP tool, ~150 LOC)
         ├── contexthealth.Classify(...)     (existing code path)
         ├── usage.BuildTokenTimeline(...)   (existing code path)
         └── compose one Markdown blob:
               1. Verdict header   (state · action · context fill %)
               2. Reason            (one sentence)
               3. Tokens            (trajectory headline + sparkline + table)
               4. Top bloat sources
```

The slashcommand stays a byte-for-byte echo of the `Markdown` field, matching all 7 existing klyne slashcommands.

## Output layout (verdict-on-top)

```
# Session status — `<short-session-id>`

**State:** drifting · **Action:** continue · **Context fill:** 14%

> <one-sentence reason>

## Tokens

<trajectory headline sentence — same as today's /klyne:tokens>

▁▂▃▅▆▇█▇▆▅▃▂
first ~28k · peak ~31k · now ~28k

| time     | input tokens | % of context | cached | uncached |
|----------|-------------:|-------------:|-------:|---------:|
| 10:14    |       28,450 |          14% | 20,100 |    8,350 |
| 10:09    |       …      |          …   | …      |    …     |

## Top bloat sources

1. <file/tool> — 4,200 tokens
2. …
```

Notes:
- Verbatim reuse of today's table columns from `tool_token_timeline.go:327-345`.
- Sparkline + trajectory headline reused from `renderTrajectorySentence` and `renderSparkline`.
- Bloat-sources block reused from today's `/klyne:health` renderer (`renderHealthMarkdown` in `tool_context_health.go`).

## Edge cases

| Case | Behavior |
|---|---|
| Ambiguous session in cwd (multiple candidates) | Emit candidate list once at top, short-circuit both subsystems. Both already emit identical ambiguity copy — dedupe at composition. |
| No session under cwd | Single "no session" message; no headers, no empty sections. |
| Health computable but token timeline empty (very short session) | Show verdict + "no token-timeline rows yet" placeholder under the Tokens header. Do not fail the whole call. |
| ContextWindow unknown for the model | Use the 4-column table (no `% of context`) — same fallback as today's `/klyne:tokens`. |
| Session deadline exceeded mid-call | Surface partial result with explicit "timed out after Ns" footer rather than empty error (matches today's `get_context_health` timeout pattern). |

## Files touched

**New**
- `internal/mcpserver/tool_session_status.go` — composition + renderer. Calls into `contexthealth.Classify` and `usage.BuildTokenTimeline`, then composes one Markdown blob.
- `internal/mcpserver/tool_session_status_test.go` — happy path, ambiguous short-circuit, no-session, tokens-empty, context-window-unknown, deadline-exceeded.
- `internal/mcpserver/slashcommands/status.md` — dumb echo shim. Frontmatter `description`: "Unified context check — health verdict + token timeline + bloat sources for the active session".

**Edit**
- `internal/mcpserver/server.go` — register `get_session_status`.
- `internal/mcpserver/tool_bootstrap.go` — strip the "Current session health" tail section; rename `## klyne memory (SQLite store)` → `## klyne runbooks`.
- `internal/mcpserver/tool_bootstrap_test.go` — drop health-tail assertions; add `klyne runbooks` header assertion.
- `internal/mcpserver/slashcommands/bootstrap.md` — frontmatter description drops "latest health verdict"; body description list updates to reflect three sections instead of four.

**Delete**
- `internal/mcpserver/slashcommands/health.md`
- `internal/mcpserver/slashcommands/tokens.md`

## Migration

- Anyone with muscle memory for `/klyne:tokens` or `/klyne:health` will see "skill not found" on first invocation. One-line mention in the next handoff/changelog. No aliases per the "Replace both" decision.
- MCP tool IDs `get_context_health` and `get_token_timeline` are unchanged — any external integration or web cockpit code keeps working.

## Open questions

None — all decision points resolved during brainstorming. If the heatmap turns out to be missed in practice, it's a one-block re-add.

## Cross-refs

- Follow-up memory: `~/.claude/projects/-Users-mohitpatel-Desktop-Project-klyne/memory/project_klyne_followup_tokens_health_merge.md` (delete on ship).
- Existing pattern reference: `internal/mcpserver/slashcommands/handoff.md`, `bootstrap.md` (dumb-echo shim shape).
- Existing renderers to reuse:
  - `internal/mcpserver/tool_context_health.go` — verdict header, bloat sources.
  - `internal/mcpserver/tool_token_timeline.go:255-388` — trajectory headline, sparkline, table.

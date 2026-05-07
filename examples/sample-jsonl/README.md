# Sample JSONL Fixtures

Synthetic JSONL fixtures for Claude Code and Codex CLI sessions used across multiple workstreams.

## Purpose

| Consumer | What they need |
|----------|---------------|
| **W4** (Claude connector) | Realistic Claude Code JSONL to drive parser development and tests |
| **W5** (Codex connector) | Realistic Codex CLI JSONL to drive parser development and tests |
| **W15** (compact recovery) | At least one fixture per CLI that contains a compact/summary marker, followed by messages with a dramatic token drop |
| **W16** (perf benchmarks) | Seed data for throughput and latency benchmarks |

## File Layout

```
examples/sample-jsonl/
├── README.md                           (this file)
├── generate.go                         (standalone generator — not part of module build)
├── claude/
│   ├── session-001-simple.jsonl        (7 lines: user+assistant text only, no tools, no compact)
│   ├── session-002-with-tools.jsonl    (13 lines: 3 tool uses — Read, Bash, Edit — with paired tool_results)
│   └── session-003-with-compact.jsonl  (30 lines: tool-heavy session, summary marker, 5 post-compact messages)
└── codex/
    └── session-real-001.jsonl          (18 lines: sanitized real Codex JSONL — session_meta, turn_context,
                                         response_item/{message/user, message/assistant, function_call,
                                         function_call_output, reasoning}, event_msg/{token_count, agent_message,
                                         task_started, task_complete})
```

## Regenerating

```bash
# From the repo root:
go run examples/sample-jsonl/generate.go
```

The generator uses a fixed start timestamp (`2026-05-06T10:00:00.000Z`) and deterministic UUID sequences, so running it twice produces byte-identical output.

The hand-authored fixtures in this directory are the canonical source. The generator reproduces them; it does not replace manual editing.

## Format Assumptions — Claude Code

Claude Code writes one JSON object per line to `~/.claude/projects/<encoded-cwd>/<uuid>.jsonl`. The observed fields are:

| Field | Type | Notes |
|-------|------|-------|
| `type` | string | `"user"`, `"assistant"`, `"system"`, `"summary"` |
| `uuid` | string | UUIDv4 identifying this message |
| `parentUuid` | string\|null | UUID of the message this replies to; `null` for first message |
| `sessionId` | string | UUIDv4 shared across all lines in a session |
| `timestamp` | string | ISO 8601 with milliseconds and `Z` suffix |
| `cwd` | string | Working directory (present on `user` and `system` lines) |
| `version` | string | Claude Code version (present on `user` lines) |
| `message.role` | string | `"user"` or `"assistant"` |
| `message.model` | string | Model ID (present on `assistant` lines) |
| `message.usage` | object | `input_tokens`, `output_tokens`, `cache_read_input_tokens`, `cache_creation_input_tokens` |
| `message.stop_reason` | string | `"end_turn"` or `"tool_use"` |

### Tool use shape

When the assistant calls a tool, `message.content` is an array with one `tool_use` object:
```json
{"type": "tool_use", "id": "toolu_…", "name": "Read", "input": {"file_path": "…"}}
```

The paired user line carries a `tool_result`:
```json
{"type": "tool_result", "tool_use_id": "toolu_…", "content": "…file content…"}
```

### Compact / summary marker

When the user runs `/compact`, Claude Code appends a `type:"summary"` line:
```json
{"type": "summary", "summary": "…rolled-up text…", "leafUuid": "…uuid of last assistant msg…", "sessionId": "…", "timestamp": "…"}
```

W15 detects compaction by looking for this line **and** a dramatic drop in `message.usage.input_tokens` on the first assistant message after the summary (the summary replaces the full context, so the token count resets).

**Field-name decision**: `leafUuid` uses camelCase to match the convention of `parentUuid` and `sessionId` seen on other lines. If real fixtures show a different casing (e.g., `leaf_uuid`), update W4 and these fixtures together.

## Format Assumptions — Codex CLI

Codex CLI writes sessions to `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl`. The real format
(audited against live sessions in Wave 1) uses a top-level envelope on every line:

```json
{"timestamp": "ISO-8601", "type": "...", "payload": {...}}
```

### Top-level `type` values

| `type` | Purpose | Parser action |
|--------|---------|---------------|
| `session_meta` | First line; `payload.id` = session UUID, `payload.cwd` = working dir | Update per-file state; return nil |
| `turn_context` | Per-turn metadata; `payload.model` = model in use | Update per-file model; return nil |
| `response_item` | Messages and tool calls; `payload.type` disambiguates | See below |
| `event_msg` | High-level lifecycle events (duplicates of response_item) | Skip |

### `response_item` payload types

| `payload.type` | Notes | Emitted `Role` |
|----------------|-------|----------------|
| `message` with `role=user` | `content[*].type=input_text` | `user` |
| `message` with `role=assistant` | `content[*].type=output_text` | `assistant` |
| `message` with `role=developer` | System-injected context | skipped |
| `function_call` | `payload.name`, `payload.arguments`, `payload.call_id` | `assistant` (ToolCalls populated) |
| `function_call_output` | `payload.call_id`, `payload.output` | `tool` (ToolResults populated) |
| `reasoning` | GPT-5 chain-of-thought; encrypted | skipped |

### State model

The parser is **stateful per file**: `session_meta` and `turn_context` lines accumulate
metadata (sessionID, projectPath, model). Message-emitting lines inherit this metadata.
A message line seen before `session_meta` is silently skipped.

## Known Fragility

1. **Codex format is experimental** — field names and line types may change between Codex CLI
   versions. W5 treats `session-real-001.jsonl` as the canonical reference; update this file
   and the parser if the real schema changes.

2. **Claude Code `parentUuid` on tool-result lines** — public observation suggests tool_result
   `user` lines set `parentUuid` to the preceding `assistant` line's `uuid`. W4 should verify
   against real sessions.

3. **Token counts** — Codex token counts appear only in `event_msg/token_count` lines, which
   are currently skipped. W9 (cost engine) will add token extraction from these lines in a
   future workstream.

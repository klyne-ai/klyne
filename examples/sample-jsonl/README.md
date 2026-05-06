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
    ├── session-001-simple.jsonl        (7 lines: input/output text only)
    ├── session-002-with-tools.jsonl    (14 lines: 4 function_call + function_call_output pairs)
    └── session-003-with-compact.jsonl  (28 lines: function calls, compact_event marker, post-compact messages)
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

Codex CLI writes sessions to `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl`. The format is experimental (spec §13 risk #2) and has changed between versions. These fixtures reflect a plausible shape inferred from public Codex documentation as of May 2026.

| Field | Type | Notes |
|-------|------|-------|
| `type` | string | `"session_meta"`, `"input"`, `"output"`, `"function_call"`, `"function_call_output"`, `"compact_event"` |
| `session_id` | string | Session identifier (snake_case — differs from Claude's `sessionId`) |
| `model` | string | Model name on `session_meta` and `output` lines |
| `timestamp` | string | ISO 8601 with milliseconds and `Z` suffix |
| `usage.prompt_tokens` | int | Present on `output` lines |
| `usage.completion_tokens` | int | Present on `output` lines |
| `usage.total_tokens` | int | Present on `output` lines |

### Function call shape

```json
{"type": "function_call", "call_id": "…", "name": "shell", "arguments": {"command": "…"}, "timestamp": "…"}
{"type": "function_call_output", "call_id": "…", "output": "…", "timestamp": "…"}
```

### Compact event marker (SYNTHETIC — must be verified in Wave 1)

Codex's compact signal is not publicly documented. This fixture uses an invented marker:
```json
{"type": "compact_event", "session_id": "…", "before_tokens": 18420, "after_tokens": 2180, "summary": "…", "timestamp": "…"}
```

W15 detects compaction by looking for this line **and** a dramatic drop in `usage.prompt_tokens` on the first `output` message after the event.

**W5 must verify**: When real Codex sessions with compaction are available in Wave 1, replace this invented marker with the actual signal. Update `session-003-with-compact.jsonl` and the generator accordingly.

## Known Fragility

1. **Codex format is experimental** — field names, line types, and compact signal may change between Codex CLI versions. W5 should treat these fixtures as a starting point and replace them with real captured sessions as soon as they are available.

2. **Claude Code `parentUuid` on tool-result lines** — public observation suggests tool_result `user` lines set `parentUuid` to the preceding `assistant` line's `uuid`. This fixture follows that convention; W4 should verify against real sessions.

3. **Token counts are realistic but invented** — actual token counts depend on model, prompt, and cache state. The values here are plausible for the conversation length but are not derived from real API calls.

4. **`session-003-with-compact.jsonl` (both CLIs)** — the post-compact messages have their `parentUuid` pointing to the last pre-compact assistant message (not the summary line). This is the observed behavior for Claude Code; Codex behavior is assumed to be similar.

# `/klyne:precompact` — pre-compact context recovery

> Status: shipped. MCP tool `get_pre_compact_context`. No AI calls. Covered by [`docs/proof/01-compact-recovery/`](../proof/01-compact-recovery/).

Recover the messages immediately preceding the last `/compact` event in a session — the exact context Claude Code summarised away and can no longer see.

## Trigger

```text
/klyne:precompact
```

The host LLM auto-resolves the session from the current working directory. The output is the recovered messages with their roles and timestamps, in original order.

## Why it matters

`/compact` is destructive from the AI's point of view — after compaction, the original turns are replaced by a summary in the AI's visible context. They are NOT deleted from disk. klyne reads them back from the source JSONL on demand.

A real example from the maintainer's machine: session `15009012` (`orders-service`) ran `/compact` three times. The largest event shrank **793,401 tokens → 9,002** — an 88× compression ratio. Every recoverable token is in JSONL on disk; klyne hands them back when the user asks.

## What's returned

- `trigger` — what caused the compact (auto threshold, manual, tool-call output overflow)
- `compact_timestamp` — when the boundary fell
- `pre_tokens` — total input-token weight in the pre-compact window
- `messages` — oldest first, with role / one-line content preview

## No-op cases

| Case | Returned markdown |
|---|---|
| Session has not been compacted | "This session has not been /compact'd yet — nothing to recover." |
| No session found in cwd | "No Claude Code session found for this working directory." |
| Multiple sessions in cwd | Candidate list + "Pick one and call get_pre_compact_context again with session_id." |

## Implementation

- `internal/mcpserver/tool_get_pre_compact_context.go` — MCP handler
- `internal/mcpserver/precompact.go` — boundary detection + message extraction
- `internal/mcpserver/slashcommands/precompact.md` — slash-prompt definition

# `/klyne:health` — context health classifier

> Status: shipped. MCP tool `get_context_health`. No AI calls. Deterministic over JSONL bytes.

Classify the current session's context health and list the top bloat sources.

## Trigger

```text
/klyne:health
```

The host LLM auto-resolves the session from the current working directory.

## Verdicts

The classifier returns one of four states, each with a recommended action:

| Verdict | When it fires | Recommended action |
|---|---|---|
| `healthy` | Context fill < 50 %, no acceleration, low bloat | continue |
| `drifting` | One soft signal (rising per-turn cost, stale topic) | watch — no action required |
| `risky` | Two soft signals or context fill > 70 % | `/compact` if topic complete |
| `rescue_now` | Context fill > 90 % or one hard ceiling tripped | `/compact` or new session immediately |

## Top bloat sources

Alongside the verdict, the tool lists the top 3 tool-output buckets contributing the most input-token weight to the current context. A typical row:

```
- **Bash** — 34% of tool output (12 calls)
- **Read** — 22% of tool output (47 calls)
- **WebFetch** — 18% of tool output (3 calls)
```

This is the signal that lets the user trim deliberately rather than guess.

## Companion features

- **[Proactive advisor](./proactive-session-advisor.md)** — same classifier, but pushed to the user via the `UserPromptSubmit` hook so they don't have to ask.
- **[Token timeline](./token-timeline.md)** — per-turn cost trajectory that the verdict draws on.
- **[Pre-compact recovery](./pre-compact-recovery.md)** — if `/compact` has already fired and the user regrets the loss.

## Implementation

- `internal/mcpserver/tool_context_health.go` — MCP handler
- `internal/contexthealth/` — classifier rules + bloat aggregation
- `internal/mcpserver/slashcommands/health.md` — slash-prompt definition

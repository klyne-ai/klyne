# `klyne audit-sessions` — data-integrity verification

> Status: shipped. Read-only CLI. Exits 1 when it finds mismatches — that's a feature, not a smoke-test failure.

Verify klyne's stored session stats against the raw JSONL on disk. The point is trust: every headline number klyne shows (tokens, messages, cost) should be re-derivable from the source bytes, and `audit-sessions` is the assertion that runs that re-derivation.

## CLI

```bash
klyne audit-sessions                 # all sessions
klyne audit-sessions --limit 20      # most recent 20
klyne audit-sessions --since=168h    # last 7 days only
```

## What it checks

For each audited session, the command re-reads the source JSONL and compares against the SQLite-stored stats:

- Message count
- Input / output / cached-read / cached-write token totals
- Cost (when the model is priced)
- Earliest / latest message timestamps

A real run on the maintainer's machine: **17/17 ✓ (100 %)** at the time of the 30-day window cited in [`README.md`](../../README.md).

## Exit codes

| Exit | Meaning |
|---|---|
| `0` | Every audited session matched |
| `1` | One or more mismatches — output describes which session and which field |
| `>1` | Hard error (DB unreachable, JSONL unreadable, etc.) |

In CI / smoke-test contexts, treat exit 1 as informational unless you assert zero mismatches.

## Why it ships

klyne reads every headline number from a local SQLite store maintained by the daemon. If the ingester drifts from the source JSONL — a parse bug, a schema migration miss, a counting error — every other surface (cockpit, MCP tools, advisor) reports the wrong thing. `audit-sessions` is the user's red-team check on klyne itself.

## Implementation

- `cmd/klyne/audit.go` — CLI surface
- `internal/audit/` — comparison engine
- Re-parses JSONL directly; does not trust the daemon's in-memory state

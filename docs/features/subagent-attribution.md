# `klyne subagents` — Task-tool subagent attribution

> Status: shipped. Read-only CLI. Reads JSONL files directly — works even before the daemon has ingested them.

Claude Code writes Task-tool subagent transcripts under
`~/.claude/projects/<project>/<parent-session-id>/subagents/agent-XXX.jsonl`.
The parent session's `cost_usd` **does not** include this spend — the
cost engine only sees the final Task tool result, not the subagent's
full conversation.

`klyne subagents` reads those JSONLs directly and rolls them up per parent session:

```
| Parent session | Project                     | Subagents | Tokens in | Tokens out | Cache % | Last activity |
|---|---|---:|---:|---:|---:|---|
| ccf1c911       | …/Private/project-redacted  |        31 |    201.1M |      882k |     96% | 4d ago        |
```

A real run on the maintainer's transcripts found **134 subagents across
5 parent sessions, 555M tokens** — most of which was hidden from the
headline cost number until now.

## CLI

```bash
klyne subagents                       # all parent sessions, sorted by tokens
klyne subagents --since=168h          # last week only
klyne subagents --limit=10 --json     # structured output
```

## Implementation

- `cmd/klyne/subagents.go` — CLI surface
- `internal/subagent/` — JSONL walker, rollup, formatting
- No DB writes; no daemon dependency

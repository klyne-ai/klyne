# `klyne files` — per-file heatmap

> Status: shipped. Read-only CLI. No AI calls. Reads the local SQLite store; no JSONL re-parsing at query time.

Per-file activity rollup. Walks every locally-stored tool call, extracts the
`file_path` / `path` / `notebook_path` argument, and aggregates by file across sessions.

```
| Rank | File                                         | Reads | Edits | Writes | Sessions | Last touched |
|---:|----------------------------------------------|------:|------:|-------:|---------:|--------------|
|  1 | …services/discountEngineService.js           |    51 |    60 |      0 |        8 | 13d ago      |
```

Inspired by [token-dashboard](https://github.com/)'s "hotspot" view, adapted to klyne's read-only contract.

## CLI

```bash
klyne files                            # top 50 across all sessions
klyne files --since=168h --limit=20    # last week, top 20
klyne files --mutated-only             # drop files that were only Read
klyne files --json                     # structured output for piping
```

## Implementation

- `cmd/klyne/files.go` — CLI surface
- `internal/fileheat/` — path extractor + aggregation
- Per-tool dispatch table covers `Read`, `Edit`, `Write`, `Glob`, `Grep`, `MultiEdit`, `NotebookEdit`, `apply_patch`. Unknown MCP tools fall through to a generic "looks like a path" heuristic.

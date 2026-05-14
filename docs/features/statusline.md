# `klyne statusline`

> Status: shipped. Read-only CLI. No AI calls. Never breaks the user's prompt — exits 0 even when no session is found.

A single-line summary suitable for Claude Code's `statusLine` settings hook.

## Wire it up

```json
{
  "statusLine": {
    "type": "command",
    "command": "klyne statusline"
  }
}
```

## Formats

| Flag | Output | Use case |
|---|---|---|
| `--format=short` (default) | `klyne ▸ 38% ctx · 62k/160k · 5h 1%` | Most terminals |
| `--format=mini` | `38% · 1%` | Tight statuslines |
| `--format=plain` | `klyne 38% ctx · 62k/160k · 5h 1%` | No-unicode setups |

When no session is found in the current cwd, the output is `klyne ▸ idle` (still exit 0).

## Implementation

- `cmd/klyne/statusline.go` — CLI surface + format dispatch
- `cmd/klyne/statusline_test.go` — table-driven tests over the three formats and the idle path
- Reads from `internal/contexthealth` (context fill) + `internal/usage` (5h-window utilisation)

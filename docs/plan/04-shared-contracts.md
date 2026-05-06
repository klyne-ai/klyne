# 04 · Shared Contracts (W0 deliverables)

These artifacts must be **finalized by W0 before any fan-out**. Each line is a file path or a typed contract. After Wave 0 lands, **deviation requires a labeled `contract-change` PR with human review** — never a silent in-stream edit.

---

## 1. Go module + dependencies

`go.mod` declares `module github.com/<owner>/agentdeck`, Go 1.23.

Pinned dependencies (per spec §10):

| Package | Version | Purpose |
|---|---|---|
| `modernc.org/sqlite` | v1.44.3 | Pure-Go SQLite (CGO-free) |
| `github.com/fsnotify/fsnotify` | v1.7.x | File watching |
| `github.com/shirou/gopsutil/v3` | v3.x | Process detection (v1.1, but vendored now) |
| `github.com/go-chi/chi/v5` | v5.x | HTTP router |
| `github.com/r3labs/sse/v2` | v2.x | SSE server |
| `github.com/spf13/cobra` | latest | CLI sub-commands |
| `github.com/pelletier/go-toml/v2` | latest | Config |

---

## 2. `Connector` interface

File: **`internal/connectors/connector.go`** (W0-owned, frozen)

Verbatim from spec §11:

```go
package connectors

type Connector interface {
    Name() string                                    // "claude" | "codex"
    Discover(ctx context.Context) ([]string, error)  // existing JSONL files
    Watch(ctx context.Context, events chan<- RawEvent) error
    Parse(line []byte, path string) (*Message, error)
    Pricing() PricingTable                           // for cost engine
}
```

Plus supporting types in the **same file**: `RawEvent`, `Message`, `Session`, `PricingTable`, `ToolCall`, `ToolResult`. Every field carries a `json:"..."` tag — wire format is locked.

---

## 3. Canonical `Message` struct

Fields (spec §7 ingest pipeline):

```
id, session_id, cli, project_path, role, content,
tool_calls, tool_results, tokens_in, tokens_out,
cost_usd, model, ts, parent_uuid
```

**Locked decisions:**
- Time fields are `int64` epoch-ms. Connectors do not choose.
- `role ∈ {"user", "assistant", "tool", "system"}`.
- `cli ∈ {"claude", "codex"}`.

---

## 4. SQLite schema

Three migration files in `internal/store/migrations/`:

### `001_init.sql`
- `sessions` — id, cli, project_path, encoded_cwd, started_at, last_msg_at, msg_count, tokens_in, tokens_out, cost_usd, model, status, raw_path
- `messages` — id, session_id, parent_uuid, role, content, tool_name, tokens_in, tokens_out, cost_usd, model, ts
- `threads` — id, title, project_path, first_ts, last_ts (v1.1 stub now to avoid migration churn)
- `thread_sessions` — thread_id, session_id
- `schema_migrations` — version, applied_at

### `002_fts.sql`
- `messages_fts` virtual table (FTS5, BM25 ranking)
- INSERT/UPDATE/DELETE triggers wired to `messages`

### `003_summaries.sql`
- `session_summaries` — session_id, version, text, model, ts
- `compact_events` — session_id, ts, before_token_count, after_token_count (W15 needs it; cheaper to add now)

**PRAGMAs** (applied on every connection via hook — W1 implements):

```sql
PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;
PRAGMA temp_store = MEMORY;
PRAGMA mmap_size = 268435456;     -- 256 MB
PRAGMA busy_timeout = 5000;
PRAGMA foreign_keys = ON;
```

---

## 5. HTTP API surface

File: **`internal/api/contracts.go`** (W0-owned, frozen)

Frozen route list:

| Method | Path | Response DTO |
|---|---|---|
| GET  | `/sessions?cli=&project=&limit=&before=` | `SessionListResponse` |
| GET  | `/sessions/:id` | `SessionResponse` |
| GET  | `/sessions/:id/messages?limit=&before=` | `MessageListResponse` |
| GET  | `/sessions/:id/restore` | `RestoreResponse` |
| GET  | `/sessions/:id/summary` | `SummaryResponse` |
| GET  | `/search?q=&limit=` | `SearchResponse` |
| GET  | `/cost/summary?group=session\|project\|day\|model&since=&until=` | `CostSummaryResponse` |
| GET  | `/settings` | `SettingsResponse` |
| PUT  | `/settings` | `SettingsResponse` |
| GET  | `/wizard/detect` | `WizardDetectResponse` |
| POST | `/wizard/complete` | `204 No Content` |
| GET  | `/events` | SSE stream |
| GET  | `/healthz` | `{ok: true, version, schema_version}` |

---

## 6. SSE event shapes

File: **`internal/api/sse_events.go`** (W0-owned, frozen)

| Event | Payload |
|---|---|
| `MsgNew` | `{session_id, message_id, ts, role, model, tokens_in, tokens_out, cost_usd}` |
| `SummaryReady` | `{session_id, version, ts, model}` |
| `SessionUpdate` | `{session_id, last_msg_at, msg_count, cost_usd, status}` |
| `CostTick` | `{ts, total_usd_today}` |
| `ThreadRebuild` | `{thread_count, ts}` (v1.1 stub now to avoid event-name drift) |
| `CompactDetected` | `{session_id, ts}` (W15 emits) |

Each event JSON-tagged; each has a corresponding TS type in `ui/src/lib/types.ts`.

---

## 7. Config TOML schema

File: **`internal/config/schema.go`** (W0-owned, frozen)

```toml
[server]
addr = "127.0.0.1:7878"

[paths]
db = "~/.agentdeck/agentdeck.db"
pricing_override = "~/.agentdeck/pricing.json"

[connectors.claude]
enabled = true
root = "~/.claude/projects"

[connectors.codex]
enabled = true
root = "~/.codex/sessions"

[ai]
summary_model = "auto"      # or explicit provider/model
title_model   = "auto"
embed_model   = "off"        # v1.1
# provider_overrides allowed per task
```

---

## 8. Pricing JSON format

File: **`internal/cost/pricing_schema.go`** (W0-owned, frozen)

```json
{
  "version": 1,
  "models": {
    "claude-sonnet-4.5": {
      "prompt_per_mtok": 3.00,
      "completion_per_mtok": 15.00,
      "cache_read_per_mtok": 0.30,
      "cache_write_per_mtok": 3.75
    }
  }
}
```

Required model coverage in v1 pricing table (W9 fills in values):
`claude-sonnet-4.5`, `claude-haiku-4`, `claude-opus-4.6`, `gpt-5`, `gpt-5-mini`, `gpt-5-nano`, `gemini-2.5-flash`, `gemini-2.5-flash-lite`, `text-embedding-3-small`, `llama3.1:8b` (free).

---

## 9. Repo skeleton (spec §11)

W0 produces every directory and an empty/stub file at every leaf in spec §11. **No improvisation.** Improvisation here cascades into 18 collisions later.

---

## 10. CI / lint / format config

| File | Contents |
|---|---|
| `.golangci.yml` | enable `errcheck`, `gosec`, `govet`, `staticcheck`, `unused`, `gocyclo`, `goconst`, `gofumpt`. Disable nothing without a justifying comment. |
| `.editorconfig` | 2-space SQL/TS, tab Go, LF line endings |
| `.github/workflows/ci.yml` | Matrix `os=[ubuntu-latest, macos-latest, windows-latest]`, Go 1.23. Steps: `go test ./... -race -coverprofile=cov.out`, `golangci-lint`, `cd ui && npm ci && npm run check && npm run test`. Coverage gate: 80% on changed lines. |

---

## Source of truth + change protocol

- Every type that crosses workstream boundaries lives in **exactly one** W0-owned file.
- Changing any file in this document requires a PR labeled **`contract-change`** with **human review**.
- The PR description must list every workstream affected and post a "heads up" on each affected branch's PR.
- No file in this document may be edited by an in-stream agent without that PR landing first.

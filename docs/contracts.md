# Shared contracts index (W0-frozen)

This file is the cross-reference index for every W0-owned contract. It
exists so any downstream workstream can find the exact file (and line
range) that owns a given DTO, route, event, or schema element.

> **Change protocol.** Every file listed here is locked. Editing any of
> them requires a PR labeled **`contract-change`** with human review and
> a heads-up posted to every affected workstream branch. In-stream
> agents must NOT silently edit these files. The PR description must
> list every workstream affected.

The canonical authority for what belongs in W0 is
[`docs/plan/04-shared-contracts.md`](plan/04-shared-contracts.md). This
file is the *implementation index*; that file is the *spec*.

---

## 1. Connector interface + canonical types

**File:** [`internal/connectors/connector.go`](../internal/connectors/connector.go)
**Spec:** §7 (data flow), §11 (interface), §18 #9 (locked CLI set)
**Plan:** `04-shared-contracts.md` §2, §3

| Symbol            | Kind      | Purpose |
|-------------------|-----------|---------|
| `Connector`       | interface | The 5-method contribution surface (Name, Discover, Watch, Parse, Pricing). |
| `CLI`             | string    | Enum: `claude`, `codex`. v1 set is locked. |
| `Role`            | string    | Enum: `user`, `assistant`, `tool`, `system`. |
| `SessionStatus`   | string    | Enum: `active`, `idle`, `compacted`. |
| `RawEvent`        | struct    | One tailed JSONL line + source coords. |
| `ToolCall`        | struct    | Native tool invocation payload. |
| `ToolResult`      | struct    | Tool execution response payload. |
| `Message`         | struct    | Canonical wire format — every field JSON-tagged. |
| `Session`         | struct    | Mirror of the `sessions` SQL row. |
| `PricingTable`    | struct    | Per-connector pricing contribution. |
| `PerTokenRates`   | struct    | LiteLLM-style rate card (mirrors `cost.PerTokenRates`). |

**Locked decisions in this file:**

- `Message.Ts`, `Session.StartedAt`, `Session.LastMsgAt`, etc. are `int64`
  epoch-milliseconds. Connectors do not pick a different unit.
- `Message.Role` is one of the four `Role*` constants.
- `Message.CLI` is one of the two `CLI*` constants.

---

## 2. SQL schema

**Files:**

- [`internal/store/migrations/001_init.sql`](../internal/store/migrations/001_init.sql)
- [`internal/store/migrations/002_fts.sql`](../internal/store/migrations/002_fts.sql)
- [`internal/store/migrations/003_summaries.sql`](../internal/store/migrations/003_summaries.sql)
- [`internal/store/migrations/embed.go`](../internal/store/migrations/embed.go)

**Spec:** §5 (PRAGMAs), §7 (schema)
**Plan:** `04-shared-contracts.md` §4

**Tables introduced (final list):**

| Migration | Table              | Notes |
|-----------|--------------------|-------|
| 001       | `schema_migrations`| Version tracking (W1 writes rows). |
| 001       | `sessions`         | One row per CLI session. |
| 001       | `messages`         | One row per turn. FK → `sessions(id)`. |
| 001       | `threads`          | v1.1 surface, declared early. |
| 001       | `thread_sessions`  | Join table for `threads`. |
| 002       | `messages_fts`     | FTS5 virtual table over `messages.content`, BM25 ranked. |
| 002       | (triggers) `messages_ai/au/ad` | Keep `messages_fts` in sync. |
| 003       | `session_summaries`| (session_id, version) PK. |
| 003       | `compact_events`   | W15 / cost dashboard input. |

**PRAGMAs (W1 applies on every connection — single source of truth):**
declared in `embed.go` as `PerConnectionPRAGMAs`.

**Migration ordering rule.** Add new migrations as `004_*.sql`,
`005_*.sql`, ... — never edit a landed migration. The migrations test
(`migrations_test.go`) asserts both lexicographic ordering and that
every required contract table exists after all migrations apply.

---

## 3. HTTP API contract

**File:** [`internal/api/contracts.go`](../internal/api/contracts.go)
**Spec:** §7
**Plan:** `04-shared-contracts.md` §5

**Routes** (every `Route*` constant is asserted unique by
`contracts_test.go`):

| Constant                | Path                          | Owner |
|-------------------------|-------------------------------|-------|
| `RouteSessions`         | `/sessions`                   | W7    |
| `RouteSession`          | `/sessions/{id}`              | W7    |
| `RouteSessionMessages`  | `/sessions/{id}/messages`     | W7    |
| `RouteSessionRestore`   | `/sessions/{id}/restore`      | W7 + W15 |
| `RouteSessionSummary`   | `/sessions/{id}/summary`      | W7 + W11 |
| `RouteSearch`           | `/search`                     | W7 + W3 |
| `RouteCostSummary`      | `/cost/summary`               | W7 + W9 |
| `RouteSettings`         | `/settings`                   | W7 + W6 |
| `RouteWizardDetect`     | `/wizard/detect`              | W7    |
| `RouteWizardComplete`   | `/wizard/complete`            | W7    |
| `RouteEvents`           | `/events`                     | W8    |
| `RouteHealthz`          | `/healthz`                    | W7    |

**DTOs declared here (response unless noted):**

`SessionListResponse`, `SessionResponse`, `MessageListResponse`,
`RestoreResponse`, `SummaryResponse`, `SearchResponse`, `SearchHit`,
`CostSummaryResponse`, `CostBucket`, `CostGroup` (enum),
`SettingsResponse`, `SettingsAI`, `TaskModel`, `DetectedProviders`,
`SettingsUpdateRequest` (request), `WizardDetectResponse`,
`WizardConnectors`, `WizardRecommendation`, `HealthzResponse`.

---

## 4. SSE event contract

**File:** [`internal/api/sse_events.go`](../internal/api/sse_events.go)
**Spec:** §7 (live update flow)
**Plan:** `04-shared-contracts.md` §6

| Event constant           | Wire name           | Payload struct      |
|--------------------------|---------------------|---------------------|
| `EventMsgNew`            | `msg.new`           | `MsgNew`            |
| `EventSummaryReady`      | `summary.ready`     | `SummaryReady`      |
| `EventSessionUpdate`     | `session.update`    | `SessionUpdate`     |
| `EventCostTick`          | `cost.tick`         | `CostTick`          |
| `EventThreadRebuild`     | `thread.rebuild`    | `ThreadRebuild`     |
| `EventCompactDetected`   | `compact.detected`  | `CompactDetected`   |

`AllEvents()` returns the canonical list.

---

## 5. Config schema

**File:** [`internal/config/schema.go`](../internal/config/schema.go)
**Spec:** §8 (BYOK / AI section), §11 (paths)
**Plan:** `04-shared-contracts.md` §7

| Type                      | TOML table          |
|---------------------------|---------------------|
| `Config`                  | (root)              |
| `ServerConfig`            | `[server]`          |
| `PathsConfig`             | `[paths]`           |
| `ConnectorsConfig`        | `[connectors]`      |
| `ClaudeConnectorConfig`   | `[connectors.claude]` |
| `CodexConnectorConfig`    | `[connectors.codex]`  |
| `AIConfig`                | `[ai]`              |

`Defaults()` returns the documented v1 defaults — W6's loader starts
from `Defaults()` and overlays parsed TOML on top.

Sentinel values: `AIModelAuto = "auto"`, `AIModelOff = "off"`.

---

## 6. Pricing schema

**File:** [`internal/cost/pricing_schema.go`](../internal/cost/pricing_schema.go)
**Spec:** §18 #6 (LiteLLM-style table)
**Plan:** `04-shared-contracts.md` §8

| Type             | Notes |
|------------------|-------|
| `PricingFile`    | Top-level `{version, models}` JSON. |
| `PerTokenRates`  | Per-model rates, USD per 1M tokens. Mirror of the field in `internal/connectors/connector.go`. |

W0 ships **schema only**. W9 fills in the v1 default rate values for the
required model coverage list (claude-sonnet-4.5, claude-haiku-4,
claude-opus-4.6, gpt-5, gpt-5-mini, gpt-5-nano, gemini-2.5-flash,
gemini-2.5-flash-lite, text-embedding-3-small, llama3.1:8b).

---

## 7. Contract dump tool

**Files:**

- [`tools/dump-contracts/main.go`](../tools/dump-contracts/main.go)
- [`tools/dump-contracts/main_test.go`](../tools/dump-contracts/main_test.go)

**Purpose.** A tiny Go AST walker that emits a JSON dump of every
exported struct (and its fields + JSON tags) declared in the W0-frozen
contract files. W13's `ui/scripts/check-contracts.ts` (later workstream)
loads this dump and asserts the TS types in `ui/src/lib/types.ts` match
field-by-field.

**Default scan list:**

```
internal/api/contracts.go
internal/api/sse_events.go
internal/connectors/connector.go
```

Run from the repo root: `go run ./tools/dump-contracts -out contracts.json`

---

## 8. CLI entrypoint

**Files:**

- [`cmd/agentdeck/main.go`](../cmd/agentdeck/main.go)
- [`cmd/agentdeck/start.go`](../cmd/agentdeck/start.go)
- [`cmd/agentdeck/stop.go`](../cmd/agentdeck/stop.go)
- [`cmd/agentdeck/doctor.go`](../cmd/agentdeck/doctor.go)

W0 ships stubs only. Every body prints `not implemented (W12)` (or, for
`doctor`, a JSON envelope `{"status":"stub","note":"not implemented (W12)"}`)
and exits 0. The version string is hardcoded `v0.0.0-bootstrap` and
will be wired to a build-time `-ldflags` value by W17.

---

## Cross-stream "where do I find ...?" cheatsheet

| If you need ...                                | Look here |
|------------------------------------------------|-----------|
| The wire format for a chat message             | `internal/connectors/connector.go` → `Message` |
| The list of HTTP routes the daemon exposes     | `internal/api/contracts.go` → `AllRoutes()` |
| The list of SSE event names                    | `internal/api/sse_events.go` → `AllEvents()` |
| The TOML field name for a config option        | `internal/config/schema.go` |
| The shape of `pricing.json`                    | `internal/cost/pricing_schema.go` |
| The PRAGMA list for a fresh SQLite connection  | `internal/store/migrations/embed.go` → `PerConnectionPRAGMAs` |
| The ordered list of SQL migrations             | `internal/store/migrations/*.sql` (lexicographic) |
| A JSON dump of every DTO (for TS contract-check) | `go run ./tools/dump-contracts` |

---

## Boundary reminders

W0 produces **types and stubs only**. The following implementations
belong to other workstreams and **must not** be added to any file in
this index:

- Handler bodies → W7
- Store DB open/migrate → W1
- Connector parse logic → W4 (Claude), W5 (Codex)
- Provider integrations → W10
- Pricing data values → W9
- UI code → W13 / W14
- Compact recovery logic → W15

If you find yourself wanting to add one of these to a W0 file, that's
the signal to instead create the implementation file in its target
package and have it depend on the W0 contract.

Now I have the full spec. Let me produce the multi-agent execution plan.

---

# klyne — Multi-Agent Parallel Execution Plan

> Companion to the v1 shipping spec. Read this top-to-bottom once; then execute Section 7 ("Recommended kickoff sequence") to start work today.

---

## Executive summary

The v1 14-day plan in §15 of the spec is written as a single-developer sequence. To parallelize across multiple AI agents, we restructure it as a **DAG of 19 workstreams** that run in **5 waves**. Wave 0 is a single bootstrap agent that produces the contracts every other workstream depends on. Waves 1–4 fan out to up to 7 agents in parallel. Critical path is **W0 → W1 (store) → W4 (Claude connector) → W11 (summarizer) → W15 (compact recovery) → W18 (release)**, ~10 effective serial days; the remaining ~4 days of slack are absorbed by parallelism, hitting the 14-day budget with breathing room.

The single-writer rule is enforced by giving each workstream **exclusive ownership** of one or more directories. The contracts that span workstream boundaries (`Connector` interface, `Message` struct, SQLite schema, HTTP/SSE shapes, pricing JSON, config TOML) are **defined once in Wave 0** and frozen — drift is the dominant integration risk and Wave 0 mitigates it.

---

## 1. Workstream decomposition

Workstreams are listed by ID. Owned paths follow §11 of the spec exactly. Sizes: XS ≤ 0.5d, S ≤ 1d, M ≤ 1.5d, L ≤ 2.5d, XL ≤ 4d (single-agent-equivalent effort).

### W0 — Bootstrap & shared contracts
- **Goal:** Produce repo skeleton, locked interfaces, schema, and CI scaffolding so all other agents can fan out without colliding.
- **Owned paths:**
  - `/Users/mohitpatel/Desktop/Project/agentdeck/go.mod`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/go.sum`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/Makefile`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/README.md` (stub only — full copy in W17)
  - `/Users/mohitpatel/Desktop/Project/agentdeck/LICENSE`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/.gitignore`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/.golangci.yml`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/.editorconfig`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/.github/workflows/ci.yml` (lint + test only; release in W17)
  - `/Users/mohitpatel/Desktop/Project/agentdeck/.github/ISSUE_TEMPLATE/*.yml`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/cmd/klyne/main.go` (cobra root with `start`/`stop`/`doctor` subcommand stubs returning "not implemented")
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/connectors/connector.go` (interface + `RawEvent`, `Message`, `PricingTable` types — **no implementations**)
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/store/migrations/001_init.sql`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/store/migrations/002_fts.sql`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/store/migrations/003_summaries.sql`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/store/migrations/embed.go` (just `//go:embed *.sql` + `var FS embed.FS`)
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/api/contracts.go` (route list constants, request/response DTOs — **handlers in W7**)
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/api/sse_events.go` (SSE event payload types: `MsgNew`, `SummaryReady`, `SessionUpdate`, `CostTick`, `ThreadRebuild`)
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/config/schema.go` (typed struct for `~/.klyne/config.toml` — **loader in W6**)
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/cost/pricing_schema.go` (typed struct + JSON tags for the LiteLLM-style pricing table — **table data + lookup in W9**)
  - `/Users/mohitpatel/Desktop/Project/agentdeck/examples/sample-jsonl/README.md` (describes fixture layout)
  - `/Users/mohitpatel/Desktop/Project/agentdeck/examples/sample-jsonl/claude/*.jsonl` (3–5 hand-collected fixture sessions, including one that hits `/compact`)
  - `/Users/mohitpatel/Desktop/Project/agentdeck/examples/sample-jsonl/codex/*.jsonl` (3–5 fixture sessions)
  - `/Users/mohitpatel/Desktop/Project/agentdeck/docs/contracts.md` (this section, frozen — change requires PR + cross-stream review)
- **Inputs:** spec §7, §8, §10, §11, §18.
- **Outputs:** every contract, schema, fixture, and module skeleton downstream agents need. Compiles green and `go test ./...` passes (no tests yet, but no errors).
- **Dependencies:** none.
- **Recommended agent:** `general-purpose` (Claude Code session, opus). One agent. Do not parallelize this.
- **Effort:** L (~2 days). This is the single biggest leverage point in the entire plan.
- **Acceptance criteria:**
  - `go build ./...` succeeds.
  - `go vet ./...` clean.
  - `golangci-lint run` clean.
  - `go test ./...` passes (will be empty).
  - CI green on push.
  - `klyne doctor` exits 0 with a "stub" message.
  - The 5 spec sections that contracts derive from (§7 schema, §8 selector inputs, §11 layout, §18 decisions, §12 budgets) are referenced by file:line in `docs/contracts.md`.
  - Every type that crosses a workstream boundary lives in exactly one file owned by W0.

### W1 — Store: open + migrate + dual handles
- **Goal:** Implement the SQLite store layer (open with WAL pragmas, dual `*sql.DB` handles, migration runner) so every other backend stream can persist and read.
- **Owned paths:**
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/store/db.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/store/db_test.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/store/migrate.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/store/migrate_test.go`
- **Inputs:** `internal/store/migrations/*.sql` (W0), spec §5 PRAGMAs, §7 schema.
- **Outputs:** `store.Open(path string) (*store.DB, error)` returning an object with `.Read() *sql.DB` and `.Write() *sql.DB`. `MaxOpenConns=1` on write, `N` on read. Migration runner is idempotent, version-tracked in a `schema_migrations` table.
- **Dependencies:** W0.
- **Recommended agent:** `golang-patterns` + `tdd-guide` (Claude Code, sonnet).
- **Effort:** S.
- **Acceptance criteria:**
  - All 6 PRAGMAs in §5 verified by querying `PRAGMA <name>` after open.
  - Migration runner re-applies cleanly (test: open twice, no error).
  - 80%+ coverage in `internal/store/`.
  - Concurrent-write stress test: 10 goroutines INSERT against the write handle, no `SQLITE_BUSY`.

### W2 — Store: messages + sessions DAOs
- **Goal:** CRUD for `messages` and `sessions` tables, with the rollup (`last_msg_at`, `msg_count`, `cost_total`) handled inside `InsertMessage`.
- **Owned paths:**
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/store/messages.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/store/messages_test.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/store/sessions.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/store/sessions_test.go`
- **Inputs:** W1 (`store.DB`), W0 (`Message` type).
- **Outputs:**
  - `messages.Insert(ctx, db, *Message) error` (also bumps session counters in same tx).
  - `messages.ListBySession(ctx, db, sessionID, limit, before) ([]*Message, error)`.
  - `sessions.Upsert(ctx, db, *Session) error`.
  - `sessions.List(ctx, db, filter) ([]*Session, error)`.
  - `sessions.Get(ctx, db, id) (*Session, error)`.
- **Dependencies:** W1.
- **Recommended agent:** `golang-patterns` + `tdd-guide`.
- **Effort:** S.
- **Acceptance criteria:**
  - 80%+ coverage.
  - Inserting 5,000 messages in one tx completes in <1 s on dev laptop (rough proxy for §12 ingest budget; real bench is W16).
  - `ListBySession` returns messages in `ts ASC` order; pagination cursor stable across inserts.

### W3 — Store: search (FTS5 BM25) + summaries DAO
- **Goal:** FTS5 search query + summary CRUD.
- **Owned paths:**
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/store/search.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/store/search_test.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/store/summaries.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/store/summaries_test.go`
- **Inputs:** W1, W2 (for `messages_fts` triggers — defined in W0 migration but exercised here).
- **Outputs:**
  - `search.Query(ctx, db, q string, limit int) ([]SearchHit, error)` using BM25 ranking over `messages_fts`.
  - `summaries.Latest(ctx, db, sessionID) (*Summary, error)`.
  - `summaries.Insert(ctx, db, *Summary) error` (auto-increments `version`).
- **Dependencies:** W1, W2.
- **Recommended agent:** `golang-patterns` + `tdd-guide`.
- **Effort:** S.
- **Acceptance criteria:**
  - Test fixture with 1,000 messages; BM25 ranks the seeded "needle" message first.
  - Search returns within 50 ms on 10K-msg test DB (sanity check; full p95 in W16).
  - 80%+ coverage.

### W4 — Claude Code connector
- **Goal:** Discover, watch, and parse `~/.claude/projects/<encoded-cwd>/<uuid>.jsonl` into canonical `Message` and `Session`.
- **Owned paths:**
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/connectors/claude/claude.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/connectors/claude/parse.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/connectors/claude/parse_test.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/connectors/claude/watch.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/connectors/claude/watch_test.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/connectors/claude/decode_cwd.go` (decodes the encoded CWD path segment)
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/connectors/claude/decode_cwd_test.go`
- **Inputs:** W0 (`Connector` interface, `Message` type, fixtures in `examples/sample-jsonl/claude/`).
- **Outputs:** A `claude.Connector` value that satisfies `connectors.Connector`. Pricing comes from W9 via injected `cost.Lookup` — connector itself does not own pricing data, just declares which models it emits.
- **Dependencies:** W0.
- **Recommended agent:** `golang-patterns` + `tdd-guide` (parsing JSONL is exactly the case `regex-vs-llm-structured-text` calls "regex/structured parser, no LLM").
- **Effort:** M.
- **Acceptance criteria:**
  - Parses every fixture in `examples/sample-jsonl/claude/` to `[]*Message` with no errors.
  - Covers all 6 message types observed in fixtures (user, assistant, tool_use, tool_result, system, summary/compact-marker).
  - `Watch` test: writes new lines to a temp file, asserts events arrive on channel within 200 ms.
  - Tolerates unknown fields (forward-compat), skips malformed lines with a warning log not a panic.
  - Decoded CWD round-trips: `decode(encode(x)) == x` for 50+ random paths.
  - 80%+ coverage.

### W5 — Codex CLI connector
- **Goal:** Same as W4, for `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl`.
- **Owned paths:**
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/connectors/codex/codex.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/connectors/codex/parse.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/connectors/codex/parse_test.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/connectors/codex/watch.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/connectors/codex/watch_test.go`
- **Inputs:** W0 (`Connector` interface, fixtures in `examples/sample-jsonl/codex/`).
- **Outputs:** `codex.Connector` value satisfying `connectors.Connector`.
- **Dependencies:** W0. **Independent of W4** — runs in parallel.
- **Recommended agent:** `golang-patterns` + `tdd-guide`.
- **Effort:** M.
- **Acceptance criteria:** mirror of W4, plus: handles the date-partitioned directory layout (today's directory may not exist yet at watcher start; create-aware).

### W6 — Config loader
- **Goal:** Read/write `~/.klyne/config.toml`, exposing typed config to the rest of the app.
- **Owned paths:**
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/config/config.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/config/config_test.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/config/paths.go` (XDG-aware default paths per OS)
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/config/paths_test.go`
- **Inputs:** W0 (`schema.go`).
- **Outputs:** `config.Load() (*Config, error)`, `config.Save(*Config) error`. Atomic writes (write to temp, rename). Defaults applied if file missing.
- **Dependencies:** W0.
- **Recommended agent:** `golang-patterns` + `tdd-guide`.
- **Effort:** XS.
- **Acceptance criteria:** round-trip TOML; missing-file path produces defaults and writes; 80%+ coverage.

### W7 — HTTP API + handlers (no AI yet)
- **Goal:** chi router and the read-only handlers that frontend needs on Day 2: `/sessions`, `/sessions/:id`, `/sessions/:id/messages`, `/search`, `/cost/summary`, `/settings` (GET/PUT).
- **Owned paths:**
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/api/http.go` (router + middleware: logger, recover, CORS-loopback-only)
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/api/http_test.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/api/handlers/sessions.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/api/handlers/sessions_test.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/api/handlers/search.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/api/handlers/search_test.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/api/handlers/cost.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/api/handlers/cost_test.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/api/handlers/settings.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/api/handlers/settings_test.go`
- **Inputs:** W0 (`api/contracts.go`), W2, W3, W6.
- **Outputs:** `api.NewRouter(deps Deps) http.Handler`. Each handler validates its DTO against the contract and delegates to store DAOs.
- **Dependencies:** W0, W2, W3, W6.
- **Recommended agent:** `golang-patterns` + `tdd-guide`.
- **Effort:** M.
- **Acceptance criteria:**
  - Every route in `contracts.go` has a handler + a passing test using `httptest`.
  - 4xx on bad inputs; never 5xx for client errors.
  - 80%+ coverage.
  - **Hard boundary:** does NOT register `/events` (that's W8) and does NOT register AI/summary routes (W11/W15).

### W8 — SSE hub + `/events` endpoint
- **Goal:** Single broadcast hub that backend writers fan out to per-tab subscribers.
- **Owned paths:**
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/api/sse.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/api/sse_test.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/api/handlers/events.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/api/handlers/events_test.go`
- **Inputs:** W0 (`sse_events.go`), W7 (router registration point — W7 exposes a `Mount(r chi.Router)` hook the SSE handler calls).
- **Outputs:** `sse.NewHub() *Hub` with `Publish(event)` and `Subscribe() (<-chan Event, cancel)`. Heartbeat every 15 s. Auto-reconnect-friendly (Last-Event-ID supported).
- **Dependencies:** W0, W7 (must agree on mount point).
- **Recommended agent:** `golang-patterns` + `tdd-guide`.
- **Effort:** S.
- **Acceptance criteria:**
  - 100 concurrent subscribers, 1,000 events/s, no dropped events on slow consumers (back-pressure via per-sub buffered channel + drop-with-log policy documented).
  - End-to-end test: publish `MsgNew`, EventSource client receives it, payload matches.
  - p95 latency from `Publish` to client receive: <100 ms locally (§12).

### W9 — Cost engine + pricing table
- **Goal:** Embed a LiteLLM-style pricing JSON in the binary; provide `Lookup(model) -> per-token rates` and `Cost(tokensIn, tokensOut, model) -> usd`.
- **Owned paths:**
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/cost/pricing.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/cost/pricing_test.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/cost/pricing.json` (embedded via `//go:embed`)
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/cost/refresh.go` (loads override from `~/.klyne/pricing.json` if present)
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/cost/refresh_test.go`
- **Inputs:** W0 (`pricing_schema.go`), W6 (config dir).
- **Outputs:** `cost.New() *Engine` exposing `Lookup`, `Cost`, `RollupSession(sessionID)`, `RollupProject(path, since)`.
- **Dependencies:** W0, W6.
- **Recommended agent:** `golang-patterns` + `tdd-guide`.
- **Effort:** S.
- **Acceptance criteria:**
  - Pricing for at minimum: claude-sonnet-4.5, claude-haiku-4, claude-opus-4.6, gpt-5, gpt-5-mini, gpt-5-nano, gemini-2.5-flash, gemini-2.5-flash-lite, text-embedding-3-small, llama3.1:8b (free).
  - Per-session $ matches `ccusage` output to within 0.5% on a fixture (this is the spec's D7 acceptance criterion).
  - Override file overrides embedded values; tests cover both paths.
  - 80%+ coverage.

### W10 — AI providers (Anthropic / OpenAI / Gemini / Ollama) — API-key only
- **Goal:** A `Provider` interface and 4 implementations. **Never touches `~/.claude` OAuth or `~/.codex/auth.json`** (§17, §18).
- **Owned paths:**
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/ai/provider.go` (interface + shared types: `ChatRequest`, `ChatResponse`, `EmbedRequest`, `EmbedResponse`, `ProviderInfo`)
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/ai/providers/anthropic.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/ai/providers/anthropic_test.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/ai/providers/openai.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/ai/providers/openai_test.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/ai/providers/gemini.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/ai/providers/gemini_test.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/ai/providers/ollama.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/ai/providers/ollama_test.go`
- **Inputs:** W0 (config schema for which envs to read), spec §8.
- **Outputs:** Each provider satisfies the `ai.Provider` interface. `ai.providers.DetectAvailable()` returns `[]ProviderInfo` based on env detection (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`, `http://localhost:11434/api/tags`).
- **Dependencies:** W0.
- **Recommended agent:** `general-purpose` with `claude-api` skill loaded (knows current Claude Messages API). Add `mcp-server-patterns` if any provider exposes streaming we want.
- **Effort:** M.
- **Acceptance criteria:**
  - Each provider tested with `httptest.Server` mocking the upstream API; no real network calls in CI.
  - Anthropic provider rejects (returns error, never tries) requests if the only credential is an OAuth token (defensive: the env var name `ANTHROPIC_API_KEY` is the only path; if absent, return `ErrNoCredential`).
  - Ollama provider degrades gracefully if `localhost:11434` is unreachable.
  - 80%+ coverage.

### W11 — Smart selector + summarize/title tasks
- **Goal:** §8 routing logic plus the actual summarize and title tasks that run on the message stream.
- **Owned paths:**
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/ai/selector.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/ai/selector_test.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/ai/tasks/summarize.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/ai/tasks/summarize_test.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/ai/tasks/title.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/ai/tasks/title_test.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/ai/tasks/runner.go` (worker goroutine: every 50 messages or on compact event, fetch window, run summarize, persist)
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/ai/tasks/runner_test.go`
- **Inputs:** W0 (config), W3 (summaries DAO), W10 (providers), spec §8 matrix.
- **Outputs:**
  - `selector.Pick(task TaskKind, available []ProviderInfo) (Choice, Reason)` — pure function, table-driven, easy to test.
  - `tasks.Runner` that subscribes to `MsgNew` events from W8 and triggers summarization windows.
  - Summary persisted via W3, broadcast via W8 as `SummaryReady`.
- **Dependencies:** W0, W3, W8, W10.
- **Recommended agent:** `general-purpose` with `cost-aware-llm-pipeline` and `claude-api` skills.
- **Effort:** M.
- **Acceptance criteria:**
  - Selector unit tests cover all 4×4 combinations (4 tasks × 4 provider availabilities) of §8.
  - Reason strings are user-readable (used in wizard UI).
  - End-to-end summarize test using mock providers: 50-msg fixture → summary persisted → SSE event broadcast.
  - 80%+ coverage.

### W12 — Wiring layer (`internal/app`) + cobra commands
- **Goal:** Compose all the modules into a running daemon. Implement `klyne start`, `klyne stop`, `klyne doctor`. Bind `127.0.0.1:7878` only.
- **Owned paths:**
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/app/app.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/app/app_test.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/app/lifecycle.go` (graceful shutdown, signal handling)
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/app/lifecycle_test.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/app/openbrowser.go` (cross-OS `xdg-open`/`open`/`start`)
  - `/Users/mohitpatel/Desktop/Project/agentdeck/cmd/klyne/start.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/cmd/klyne/stop.go`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/cmd/klyne/doctor.go`
- **Inputs:** all of W1–W11.
- **Outputs:** A single binary that, when invoked with no args (cobra root → start), spins up the watcher, store, API, SSE, AI runner, and opens the browser.
- **Dependencies:** W1, W2, W3, W4, W5, W6, W7, W8, W9, W10, W11. **This is the integration node.**
- **Recommended agent:** `general-purpose` (Claude Code, opus). Single-agent only.
- **Effort:** M.
- **Acceptance criteria:**
  - `klyne` (no args) runs end-to-end against a tempdir with the fixture JSONL: starts, ingests, serves, browser-opens (suppressed in CI).
  - `klyne doctor` reports: detected JSONL roots, detected provider keys, DB path, DB size, schema version, daemon version. Exits 0 if all green, 1 otherwise.
  - `klyne stop` writes pidfile to `~/.klyne/daemon.pid` on start; `stop` reads it and SIGTERMs.
  - SIGTERM → flushes writer queue → closes DB → exits within 2 s.

### W13 — Frontend foundation: SvelteKit shell + lib + stores
- **Goal:** SvelteKit 5 scaffold with Tailwind 4, runes-based stores, API client, SSE client. **No business components yet.**
- **Owned paths:**
  - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/package.json`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/svelte.config.js` (adapter-static)
  - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/vite.config.ts`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/tsconfig.json`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/tailwind.config.ts`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/postcss.config.js`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/.eslintrc.cjs`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/src/app.html`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/src/app.css`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/src/lib/api.ts` (typed fetch wrappers — types mirror W0 `contracts.go` exactly)
  - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/src/lib/api.test.ts`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/src/lib/sse.ts` (EventSource client; reconnect; payload types mirror W0 `sse_events.go`)
  - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/src/lib/sse.test.ts`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/src/lib/types.ts` (TS mirror of `Message`, `Session`, `Summary`, etc.)
  - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/src/lib/stores.svelte.ts`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/src/lib/stores.svelte.test.ts`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/src/routes/+layout.svelte` (just shell; navigation in W14)
  - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/src/routes/+page.svelte` (placeholder "loading…")
- **Inputs:** W0 (`contracts.go`, `sse_events.go` — agent must read Go types and translate to TS; types.ts is checked-in canonical TS that downstream UI workstreams import).
- **Outputs:** Buildable SvelteKit app, `npm run build` produces `ui/build/`. Type-safe `api.fetchSessions()`, `api.search(q)`, etc. Type-safe `sse.subscribe(handler)`.
- **Dependencies:** W0.
- **Recommended agent:** `coding-standards` + `frontend-patterns` + `tdd-guide` (Claude Code, sonnet).
- **Effort:** M.
- **Acceptance criteria:**
  - `npm run build` succeeds; output goes to `ui/build/`.
  - `npm run check` (svelte-check) clean.
  - `npm run test` (vitest) passes; 80%+ coverage on `lib/`.
  - `api.ts` and `sse.ts` types are 1:1 with W0 contracts (mechanical script in `ui/scripts/check-contracts.ts` parses Go contracts file and asserts TS types match — written here, run in CI).
  - **Hard boundary:** no business components (`SessionList`, `SessionView`, etc.). Those are W14.

### W14 — Frontend components + routes (sessions/search/cost)
- **Goal:** All v1 user-facing pages except wizard and restore-context modal.
- **Owned paths:**
  - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/src/lib/components/SessionList.svelte`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/src/lib/components/SessionList.test.ts`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/src/lib/components/SessionView.svelte`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/src/lib/components/SessionView.test.ts`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/src/lib/components/SearchBar.svelte`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/src/lib/components/SearchBar.test.ts`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/src/lib/components/CostPanel.svelte`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/src/lib/components/CostPanel.test.ts`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/src/lib/components/MessageBubble.svelte`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/src/lib/components/MessageBubble.test.ts`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/src/lib/components/ToolCallBlock.svelte`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/src/lib/components/ToolCallBlock.test.ts`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/src/routes/+page.svelte` (overwrites W13's placeholder — **W13 hands off this single file** to W14)
  - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/src/routes/sessions/[id]/+page.svelte`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/src/routes/search/+page.svelte`
- **Inputs:** W13 (lib, types, stores), W7 + W8 (live API).
- **Outputs:** Functional dashboard, session detail, search page, cost panel; live updating via SSE.
- **Dependencies:** W13, W7, W8.
- **Recommended agent:** `frontend-patterns` + `coding-standards` + `tdd-guide`. (`impeccable` skill is overkill for v1 — spec §10 says Tailwind defaults only.)
- **Effort:** L.
- **Acceptance criteria:**
  - All four flows in spec §6 (A install/dashboard, B daily, D find old thread) work end-to-end against a running daemon.
  - 80%+ coverage on components (Vitest + @testing-library/svelte).
  - Visible empty states for: no sessions, no search results, no cost yet.
  - Keyboard shortcuts: `/` focuses search, `j/k` navigates session list, `Enter` opens.

### W15 — Compact recovery + wizard + restore-context (cross-cutting feature slice)
- **Goal:** The "killer demo" flow C plus the first-run wizard. Touches both backend and frontend but **owns disjoint files** from W11/W12/W14.
- **Owned paths:**
  - **Backend:**
    - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/api/handlers/restore.go` (`GET /sessions/:id/restore` returns `{ summary, last_messages, resume_command }`)
    - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/api/handlers/restore_test.go`
    - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/api/handlers/wizard.go` (`GET /wizard/detect`, `POST /wizard/complete`)
    - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/api/handlers/wizard_test.go`
    - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/connectors/claude/compact.go` (heuristic: synthetic summary message + token-count drop signal)
    - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/connectors/claude/compact_test.go`
    - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/resume/builder.go` (build `claude --resume <id>` / `codex resume --last` strings, OS-quoted)
    - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/resume/builder_test.go`
  - **Frontend:**
    - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/src/lib/components/RestoreContext.svelte`
    - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/src/lib/components/RestoreContext.test.ts`
    - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/src/lib/components/ModelPicker.svelte`
    - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/src/lib/components/ModelPicker.test.ts`
    - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/src/lib/components/Wizard/Welcome.svelte`
    - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/src/lib/components/Wizard/Detection.svelte`
    - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/src/lib/components/Wizard/ModelPick.svelte`
    - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/src/lib/components/Wizard/Done.svelte`
    - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/src/lib/components/Wizard/*.test.ts`
    - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/src/routes/wizard/+page.svelte`
    - `/Users/mohitpatel/Desktop/Project/agentdeck/ui/src/routes/settings/+page.svelte`
- **Inputs:** W4 (Claude connector — but compact heuristic is a separate file the connector imports, so no file collision), W7 (router mount via `Mount(r)`), W11 (selector for "recommended" badges in wizard), W14 (UI components: imports them, doesn't modify them).
- **Outputs:** Compact detection writes a `compact_event` row + emits SSE; restore endpoint returns the resume Markdown block; wizard fully functional.
- **Dependencies:** W4, W7, W11, W14.
- **Recommended agent:** `general-purpose` with `regex-vs-llm-structured-text` (compact heuristic is regex/structured) + `frontend-patterns`. **One agent; this slice has high internal cohesion.**
- **Effort:** L.
- **Acceptance criteria:**
  - Synthetic fixture JSONL with a `/compact` event triggers detection; "Restore context" returns Markdown bundle exactly matching golden file.
  - `claude --resume <uuid>` and `codex resume --last` are correctly quoted on each OS (test on Windows with backslash paths).
  - Wizard end-to-end happy path covered by Vitest + Playwright (Playwright optional if not yet wired; otherwise pure unit).
  - 80%+ coverage on the new code.

### W16 — Performance bench + perf-regression test
- **Goal:** Validate the §12 budgets and add a CI-runnable bench that fails on regression.
- **Owned paths:**
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/bench/bench_test.go` (Go benchmarks: search p95, ingest throughput)
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/bench/ram_test.go` (RAM after-hour-idle simulation; OS-gated)
  - `/Users/mohitpatel/Desktop/Project/agentdeck/internal/bench/fixtures/100k_msgs.go` (generator)
  - `/Users/mohitpatel/Desktop/Project/agentdeck/scripts/bench.sh`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/docs/perf.md`
- **Inputs:** W12 (full daemon assembled), W3 (search), W9 (cost rollups).
- **Outputs:** Reproducible numbers for every row of §12; CI step that emits `BUDGET_VIOLATION` if any p95 > budget.
- **Dependencies:** W12.
- **Recommended agent:** `golang-patterns` + `optimize` skill.
- **Effort:** S.
- **Acceptance criteria:**
  - Every §12 row has a corresponding bench. SSE latency, binary size, and cold-start are scripted measurements (not Go benches).
  - Bench output is JSON; CI uploads as artifact.
  - All budgets met on a plain GitHub Actions runner.

### W17 — Release pipeline + install paths + landing page + README
- **Goal:** Produce shippable artifacts and the public-facing materials.
- **Owned paths:**
  - `/Users/mohitpatel/Desktop/Project/agentdeck/.github/workflows/release.yml` (goreleaser, multi-arch, `embed.FS` of `ui/build/` baked in)
  - `/Users/mohitpatel/Desktop/Project/agentdeck/.goreleaser.yaml`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/scripts/install.sh`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/scripts/install.ps1`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/scripts/release.sh`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/scripts/sign-darwin.sh` (codesign + notarize stub; reads env vars, no-ops if absent)
  - `/Users/mohitpatel/Desktop/Project/agentdeck/Formula/klyne.rb` (homebrew tap stub)
  - `/Users/mohitpatel/Desktop/Project/agentdeck/winget/manifest.yaml`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/README.md` (full copy from spec §16 — overwrites W0 stub)
  - `/Users/mohitpatel/Desktop/Project/agentdeck/docs/architecture.md`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/docs/connector-guide.md`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/docs/byok-matrix.md`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/docs/CHANGELOG.md`
  - `/Users/mohitpatel/Desktop/Project/agentdeck/docs/landing/` (static site for `klyne.dev`, deployable to Cloudflare Pages)
- **Inputs:** W12 (working binary), W14 (built UI), W16 (perf docs).
- **Outputs:** Tagged v1.0.0 release with darwin/linux/windows × amd64/arm64 artifacts. brew formula, winget manifest, install.sh.
- **Dependencies:** W12, W14, W16.
- **Recommended agent:** `general-purpose` with `deployment-patterns` skill.
- **Effort:** M.
- **Acceptance criteria:**
  - `git tag v1.0.0-rc1 && git push --tags` triggers CI; artifacts produced.
  - `bash scripts/install.sh` on a fresh Linux VM installs and runs.
  - README renders correctly on GitHub.
  - Binary size ≤ 25 MB (§12).

### W18 — Alpha bug-bash + final cut
- **Goal:** Run §15 D14: 5-user alpha, fix top 5 bugs, tag v1.0.0.
- **Owned paths:** anything reachable; this is the cleanup pass. Must respect single-writer per file as bugs are assigned (ad-hoc).
- **Inputs:** W17.
- **Outputs:** Closed alpha bugs, tagged v1.0.0, public-launch checklist green.
- **Dependencies:** W17.
- **Recommended agent:** human-in-the-loop + `general-purpose` agents dispatched per bug.
- **Effort:** S (assuming alpha quality is decent).
- **Acceptance criteria:** §14 Day-14 metrics row.

---

## 2. Dependency graph

ASCII DAG (left-to-right). Boxes are workstreams; arrows are "must-finish-before".

```
                                                 ┌── W4 (claude connector) ──┐
                                                 │                            │
                                                 ├── W5 (codex connector)  ───┤
                                                 │                            │
                              ┌── W1 (store db) ─┼── W2 (msgs/sessions) ─┐    │
                              │                  │                       │    │
W0 (bootstrap & contracts) ───┼── W6 (config)  ──┤                       ├── W7 (api+handlers) ──┐
                              │                  │                       │                       │
                              ├── W9 (cost) ─────┘                       │                       │
                              │                  ┌── W3 (search/summ) ───┘                       │
                              │                  │                                               │
                              ├── W10 (providers) ──┐                                             │
                              │                    │                                              │
                              └── W13 (ui shell) ──┼─── W14 (ui components) ─┐                    │
                                                   │                          │                   │
                                                   │   ┌── W8 (sse hub) ──────┼───────────────────┤
                                                   │   │                      │                   │
                                                   │   │                      │                   │
                                                   │   └─── W11 (selector + summ tasks) ──┐       │
                                                   │                                       │       │
                                                   │                                       ├── W12 (app wiring) ── W16 (perf) ── W17 (release) ── W18 (alpha)
                                                   │                                       │
                                                   └── W15 (compact + wizard + restore) ──┘
```

### Critical path (longest chain)

`W0 → W1 → W2 → W3 → W11 → W15 → W12 → W16 → W17 → W18`

Sum of effort along the critical path: **L + S + S + S + M + L + M + S + M + S ≈ 12.5 days** of single-agent serial work. Everything else fits inside that window.

The two non-critical tracks that absorb agents in parallel:
1. **Connectors track:** W0 → {W4, W5} → meets critical path at W12.
2. **Frontend track:** W0 → W13 → W14 → meets critical path at W15 (which is cross-cutting) and W12.
3. **Infra track:** W0 → {W6, W7, W8, W9, W10} → W12.

### Wide parallel layers

| Layer | Concurrent workstreams | Max agents |
|---|---|---|
| Wave 0 | W0 | 1 |
| Wave 1 | W1, W4, W5, W6, W9, W10, W13 | **7** |
| Wave 2 | W2, W3, W7, W8, W11, W14 | **6** |
| Wave 3 | W12, W15 | 2 |
| Wave 4 | W16, W17 | 2 |
| Wave 5 | W18 | 1 (+ ad-hoc fan-out per bug) |

---

## 3. Phased execution plan

### Wave 0 — Bootstrap (Day 1, ~2d wallclock at solo pace, ~1d if you babysit)
- **Workstreams:** W0
- **Agents in parallel:** 1
- **Merge point:** every contract file compiles, `go test ./...` green, CI green, `docs/contracts.md` complete and reviewed by you (the human).
- **Integration risk:** **highest of any wave.** A drift here cascades. Mitigation: don't fan out until you (the human) sight-read `connector.go`, `contracts.go`, `sse_events.go`, the three migration SQL files, and `pricing_schema.go`. Spend 30 minutes on this even if it feels boring; it saves days later.

### Wave 1 — Foundation fan-out (Days 2–4, 7 agents)
- **Workstreams:** W1, W4, W5, W6, W9, W10, W13
- **Agents in parallel:** 7 (one per workstream; assign each to its own git worktree under `~/klyne-worktrees/W{N}-{name}`)
- **Merge point:** all 7 land green to `main`. Use the `superpowers:using-git-worktrees` and `superpowers:dispatching-parallel-agents` skills.
- **Integration risk:** medium. The contracts make collisions structurally unlikely, but watch for:
  - W1 + W9 both touching `~/.klyne/` paths logically — W1 owns DB file, W9 owns pricing override file; document in `docs/contracts.md`.
  - W4 + W5 both depending on `examples/sample-jsonl/` fixtures — fixtures were committed by W0 and are read-only here.
  - W13 will inevitably need a TS type that's missing from W0 — agent should open a PR back to W0's contract files (treated as human-reviewed change, not silent edit).

### Wave 2 — Mid-layer fan-out (Days 4–7, up to 6 agents)
- **Workstreams:** W2, W3, W7, W8, W11, W14
- **Agents in parallel:** up to 6 (some take dependencies on Wave 1 outputs — see DAG)
  - W2 starts when W1 lands.
  - W3 starts when W2 lands. (Or have one agent own W2+W3 sequentially — see Risk register.)
  - W7 starts when W2, W3, W6 are all green.
  - W8 starts when W7 has merged its `Mount` hook.
  - W11 starts when W3, W8, W10 are green.
  - W14 starts when W13 lands and W7+W8 are partially up (mocks are fine until they aren't).
- **Merge point:** `klyne start` (W12 is next wave) is buildable. `make dev` runs daemon + UI together with hot reload.
- **Integration risk:** medium-high — this is where SSE event shapes get exercised end-to-end. If W8's `MsgNew` payload doesn't match what W14's `SessionList` expects, both think the other is wrong. Mitigation: `ui/scripts/check-contracts.ts` (built in W13) runs in CI on every PR and rejects type drift.

### Wave 3 — Integration (Days 8–10, 2 agents)
- **Workstreams:** W12, W15
- **Agents in parallel:** 2. **W12 must be one agent only**, because it touches every package's exported API.
- **Merge point:** the four user flows in §6 work end-to-end on a fresh machine with fixture data. `klyne doctor` green.
- **Integration risk:** high. This is when bugs that survived unit tests because mocks lied surface. Allocate a full day of buffer. Use `superpowers:systematic-debugging` and `verification-loop` skills.

### Wave 4 — Hardening (Days 11–13, 2 agents)
- **Workstreams:** W16, W17
- **Agents in parallel:** 2.
- **Merge point:** every §12 budget green; release artifacts produced; install.sh works on a fresh Linux VM and on a Mac that's not the dev machine.
- **Integration risk:** low (each workstream owns disjoint files), but performance tuning may require touching hotspots in W2/W3/W4/W14. Treat those as scoped PRs reviewed by the original owner before merge.

### Wave 5 — Ship (Day 14, ad-hoc)
- **Workstreams:** W18
- **Agents in parallel:** 1–N depending on bug volume.
- **Merge point:** v1.0.0 tag pushed.
- **Integration risk:** unknowable until alpha; mitigation is the buffer day, the `verification-before-completion` skill, and the discipline to scope-cut bugs to v1.0.1 if they don't block flows §6.

---

## 4. Shared-contract artifacts (the W0 deliverables, in order)

These are the artifacts W0 must finalize before fan-out. Each line is a file path or a typed contract; a deviation requires a cross-stream PR, never a silent in-stream change.

1. **Go module name + skeleton.** `go.mod` declares `module github.com/<owner>/klyne`, Go 1.23, with deps pinned per spec §10:
   - `modernc.org/sqlite v1.44.3`
   - `github.com/fsnotify/fsnotify v1.7.x`
   - `github.com/shirou/gopsutil/v3 v3.x`
   - `github.com/go-chi/chi/v5 v5.x`
   - `github.com/r3labs/sse/v2 v2.x`
   - `github.com/spf13/cobra latest`
   - `github.com/pelletier/go-toml/v2` (config)
2. **`Connector` interface** in `internal/connectors/connector.go`, verbatim from spec §11, plus the supporting types `RawEvent`, `Message`, `Session`, `PricingTable`, `ToolCall`, `ToolResult`. Field tags must include `json:"..."` so wire format is locked.
3. **Canonical `Message` struct** — exact fields from spec §7 ingest pipeline (id, session_id, cli, project_path, role, content, tool_calls, tool_results, tokens_in, tokens_out, cost_usd, model, ts, parent_uuid). Time fields are `int64` epoch-ms (decision: lock here, don't let connectors choose).
4. **SQLite schema** as 3 migration files in `internal/store/migrations/`:
   - `001_init.sql`: `sessions`, `messages`, `threads`, `thread_sessions`, `schema_migrations`.
   - `002_fts.sql`: `messages_fts` virtual table + INSERT/UPDATE/DELETE triggers wired to `messages`.
   - `003_summaries.sql`: `session_summaries`, plus a `compact_events(session_id, ts, before_token_count, after_token_count)` table (W15 needs it; cheaper to add now than rev migrations later).
5. **HTTP API surface** in `internal/api/contracts.go` — every route + DTO. Frozen list:
   - `GET /sessions?cli=&project=&limit=&before=` → `SessionListResponse`
   - `GET /sessions/:id` → `SessionResponse`
   - `GET /sessions/:id/messages?limit=&before=` → `MessageListResponse`
   - `GET /sessions/:id/restore` → `RestoreResponse`
   - `GET /sessions/:id/summary` → `SummaryResponse`
   - `GET /search?q=&limit=` → `SearchResponse`
   - `GET /cost/summary?group=session|project|day|model&since=&until=` → `CostSummaryResponse`
   - `GET /settings` / `PUT /settings` → `SettingsResponse` / `SettingsUpdateRequest`
   - `GET /wizard/detect` → `WizardDetectResponse`
   - `POST /wizard/complete` → `204`
   - `GET /events` → SSE stream
   - `GET /healthz` → `{ok: true, version, schema_version}`
6. **SSE event shapes** in `internal/api/sse_events.go`:
   - `MsgNew{session_id, message_id, ts, role, model, tokens_in, tokens_out, cost_usd}`
   - `SummaryReady{session_id, version, ts, model}`
   - `SessionUpdate{session_id, last_msg_at, msg_count, cost_usd, status}`
   - `CostTick{ts, total_usd_today}`
   - `ThreadRebuild{thread_count, ts}` (v1.1 stub now to avoid event-name drift later)
   - `CompactDetected{session_id, ts}` (W15 needs it)
   - Each event JSON-tagged. Each event has a corresponding TS type in `ui/src/lib/types.ts`.
7. **`~/.klyne/config.toml` schema** in `internal/config/schema.go`:
   ```toml
   [server]
   addr = "127.0.0.1:7878"

   [paths]
   db = "~/.klyne/klyne.db"
   pricing_override = "~/.klyne/pricing.json"

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
8. **Pricing JSON format** in `internal/cost/pricing_schema.go`:
   ```json
   {
     "version": 1,
     "models": {
       "claude-sonnet-4.5": {"prompt_per_mtok": 3.00, "completion_per_mtok": 15.00, "cache_read_per_mtok": 0.30, "cache_write_per_mtok": 3.75},
       "...": {}
     }
   }
   ```
9. **Repo skeleton** matching spec §11 exactly. No improvisation.
10. **CI/lint/format config:**
    - `.golangci.yml`: enable `errcheck`, `gosec`, `govet`, `staticcheck`, `unused`, `gocyclo`, `goconst`, `gofumpt`. Disable nothing without a comment justifying it.
    - `.editorconfig`: 2-space SQL/TS, tab Go, LF line endings.
    - `.github/workflows/ci.yml`: matrix `os=[ubuntu-latest, macos-latest, windows-latest]`, Go 1.23, run `go test ./... -race -coverprofile=cov.out`, `golangci-lint`, `cd ui && npm ci && npm run check && npm run test`. Coverage gate: 80% on changed lines (use `gocover-cobertura` + GitHub Actions check).

---

## 5. Per-workstream prompt templates

Every prompt below is self-contained — paste into a fresh agent session, no other context required (other than the spec file path). Each prompt assumes the agent has access to `superpowers:test-driven-development` and the Go/TS skills it'll need; the prompts mention them but don't depend on them being auto-loaded.

> **Universal preamble (prepend to every prompt below):**
> You are working on the klyne repository at `/Users/mohitpatel/Desktop/Project/agentdeck`. The shipping spec is `compass_artifact_wf-d189e421-ff1d-443c-95b8-19b52fcd59b4_text_markdown.md` in the repo root. Read §1, §5, §7, §11, §18 before touching code. The `docs/contracts.md` file (produced in W0) lists every contract you must conform to. Decisions in spec §18 are LOCKED — do not propose alternatives.
>
> **TDD is mandatory.** Write tests first using the `superpowers:test-driven-development` skill. Target ≥80% coverage on new code; CI enforces this on changed lines. After every change >30 lines, run the project's quality gate (`make ci` or the equivalent commands listed in W0's `Makefile`). Use `superpowers:verification-before-completion` before claiming done.
>
> **Single-writer rule.** You may only modify files under "owned paths" below. To change anything else, open a PR against W0's `docs/contracts.md` first and wait for human review.

### W0 prompt
> **Goal:** Bootstrap the klyne repo. Produce the Go module, repo skeleton from spec §11, locked contracts (`Connector` interface, `Message` struct, SQLite migrations, HTTP API DTOs, SSE event types, config schema, pricing schema), CI pipeline, fixture JSONL, and `docs/contracts.md`.
>
> **Spec sections:** §5 (PRAGMAs), §7 (schema, pipeline), §10 (versions), §11 (repo layout, `Connector` interface), §17 (non-goals), §18 (locked decisions).
>
> **Owned paths:** [paste from §1 above]
>
> **Inputs:** the spec file. The fixture JSONL must be hand-collected from real Claude Code and Codex sessions; if not available, write a tiny generator script (kept in `examples/sample-jsonl/generate.go`) that produces realistic synthetic ones. Include at least one fixture per CLI that contains a `/compact` event for W15.
>
> **Acceptance criteria:**
> - `go build ./...` and `go test ./...` and `golangci-lint run` all green on macOS, Linux, Windows CI.
> - Every type that crosses workstream boundaries is defined in exactly one file you own.
> - `docs/contracts.md` references each contract by file:line and pins the spec section it derives from.
> - cobra root + `start`/`stop`/`doctor` stubs compile and exit 0 with "not implemented" messages.
> - The 3 migration SQL files exactly match spec §7 plus the `compact_events` table I described in the plan.
>
> **Testing requirements:** No business logic to test yet, but: write a `go test` in `internal/store/migrations/` that loads each `.sql` file with `modernc.org/sqlite` and confirms it parses. Write a `go test` that asserts every route constant in `contracts.go` is unique.
>
> **Hard boundaries:** do NOT implement any handler bodies (W7), do NOT implement the store DB (W1), do NOT implement any connector (W4/W5). You produce skeletons only.

### W1 prompt
> **Goal:** Implement `internal/store/db.go` and `internal/store/migrate.go`. `Open(path)` returns a `*store.DB` with separate read (`MaxOpenConns=N`) and write (`MaxOpenConns=1`) handles, applying all 6 PRAGMAs from spec §5. Migrations from `internal/store/migrations/` apply idempotently and track version in `schema_migrations`.
>
> **Spec sections:** §5, §7.
>
> **Owned paths:** [list]
>
> **Inputs:** W0's `internal/store/migrations/*.sql` (read-only).
>
> **Acceptance criteria:**
> - Every PRAGMA from §5 verifiable by querying `PRAGMA <name>` post-open.
> - Migrations idempotent (run, restart, run again — no error, no duplicate work).
> - 80%+ coverage.
> - Stress test: 10 goroutines × 1000 inserts each via the write handle, no `SQLITE_BUSY` (use `busy_timeout=5000`).
>
> **Testing:** TDD-first. Write `db_test.go` listing the assertions before implementation. Use `t.TempDir()` for DB paths.
>
> **Hard boundaries:** do NOT add any DAOs (W2/W3 own those). Do NOT call any provider/connector packages.

### W2 prompt
> **Goal:** Implement DAOs for `messages` and `sessions` tables: insert, get, list-with-pagination, plus the rollup updates (`last_msg_at`, `msg_count`, `tokens_in/out`, `cost_usd`) on `sessions` triggered inside `messages.Insert` (single tx).
>
> **Spec sections:** §7.
>
> **Owned paths:** [list]
>
> **Inputs:** W0's `Message`/`Session` types, W1's `store.DB`.
>
> **Acceptance criteria:**
> - `messages.Insert` and `sessions.Upsert` are concurrency-safe (use the write handle from `store.DB.Write()`).
> - `ListBySession` paginates by `(ts, id)` cursor; ordering stable across concurrent inserts.
> - 80%+ coverage.
>
> **Testing:** TDD. Use a real (in-tempdir) SQLite DB, not mocks.
>
> **Hard boundaries:** do NOT touch `messages_fts` (DDL is in W0 migrations; FTS reads are W3). Do NOT add summary code (W3).

### W3 prompt
> **Goal:** Implement `search.Query` (FTS5 BM25) and `summaries` DAO (insert with auto-versioning, latest-by-session).
>
> **Spec sections:** §7, §12 (search budget).
>
> **Owned paths:** [list]
>
> **Inputs:** W0 contracts, W1 `store.DB`, W2 message DAO.
>
> **Acceptance criteria:**
> - `search.Query` returns ranked results using `bm25()`; query time <50 ms on 10K-msg fixture.
> - `summaries.Insert` increments `version` per session atomically.
> - 80%+ coverage including: empty result, multi-session results, snippet extraction.
>
> **Testing:** TDD. Seed fixture with planted "needle" message and assert it ranks #1.
>
> **Hard boundaries:** do NOT call any AI provider (W11). Search is pure SQL here.

### W4 prompt
> **Goal:** Implement `claude.Connector` satisfying `connectors.Connector` (W0). Discover JSONL files under `~/.claude/projects/<encoded-cwd>/<uuid>.jsonl`, parse each line into the canonical `Message`, and watch for new lines via `fsnotify`.
>
> **Spec sections:** §7, §11, §13 (risk: parser tolerance).
>
> **Owned paths:** [list]
>
> **Inputs:** W0 `Connector` interface + fixtures in `examples/sample-jsonl/claude/`.
>
> **Acceptance criteria:**
> - Parses every fixture without error.
> - Covers 6 message types (user, assistant, tool_use, tool_result, system, summary/compact-marker). Each has a dedicated test.
> - `Watch` test: appends new line to a temp file, asserts event arrives on channel within 200 ms.
> - Decoded CWD round-trip test (50+ paths).
> - Tolerates unknown fields (forward-compat); skips malformed lines with a warning, never panics.
> - 80%+ coverage.
>
> **Testing:** TDD. Use the `regex-vs-llm-structured-text` skill — this is structured parsing, no LLM. Use Go subtests, one per message type. Use `t.TempDir()` for `Watch` tests.
>
> **Hard boundaries:** do NOT detect `/compact` events here — that's W15, in a separate file the connector imports. Do NOT write to the DB — connector emits `RawEvent` to a channel; downstream code (W12 wiring) writes.

### W5 prompt
> *Identical structure to W4 but for Codex. Path: `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl`. Note the date-partitioned dirs may not exist at watcher start; use `fsnotify` create-aware patterns.*

### W6 prompt
> **Goal:** Implement `~/.klyne/config.toml` load/save with defaults and atomic write.
>
> **Spec sections:** §11, §18 (telemetry off in v1).
>
> **Owned paths:** [list]
>
> **Inputs:** W0 `config/schema.go`.
>
> **Acceptance criteria:**
> - `Load()` returns defaults if file missing and writes them.
> - `Save()` writes atomically (temp + rename) so a crash mid-write doesn't corrupt the file.
> - XDG-aware paths: macOS uses `~/Library/Application Support/klyne`, Linux uses `$XDG_CONFIG_HOME/klyne`, Windows uses `%APPDATA%\klyne`. Spec says `~/.klyne/` everywhere — for v1, do that on macOS+Linux and use `%USERPROFILE%\.klyne\` on Windows; XDG is a v1.1 polish.
> - 80%+ coverage.
>
> **Testing:** TDD using `t.TempDir()`. Override the home dir via an injected function.
>
> **Hard boundaries:** do NOT read API keys here; that's W10's job (and W10 reads env vars, not config).

### W7 prompt
> **Goal:** Implement chi-based HTTP router and the read-only handlers (`/sessions`, `/sessions/:id`, `/sessions/:id/messages`, `/search`, `/cost/summary`, `/settings`). Bind `127.0.0.1:7878` only.
>
> **Spec sections:** §6 (flows that need each route), §7, §11.
>
> **Owned paths:** [list]
>
> **Inputs:** W0 `contracts.go`, W2 + W3 DAOs, W6 config, W9 cost engine.
>
> **Acceptance criteria:**
> - Every route in `contracts.go` has a passing `httptest` test.
> - 4xx for bad inputs; never 5xx for client errors.
> - Loopback-only enforced: handler refuses non-127.0.0.1 source IPs (middleware).
> - 80%+ coverage.
>
> **Testing:** TDD with `httptest.NewServer`. Use real (tempdir) store.
>
> **Hard boundaries:** do NOT register `/events` (W8) or `/sessions/:id/restore` (W15) or `/wizard/*` (W15). Do NOT call AI providers (W11).

### W8 prompt
> **Goal:** Implement an SSE hub (`internal/api/sse.go`) and the `/events` handler. Hub: pub/sub, per-subscriber buffered channel, drop-with-log on slow subscribers, heartbeat every 15 s, supports Last-Event-ID for reconnect.
>
> **Spec sections:** §5 (real-time choice), §7 (event names), §12 (latency budget).
>
> **Owned paths:** [list]
>
> **Inputs:** W0 `sse_events.go`. W7's router exposes a `Mount(r chi.Router)` hook (or similar) the SSE handler attaches to.
>
> **Acceptance criteria:**
> - Test with 100 concurrent subscribers, 1000 events/s, no panics, slow subscribers don't block fast ones.
> - End-to-end test: publish `MsgNew`, EventSource client receives it within 100 ms.
> - 80%+ coverage.
>
> **Testing:** TDD. Use `httptest` + a custom EventSource-equivalent reader.
>
> **Hard boundaries:** the only files you own are listed. Do NOT modify W7's router files; coordinate via the `Mount` hook.

### W9 prompt
> **Goal:** Implement the cost engine: embed pricing JSON, lookup-by-model, compute per-message and rollup-per-session/project/day/model.
>
> **Spec sections:** §7 (cost fields), §10 (LiteLLM pricing), §15 D7.
>
> **Owned paths:** [list]
>
> **Inputs:** W0 `pricing_schema.go`, W6 config (override path).
>
> **Acceptance criteria:**
> - All listed models present in `pricing.json` (see §1 plan).
> - `Cost(tokensIn, tokensOut, model)` returns USD; 0 with a warning log if model unknown.
> - Per-session $ matches `ccusage` to within 0.5% on a fixture.
> - Override file in `~/.klyne/pricing.json` takes precedence.
> - 80%+ coverage.
>
> **Testing:** TDD. Golden file from `ccusage` is the truth source.

### W10 prompt
> **Goal:** Implement the `ai.Provider` interface and 4 implementations (Anthropic, OpenAI, Gemini, Ollama). API-key only — never reads `~/.claude` OAuth or `~/.codex/auth.json`.
>
> **Spec sections:** §8, §17, §18.
>
> **Owned paths:** [list]
>
> **Inputs:** spec §8.
>
> **Acceptance criteria:**
> - Each provider tested against `httptest.Server` mock; no real network calls in CI.
> - `DetectAvailable()` checks env vars and Ollama localhost; no false positives.
> - **Anthropic provider must hard-fail (return `ErrNoCredential`) if `ANTHROPIC_API_KEY` is unset, even if `~/.claude` exists.** Document this in a comment citing spec §8 enforcement date.
> - 80%+ coverage.
>
> **Testing:** TDD. Use the `claude-api` skill for the Anthropic Messages API shape. Provider responses use the canonical `ChatResponse` from `provider.go`.
>
> **Hard boundaries:** do NOT implement the selector (W11). Do NOT implement the summarize task (W11).

### W11 prompt
> **Goal:** Implement the smart selector (`selector.Pick`) per spec §8 and the summarize/title tasks. Wire a runner that subscribes to `MsgNew` SSE events and triggers summarization every 50 messages or on a `CompactDetected` event.
>
> **Spec sections:** §6 flow C, §7 step 4, §8.
>
> **Owned paths:** [list]
>
> **Inputs:** W0 config, W3 summaries DAO, W8 SSE hub (subscribe), W10 providers.
>
> **Acceptance criteria:**
> - Selector unit tests cover the §8 matrix exhaustively.
> - Reason strings are user-readable (will be displayed in wizard).
> - Runner test: feed mock providers, 50-message fixture → summary persisted → `SummaryReady` event broadcast.
> - 80%+ coverage.
>
> **Testing:** TDD. Use `cost-aware-llm-pipeline` skill to validate routing logic. Mock providers from W10 via the `Provider` interface.

### W12 prompt
> **Goal:** Wire all packages into a running daemon. `klyne start` (or `klyne` no-arg) opens browser at `127.0.0.1:7878`. Implement `klyne stop` (pidfile) and `klyne doctor`.
>
> **Spec sections:** §6 flows A & B, §11.
>
> **Owned paths:** [list]
>
> **Inputs:** W1–W11.
>
> **Acceptance criteria:**
> - Fresh-machine smoke: `klyne` against tempdir with fixture JSONL → ingests, serves UI (embedded), opens browser (suppress in CI), all 4 spec §6 flows work.
> - `klyne doctor` returns structured JSON with: detected JSONL roots, detected provider keys, DB path/size, schema version, daemon version. Exit 0 if green, 1 otherwise.
> - SIGTERM → flush writer queue → close DB → exit within 2 s.
> - 80%+ coverage on `internal/app/`.
>
> **Testing:** TDD for `app.go` is hard but doable: write a `BuildOnly()` constructor that returns the assembled struct without starting servers, and unit-test the assembly. Integration test starts a real server on a random port.
>
> **Hard boundaries:** do NOT modify any package's exported API to make wiring easier — open a PR back to that workstream's owner.

### W13 prompt
> **Goal:** SvelteKit 5 scaffold with Tailwind 4, runes-based stores, typed API client, typed SSE client, type-checked against W0 Go contracts.
>
> **Spec sections:** §10, §11, §18.
>
> **Owned paths:** [list]
>
> **Inputs:** W0 `internal/api/contracts.go` and `internal/api/sse_events.go` (read-only Go source — translate to TS).
>
> **Acceptance criteria:**
> - `npm run build` succeeds; `ui/build/` contains static output ready for `embed.FS`.
> - `npm run check` (svelte-check) clean.
> - `npm run test` (vitest) passes; 80%+ coverage on `lib/`.
> - `ui/scripts/check-contracts.ts` runs in `npm run check` and asserts every Go DTO field is mirrored in the corresponding TS type.
>
> **Testing:** Vitest + @testing-library/svelte. TDD for `api.ts`, `sse.ts`, `stores.svelte.ts`. Mock `fetch` and `EventSource`.
>
> **Hard boundaries:** no business components (`SessionList`, etc.). No business routes (only `+layout.svelte` and a placeholder `+page.svelte`). All of those are W14.

### W14 prompt
> **Goal:** Implement v1 user-facing pages: dashboard (session list), session detail, search, and the cost panel component. Live updates via SSE.
>
> **Spec sections:** §6 flows A/B/D.
>
> **Owned paths:** [list]
>
> **Inputs:** W13 lib, W7+W8 backend.
>
> **Acceptance criteria:**
> - All four flows in spec §6 (A, B, D — not C, that's W15) work end-to-end against a running daemon.
> - 80%+ component coverage with @testing-library/svelte.
> - Empty states present for: no sessions, no search results, no cost data.
> - Keyboard shortcuts: `/` focus search, `j/k` navigate session list, `Enter` open.
> - Tailwind defaults only — no custom design system in v1 (spec §18 #10).
>
> **Testing:** Vitest + @testing-library/svelte. TDD per component. Add a Playwright smoke if time allows; not required.

### W15 prompt
> **Goal:** Implement the `/compact` recovery flow (spec §6 flow C) end-to-end (backend + frontend), the first-run wizard (spec §6 flow A), and the "Open in CLI" command builder.
>
> **Spec sections:** §6 flows A & C, §8 (wizard model picker), §13 risk #4.
>
> **Owned paths:** [list — disjoint from W4/W11/W14 by design]
>
> **Inputs:** W4 (Claude connector), W7 (router `Mount` hook), W11 (selector for "recommended" badges), W14 (UI components — IMPORT, do not modify).
>
> **Acceptance criteria:**
> - Synthetic fixture with `/compact` event triggers detection; emits `CompactDetected` SSE; restoring returns Markdown bundle (rolling summary + last 20 messages) matching golden file.
> - "Open in CLI" produces correctly-quoted commands per OS for both `claude --resume` and `codex resume`.
> - Wizard 4 screens (Welcome / Detection / ModelPick / Done) work end-to-end. POST `/wizard/complete` writes the chosen settings via W6 config.
> - 80%+ coverage on new code.
>
> **Testing:** TDD. Use `regex-vs-llm-structured-text` for compact heuristic. Golden-file test for the resume Markdown.

### W16 prompt
> **Goal:** Validate every §12 performance budget with a CI-runnable bench.
>
> **Spec sections:** §12.
>
> **Owned paths:** [list]
>
> **Inputs:** W12 (working daemon).
>
> **Acceptance criteria:**
> - Every §12 row has a dedicated bench/test. Idle RAM, active RAM, cold-start, search p95, ingest throughput, summary turnaround (mocked provider — turnaround budget is for real Gemini, document and skip in CI), disk footprint, SSE latency, binary size.
> - JSON output uploaded as CI artifact.
> - Budget violations fail CI (separate job, doesn't gate merge in v1 but produces a visible status check).
>
> **Testing:** Use `optimize` skill for hotspot identification.

### W17 prompt
> **Goal:** Release pipeline (goreleaser, multi-arch), install scripts, brew/winget stubs, README (spec §16 verbatim), docs, landing page.
>
> **Spec sections:** §15 D12–D14, §16.
>
> **Owned paths:** [list]
>
> **Inputs:** W12 (binary), W14 (UI build), W16 (perf docs).
>
> **Acceptance criteria:**
> - `git tag v1.0.0-rc1 && git push --tags` triggers release CI; artifacts produced for darwin/linux/windows × amd64/arm64.
> - `bash scripts/install.sh` works on a fresh Linux VM.
> - Binary size ≤ 25 MB.
> - README is spec §16 verbatim with project-specific links.
>
> **Testing:** Manual install on a fresh macOS and Linux. Document steps in `docs/release-checklist.md`.

### W18 prompt (per-bug template)
> **Goal:** Fix bug `<id>` reported by alpha user `<name>`. Root-cause first (`superpowers:systematic-debugging`), write a regression test, then fix. Do not expand scope beyond the reported bug.
>
> **Owned paths:** whatever the bug touches; obtain explicit owner sign-off if you must change a file outside your historical workstream.
>
> **Acceptance criteria:** regression test fails before fix, passes after; coverage doesn't regress; reviewer signs off.

---

## 6. Risk register specific to multi-agent execution

| ID | Risk | Likelihood | Impact | Owner | Mitigation |
|---|---|---|---|---|---|
| R1 | **Two agents redefine the same type with subtle differences** (e.g., `Message.Tokens` int vs int64) | High | High | W0 | All cross-stream types live in W0-owned files. Any change requires PR; CI rejects PRs that modify these files unless labeled `contract-change` and reviewed by human. |
| R2 | **`internal/store/migrations/*.sql` collisions** if W2 or W3 try to "fix" a missing column | High | High | W0 | Migration files are append-only and W0-owned. New tables go in `004_*.sql` etc. — owned by whichever workstream introduces the need, but added in a fresh file. |
| R3 | **W7 + W8 + W15 all want to register routes on the chi router** — last writer wins | High | Medium | W7 | W7 exposes `Mount(r chi.Router, deps Deps)` and a `RegisterHook(name string, fn func(chi.Router))` callback. W8 and W15 register via the hook, never by editing W7's files. |
| R4 | **TS types drift from Go DTOs** silently | High | High | W13 | `ui/scripts/check-contracts.ts` reflects on the parsed Go contract file (using a tiny Go AST tool W0 ships in `tools/dump-contracts/`) and asserts every TS type matches. Runs in CI. |
| R5 | **fsnotify watcher fights the bulk back-fill** on first run, double-ingesting | Medium | Medium | W4/W5 | Each connector implements a "warm-up" mode: on `Watch` start, take an inode/offset snapshot, replay file from byte 0 → DB, then attach watcher seeking from snapshot offset. Tests in W4/W5 cover this. |
| R6 | **SSE hub deadlock** when subscriber count × event rate exceeds buffer | Medium | High | W8 | Per-subscriber buffered channel + non-blocking send + drop-with-log policy. Stress test in W8 with 100 subs × 1000 evts/s. |
| R7 | **AI provider latency stalls the writer goroutine** if mis-wired | Medium | High | W11/W12 | Summaries run in their own goroutine pool subscribed to `MsgNew` events, never inline with the writer. Document this invariant in `internal/app/app.go`. |
| R8 | **Race between W14 and W15 over `MessageBubble.svelte`** if compact-event rendering bleeds into the bubble | Medium | Medium | W15 | W15 owns its own `RestoreContext.svelte` and uses `MessageBubble` read-only via import. If a bubble change is needed, W15 opens a PR to W14. |
| R9 | **fsnotify on Linux misses events under heavy load** (inotify queue overflow) | Low | High | W4/W5 | Periodically (every 30 s) scan filesystem for new files and reconcile against watched set; backstops fsnotify. |
| R10 | **Codex JSONL format changes** mid-development | Medium | Medium | W5 | Spec §13 risk #2 — keep parser tolerant; pin format version in test fixtures; v1 supports current Codex output as of May 2026. |
| R11 | **Anthropic ToS changes** before launch | Low | Critical | Human | We never reuse OAuth (§17 #2). Even if Anthropic restricts more, we're already conservative. Re-check the ToS page on Day 13 before tagging. |
| R12 | **Wave 3 integration discovers a contract bug** that requires a W0 ripple | Medium | High | Human | Allocate a "contract revision" PR slot in Wave 3. Treat it as a blocking dep: when a contract changes, every affected workstream gets an automatic update PR. |
| R13 | **Solo dev becomes the bottleneck** reviewing 7 agent PRs in Wave 1 | High | Medium | Human | Use `code-review:code-review` skill on each PR; batch reviews twice/day; require agents to self-review with `superpowers:requesting-code-review` before opening PR. |
| R14 | **Worktree confusion** — agent commits to wrong branch | Medium | Medium | Human | Each agent gets a worktree at `~/klyne-worktrees/W{N}-{slug}` on a branch `wave{X}/W{N}-{slug}`. Use `superpowers:using-git-worktrees`. CI rejects pushes to `main` that aren't via a labeled PR. |
| R15 | **Coverage gate gaming** — agent writes shallow tests to hit 80% | Medium | High | Human | Coverage is necessary, not sufficient. Manual review of every test file with `superpowers:requesting-code-review` checklist. |

---

## 7. Recommended kickoff sequence (do this today)

These 5 dispatches start the project. Each line is a discrete action.

1. **Initialize the git repo and worktree base.**
   ```
   cd /Users/mohitpatel/Desktop/Project/agentdeck
   git init
   git checkout -b main
   git add compass_artifact_wf-d189e421-ff1d-443c-95b8-19b52fcd59b4_text_markdown.md
   git commit -m "chore: vendor shipping spec v1.0"
   git remote add origin <your-private-github-url>
   git push -u origin main
   mkdir -p ~/klyne-worktrees
   ```

2. **Dispatch W0 (bootstrap agent) in a fresh Claude Code session.**
   Paste the W0 prompt from §5 above. Run as Claude Opus 4.7 (1M context). Do not parallelize anything else until W0 lands. Expect 1–2 days of wallclock; budget human review time on contracts (`docs/contracts.md`, `internal/connectors/connector.go`, the 3 SQL migration files, `internal/api/contracts.go`, `internal/api/sse_events.go`).

3. **After W0 lands and is reviewed: create 7 Wave-1 worktrees.**
   ```
   git worktree add ~/klyne-worktrees/W1-store     wave1/W1-store
   git worktree add ~/klyne-worktrees/W4-claude    wave1/W4-claude
   git worktree add ~/klyne-worktrees/W5-codex     wave1/W5-codex
   git worktree add ~/klyne-worktrees/W6-config    wave1/W6-config
   git worktree add ~/klyne-worktrees/W9-cost      wave1/W9-cost
   git worktree add ~/klyne-worktrees/W10-ai       wave1/W10-ai
   git worktree add ~/klyne-worktrees/W13-ui-shell wave1/W13-ui-shell
   ```

4. **Fan out 7 Wave-1 agents.**
   - W1, W2 (queued behind W1), W4, W5, W6, W9 → use Claude Code (sonnet, fine for these).
   - W10 → use Claude Code (sonnet) with `claude-api` skill.
   - W13 → use Claude Code (sonnet) with `coding-standards` + `frontend-patterns`.
   - Optionally use Codex CLI for one of W4 or W5 to validate the cross-CLI dogfood story (you'll find your own bugs faster).
   - Each agent gets its prompt template from §5, customized only with its worktree path.

5. **Set up your review cadence.**
   - **Twice daily** (e.g., 11am and 5pm), use `code-review:code-review` to review every open PR.
   - **End of each wave**, use `superpowers:verification-before-completion` and run `make ci` against the merged branch.
   - When integration issues surface, log them as "contract revision" candidates and triage immediately — a 30-minute contract patch in Wave 1 saves a 3-day rework in Wave 3.

After Wave 1 lands, your kickoff for Wave 2 is symmetric: 6 worktrees, 6 agents, identical pattern.

---

## Closing notes (deliberately short)

- The plan honors every locked decision in §18, every non-goal in §17, every budget in §12, and every fixture/contract requirement implied by §7 + §11.
- The single biggest leverage point is **W0**. Don't rush it. Don't fan out before it lands.
- The single biggest risk is **contract drift** (R1, R4, R12). The structural mitigation is "every cross-stream type lives in exactly one W0-owned file"; the procedural mitigation is "contract changes are labeled PRs reviewed by you, not silent edits."
- Use git worktrees per spec advice in `superpowers:using-git-worktrees`. Do not let two agents share a working tree. Ever.

---

### Critical Files for Implementation
- /Users/mohitpatel/Desktop/Project/agentdeck/internal/connectors/connector.go
- /Users/mohitpatel/Desktop/Project/agentdeck/internal/api/contracts.go
- /Users/mohitpatel/Desktop/Project/agentdeck/internal/api/sse_events.go
- /Users/mohitpatel/Desktop/Project/agentdeck/internal/store/migrations/001_init.sql
- /Users/mohitpatel/Desktop/Project/agentdeck/docs/contracts.md

# Wave 1 Integration Notes

> **Read this before dispatching Wave 2 agents.** Captures decisions made during Wave 1 and verified facts about exposed APIs that Wave 2 will build on.

## Orchestrator-verified status

The orchestrator (Opus) ran all checks independently — these are not just sub-agent self-attestations:

| Check | Result |
|---|---|
| `go mod tidy` | clean (4 direct deps: cobra, fsnotify, go-toml/v2, modernc.org/sqlite) |
| `go build ./...` | ✅ |
| `go vet ./...` | ✅ |
| `go test -race -count=1 -coverprofile=cov.out ./...` | ✅ (10 packages green) |
| `cd ui && npm run check` | ✅ (svelte-check 0 errors; 127 Go→TS field mappings verified) |
| `cd ui && npm run test` | ✅ (49 tests pass) |
| `cd ui && npm run build` | ✅ (builds to `ui/build/` via adapter-static) |
| Compile-time interface check | ✅ (`claude.Connector` and `codex.Connector` satisfy `connectors.Connector`) |
| Contract drift check | ✅ (zero modifications to W0-owned contract files) |
| Path discipline | ✅ (every agent wrote only to its owned paths) |

## Coverage by package

| Package | Coverage | Owner |
|---|---|---|
| `internal/store` | 78.8% | W1 |
| `internal/connectors/claude` | 80.3% | W4 |
| `internal/connectors/codex` | 82.7% | W5 |
| `internal/config` | 80.0% | W6 |
| `internal/cost` | 88.1% | W9 |
| `internal/ai/providers` | 89.0% | W10 |
| `internal/api` | 50.0% | W0 (will rise when W7 lands handlers) |
| `tools/dump-contracts` | 73.8% | W0 |
| `ui/src/lib` | 97.8% statements / 89% branches | W13 |

## Exposed APIs Wave 2 will consume

### `internal/store` (W1)
```go
func Open(path string) (*DB, error)
func (db *DB) Read() *sql.DB    // MaxOpenConns=8
func (db *DB) Write() *sql.DB   // MaxOpenConns=1
func (db *DB) Close() error
func (db *DB) SchemaVersion(ctx context.Context) (int, error)
func Apply(ctx context.Context, db *sql.DB) error  // migration runner
```

### `internal/connectors/claude` (W4) and `internal/connectors/codex` (W5)
Both satisfy `connectors.Connector` (verified by compile-time `var _ connectors.Connector = (*Connector)(nil)` assertion).

### `internal/config` (W6)
```go
func Load() (*Config, error)
func Save(*Config) error
func ConfigDir() string
func ConfigFile() string
func DBPath() string
func PricingOverridePath() string
```
- `HomeDir` is exposed as a package-level `var` for test override.
- macOS/Linux: `$HOME/.agentdeck/`; Windows: `%USERPROFILE%\.agentdeck\`.

### `internal/cost` (W9)
```go
func New(cfg *config.Config) (*Engine, error)
func (e *Engine) Lookup(model string) (PerTokenRates, bool)
func (e *Engine) Cost(tokensIn, tokensOut int64, model string) float64
func (e *Engine) RollupSession(ctx, db *sql.DB, sessionID string) (float64, error)
func (e *Engine) RollupProject(ctx, db *sql.DB, path string, since int64) (float64, error)
func (e *Engine) RollupDaily(ctx, db *sql.DB, dayStartMs int64) (float64, error)
func (e *Engine) RollupByModel(ctx, db *sql.DB, since int64) (map[string]float64, error)
```
- Pricing table embedded with **approximate** rates as of 2025-05. **W17 must refresh before launch.**
- Rollup methods take `*sql.DB` directly (not the wrapped `*store.DB`) so they're trivially mockable.

### `internal/ai` and `internal/ai/providers` (W10)
```go
type Provider interface {
    Name() string
    Chat(ctx, ChatRequest) (*ChatResponse, error)
    Embed(ctx, EmbedRequest) (*EmbedResponse, error)  // returns ErrUnsupported if N/A
    Models() []string
}
```
- Constructors: `NewAnthropic(opts)`, `NewOpenAI(opts)`, `NewGemini(opts)`, `NewOllama(opts)`.
- Each `Opts` struct includes `BaseURL` for `httptest.Server` injection.
- `providers.DetectAvailable(ctx) []ProviderInfo` — env-var-only, no filesystem reads of OAuth tokens.

### `ui/src/lib/{api,sse,stores.svelte,types}.ts` (W13)
- `api.ts` covers all read-only routes; restore/wizard endpoints stubbed for W15.
- `sse.ts` reconnects on close, dispatches by event name with typed handlers.
- `stores.svelte.ts` uses Svelte 5 `$state` runes — no external state library.
- `types.ts` mirrors all Go DTOs/events 1:1; `ui/scripts/check-contracts.ts` enforces in CI.

## Decisions locked in by Wave 1

### Field-name conventions in canonical `*connectors.Message`
From W4/W5 parsers (these become the source of truth for downstream code):

- **Claude**: `uuid` → `Message.ID` (outer wins over inner `message.id`); `cwd` → `ProjectPath`; `usage.input_tokens/output_tokens` → tokens; `summary` lines → role `system` with `leafUuid` → `ParentUUID`.
- **Codex**: `session_id` snake_case as-is; `usage.prompt_tokens/completion_tokens` → tokens; synthetic `compact_event` lines → role `system` (W15 will revisit).

### Tool calls / results storage
W4 keeps `tool_use` inline on the assistant `Message.ToolCalls` slice (one Message per JSONL line, not split). `tool_result` user lines emit a separate `Message` with role `tool` and `Message.ToolResults` populated.

**This is the canonical reader contract.** W2 (Wave 2) must decide the storage shape:
- **Option A:** JSON-encode `ToolCalls` / `ToolResults` slices into `tool_calls_json` / `tool_results_json` columns on `messages`.
- **Option B:** Add a separate `tool_calls(message_id, idx, name, input_json)` table in `004_*.sql` — this is the cleanest path for FTS but adds a join.

Recommendation (orchestrator): **Option A** for v1 — opaque to FTS but trivial; the user's history search is over message text, not tool-call structure. Revisit in v1.1 if needed.

## Toolchain gotchas (still applicable in Wave 2)

1. **Go 1.21 locally; spec wants 1.23.** `GOTOOLCHAIN=local` pins it. CI uses 1.23 via `go-version-file: go.mod`. `go.mod` directive is currently `go 1.21.0` — bump to `go 1.23` once `brew install go@1.23` is done.
2. **`CGO_ENABLED=0` must be set** in Makefile (`build`, `test`, `vet`) and CI. Already wired.
3. **`go-chi/chi/v5@latest` requires Go 1.22.** W7 (Wave 2) must pin to `v5.0.12` or older, OR upgrade Go locally first.
4. **`fsnotify@latest` requires Go 1.23.** Wave 1 pinned `v1.7.0`. Wave 2's W8 SSE hub doesn't need fsnotify, so this only matters if W12 wiring needs to add anything.

## Stubs / deferrals from Wave 1

| Item | Source | Resolved by |
|---|---|---|
| `messages.tool_calls_json` vs separate table | W4 reader vs W0 schema | W2 (decide + new migration if needed) |
| ccusage parity test | W9 brief | W16 / W17 |
| Pricing rates refresh from live vendor pages | W9 | W17 |
| Real `compact_event` signal in Codex | W5 | W15 (when real fixtures captured) |
| `tool_result` structured output threading | W4 | W2 (if Option A) — store raw json.RawMessage |
| Vite version (5.x not 7.x) | W13 | revisit when stack catches up |
| Tailwind 3.x not 4.x | W13 | revisit when 4 is on stable npm |
| `vite-plugin-svelte` v3 (warns about Svelte 5 active support migration to v4 / next-channel) | W13 | bump when v4 is stable |

## Wave 2 unblocked

The 6 Wave 2 workstreams (`W2`, `W3`, `W7`, `W8`, `W11`, `W14`) can fan out next.

**Mid-wave dependencies to respect:**
- W3 starts after W2 (uses messages DAO).
- W7 starts after W2, W3, W6 are all green.
- W8 starts after W7 (registers via `RouterMounter`).
- W11 starts after W3, W8, W10.
- W14 starts after W13, W7, W8 (mocks OK until real).

If you dispatch all 6 in one batch, the slower ones (W3/W7/W8/W11/W14) may need to start with mocks for unfinished deps. Acceptable — the orchestrator will reconcile at integration.

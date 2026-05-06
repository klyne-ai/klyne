# W0 Integration Notes

> **Read this before dispatching Wave 1 agents.** Captures decisions made during W0 that deviate from the spec/plan or defer choices to later workstreams.

## Toolchain

- **Local Go is 1.21.3.** Spec §10 calls for Go 1.23+. The local toolchain has been pinned via `go env -w GOTOOLCHAIN=local` to prevent the failed auto-download of 1.23. The `go` directive in `go.mod` is set to `1.21` accordingly.
- **CI uses Go 1.23** via `go-version-file: go.mod` (matrix value is overridden by the directive once we bump it). When the user installs Go 1.23+, bump `go.mod`'s `go` directive to `1.23` and remove the `GOTOOLCHAIN=local` from `Makefile` targets.

## CGO

- **`CGO_ENABLED=0` is set in Makefile (`build`, `test`, `vet`) and CI workflow.** Reasons:
  - Spec §5 locks the SQLite driver to `modernc.org/sqlite` (pure Go). CGO is not required.
  - On macOS 26 with Go 1.21 internal linker, CGO-on builds can produce binaries dyld rejects (`missing LC_UUID`).
- All Wave 1 backend agents must keep CGO off. If a future workstream legitimately needs CGO (e.g., `sqlite-vec` extension in v1.1), revisit then.

## Dependency version deviation

- **`modernc.org/sqlite` is pinned to `v1.34.4`** instead of the spec's `v1.44.3` because `v1.44.3` requires Go 1.24+. Schema-side this is irrelevant — both speak SQLite 3.44+. **Bump to `v1.44.3` once the toolchain catches up.**

## Schema decisions deferred to W1/W2

- The `messages` table in `001_init.sql` has a single scalar `tool_name` column (verbatim from spec §7).
- The canonical `Message` struct exposes `[]ToolCall` / `[]ToolResult` slices.
- These cannot be losslessly stored in the table as written. **W2 must decide:**
  - Option A: JSON-encode the slices into a `tool_calls_json` / `tool_results_json` column (single-table, simple queries, but opaque to FTS).
  - Option B: Add a `tool_calls(message_id, idx, name, input_json)` table in `004_*.sql` and a `tool_results(...)` table.
- This is a contract-relevant choice — open a `contract-change` PR with the chosen direction.

## Schema decisions deferred to W6 (config loader)

- `internal/config/schema.go` exposes `AIConfig.SummaryModel` / `TitleModel` / `EmbedModel` as raw strings (matching the TOML in `docs/plan/04-shared-contracts.md` §7 — `summary_model = "auto"`).
- The HTTP-side `SettingsAI` DTO (in `internal/api/contracts.go`) uses a structured `TaskModel{Provider, Model}` shape for clarity.
- W6's loader must bridge the two: parse the string `"auto"` or `"<provider>:<model>"` into `TaskModel`. Tests for both representations.

## API surface decision (minor extension)

- `SettingsResponse` includes a `DetectedProviders` field (booleans only, **never** key material) so the wizard UI can avoid a second roundtrip.
- This is a strict superset of `WizardDetectResponse.Providers`. If Wave 2 prefers separate endpoints, this field can be removed.

## Coverage snapshot at end of W0

| Package | Coverage |
|---|---|
| `internal/api` | 50.0% (will rise as W7 lands handlers) |
| `tools/dump-contracts` | 73.8% |
| `internal/store/migrations` | n/a (pure SQL files; `go test` reports "[no statements]") |

Other packages have no test files yet — they're skeletons. Wave 1 agents are responsible for hitting the 80% bar in their owned packages.

## Files NOT yet created (deferred to later workstreams, intentionally)

- `internal/store/db.go`, `migrate.go` — **W1**
- `internal/store/messages.go`, `sessions.go`, `search.go`, `summaries.go` — **W2 / W3**
- `internal/connectors/claude/**`, `internal/connectors/codex/**` — **W4 / W5**
- `internal/api/handlers/**`, `internal/api/http.go`, `internal/api/sse.go` — **W7 / W8**
- `internal/cost/pricing.go`, `pricing.json`, `refresh.go` — **W9**
- `internal/ai/**` — **W10 / W11**
- `internal/app/**`, `cmd/agentdeck/start.go|stop.go|doctor.go` real bodies — **W12** (cobra stubs exist now, prints "not implemented (W12)")
- `ui/**` — **W13 / W14**
- `internal/bench/**` — **W16**
- Release pipeline (`.goreleaser.yaml`, install scripts, brew formula, winget manifest, full README, landing page) — **W17**

## Verified-green at W0 hand-off

- `make vet` ✅
- `make test` ✅ (race + coverage)
- `go run ./cmd/agentdeck doctor` ✅ (prints stub JSON, exits 0)
- `go run ./cmd/agentdeck --version` ✅ (prints `v0.0.0-bootstrap`)
- 6 fixtures parse as valid JSON ✅
- `go run ./tools/dump-contracts` ✅ (31 structs dumped; all 14 cross-stream types present)
- 31 cross-stream types live in exactly one file each (no drift)

## Human review checklist before fanning out to Wave 1

- [ ] Sight-read `internal/connectors/connector.go` — interface + types
- [ ] Sight-read `internal/api/contracts.go` — every route DTO present
- [ ] Sight-read `internal/api/sse_events.go` — all 6 events typed
- [ ] Sight-read the 3 SQL migration files (and the `compact_events` table)
- [ ] Sight-read `internal/cost/pricing_schema.go` and `internal/config/schema.go`
- [ ] Sight-read `docs/contracts.md`
- [ ] Confirm `cmd/agentdeck doctor` output is acceptable as a stub
- [ ] Decide on `tool_calls` storage shape (option A vs B above) and update `docs/contracts.md`

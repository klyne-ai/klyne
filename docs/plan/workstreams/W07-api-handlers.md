# W7 · HTTP API + Read-Only Handlers

> **Wave:** 2 · **Effort:** M · **Depends on:** W0, W2, W3, W6 · **Recommended skills:** `golang-patterns` + `superpowers:test-driven-development`

---

## Universal preamble

You are working on the klyne repo. Spec at `compass_artifact_wf-d189e421-ff1d-443c-95b8-19b52fcd59b4_text_markdown.md`. §18 decisions are LOCKED. TDD-first; ≥80% coverage. After every change >30 lines run `make ci`. Use `superpowers:verification-before-completion` before claiming done.

**Single-writer rule:** modify only files under "Owned paths". Open a `contract-change` PR for anything else.

---

## Goal

chi-based HTTP router and the read-only handlers the frontend needs:
`/sessions`, `/sessions/:id`, `/sessions/:id/messages`, `/sessions/:id/summary`, `/search`, `/cost/summary`, `/settings` (GET + PUT), `/healthz`.

Bind `127.0.0.1:7878` only. Loopback-enforced in middleware.

---

## Spec sections

- §6 (flows that need each route)
- §7 (data shapes)
- §11 (file layout)

---

## Owned paths

```
internal/api/http.go                           (router + middleware: logger, recover, loopback-CORS)
internal/api/http_test.go
internal/api/handlers/sessions.go
internal/api/handlers/sessions_test.go
internal/api/handlers/search.go
internal/api/handlers/search_test.go
internal/api/handlers/cost.go
internal/api/handlers/cost_test.go
internal/api/handlers/settings.go
internal/api/handlers/settings_test.go
internal/api/handlers/health.go
internal/api/handlers/health_test.go
```

---

## Inputs

- W0 `internal/api/contracts.go` (DTOs and route constants).
- W2 messages + sessions DAOs.
- W3 search + summaries DAOs.
- W6 config loader (for `/settings`).
- W9 cost engine (for `/cost/summary` — inject via `Deps`, mock in tests).

---

## Outputs

```go
package api

type Deps struct {
    DB     *store.DB
    Cfg    *config.Config
    Cost   *cost.Engine
    Logger *slog.Logger
}

func NewRouter(deps Deps) http.Handler

// Crucial integration hook for W8 (SSE) and W15 (restore/wizard):
type RouterMounter interface {
    Mount(r chi.Router)
}

func (rt *Router) RegisterMounter(m RouterMounter)
```

`RegisterMounter` is what mitigates **risk R3**. W8 and W15 attach via this hook — they never edit your files.

---

## Acceptance criteria

- [ ] Every route in W0's `contracts.go` (except `/events`, `/sessions/:id/restore`, `/wizard/*`) has a passing `httptest` test.
- [ ] **Loopback-only enforced:** middleware refuses non-127.0.0.1 source IPs. Tested.
- [ ] 4xx for bad inputs; **never 5xx for client errors** (only 5xx for genuine server errors).
- [ ] All responses use the canonical DTOs from `contracts.go`.
- [ ] 80%+ coverage.
- [ ] `RegisterMounter` works — write a smoke test that registers a fake mounter and asserts a route shows up.

---

## Routes you own

| Method | Path | DTO |
|---|---|---|
| GET  | `/sessions` | `SessionListResponse` |
| GET  | `/sessions/:id` | `SessionResponse` |
| GET  | `/sessions/:id/messages` | `MessageListResponse` |
| GET  | `/sessions/:id/summary` | `SummaryResponse` |
| GET  | `/search` | `SearchResponse` |
| GET  | `/cost/summary` | `CostSummaryResponse` |
| GET  | `/settings` | `SettingsResponse` |
| PUT  | `/settings` | `SettingsResponse` |
| GET  | `/healthz` | `{ok, version, schema_version}` |

---

## Testing requirements (TDD-first)

Use `httptest.NewServer`, real (tempdir) store, real config tempfile.

Per route:
1. Happy path with seeded data.
2. 4xx for invalid query params.
3. 4xx for not-found IDs.
4. Auth middleware: non-127.0.0.1 source → 403.
5. Pagination params respected (where applicable).

`TestRegisterMounter` — fake mounter registers `/test`; assert reachable.

---

## Hard boundaries

- Do **NOT** register `/events` — that's **W8**.
- Do **NOT** register `/sessions/:id/restore` or `/wizard/*` — those are **W15**.
- Do **NOT** call any AI provider — **W11** owns that.
- Do **NOT** edit `internal/api/contracts.go` or `sse_events.go` — those are W0-owned (open a `contract-change` PR if needed).

---

## Done

When W14 (frontend components) can hit your endpoints from `localhost:7878` and render the dashboard, session detail, search, and cost panels, and W8 / W15 can attach via `RegisterMounter` without touching your files.

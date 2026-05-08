# W12 · Wiring Layer + cobra Commands

> **Wave:** 3 · **Effort:** M · **Depends on:** W1–W11 · **Recommended skills:** `general-purpose` (Opus 4.7)
>
> **Single agent only.** This file touches every package's exported API.

---

## Universal preamble

You are working on the agentdeck repo. Spec at `compass_artifact_wf-d189e421-ff1d-443c-95b8-19b52fcd59b4_text_markdown.md`. §18 decisions are LOCKED. TDD-first; ≥80% coverage. After every change >30 lines run `make ci`. Use `superpowers:verification-before-completion` before claiming done.

**Single-writer rule:** modify only files under "Owned paths". If a wiring need exposes a missing function in another package, **open a PR back to that workstream's owner** — never silently add to their files.

---

## Goal

Compose all the modules into a running daemon. Implement `agentdeck start`, `agentdeck stop`, `agentdeck doctor`. Bind `127.0.0.1:7878` only. Open the browser on first run.

---

## Spec sections

- §6 flows A & B (the daemon must make these work end-to-end)
- §11 (file layout)

---

## Owned paths

```
internal/app/app.go                  (composition root: store, connectors, hub, cost, AI runner, API)
internal/app/app_test.go
internal/app/lifecycle.go            (graceful shutdown, signal handling)
internal/app/lifecycle_test.go
internal/app/openbrowser.go          (cross-OS xdg-open / open / start)
internal/app/openbrowser_test.go
cmd/klyne/start.go
cmd/klyne/stop.go
cmd/klyne/doctor.go
```

---

## Inputs

- All of W1–W11.
- W14's built UI (`ui/build/`) is embedded via `go:embed`. Wire `embed.FS` here and serve at `/`.

---

## Outputs

```go
package app

type App struct { /* assembled deps */ }

func BuildOnly(cfg *config.Config) (*App, error)   // assembles without starting servers — for tests
func New(cfg *config.Config) (*App, error)
func (a *App) Start(ctx context.Context) error      // starts watcher, store, API, SSE, AI runner; opens browser
func (a *App) Stop(ctx context.Context) error       // flushes writer queue, closes DB, exits within 2s
```

cobra commands:
- `agentdeck` (no args) → calls `start` subcommand internally.
- `agentdeck start` → starts daemon; writes pidfile to `~/.agentdeck/daemon.pid`.
- `agentdeck stop` → reads pidfile and SIGTERMs the daemon.
- `agentdeck doctor` → diagnostic JSON:
  - detected JSONL roots (`~/.claude/projects/`, `~/.codex/sessions/`)
  - detected provider keys (no values, just booleans)
  - DB path + size
  - schema version
  - daemon version
  - exits 0 if green, 1 otherwise

---

## Wiring topology

```
fsnotify watchers (W4 + W5)
        │
        ▼
   RawEvent channel
        │
        ▼
   Writer goroutine ── store.InsertMessage / UpsertSession (W2)
        │
        ▼
   Hub.Publish(MsgNew) (W8)
        │
        ├──► EventSource clients (frontend)
        │
        └──► AI tasks.Runner (W11)
                   │
                   ▼
              Selector → Provider (W10) → InsertSummary (W3) → Hub.Publish(SummaryReady)

HTTP server (W7 router + W8 SSE + W15 mounters) on 127.0.0.1:7878
   ├──► serves chi routes
   ├──► serves /events SSE
   └──► serves /  → embed.FS of ui/build/
```

---

## Acceptance criteria

- [ ] **Fresh-machine smoke:** `agentdeck` against a tempdir with fixture JSONL → ingests, serves UI (embedded), opens browser (suppressed in CI), all four spec §6 flows work.
- [ ] `agentdeck doctor` returns structured JSON with the items above. Exit 0 if green, 1 otherwise.
- [ ] **SIGTERM → flush writer queue → close DB → exit within 2s.**
- [ ] Pidfile lifecycle: `start` writes; `stop` reads + SIGTERM + waits + removes; both `start` and `stop` are idempotent (e.g., `stop` when no pid — exit 0 with message).
- [ ] **Summaries run in their own goroutine pool** — never inline with the writer (mitigates risk R7). Document this invariant in `app.go`.
- [ ] 80%+ coverage on `internal/app/`.

---

## Testing requirements (TDD-first)

`app.go` is integration-flavored — write `BuildOnly()` returning the assembled struct without starting servers and unit-test the assembly:

1. `TestBuildOnly_AllDepsWired` — every field non-nil after build.
2. `TestApp_StartStop_Roundtrip` — start in goroutine; stop; assert ≤ 2s.
3. `TestApp_SIGTERM_FlushesWriter` — emit messages; SIGTERM mid-stream; assert all flushed before exit.
4. `TestDoctor_HealthyExit0` — set up tempdir env; `doctor` returns expected JSON, exit 0.
5. `TestDoctor_MissingDB_Exit1` — DB not openable → exit 1 with reason.
6. `TestStart_OpenBrowserCalled` — inject mock browser opener; assert called with `http://127.0.0.1:7878`.
7. `TestPidfile_Lifecycle`.
8. `TestEmbeddedUI_Served` — GET `/` returns HTML from embedded `ui/build/index.html`.

Integration test: start a real server on a random port (not 7878), hit each route, assert green.

---

## Hard boundaries

- Do **NOT** modify any package's exported API to make wiring easier — open a PR to the workstream owner.
- Do **NOT** implement summarization, search, parsing, or rendering yourself — call the existing modules.
- Do **NOT** ship the embedded UI without verifying `ui/build/` exists (build step in Makefile must produce it before `go build`).

---

## Done

When all four user flows in spec §6 work end-to-end on a fresh machine, `agentdeck doctor` returns green, and the W16 perf bench can run against a real daemon.

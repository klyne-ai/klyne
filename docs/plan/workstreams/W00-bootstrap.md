# W0 · Bootstrap & Shared Contracts

> **Wave:** 0 · **Effort:** L (~2d) · **Dependencies:** none · **Recommended agent:** `general-purpose` (Opus 4.7), single agent — **do not parallelize**

This workstream is the single biggest leverage point in the entire project. Every other workstream depends on the contracts you produce. Drift here cascades.

---

## Universal preamble (read first)

You are working on the klyne repository at `/Users/mohitpatel/Desktop/Project/klyne`. The shipping spec is at `compass_artifact_wf-d189e421-ff1d-443c-95b8-19b52fcd59b4_text_markdown.md` in the repo root. Read §1, §5, §7, §8, §11, §17, §18 before touching code. Decisions in spec §18 are LOCKED.

**TDD is mandatory.** Write tests first using the `superpowers:test-driven-development` skill. Target ≥80% coverage on new code. Run `make ci` (which you set up) after every change >30 lines. Use `superpowers:verification-before-completion` before claiming done.

---

## Goal

Produce the Go module, repo skeleton from spec §11, locked contracts (`Connector` interface, `Message` struct, SQLite migrations, HTTP API DTOs, SSE event types, config schema, pricing schema), CI pipeline, fixture JSONL files, and `docs/contracts.md`.

After this workstream lands, **7 Wave-1 agents fan out** — every one of them depends on the contracts you produced.

---

## Spec sections to read

- §5 (PRAGMAs, tech choices)
- §7 (data flow, schema)
- §8 (smart selector inputs — these inform `Provider` interface and config)
- §10 (versions)
- §11 (repo layout, `Connector` interface)
- §17 (non-goals)
- §18 (locked decisions)

---

## Owned paths (you produce all of these)

```
go.mod
go.sum
Makefile
README.md                          (stub only — full copy in W17)
LICENSE
.gitignore
.golangci.yml
.editorconfig
.github/workflows/ci.yml           (lint + test only; release in W17)
.github/ISSUE_TEMPLATE/bug.yml
.github/ISSUE_TEMPLATE/feature.yml
.github/ISSUE_TEMPLATE/connector_request.yml
cmd/klyne/main.go              (cobra root + start/stop/doctor stubs returning "not implemented")
internal/connectors/connector.go   (interface + RawEvent, Message, Session, PricingTable, ToolCall, ToolResult — NO IMPLEMENTATIONS)
internal/store/migrations/001_init.sql
internal/store/migrations/002_fts.sql
internal/store/migrations/003_summaries.sql
internal/store/migrations/embed.go (just //go:embed *.sql + var FS embed.FS)
internal/api/contracts.go          (route list constants + request/response DTOs — handlers in W7)
internal/api/sse_events.go         (SSE event payload types: MsgNew, SummaryReady, SessionUpdate, CostTick, ThreadRebuild, CompactDetected)
internal/config/schema.go          (typed struct for ~/.klyne/config.toml — loader in W6)
internal/cost/pricing_schema.go    (typed struct + JSON tags for LiteLLM-style pricing — table data + lookup in W9)
tools/dump-contracts/main.go       (tiny Go AST tool that emits contract JSON for the TS contract-check)
examples/sample-jsonl/README.md
examples/sample-jsonl/claude/*.jsonl  (3–5 hand-collected fixtures, including one with /compact)
examples/sample-jsonl/codex/*.jsonl   (3–5 fixtures)
examples/sample-jsonl/generate.go     (synthetic generator if real fixtures unavailable)
docs/contracts.md                  (this section, frozen — change requires PR + cross-stream review)
```

---

## Inputs

- The shipping spec file (above paths).
- For fixture JSONL: hand-collect from real Claude Code and Codex sessions if available. If not, write a tiny generator script in `examples/sample-jsonl/generate.go` that produces realistic synthetic ones. **Include at least one fixture per CLI containing a `/compact` event** so W15 can test against it later.

---

## Outputs (acceptance contract for downstream agents)

Every contract listed in [`../04-shared-contracts.md`](../04-shared-contracts.md) is implemented and committed.

In particular:
- `internal/connectors/connector.go` matches spec §11 verbatim.
- The 3 migration SQL files match spec §7 (`sessions`, `messages`, `messages_fts`, `session_summaries`, `threads`, `thread_sessions`) **plus** a `compact_events(session_id, ts, before_token_count, after_token_count)` table for W15.
- The PRAGMAs in spec §5 are documented in `internal/store/migrations/embed.go` as constants (W1 implements applying them).
- `internal/api/contracts.go` lists every route + DTO from [`../04-shared-contracts.md`](../04-shared-contracts.md) §5.
- `internal/api/sse_events.go` defines all 6 SSE events from [`../04-shared-contracts.md`](../04-shared-contracts.md) §6.

---

## Acceptance criteria

- [ ] `go build ./...` succeeds on macOS, Linux, Windows.
- [ ] `go vet ./...` clean.
- [ ] `golangci-lint run` clean.
- [ ] `go test ./...` passes (mostly empty, but no errors).
- [ ] CI green on push to a feature branch.
- [ ] `klyne doctor` exits 0 with a "stub" message.
- [ ] Every type that crosses a workstream boundary lives in **exactly one** file you own.
- [ ] `docs/contracts.md` references each contract by file:line and pins the spec section it derives from (§7, §8, §11, §12, §18).
- [ ] At least one fixture per CLI in `examples/sample-jsonl/` contains a `/compact` event.
- [ ] `tools/dump-contracts/main.go` produces a JSON dump of all DTO types — used by W13's contract-check.

---

## Testing requirements

- Write `internal/store/migrations/migrations_test.go` that loads each `.sql` file with `modernc.org/sqlite` and confirms it parses.
- Write `internal/api/contracts_test.go` that asserts every route constant in `contracts.go` is unique.
- Write `tools/dump-contracts/main_test.go` covering its dump format.

---

## Hard boundaries (do NOT cross)

- Do **NOT** implement any handler bodies (W7).
- Do **NOT** implement the store DB open/migrate (W1).
- Do **NOT** implement any connector logic (W4 / W5).
- Do **NOT** implement provider integrations (W10).
- Do **NOT** ship pricing data values — define the schema only (W9 fills the table).
- Do **NOT** write any UI code (W13 / W14).

You produce **skeletons and types only**.

---

## Done-criteria summary for the human reviewer

Before merging W0:
1. Read every file in "Owned paths" once.
2. Cross-check each against [`../04-shared-contracts.md`](../04-shared-contracts.md).
3. Confirm `make ci` runs all checks.
4. Confirm `klyne doctor` is invokable and prints a stub message.
5. Sight-read `docs/contracts.md` — it should be the index a Wave-1 agent reads after this file.

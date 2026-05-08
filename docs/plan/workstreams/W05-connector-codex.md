# W5 · Codex CLI Connector

> **Wave:** 1 · **Effort:** M · **Depends on:** W0 · **Recommended skills:** `golang-patterns` + `superpowers:test-driven-development` + `regex-vs-llm-structured-text`

> **Independent of W4** — runs in parallel.

---

## Universal preamble

You are working on the klyne repo. Spec at `compass_artifact_wf-d189e421-ff1d-443c-95b8-19b52fcd59b4_text_markdown.md`. §18 decisions are LOCKED. TDD-first; ≥80% coverage. After every change >30 lines run `make ci`. Use `superpowers:verification-before-completion` before claiming done.

**Single-writer rule:** modify only files under "Owned paths". Open a `contract-change` PR for anything else.

---

## Goal

Implement `codex.Connector` satisfying `connectors.Connector` (W0). Discover JSONL files under `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl`, parse each line into the canonical `Message`, and watch for new lines via `fsnotify`.

The structure mirrors W4 closely; the path layout and message schema differ.

---

## Spec sections

- §7 (data flow, message normalization)
- §11 (`Connector` interface)
- §13 risk #2 (Codex JSONL format is "experimental" — parser must be tolerant)

---

## Owned paths

```
internal/connectors/codex/codex.go
internal/connectors/codex/parse.go
internal/connectors/codex/parse_test.go
internal/connectors/codex/watch.go
internal/connectors/codex/watch_test.go
```

---

## Inputs

- W0 `Connector` interface and types.
- W0 fixtures in `examples/sample-jsonl/codex/`.

---

## Outputs

```go
package codex

func New(root string) *Connector  // root = "~/.codex/sessions" (expanded)

// Implements connectors.Connector
```

---

## Acceptance criteria (mirror of W4 + Codex specifics)

- [ ] Parses every fixture in `examples/sample-jsonl/codex/`.
- [ ] Covers every message type observed in fixtures (typically `user`, `assistant`, `tool_use`, `tool_result`, `function_call`, `function_call_output`, `system`).
- [ ] `Watch` test: appends new line to a temp file, asserts event arrives within 200ms.
- [ ] **Date-partitioned directory layout handled:** today's directory may not exist at watcher start. The watcher must:
  - Create-aware: watch parent dir, attach to new YYYY/MM/DD subdirs as they appear.
  - On day rollover, attach to the new date directory.
- [ ] Tolerates unknown fields (forward-compat); skips malformed lines with a warning log, never panics.
- [ ] **Codex token totals are emitted in JSONL** per spec §2 — extract them into `tokens_in` / `tokens_out` on the `Message`.
- [ ] **Warm-up + watch deduplication** (risk R5).
- [ ] **fsnotify backstop scan every 30s** (risk R9).
- [ ] 80%+ coverage.

---

## Testing requirements (TDD-first)

1. `TestParse_<MessageType>` ×N — one per observed type.
2. `TestParse_TokenExtraction` — message with token totals → `tokens_in/out` populated.
3. `TestParse_UnknownField_Tolerated`.
4. `TestParse_MalformedLine_Skipped`.
5. `TestDiscover_FindsFiles_AcrossDates` — tempdir with 3 YYYY/MM/DD subdirs.
6. `TestWatch_NewDateDir_AttachesAutomatically` — watcher running; create today's dir mid-run; new files there get picked up.
7. `TestWatch_NoDoubleEmit_OnWarmup`.
8. `TestWatch_BackstopDetectsNewFile`.

---

## Hard boundaries

- Do **NOT** read `~/.codex/auth.json` — spec §17 #3, §18 #7. The conservative path is to never touch ChatGPT-Plus tokens.
- Do **NOT** write to the DB — emit to channel; W12 wiring writes.
- Do **NOT** touch `internal/connectors/claude/**` (W4).
- Do **NOT** modify W0's `Connector` interface.

---

## Done

When the daemon (W12) can ingest a real Codex CLI session in real time and the messages appear in the SvelteKit UI (W14) without a browser refresh.

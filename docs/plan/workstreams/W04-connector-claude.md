# W4 · Claude Code Connector

> **Wave:** 1 · **Effort:** M · **Depends on:** W0 · **Recommended skills:** `golang-patterns` + `superpowers:test-driven-development` + `regex-vs-llm-structured-text`

---

## Universal preamble

You are working on the klyne repo. Spec at `compass_artifact_wf-d189e421-ff1d-443c-95b8-19b52fcd59b4_text_markdown.md`. §18 decisions are LOCKED. TDD-first; ≥80% coverage. After every change >30 lines run `make ci`. Use `superpowers:verification-before-completion` before claiming done.

**Single-writer rule:** modify only files under "Owned paths". Open a `contract-change` PR for anything else.

---

## Goal

Implement `claude.Connector` satisfying `connectors.Connector` (W0). Discover JSONL files under `~/.claude/projects/<encoded-cwd>/<uuid>.jsonl`, parse each line into the canonical `Message`, and watch for new lines via `fsnotify`.

This is **structured parsing — no LLM**. Use the `regex-vs-llm-structured-text` skill.

---

## Spec sections

- §7 (data flow, message normalization)
- §11 (`Connector` interface)
- §13 risk #1 + #4 (parser tolerance, `/compact` heuristic — note: compact detection lives in W15, **not here**)

---

## Owned paths

```
internal/connectors/claude/claude.go         (constructor + Connector interface methods)
internal/connectors/claude/parse.go          (JSONL line → *connectors.Message)
internal/connectors/claude/parse_test.go
internal/connectors/claude/watch.go          (fsnotify-based Watch())
internal/connectors/claude/watch_test.go
internal/connectors/claude/decode_cwd.go     (decode encoded CWD path segment)
internal/connectors/claude/decode_cwd_test.go
```

---

## Inputs

- W0's `Connector` interface, `Message` / `RawEvent` / `Session` / `ToolCall` types.
- W0's fixtures in `examples/sample-jsonl/claude/` (read-only).

---

## Outputs

```go
package claude

func New(root string) *Connector  // root = "~/.claude/projects" (expanded)

// Implements connectors.Connector:
//   Name() string                                       → "claude"
//   Discover(ctx) ([]string, error)                     → list JSONL files
//   Watch(ctx, events chan<- RawEvent) error
//   Parse(line []byte, path string) (*Message, error)
//   Pricing() PricingTable                              → empty/nil; cost is W9-injected, not connector-owned
```

---

## The 6 message types you must handle

From the fixture set — each gets a dedicated subtest:

1. `user` — user message
2. `assistant` — model response (with optional tool_calls)
3. `tool_use` — assistant invoked a tool (subset of assistant role; emit as separate Message with `role="tool"` and `tool_name` set, OR keep nested in assistant — pick one and document)
4. `tool_result` — result of a tool call
5. `system` — system messages (compact-marker, init, etc.)
6. `summary` / compact-marker — synthetic summary message Claude inserts post-`/compact`

Document your decision on tool_use representation in `internal/connectors/claude/parse.go` doc comment.

---

## Acceptance criteria

- [ ] Parses every fixture in `examples/sample-jsonl/claude/` to `[]*Message` with no errors.
- [ ] All 6 message types covered by dedicated subtests.
- [ ] `Watch` test: writes new lines to a temp file, asserts events arrive on channel within 200ms.
- [ ] **Tolerates unknown fields** (forward-compat) — uses `json.RawMessage` or struct tags with `,omitempty` so future Claude updates don't break parsing.
- [ ] **Skips malformed lines with a warning log, never panics.**
- [ ] Decoded CWD round-trips: `decode(encode(x)) == x` for 50+ random paths (test names with spaces, slashes, unicode).
- [ ] **Warm-up + watch deduplication** (mitigates risk R5): on `Watch` start, take an inode/byte-offset snapshot, replay file from byte 0 → channel, then attach watcher seeking from snapshot offset. No double-emission.
- [ ] **fsnotify backstop** (mitigates risk R9): every 30s, scan filesystem for new files and reconcile against watched set.
- [ ] 80%+ coverage.

---

## Testing requirements (TDD-first)

Use Go subtests, one per message type. Use `t.TempDir()` for `Watch` tests.

1. `TestParse_<MessageType>` ×6 — one per type.
2. `TestParse_UnknownField_Tolerated` — JSON line with unknown field; parses without error.
3. `TestParse_MalformedLine_Skipped` — broken JSON; returns error; caller can skip.
4. `TestDiscover_FindsFiles` — tempdir mimicking `~/.claude/projects/<encoded-cwd>/`; returns expected paths.
5. `TestWatch_NewLinesArrive` — write a line; channel receives within 200ms.
6. `TestWatch_NoDoubleEmit_OnWarmup` — file has 10 lines on watch start; channel receives exactly 10, not 20.
7. `TestDecodeCWD_RoundTrip` — table-driven with 50+ paths.
8. `TestWatch_BackstopDetectsNewFile` — create new file outside fsnotify event → 30s later (fast-forward time) it appears in watcher.

---

## Hard boundaries

- Do **NOT** detect `/compact` events here — that's W15, in `internal/connectors/claude/compact.go` which **the connector imports** but the heuristic itself is W15's file.
- Do **NOT** write to the DB — connector emits `RawEvent` / `Message` to a channel. Downstream code (W12 wiring) writes.
- Do **NOT** modify W0's `Connector` interface — open a `contract-change` PR.
- Do **NOT** touch `internal/connectors/codex/**` (W5).

---

## Done

When W12 can wire `claude.New(...)` into the daemon, ingest fixtures end-to-end, and the W11 summarizer sees `MsgNew` events fired from new Claude sessions on disk.

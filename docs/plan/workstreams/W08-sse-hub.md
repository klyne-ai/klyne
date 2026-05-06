# W8 · SSE Hub + `/events` Endpoint

> **Wave:** 2 · **Effort:** S · **Depends on:** W0, W7 · **Recommended skills:** `golang-patterns` + `superpowers:test-driven-development`

---

## Universal preamble

You are working on the agentdeck repo. Spec at `compass_artifact_wf-d189e421-ff1d-443c-95b8-19b52fcd59b4_text_markdown.md`. §18 decisions are LOCKED (SSE, not WebSockets). TDD-first; ≥80% coverage. After every change >30 lines run `make ci`. Use `superpowers:verification-before-completion` before claiming done.

**Single-writer rule:** modify only files under "Owned paths". Open a `contract-change` PR for anything else. **Do NOT modify W7's router files** — attach via the `RouterMounter` hook.

---

## Goal

Implement an SSE broadcast hub and the `/events` endpoint. Single hub, fan-out to per-tab subscribers. Heartbeat every 15s. Drop-with-log for slow consumers (no head-of-line blocking).

---

## Spec sections

- §5 (real-time choice — SSE)
- §7 step 7 (event names)
- §12 (SSE latency budget: <100ms p95)

---

## Owned paths

```
internal/api/sse.go
internal/api/sse_test.go
internal/api/handlers/events.go
internal/api/handlers/events_test.go
```

---

## Inputs

- W0 `internal/api/sse_events.go` (typed event payloads).
- W7 router exposes `RegisterMounter` — register your `/events` route via that hook, **never by editing W7's files**.

---

## Outputs

```go
package api

type Hub struct { /* ... */ }

func NewHub() *Hub

func (h *Hub) Publish(event Event)              // non-blocking; per-sub channels are bounded
func (h *Hub) Subscribe() (<-chan Event, func()) // returns channel + cancel func

// handlers/events.go implements RouterMounter:
type EventsMounter struct{ Hub *Hub }
func (m *EventsMounter) Mount(r chi.Router)     // registers GET /events as SSE
```

`Event` is a tagged union — backed by the types in `sse_events.go` (`MsgNew`, `SummaryReady`, `SessionUpdate`, `CostTick`, `ThreadRebuild`, `CompactDetected`).

---

## Behavior contract

- **Per-subscriber buffered channel** (e.g., size 64). On full buffer, **drop event with a warning log** — do not block the publisher.
- **Heartbeat:** every 15s send a `: ping` SSE comment to keep the connection alive.
- **Last-Event-ID supported** — when a client reconnects with `Last-Event-ID: <n>`, hub may replay missed events from a small ring buffer (best-effort; bounded memory).
- **Unsubscribe on connection close** — handler detects client disconnect, calls cancel func.

---

## Acceptance criteria

- [ ] Stress test: 100 concurrent subscribers, 1000 events/s for 5s; no panics; slow subscribers don't block fast ones (drop-with-log policy works).
- [ ] End-to-end test: `Publish(MsgNew{...})`, EventSource client receives it within 100ms.
- [ ] Heartbeat verified — client kept alive past idle timeout.
- [ ] Cancel func: subscribe, publish, cancel, publish — second publish does not deliver to the cancelled subscriber.
- [ ] 80%+ coverage.

---

## Testing requirements (TDD-first)

1. `TestHub_PublishSubscribe` — single sub, one event, received.
2. `TestHub_MultipleSubs_AllReceive` — 5 subs, one publish, all 5 receive.
3. `TestHub_SlowSubDropped` — sub with slow reader; assert events drop, others unaffected.
4. `TestHub_Cancel_Stops` — cancel func stops delivery.
5. `TestHub_Stress_100x1000` — 100 subs × 1000 evts/s × 5s; no panics; latency < 100ms p95.
6. `TestEventsHandler_SSEFormat` — response Content-Type, `data:` framing, blank-line separators.
7. `TestEventsHandler_Heartbeat` — wait 16s, assert at least one heartbeat emitted.
8. `TestEventsHandler_Disconnect` — close client; assert hub unsubscribes within 1s.

Use `httptest.NewServer` plus a custom EventSource-equivalent reader.

---

## Hard boundaries

- Do **NOT** modify W7's router files. Use `RegisterMounter`.
- Do **NOT** modify `sse_events.go` (W0).
- Do **NOT** publish events directly from connectors — the daemon wiring (W12) owns the publish-from-event-loop responsibility.

---

## Done

When W11 can subscribe to `MsgNew` events, W14 can subscribe via EventSource and update the UI live, and the W16 perf bench shows <100ms p95 SSE latency.

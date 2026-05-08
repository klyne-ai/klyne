# break-advice → context-health migration

Companion to [context-rescue-strategy.md](./context-rescue-strategy.md).
The strategy doc proposes new `/sessions/{id}/context-health` and
`/sessions/{id}/handoff` endpoints, plus a `ContextRescue.svelte` component.
It does not say what happens to the existing break-advice + TokenSavings
surface, which already implements 60% of the same idea. This doc closes
that gap.

## What exists today

| Layer    | Code                                                              | Behaviour                                                                                                       |
|----------|-------------------------------------------------------------------|-----------------------------------------------------------------------------------------------------------------|
| Backend  | `internal/api/handlers/session_break_advice.go`                   | `GET /sessions/{id}/break-advice` → verdict ∈ {start_fresh, compact, continue, unavailable} + reason + topic.   |
| Backend  | `internal/api/handlers/session_usage.go`                          | `GET /sessions/{id}/usage` → context_window, context_used, context_fill_pct, calibrated 5h projections.         |
| Frontend | `ui/src/lib/components/TokenSavings.svelte`                       | Fill bar, "Compact now" CTA, "Should I start a fresh session?" button → renders break-advice verdict.           |
| Cache    | In-memory per-session, 10-min TTL (`breakAdviceCacheTTL`).         | Survives until process restart.                                                                                 |
| AI path  | `buildBreakAdviceFactory` in `internal/app/app.go:792`            | Picks a Title-class model via `ai.Pick(TaskTitle, …)`; falls back to ollama when no API key is set.             |

Three of the four "context-health" verdicts the strategy doc proposes
already exist by name in the break-advice contract (`continue` / `compact`
/ `start_fresh`). The fourth, `rescue_now`, is conceptually a stronger
`start_fresh` plus a generated handoff.

## What the strategy doc adds

1. A 4-state classifier: `Healthy`, `Drifting`, `Risky`, `Rescue now`.
   These are **session-state** labels, not just **next-action** verdicts.
2. A bloat scorecard explaining *why* the state is what it is.
3. A deterministic handoff document.
4. A Svelte component, `ContextRescue.svelte`, that surfaces all three.

The state labels do not map 1-to-1 onto the existing verdicts:

| break-advice verdict | context-health state (closest mapping)              |
|----------------------|------------------------------------------------------|
| `continue`           | `Healthy` OR `Drifting` (current contract conflates them) |
| `compact`            | `Risky`                                              |
| `start_fresh`        | `Rescue now`                                         |
| `unavailable`        | (orthogonal — error/state, not a label)              |

So the migration is a **strict superset**, not a rename. The new endpoint
must be able to produce both the state label and the recommended next action
in the same response.

## Target shape

### Endpoint

```
GET /sessions/{id}/context-health → 200
{
  "session_id":  "...",
  "state":       "healthy" | "drifting" | "risky" | "rescue_now",
  "action":      "continue" | "compact" | "start_fresh",
  "reason":      "<one sentence>",
  "bloat":       [ { "label": "...", "share_pct": 38.0, "count": 7, "kind": "tool_result" }, ... ],
  "signals":     { "context_fill_pct": 73.4, "msg_count": 184, "tool_loop_score": 0.42, ... },
  "handoff_ready": true,
  "ai_polished": false,
  "provider":    "anthropic" | "openai" | "gemini" | "ollama" | null,
  "model":       "<model-id>" | null,
  "cached_at":   1730000000000
}
```

Notes:

- `state` is the new 4-state label (the strategy doc's headline).
- `action` is the existing break-advice verdict, kept so the existing
  TokenSavings UI can read it without rewriting.
- `bloat` is new — top 5 rows, sorted by `share_pct` desc.
- `signals` exposes the inputs to the classifier so the panel can
  explain its reasoning.
- `ai_polished` distinguishes deterministic verdicts from
  AI-summarised ones (Phase 3+).

### Handoff endpoint (Phase 2)

```
GET /sessions/{id}/handoff → 200
{
  "session_id":  "...",
  "markdown":    "# Handoff\n\n...",
  "source":      "local_draft" | "ai_polished",
  "provider":    "..." | null,
  "model":       "..." | null,
  "cached_at":   1730000000000
}
```

Cached identically to break-advice (10-min TTL, in-memory).

## Migration phases

### Phase A — additive (no removal)

- Add `internal/contexthealth/` package containing the deterministic
  classifier. Pure function: takes session metadata + last N messages,
  returns the response struct above (minus AI fields).
- Add `internal/api/handlers/session_context_health.go` mounting `GET
  /sessions/{id}/context-health`.
- Add `internal/api/handlers/session_handoff.go` mounting `GET
  /sessions/{id}/handoff` (Phase 2 — can land in same release if ready).
- Add `ui/src/lib/components/ContextRescue.svelte` consuming the new
  endpoint. Mount it on the session page **above** TokenSavings, NOT
  replacing it yet.
- Both old and new components ship to production for at least one
  release. Telemetry (anonymous click counts) on each.

This is the fast, safe step. No contract removal, no UI regression.

### Phase B — converge

- Reimplement break-advice on top of the context-health classifier:
  derive the verdict from `state` (rescue_now → start_fresh, risky →
  compact, healthy/drifting → continue). Keep the `BreakAdviceResponse`
  contract byte-identical.
- Delete the duplicated message-fetch / cache logic in
  `session_break_advice.go`; have it call the same internal classifier
  helper. The HTTP shape stays.
- Update tests: the existing `session_break_advice_test.go` should
  remain green without modification.
- TokenSavings.svelte stays as-is and continues to work via break-advice.

### Phase C — UI cutover

- Mark the break-advice button inside `TokenSavings.svelte` as
  deprecated (visually unchanged for one release, then removed).
- Move the fill bar + compact CTA into ContextRescue.svelte, so the
  session page has one rescue surface instead of two.
- Keep `/sessions/{id}/usage` and `/sessions/{id}/break-advice` mounted
  for at least two releases after the UI cutover so any external
  consumers (the docs explicitly say agentdeck has none today) get a
  deprecation window.

### Phase D — endpoint sunset

- Remove `session_break_advice.go` and its tests.
- Remove the `BreakAdviceResponse` contract from `internal/api/contracts.go`
  and the corresponding TypeScript types.
- Update the contract checker (`ui/scripts/check-contracts.ts`) and the
  feature doc (`docs/features/token-savings.md`).

## Cache

Reuse the existing pattern wholesale. The classifier's input is a snapshot
of message metadata that changes every assistant turn anyway, so a 10-min
TTL keyed on `(session_id, last_msg_ts)` is correct. Bloat data is
expensive to compute (regex over recent tool results); cache it inside the
classifier output rather than recomputing per click.

The handoff endpoint caches separately because the input window for
"handoff" is bigger (more messages, more tool calls) — keying on
`(session_id, last_msg_ts, source)` so the AI-polished and local-draft
variants are independently memoised.

## AI factory

`buildBreakAdviceFactory` already does the work. Rename to
`buildContextHealthFactory` in Phase B, keep the same signature
(`AIProviderFactory`), and pass it to both handlers (context-health for
`ai_polished` reasons, handoff for `source: "ai_polished"`).

The ollama-fallback bug from the original conversation (#2) is unaffected
by this migration — it lives inside `internal/ai/providers/detect.go` and
should be fixed independently. **The migration must not depend on that
fix landing first**: deterministic Phase 1 ships with no AI provider
required.

## Backwards-compat checklist

Before each phase merges:

- [ ] All existing handler tests pass without modification (Phases A + B).
- [ ] The contract checker reports zero changes to existing types
  (Phases A + B).
- [ ] The new endpoint is added to `docs/contracts.md` and
  `internal/api/contracts.go` in the same PR that adds the handler.
- [ ] Telemetry counters are registered before old surface removal
  (Phase C).
- [ ] Sunset PR (Phase D) is gated on at least two release tags between
  Phase C and the removal.

## What we are explicitly NOT doing

- Not introducing a new context-health verdict that the existing
  break-advice cache cannot represent. Anything new must round-trip
  through `state` + `action`.
- Not removing the deterministic path even when AI polish is available.
  The eval plan ([context-rescue-eval.md](./context-rescue-eval.md))
  measures the deterministic classifier in isolation; AI polish is a
  reason field rewrite, not a verdict override.
- Not touching `/sessions/{id}/usage`. It stays the source of truth for
  context-fill numbers; ContextRescue.svelte reads both endpoints.

## Owner / sequencing

Phase A and the eval-fixture capture (per
[context-rescue-eval.md](./context-rescue-eval.md)) can run in parallel.
Phase B requires Phase A merged. Phase C requires the eval pass criteria
in the eval doc to be green for two consecutive weeks on real usage. Phase
D requires Phase C plus the two-release sunset window.

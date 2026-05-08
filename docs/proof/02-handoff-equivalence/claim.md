# Claim 02 — Klyne's handoff is structurally complete and deterministic; vanilla Claude's "what did we do?" is neither

> **Claim:** When you hit your rate-limit and need to start a fresh session, Klyne's handoff Markdown captures every structural slot the new session needs — project path, files touched (with reuse counts), commands run, recent failures, last few exchanges — and renders byte-identical output every time. Vanilla Claude's freeform "summarise what we just did" answer captures none of these reliably and varies turn-to-turn.

## Why this matters

The 5-hour rate-limit window doesn't end at the limit — it ends earlier, when each turn becomes prohibitively expensive because the full conversation is being re-fed to the model. Long sessions hit a wall well before you finish the work. The textbook escape: open a new session and bootstrap it with what you were doing.

The way users do that today: ask the AI **"summarise what we just did so I can paste it into a new session."** Two problems with that:

1. **It varies.** Ask twice, get two different summaries. The second new-session you bootstrap won't have the same context the first one did.
2. **It misses the structural details a fresh session actually needs.** The exact file paths it should re-open. The command stems it had been running. The failure that's still pending. Vanilla summaries focus on the narrative; new sessions need the artefacts.

Klyne's `generate_handoff` reads the JSONL transcript and emits a Markdown prompt with fixed sections — same input always produces the same output, every section a structured pull from the actual session events.

## The fixture

[`fixture.jsonl`](./fixture.jsonl) — a synthetic Claude Code session about adding exponential-backoff retry to a billing webhook dispatcher. Twelve messages walking through: read dispatcher → add retry → run tests → ETIMEDOUT failure → bump timeout → tests pass → start applying the same pattern to processor.ts → **session paused mid-task** (the user hit rate-limit and is about to handoff).

This is the canonical handoff scenario — work in flight, with one passing change committed, one failure resolved, and the next file just opened.

## Side-by-side: same prompt, two answers

### What vanilla Claude returns to *"summarise what we just did so I can continue in a new session"*

Realistic example output (what Claude tends to produce — paraphrased, varies turn-to-turn):

> *"We added exponential backoff retry logic to the webhook dispatcher. The initial timeout was too short and caused a test failure, so we bumped it from 1000ms to 5000ms. The dispatcher tests now pass. Next, we started applying the same retry pattern to the webhook processor."*

That paragraph reads fine, but ask yourself what the fresh new-session will have:

- File paths? **Missing.** Was it `dispatcher.ts` or `dispatcher/index.ts`?
- The exact command being run? **Missing.** Was it `npm test`, `vitest`, `jest`?
- The specific failure that was resolved? **Glossed over.** The new session won't recognise it if it recurs.
- File reuse counts? **Missing.** Which files are central vs touched once?
- Recent exchanges? **Missing.** What was the user's last instruction verbatim?

A turn later, asking the same question, you'd get a different paragraph.

### What Klyne's `generate_handoff` returns

This is **the verbatim output** of running Klyne against the fixture — captured by [`dump_test.go`](./dump_test.go). Run it yourself with `KLYNE_DUMP_HANDOFF=1 go test -v -run TestDumpHandoff ./docs/proof/02-handoff-equivalence/`:

```markdown
# Handoff from session `proof-se`

We are working in `/repo/billing`.

## Recent task

now apply the same retry pattern to webhooks/processor.ts

## Files touched

- `/repo/billing/src/webhooks/dispatcher.ts` (×3)
- `/repo/billing/src/webhooks/processor.ts`

## Commands run

- `npm test` (×2)

## Known failures

- FAIL webhooks/dispatcher.test.ts: Error: AbortError: ETIMEDOUT after 1000ms
  (3 retries × 1000ms timeout = exceeds total budget)

## Last few exchanges

**user** — run the webhook integration test

**assistant** — The 1000ms per-attempt timeout is too tight for retries — bumping to 5000ms.

**user** — okay run again

**assistant** — All 8 dispatcher tests pass. Backoff is 100/400/1600ms with 5s per-attempt timeout.

**user** — now apply the same retry pattern to webhooks/processor.ts

**assistant** — Reading processor.ts first to find the right insertion point.
```

Every fresh-session need is in there:

- **Project path**: `/repo/billing`
- **Recent task**: the user's last unfulfilled instruction, verbatim
- **Files touched** with reuse counts: `dispatcher.ts (×3)` tells the new session this file is central; `processor.ts` is the file currently open
- **Commands run** with counts: `npm test (×2)` — the new session knows the test runner
- **Known failures**: the ETIMEDOUT with the *exact* error text, so the new session recognises it if it recurs
- **Last few exchanges**: verbatim, in chronological order, including the failure resolution and the open task

**Run the same prompt again — the output is byte-for-byte identical**, asserted by `TestProof_HandoffIsDeterministic`.

## What the tests assert

[`proof_test.go`](./proof_test.go) — three assertions, all run by `make proof`:

| Assertion | What it proves |
|---|---|
| `TestProof_HandoffStructurallyComplete` | Every section header (`## Recent task`, `## Files touched`, `## Commands run`, `## Known failures`, `## Last few exchanges`) is present, and each is populated from the fixture's actual content (file paths, the npm command, the ETIMEDOUT line) |
| `TestProof_HandoffIsDeterministic` | Rendering the same fixture twice produces byte-identical output — same input → same output, every time |
| `TestProof_HandoffSurfacesIterativeReuseHonestly` | The "Files touched" table marks `dispatcher.ts` with `(×3)` because it was iterated on three times — telling the fresh session which file is central |

If any of the three break, the README claim breaks too. By design.

## Reproduce it yourself

```bash
# From the repo root
make proof

# Or, just this scenario:
go test -v ./docs/proof/02-handoff-equivalence/...

# Or, see the actual rendered handoff:
KLYNE_DUMP_HANDOFF=1 go test -v -run TestDumpHandoff \
  ./docs/proof/02-handoff-equivalence/
```

## What's *not* claimed

To stay honest:

- **Klyne does NOT prove the new session will produce identical AI output.** That depends on the model, temperature, and the user's follow-up turns. What's proven: the handoff captures the *inputs* a new session needs.
- **Klyne does NOT auto-paste the handoff.** It generates the Markdown; the user copies it into a fresh chat. Claude Code's UI doesn't expose a programmatic-paste API.
- **Klyne's handoff is not a complete transcript.** It's a structured summary — the last 6 turns, top 20 files, top 12 commands. For full content, use `get_pre_compact_context` or read the JSONL directly.

The honest pitch: **Klyne's handoff is the structurally-complete, deterministic alternative to "AI, summarise this for me." Use it when you need to bootstrap a fresh session and can't afford the variance.**

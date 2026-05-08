# Klyne — Reproducible Proof

This directory backs every public claim Klyne makes with a fixture, a Go test, and a side-by-side comparison against what the AI alone would return. **Every claim links to code you can run yourself.**

> "Don't trust marketing. Run `make proof` and judge for yourself."

## How to verify

```bash
make proof
```

This runs every test under `docs/proof/` with verbose output, so you see each assertion pass against real fixtures. Total runtime: well under 10 seconds.

If any claim fails to reproduce, the test fails red — the whole `make proof` exits non-zero. Marketing claims and code stay locked together.

## The two scenarios

### 1. After `/compact`, Klyne recovers what Claude lost

**The pain:** Anthropic's 5-hour rate-limit window exhausts faster as your context grows (every turn re-feeds the whole history). `/compact` is the official escape hatch — but it permanently destroys the original turns from Claude's view. Whatever the post-compact summary missed, the AI cannot get back. You typically re-explain it manually, costing more rate-limit budget on context the AI already had.

**What Klyne does:** Reads the original turns straight from the JSONL transcript on disk. The AI's context can no longer see them; Klyne can.

**The proof:** [`01-compact-recovery/`](./01-compact-recovery/)

### 2. Klyne's handoff is structurally complete; vanilla Claude's "what did we do" is not

**The pain:** When you start a fresh session ("hit my rate limit, opening new chat"), you need to bootstrap the new AI with what you were doing. Asking the original AI "what did we do?" produces a paragraph that varies turn-to-turn, often skips the file paths and command stems the new session needs, and burns input tokens you can't afford near the limit.

**What Klyne does:** Generates a deterministic Markdown handoff straight from JSONL — same input always produces the same output, structured into the categories a fresh session actually needs (project path, files touched, commands run, recent failures, last few exchanges).

**The proof:** [`02-handoff-equivalence/`](./02-handoff-equivalence/)

## What "proof" means here

Each subdirectory contains:

| File | What it does |
|---|---|
| `claim.md` | The exact claim, plus a side-by-side: what Claude alone returns vs what Klyne returns |
| `fixture.jsonl` | A synthetic Claude session with known content — the inputs to every test |
| `proof_test.go` | The Go assertions that verify the claim against the fixture |
| `demo.md` (optional) | Captured terminal output showing what a real run looks like |

The fixtures are deliberately small and human-readable so you can `cat fixture.jsonl` and see exactly what the test is asserting against. Nothing is hidden behind a binary blob or an opaque mock.

## What Klyne does *not* claim

To make sure we don't lose users by overclaiming:

- **Klyne does NOT reduce Claude's per-turn token cost.** It doesn't intercept the AI loop.
- **Klyne does NOT save a guaranteed % of your rate-limit budget.** The savings depend on whether you would otherwise have re-explained the lost context — which varies by user and session.
- **Klyne does NOT replace `/compact`.** Use `/compact` when you need it; Klyne lets you survive it without losing recoverable context.
- **Klyne does NOT call any AI model.** Every tool here is deterministic over JSONL bytes.

The honest one-line pitch: **Klyne is the source of truth for what your AI session has actually done — including the parts the AI itself can no longer see.**

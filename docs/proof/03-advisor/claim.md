# Proof: proactive session advisor

> **Claim under test:** klyne pushes a single one-line advisory into
> Claude Code's prompt flow at the moment a session crosses one of
> four deterministic thresholds, and never repeats the same advisory
> until that condition has cleared.

This proof exercises every advisor trigger plus the transition rule.
Each test is a pure-Go assertion against `contexthealth.RenderAdvisor`,
which is the same function the `klyne advise` hook subprocess
invokes on every prompt submission.

Run from the repo root:

```bash
go test -v ./docs/proof/03-advisor/
make proof
```

## What is asserted

The proof tests, in order:

1. **Stale-context.** A synthetic transcript opens on auth, loads
   two auth files (~12 KB each), pivots to billing across five user
   prompts, and reads one billing file (~12 KB). The advisor must
   fire `stale`, name `refund.go` in the relevant subset, and never
   leak `login.go` or `session.go` into the advisory.

2. **Acceleration.** Eight assistant turns: the first five at ~5K
   uncached input, the last three rising 9K → 15K → 25K. The advisor
   must fire `acceleration`, mention "doubled" in the copy, and
   point at `/klyne:handoff scope=current`.

3. **5-hour window urgent.** A pre-aggregated `FiveHourSummary` at
   80% of cap. The advisor must fire `window_75`, mention "5-hour
   window," and surface the consumption percentage so the user has
   a concrete number to act on.

4. **Transition rule.** A session at 80% fill fires `hard_ceiling`
   once. A second identical call returns silent. Dropping fill to
   30% clears the trigger. Spiking back to 80% re-fires.

## Why these assertions cover the v1 contract

The advisor's contract is "useful but not annoying." Each test
reflects one half of that:

- The first three tests confirm the advisor *does fire* on the
  conditions that matter — the noisy-but-useful half.
- The fourth confirms the advisor *does not refire* once a trigger
  has been observed — the not-annoying half.

If any of these assertions ever stops holding, the README's
"proactive advisor without spam" promise is broken and `make proof`
fails red. Marketing and code stay locked together by design,
matching the existing proof-driven structure under
`docs/proof/01-compact-recovery/` and `docs/proof/02-handoff-equivalence/`.

## Why no JSONL fixture in this directory

The earlier proof packages drive their assertions through a real
JSONL fixture so the parser path is also exercised. The advisor
proof is intentionally simpler: it constructs canonical
`*connectors.Message` values directly, because the trigger logic is
already covered by parser-driven tests in `cmd/klyne/advise_test.go`
and `internal/contexthealth/five_hour_window_test.go`. Adding a
JSONL fixture here would only duplicate that coverage.

When and if a regression slips past the parser-driven tests, this
proof will catch it via the exported `RenderAdvisor` API — which is
what the hook itself depends on, end-to-end.

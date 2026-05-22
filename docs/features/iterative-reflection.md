# Iterative reflection — final workflow

Decided 2026-05-22. Source of truth for the workflow change behind the
"three bullets read as repetition" + "re-running reflection loses context"
issues. If anything in the implementation drifts from this doc, the doc
wins — open a follow-up to change the spec deliberately, don't backdoor it.

## Problem

Today `/klyne:reflect` writes one `worklog_reflections` row per run. Each
re-run produces a fresh row that **replaces** the day's view (the
productivity API picks the most recent per `(project, day)`). Two
consequences:

1. A single accomplishment shows as N flat bullets (one per insight),
   reading as repetition — there is no headline + detail disclosure.
2. Re-running reflection re-evaluates the entire day's stop_summaries,
   so prose drifts, token cost compounds, and history of "I identified
   X at T1, shipped fix at T3" is lost.

## Workflow

```
T1: /klyne:reflect
    propose_reflection reads cursor for (project, day) = NULL
      → returns ALL today's stop_summaries
    AI emits insights for that slice
    record_reflection writes row #1 with
      stop_summary_cursor_ts = max(consumed stop_summary.ts)

T2: /klyne:reflect
    propose_reflection reads cursor = row #1's stop_summary_cursor_ts
      → returns only stop_summaries with ts > cursor
    If empty → no-op ("Nothing new since <ts>"). NO row written. (C1)
    Else AI emits insights for the delta slice only
    record_reflection writes row #2 with cursor advanced

T3: same as T2
```

Each row is **append-only and immutable**. The cursor lives on the row
itself (column `stop_summary_cursor_ts`) — every row records "I covered
stop_summaries up through ts X". The next run reads the max cursor for
the day. (D1)

## Display

The productivity dashboard's WHAT WAS DONE section reads **every** row
for `(project, day)` ordered by `ts ASC` and renders each as one
**group** (A1):

```
[Group 1 — 14:32]   ▸  Identified the LabStack bug
                       3 insight details (hidden until clicked)

[Group 2 — 15:48]   ▸  Created PR for LabStack bug and merged it
                       2 insight details

[Group 3 — 16:30]   ▸  Cleaned up README + reorganized docs
                       1 insight detail
```

Per group:
- **Headline** — derived from the first insight's title (via `splitTitle`).
  Future: an explicit `headline` field once we generalize the
  slash-command prompt (B is a separate workstream).
- **Details disclosure** — `▸ N details` button reveals whatever
  insights the AI emitted for that slice. Variable count per group.
  (B: prompt should produce whatever is appropriate for the topic; the
  UI just renders what comes out.)
- **Group ts** — the reflection row's `ts`, used as the time chip and
  for ordering.

Groups stack chronologically. Default: latest group expanded, older
collapsed. Honest history — no cross-time AI re-synthesis.

## Schema change

Add one nullable column to `worklog_reflections`:

```sql
ALTER TABLE worklog_reflections ADD COLUMN stop_summary_cursor_ts INTEGER;
```

Backward compat: existing rows have NULL → treated as "covers all
stop_summaries up through the row's ts". The next `/klyne:reflect` run
uses the row's `ts` as the effective cursor.

## Non-goals

- No re-synthesis of cross-time facts (we don't try to collapse
  "Identified bug at T1 + Shipped at T3" into one bullet — A1 is honest
  history, not consolidation).
- No prompt generalization in this workflow change — that's a separate
  workstream (B).
- No reflection editing — rows are append-only. Future: if the user
  doesn't like the wording of a row, they re-run `/klyne:reflect` after
  manually advancing the cursor (or we add a delete operation).
- No multi-day catch-up changes — `day` semantics stay as-is.

## Delivery phases

1. **Phase 1 (this PR)** — schema migration + cursor read/write helpers
   in `internal/store`. Backfill NULL → existing `ts` value implicitly
   via NULL semantics (no UPDATE needed).

2. **Phase 2** — `propose_reflection` MCP tool filters stop_summaries by
   cursor; `record_reflection` writes `stop_summary_cursor_ts` from the
   max consumed ts. C1 no-op shape returned when empty.

3. **Phase 3** — productivity API returns all rows for `(project,
   day)`. UI renders groups with disclosure.

4. **Phase 4 (separate workstream)** — prompt generalization (B).

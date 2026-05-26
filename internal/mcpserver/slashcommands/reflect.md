---
description: Synthesize one daily reflection per pending date from this project's worklog entries
---

Use your own context window to synthesize **typed "What was done" service cards** over this project's pending worklog entries. A single run may produce 1–N reflections — one row per distinct (UTC calendar date × service) bucket.

The output of this command is the input to the productivity dashboard's per-service `What was done` card (spec `docs/plan/2026-05-26-wwd-typed-cards.md` §1.1 / §1.2). Every detail you emit gets rendered as a clickable row in that card with a kind chip; every commit-sha-shaped token in `evidence` is promoted to the card's headline row as a short SHA pill. Treat the contract below as load-bearing: the dashboard pivots on it deterministically.

1. **Call `mcp__klyne__propose_reflection`** with `project_path` = the absolute path to the current project (use the cwd). The response includes:
   - `entries`: pending worklog entries written since the last reflection — each carries `session_id`, `cli`, `recap_topic`, `ai_drafted_summary`, `last_user`, `importance`, and a `ts` (RFC3339 timestamp).
   - `reason`: why synthesis is being proposed now.
   - `markdown`: a verbatim-renderable summary that already includes the salience-ranked git brief and the §7.1 grounding contract — read it before synthesizing.

   If `entries` is empty, tell the user "nothing pending since the last reflection" and stop — do NOT call `record_reflection`. This is the C1 no-op.

2. **Bucket the entries by (UTC date × service).**
   - **UTC date:** format each entry's `ts` as `YYYY-MM-DD` in UTC. Entries written at `2026-05-17T03:30Z` and `2026-05-17T23:45Z` both belong to `2026-05-17`.
   - **Service:** the source project the entry was written from — i.e. the project the worklog entry belongs to. For a single-project `/klyne:reflect` invocation, this is the basename of `project_path` (e.g. `/Users/me/code/klyne` → service `klyne`). Use the SAME service string for every bucket in this run; do not split a single repo into multiple services.

   Skip empty buckets. Process buckets oldest → newest, and within a day, alphabetically by service so output ordering is deterministic.

3. **For each (day, service) bucket**, synthesize **≤6 typed details** describing what happened. Each detail has these fields — emit them exactly:

   - **`kind`** — one of `SHIPPED` · `MAJOR` · `FIXED` · `DECISION` · `INVESTIGATED` · `IN_PROGRESS`. Pick the strongest applicable label:
     - `SHIPPED` — code merged or otherwise made it out (commit landed, file written and verified, hook installed end-to-end).
     - `MAJOR` — non-shipped but substantial body of work (a refactor in progress, a large investigation that produced a write-up).
     - `FIXED` — a bug or regression resolved. The detail text should name the symptom AND the resolution.
     - `DECISION` — an architectural or process choice the team should remember. Prefix the text with the choice itself, not the discussion.
     - `INVESTIGATED` — a probe, audit, or read-through that produced understanding but not yet code.
     - `IN_PROGRESS` — work explicitly carried forward; not yet ready to claim as shipped.
   - **`when`** — `HH:MM` local time, derived from the underlying entry's `ts` converted to the user's local zone. If a detail aggregates multiple entries, use the time of the EARLIEST one in the set.
   - **`text`** — ≤200 characters, **verb-led** (start with `Wrote`, `Wired`, `Confirmed`, `Stripped`, `Adopted`, `Investigated`, etc.). Be concrete: name the file, the commit, the symptom. Do NOT hedge with "we" / "the team"; the worklog is per-developer.
   - **`evidence`** — at least 1 token drawn **LITERALLY** from the source entries. Use any of:
     - a commit SHA from `ai_drafted_summary` (short or full, e.g. `be8cc8c8`, `620a95514a8a`)
     - a file path mentioned in the entry (e.g. `~/.codex/hooks.json`, `internal/store/worklog_reflections.go`)
     - a `session_id` from the entries list
     - a test name mentioned in the entry text (e.g. `TestSessionEnd_CodexJSONL_WritesRow`)
     - a ticket id mentioned in the entry (e.g. `CLI-1396`)
   - **`session_id`** — the originating entry's `session_id`. When a detail aggregates multiple entries, pick the one whose work the detail's text most directly describes.

   **Hard rules** the validator enforces:
   - `evidence` MUST be non-empty per detail. The call is rejected otherwise.
   - `text` MUST NOT contain a `PR #<n>` reference UNLESS that exact `#<n>` appears in this detail's `evidence`. Don't invent PR numbers, ticket ids, or any other external id that's not in the input. The call is rejected (not silently stripped) if you do.
   - `kind` MUST be one of the six values above — no other strings.
   - `when` MUST match `HH:MM`.
   - `text` ≤ 200 characters.
   - ≤ 6 details per bucket. If the bucket has more than 6 distinct items, merge the lower-importance ones into combined details rather than dropping them.

4. **Call `mcp__klyne__record_reflection` once per (day, service) bucket** with:
   - `project_path` — same path you passed to `propose_reflection`.
   - `day` — the bucket's UTC `YYYY-MM-DD` string.
   - `body_json` — the typed payload: `{ "service": "<service>", "details": [...] }`. This is the canonical field; the tool will render a deterministic markdown companion for legacy readers.

   Do NOT also pass `insights` when you pass `body_json` — they are alternate paths; the typed path is preferred.

   Each call returns one persisted reflection id.

5. **Report back** the list of (day, service) buckets and reflection ids you wrote (e.g. "wrote 3 typed reflections: 2026-05-15 klyne, 2026-05-16 klyne, 2026-05-17 klyne"). The reflections will surface in the next `/klyne:bootstrap` brief, on the `/worklog` page, and as service cards on the productivity dashboard automatically.

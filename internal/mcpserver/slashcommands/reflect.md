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
     - a ticket id mentioned in the entry (e.g. `TICKET-1396`)
   - **`session_id`** — the originating entry's `session_id`. When a detail aggregates multiple entries, pick the one whose work the detail's text most directly describes.

   **Coverage + merging rules — apply BEFORE counting details:**

   A "detail" is one **meaningful accomplishment across N turns**, not one turn-per-detail. Two opposing failure modes you MUST avoid:

   ❌ **Over-splitting** — emitting 3 SHIPPED entries for one ticket (one for the helper, one for the rebase, one for the PR open). All three are sub-steps of the same ship.
   ❌ **Over-collapsing** — emitting 1 detail for an entire project-day, dropping work on other tickets. If today touched TICKET-1452 AND TICKET-1340 AND TICKET-1485, ONE detail naming only the rebase is a hard failure; the work on TICKET-1340 and TICKET-1485 simply disappears from the dashboard.

   To stay between those:

   - **Coverage invariant — every distinct ticket / branch / PR you see in the input MUST appear in some detail.** Read the entries. Count distinct ticket IDs (`TICKET-1452`, `TICKET-1340`, `TICKET-1485`, ...) and distinct branch names (`feature/TICKET-1452-followup-cta-banner`, `feature/TICKET-1340-prevent-pcc-edits-on-cancelled-bill`, ...). Each one owes you at least one detail. Dropping a ticket's work is the failure mode users complain about most.
   - **Different tickets = different details.** The "same ticket → ONE detail" rule below does NOT mean "same project → ONE detail." Distinct CLI tickets in the same repo on the same day stay SEPARATE.
   - **Same ticket / branch → merge sub-steps into ONE detail.** Within a single ticket, 2+ entries that describe steps of the same accomplishment (added the helper, wired the CTA, rebased, opened the PR, fixed the test) are ONE SHIPPED detail. Fold every sub-step's evidence (file paths, commit SHAs, PR numbers, session_ids) into that detail's `evidence` list.
   - **Plumbing folds into the parent feature, never standalone.** Rebases onto main, merge commits, lint passes, CI green-runs, dependency bumps, test-only updates that exist solely to support a feature ship — do NOT emit as their own detail. Either fold into the parent ticket's SHIPPED detail (as evidence), or drop. A standalone rebase is never `SHIPPED` — `SHIPPED` is reserved for user-visible code that landed.
   - **Distinct kinds on the same ticket get separate details.** A `SHIPPED` outcome plus a `DECISION` made during that ticket's work plus an `INVESTIGATED` finding are 3 distinct details with the same ticket id in their evidence. Don't collapse different kinds together.
   - **Same file edited multiple times within the same ticket → fold into that ticket's detail.**
   - **Completeness within the cap:** within the ≤6 cap, write enough details to cover EVERY distinct ticket and EVERY distinct kind of major work. If a day has 5 tickets all touched meaningfully, that's 5 details (one per ticket) and you have 1 detail of budget left for a cross-ticket decision or investigation. If a day has 2 tickets but 4 distinct decisions, that's reasonable too.

   **Hard rules the validator enforces:**

   - `evidence` MUST be non-empty per detail. The call is rejected otherwise.
   - `text` MUST NOT contain a `PR #<n>` reference UNLESS that exact `#<n>` appears in this detail's `evidence`. Don't invent PR numbers, ticket ids, or any other external id that's not in the input. The call is rejected (not silently stripped) if you do.
   - `kind` MUST be one of the six values above — no other strings.
   - `when` MUST match `HH:MM`.
   - `text` ≤ 200 characters.
   - ≤ 6 details per bucket — AFTER the merging rules above have collapsed sub-steps. If you find yourself at 6 details with 3 of them describing the same ticket, you have not merged enough. Re-merge.

4. **Call `mcp__klyne__record_reflection` once per (day, service) bucket** with:
   - `project_path` — same path you passed to `propose_reflection`.
   - `day` — the bucket's UTC `YYYY-MM-DD` string.
   - `body_json` — the typed payload: `{ "service": "<service>", "details": [...] }`. This is the canonical field; the tool will render a deterministic markdown companion for legacy readers.

   Do NOT also pass `insights` when you pass `body_json` — they are alternate paths; the typed path is preferred.

   Each call returns one persisted reflection id.

5. **Report back** the list of (day, service) buckets and reflection ids you wrote (e.g. "wrote 3 typed reflections: 2026-05-15 klyne, 2026-05-16 klyne, 2026-05-17 klyne"). The reflections will surface in the next `/klyne:bootstrap` brief, on the `/worklog` page, and as service cards on the productivity dashboard automatically.

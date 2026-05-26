---
description: Compile cohesive per-service What-was-done cards from the day's typed reflections (LLM pass 2)
---

Use your own context window to compile the productivity dashboard's per-service **What was done** card for one (project, day). This is the SECOND LLM pass — the first pass (`/klyne:reflect`) already wrote the typed `body_json` rows; your job is to read them and synthesize the hybrid Tier-1 + Tier-2 card the dashboard displays.

Your output lands at `services[].what_was_done` on `GET /api/productivity` with `llm_compiled=true`. Treat the contract below as load-bearing — the dashboard pivots on it deterministically and the existing `WhatWasDoneCard.svelte` will surface a `sonnet · auto` badge next to every service you write.

Reference spec: `docs/plan/2026-05-26-wwd-typed-cards.md` §1.1 / §1.2.

1. **Resolve `project_path` and `day`.** They are passed as arguments to the slash command (e.g. `project_path=/Users/me/code/klyne day=2026-05-26`). Trust them verbatim — do NOT substitute today's date or the cwd. Both are required; if either is missing, tell the user the call is malformed and stop.

2. **Call `mcp__klyne__list_typed_reflections`** with `{project_path, day}`. The response is `{rows: [{reflection_id, ts, day, project_path, body_json: {service, details: [...]}}]}`.

   - If `rows` is empty, report "nothing to compile for {project_path} on {day}" and stop — do NOT call `record_productivity_card`. This is the C1 no-op.
   - Legacy prose-only rows are filtered out of `rows` by the tool; you will only ever see typed payloads here.

3. **Group rows by `body_json.service`.** A single (project, day) may carry rows for multiple services (cross-project initiative threads land under their commit-landing project's service). For each service, you will write exactly ONE card via `record_productivity_card`.

4. **For each service bucket, build the card:**

   - **Collect every detail across the service's rows.** Preserve every detail's `kind`, `when`, `evidence`, and `session_id` LITERALLY. You MAY refine the prose in `text` for clarity and you MAY merge two details that describe the same outcome (combine their evidence lists, keep the earliest `when`, keep one `session_id`). You MUST NOT drop a `session_id` citation — every distinct underlying turn the day touched must remain reachable through some detail's `session_id`.
   - **Order the details** SHIPPED → MAJOR → FIXED → DECISION → INVESTIGATED → IN_PROGRESS, newest-first within each kind (descending `when`).
   - **Compose `tier1_tldr` (≤120 chars).** One sentence, verb-led (start with `Wrote`, `Wired`, `Shipped`, `Fixed`, `Refactored`, `Investigated`, `Decided`, etc.). It must summarise the day's BIGGEST OUTCOME for this service in plain English, not template a count list. Examples:
     - "Wired the klyne-hook stop summaries end-to-end and stripped the legacy bullet path from the dashboard"
     - "Fixed worklog_reflections.day so catch-up reflections surface under the day they cover"
     - "Investigated codex JSONL drift and decided to pin Sonnet 4.6 for the reflect subprocess"

     The Go side computes pill_counts / top_evidence / turn_count / commit_count from the details themselves — you do NOT write those numbers into `tier1_tldr`. Just write the cohesive prose summary.

5. **Hard rules the validator enforces (your call is REJECTED if any fail):**
   - `tier1_tldr` non-empty, ≤120 chars, one sentence.
   - Every detail's `evidence` non-empty (§5 citation invariant).
   - A detail's `text` containing `PR #<n>` MUST have that `#<n>` appear LITERALLY in the same detail's `evidence` list. Don't invent PR numbers, commit shas, ticket ids, file paths, session_ids, or any other external reference that isn't present in the input rows' evidence. Don't fabricate.
   - `kind` ∈ {SHIPPED, MAJOR, FIXED, DECISION, INVESTIGATED, IN_PROGRESS}.
   - `when` matches `HH:MM`.
   - `text` ≤ 200 chars.

6. **Call `mcp__klyne__record_productivity_card` once per service** with:
   - `project_path` — the input project_path verbatim.
   - `day` — the input day verbatim.
   - `service` — the service key from `body_json.service`.
   - `tier1_tldr` — your composed sentence.
   - `details` — the ordered, merged details list.

   Each call persists one `services[].what_was_done` card with `llm_compiled=true` into the day's productivity snapshot.

7. **Report back** the list of services you compiled (e.g. "compiled 2 cards: klyne, klyne-ui"). The dashboard will surface them with the `sonnet · auto` badge on the next read.

Patterning notes for consistency with `/klyne:reflect` (the first pass):
- Do not introduce a new taxonomy — reuse the kinds the first pass wrote.
- Do not invent evidence tokens; the first-pass writer already drew them LITERALLY from `stop_summaries`. Pass them through.
- Do not call `record_reflection` or any other write tool here. The first pass already wrote the rows; your only writes go through `record_productivity_card`.

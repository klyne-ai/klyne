---
description: Synthesize a narrative What-was-done card for one (project, day, service) from raw stop_summaries (LLM pass 2 — v2 narrative format)
---

Synthesize the **narrative "What was done" card** the productivity dashboard shows for one service on one day. The output is the user's standup digest — they read this when they reload the page. Quality of prose matters more than completeness; coverage of every distinct ticket matters more than chronological exhaustiveness.

Your output goes to `services[].what_was_done.narrative` on `GET /api/productivity`. The dashboard renders it as:

  - a service-level summary paragraph at the top
  - four stat tiles (features shipped / bugs fixed / decisions / investigated)
  - sectioned per-ticket cards (Features shipped → Bugs fixed → Decisions → Investigated · no fix landed)
  - an optional "open question for tomorrow" line

**Reference example of the target quality**: see the user's hand-edited card from 2026-05-26 — three SHIPPED tickets with 2-4 sentence narrative bodies, two BUG FIX cards under the same ticket (CLI-1452 shipped a banner AND had a dead-code revert), one INVESTIGATED with no fix landed, plus a service summary and a followup. Reproduce that prose quality.

## Steps

1. **Resolve args.** `project_path` and `day` are passed as slash-command args (e.g. `project_path=/Users/me/code/operations-app day=2026-05-26`). Both required. If either is missing, tell the user the call is malformed and stop.

2. **Call `mcp__klyne__list_stop_summaries_for_day`** with `{project_path, day, include_suppressed: true}`. The response is `{rows: [{session_id, ts, ts_local, cli, recap_visible, importance, recap_topic, last_user, last_bash, files, ai_drafted_summary}]}`. Use `ai_drafted_summary` as the primary source of truth — it's the per-turn prose the assistant emitted via the KLYNE_SUMMARY instruction. Fall back to `last_user` + `last_bash` + `files` for context when the summary is sparse.

   If `count` is 0, report "no stop_summaries for {project_path} on {day}" and stop — do NOT call `record_productivity_card`.

3. **Filter tool-only meta turns.** Skip any row whose `ai_drafted_summary` (lowercased) starts with one of:
   - `ran /klyne:`
   - `ran productivity-sync` / `ran the productivity-sync`
   - `compiled <N> productivity card`
   - `synthesized <N> typed reflection` / `synthesized one typed reflection`
   - `synthesized and persisted`
   - `called propose_reflection`

   Also skip rows whose summary contains `productivity dashboard json` or `productivity-sync second pass` — those describe tool runs, not real work.

4. **Identify the service.** Usually the basename of `project_path` (`/Users/me/operations-app` → `operations-app`). One narrative payload per (project, day, service). Most projects have one service; rare multi-service projects (cross-repo initiative threads) may need more than one call.

5. **Group the day's work by (ticket, outcome).** A "ticket" is a CLI-NNNN id appearing in `ai_drafted_summary` or `last_user`. The same ticket may produce MULTIPLE cards under different kinds — that's correct. Example: CLI-1452 with a SHIPPED card for the banner ship AND a FIXED card for the dead-code revert. Don't collapse different outcomes of the same ticket into one card.

   Classification rules (pick the strongest signal across all the ticket's turns):
   - **SHIPPED** — work culminated in a commit + push and/or a PR opened. The commit lands user-visible code.
   - **FIXED** — a bug or regression was resolved. The card's body must name the symptom AND the resolution.
   - **DECISION** — a process / architecture / library choice was recorded. Prefix the title with the choice itself.
   - **INVESTIGATED** — read-through / audit that produced understanding but no code landed. No SHIPPED card for the same ticket.
   - **MAJOR** — substantial body of in-progress work that doesn't fit SHIPPED (e.g. a multi-turn refactor that hasn't landed yet).
   - **IN_PROGRESS** — work explicitly carried forward.

   Untickatable work (an investigation that doesn't name a ticket id) gets a card with `ticket_id` omitted. Don't force a synthetic id.

6. **For each (ticket, kind) bucket, write ONE card with:**

   - **`kind`** — one of the six values above.

   - **`ticket_id`** — `CLI-NNNN` when present, omit otherwise.

   - **`title`** (≤120 chars, ≤160 hard cap) — **outcome-led**, NOT verb-led. Describe what the change IS, not what verb the user typed.
     - ✅ "OPD payment gate removed"
     - ✅ "Cancelled bill detection mid-edit"
     - ✅ "Follow-up CTA banner in CreateBookingModal"
     - ❌ "Shipped CLI-1473 fix" (too generic)
     - ❌ "Relaxed three isAdmin checks to canManage in PaymentStep.tsx" (this is the BODY, not the title)

   - **`body`** (200-600 chars, ≤1200 hard cap) — **narrative markdown prose**, 2-4 sentences. Tell the story:
     - **1 sentence**: what was broken / what was needed (the why).
     - **1-2 sentences**: what the change is, naming concrete files / functions / commits.
     - **1 sentence**: verification state (lint / tsc / N tests passing / PR # opened / pushed to branch).

     Use backticks for code identifiers inline (`useOrderV4`, `IAM_ACCESS`, `PaymentStep.tsx`). The dashboard renders markdown — `inline code` styles correctly.

     Don't write "shipped X" / "fixed Y" verb-led one-liners. Write paragraphs a teammate could read at standup.

   - **`refs`** (typed array, drawn LITERALLY from the source rows) — what the user can scan visually below the card. Use these types:
     - `{type: "file", text: "PaymentStep.tsx"}` — file basename from `files[]` or the summary
     - `{type: "branch", text: "feature/CLI-1473"}` — branch name from the summary
     - `{type: "pr", text: "PR #432"}` — PR ref from the summary
     - `{type: "commit", text: "23e6d8e5"}` — short commit SHA
     - `{type: "ticket", text: "CLI-1473"}` — only when the ticket id appears in body or title without already being in `ticket_id`
     - `{type: "test", text: "TestSessionEnd_…"}` — test name when surfaced
     - `{type: "session", text: "<session-uuid>"}` — the originating session id (back-pointer)

     Cap each card at ~5 refs. Drop noisy session ids when a ticket already has a PR/branch/commit ref.

7. **Write `service_summary`** (1-2 sentences, ≤400 chars). Capture the THEME of the day — the common thread across the cards. What was the user actually trying to accomplish? Examples:
   - "A productive day across three parallel worktrees. The main theme was plugging gaps blocking PCCs mid-flow — payment steps that silently dead-ended, bills that went stale while prescriptions were being edited, and a follow-up CTA never wired to the live modal."
   - "Three OMS-as-source-of-truth changes landed: discount engine moved off eVital push, /v4/test/* routes deleted, and a stale Jenkins assertion repaired."

   Don't just list the tickets — synthesize what the day was ABOUT.

8. **Optionally write `followup`** (1 sentence, ≤300 chars). Use only when the source data flagged an open question / edge case worth remembering. Example: "Open question for tomorrow — CLI-1452 PR #432 has a UX edge case: after clicking 'Set as Follow-up' the banner state and payment notice hide correctly, but no confirmation that the reason dropdown has visually updated. Worth a quick smoke test on prod data before merge."

   If nothing genuinely deserves a followup, omit the field.

9. **Call `mcp__klyne__record_productivity_card`** ONCE with:
   - `project_path` — verbatim from input
   - `day` — verbatim from input
   - `service` — the service key (typically basename of project_path)
   - `service_summary` — your 1-2 sentence theme paragraph
   - `cards` — the array you built
   - `followup` — when applicable

   The tool validates and persists with `llm_compiled=true`. Reject conditions you must avoid:
   - Any `body` or `title` that mentions `PR #<n>` whose `#<n>` doesn't appear in some `ref` with `type: "pr"`.
   - Any `ref.text` that wasn't drawn LITERALLY from the source rows (no inventing branch names, commit SHAs, file paths).
   - Body > 1200 chars or title > 160 chars.
   - Card count > 20.

10. **Report back** to the user: "Wrote {N} narrative cards for {service} on {day}: {ticket_a}, {ticket_b}, …". The dashboard will surface them on the next reload.

## Quality rubric (re-read before you write the JSON)

- Every distinct CLI-NNNN ticket touched today MUST appear in at least one card. If the proposer returned work on 5 tickets, you write at least 5 cards (more if some tickets had a SHIPPED + a FIXED outcome).
- Titles describe OUTCOMES, not verbs. A title like "Cancelled bill detection mid-edit" is what the user remembers; "Implemented CLI-1340 fix" is what the AI remembers.
- Bodies read like sentences in a paragraph, not bullet points. The reader is a teammate at standup who knows the codebase but not what you did today.
- Refs are typed because the UI styles each type — don't lump everything into a generic "evidence" list.
- The service_summary tells the day's STORY in one breath. If a colleague asked "what did you work on today?" — what would you say in 10 seconds?
- Tool-only turns NEVER become cards. Skip them at step 3.

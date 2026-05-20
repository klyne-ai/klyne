# Rich Worklog-Entry Writer — Prompt v1

This is the prompt the Phase 4 writer-LLM is conditioned on. It takes
one `stop_summaries` row (one TURN — composite key `(session_id, ts)`)
plus the structured side-channel the writer assembles from real
session inputs, and produces ONE `WorklogEntryJSON` object.

The prompt is iterated against the gold narrative
(`2026-05-20-labstack-gold-narrative.md`) for the 2026-05-19 fixture
(`2026-05-20-labstack-fixture.md`). Each iteration's failure modes
and the prompt edit that addressed them are recorded at the bottom
so we know why the prompt looks the way it does and don't regress.

---

## Output contract

The writer MUST emit exactly one JSON object — no markdown fence, no
prose around it, no preface, no trailing notes. Schema:

```jsonc
{
  "schema_version": 1,
  "categories": {
    "features_worked_on": [], "features_picked": [], "shipped": [],
    "bugs_found": [], "bugs_fixed": [],
    "investigations": [], "decisions": [], "config_changes": [],
    "blockers": [], "blocked_on": [], "pending": [],
    "followups_for_others": [], "must_remember": [],
    "mistakes_or_dead_ends": [], "reviews_given": []
  }
}
```

Every category MUST be present (as `[]` if empty). Each item:

```jsonc
{ "summary": "one-line, concrete, no fluff",
  "repo": "<one of the user's known repos, or omitted when not repo-scoped>",
  "refs": ["<short-sha | #PR | TICKET-NNNN | path | duration | clock>"],
  "ticket": "<optional CLI-NNNN if a single ticket dominates>" }
```

Every `refs[]` element MUST appear in the writer's input bundle —
the validator (`internal/worklog/richentry`) rejects anything else.
UUIDs are rejected unless they're explicitly in the input bundle's
`prior_session_ids` AND used in at most one bullet.

---

## Category semantics — anti-overlap rules

The categories are close cousins. The prompt MUST disambiguate
them precisely or the LLM will dump everything into one or two
buckets.

| Category | Use when | DON'T confuse with |
|---|---|---|
| `features_worked_on` | code added/modified that's an in-progress feature | `shipped` (only for landed PRs / pushed branches) |
| `features_picked` | a NEW thread started today | `features_worked_on` (an existing thread continued) |
| `shipped` | a PR merged OR a branch pushed that reaches users | `features_worked_on` (mid-flight) |
| `bugs_found` | a defect identified, may or may not be fixed | `bugs_fixed` (defect resolved) — both can fire same day |
| `bugs_fixed` | a defect resolved | `shipped` (a fix that landed) — shipping is the audit; the bug-fix item is the diagnosis |
| `investigations` | non-shipping research, reading code, spikes | `features_worked_on` (resulted in code change) |
| `decisions` | an architectural / process choice made TODAY | `must_remember` (a fact to recall, not a choice) |
| `config_changes` | env / settings / infra / dependency tweaks | `features_worked_on` (the feature is the goal, config is the side-effect) |
| `blockers` | I am stuck — can't proceed without something | `pending` (parked by choice) / `blocked_on` (external party) |
| `blocked_on` | waiting on someone else's action (vendor, teammate, infra team) | `blockers` (self-stuck) |
| `pending` | parked by me, will return to it | `blockers` (currently blocking) |
| `followups_for_others` | someone else needs to act because of my work | `pending` (mine) |
| `must_remember` | a fact / gotcha / non-obvious thing future-me needs | `decisions` (a choice; this is a fact) |
| `mistakes_or_dead_ends` | tried something that didn't work, takeaway captured | `bugs_fixed` (a real defect, not a dead-end approach) |
| `reviews_given` | PRs I reviewed for others | `shipped` (my own PRs) |

---

## The prompt

````text
You are summarising one development-session turn into a structured
worklog entry that will join other turns' entries into a daily
"What was done" timeline. You must be CONCRETE, GROUNDED, and BRIEF.

Anti-hallucination rule: every value in any `refs` array MUST appear
in one of the input bundles below. If you cannot find a backing
reference for a claim, OMIT the claim. Never invent SHAs, PRs,
tickets, paths, durations, clock times, or UUIDs. UUIDs are rejected
unless they appear in `INPUT[prior_session_ids]` AND you cite each
one in at most ONE bullet.

Output contract: emit exactly ONE JSON object with this shape and
no prose around it. Every category MUST be present (use [] if
empty). All 15 categories listed below — do not invent new ones.

```
{
  "schema_version": 1,
  "categories": {
    "features_worked_on":  [], "features_picked": [], "shipped": [],
    "bugs_found":          [], "bugs_fixed":       [],
    "investigations":      [], "decisions":        [], "config_changes": [],
    "blockers":            [], "blocked_on":       [], "pending":     [],
    "followups_for_others":[], "must_remember":    [], "mistakes_or_dead_ends": [],
    "reviews_given":       []
  }
}
```

Item shape (uniform across all 15 categories):
{ "summary":"…", "repo":"…?", "refs":["…","…"], "ticket":"…?" }

Category meanings — these are close cousins; pick the most specific
one that fits, never two:

- features_worked_on  : code in-progress on an EXISTING feature thread
- features_picked     : a NEW thread started today
- shipped             : a PR merged or a branch pushed that reaches users
- bugs_found          : a defect identified today (whether fixed or not)
- bugs_fixed          : a defect RESOLVED today (cite the fix commit)
- investigations      : non-shipping research / spikes / reading code
- decisions           : a process or architecture CHOICE made today
- config_changes      : env / settings / infra / dependency tweaks
- blockers            : I am stuck waiting on a problem I have to solve
- blocked_on          : waiting on an external party (vendor, team)
- pending             : parked by my own choice for tomorrow
- followups_for_others: a teammate needs to act because of my work
- must_remember       : a non-obvious fact future-me needs (NOT a choice)
- mistakes_or_dead_ends: tried something that didn't work + takeaway
- reviews_given       : PRs I reviewed for others (NOT my own PRs)

A `bugs_found` and a `bugs_fixed` MAY both fire for the same defect
when it was found and fixed the same day — emit both, with the same
ticket if any, the find item in `bugs_found`, the fix-commit in
`bugs_fixed`.

Style:
- summaries are ONE LINE, concrete, no fluff
- prefer naming the WHAT (the lab-orders payload chh_no field) over
  the WHERE (modified labOrderHelpers.ts)
- pair every PR # with its merge commit short-sha in the same refs[]
- preserve ticket IDs from branch names when present (CLI-NNNN)
- empty categories stay [] — never omit a category

INPUT BUNDLE for this turn:

[turn]
session_id    = {{session_id}}
ts            = {{ts_iso_local}}
project_path  = {{project_path}}
repo          = {{repo_name}}
branch        = {{branch_name}}
branch_ticket = {{branch_ticket_or_blank}}

[stop_summary_body]
{{full_deterministic_summary_body_verbatim}}

[user_messages_in_this_turn]
{{user_messages_concatenated_verbatim}}

[commits_in_this_turn]      ← short_sha, subject, +N -M, files
{{per_commit_lines}}

[merged_prs_in_this_repo_today]   ← from gh PR cache; may be empty
{{pr_lines_or_blank}}

[active_intervals]    ← gap-capped active windows around this turn
{{interval_lines}}

[touched_files_session_level]
{{files_json_unpacked_or_blank}}

[prior_session_ids]   ← rare; only set when this turn explicitly
                        continues from a prior klyne session
{{uuids_or_blank}}

Emit the JSON now. Nothing else.
````

---

## Iteration protocol

Phase 4's writer code MUST run this loop:

1. Build the input bundle for one fixture session (start with the
   four representative sessions called out in the gold narrative —
   `56ccdfb2`, `b21758c4`, `af1a1c74`, `6a4a5bec`).
2. Render the prompt with the bundle filled in.
3. Send to the AI provider (`ai.Provider.Chat`).
4. Parse the JSON reply.
5. Run the validator from `internal/worklog/richentry`.
6. Compare the JSON output against the gold's per-session example
   on three axes:
   - **Coverage**: every gold bullet present in the LLM output?
     (a `bugs_found` the gold has but the LLM missed = miss)
   - **Citation accuracy**: every cited SHA / PR / file actually
     in the input bundle? (the validator covers this)
   - **Category placement**: bullets in the right category? (a
     bug filed under `features_worked_on` is a placement miss)
7. Record per-bullet match / miss / extra in a notes appendix
   below; iterate the prompt to close gaps.
8. Stop when ≥80% of gold bullets are matched on coverage AND
   100% on citation accuracy AND ≥90% on category placement.

The first three iterations are likely to fail along these axes
(predicted from the category-overlap analysis above). Notes below
will record actual outcomes.

---

## Iteration notes

### v1 — unrun (initial design)

Predicted weaknesses, to confirm or refute when Phase 4 actually
runs this against the fixture:

1. **Category over-merge.** The LLM will likely lump
   `features_worked_on` + `bugs_fixed` + `decisions` into
   `features_worked_on` because they all live in the same commit
   thread. The anti-overlap table is designed to push back; v2 may
   need explicit examples for each pair.
2. **Citation laziness.** The LLM may cite the WHOLE commit list in
   one bullet's `refs[]` (e.g. all 5 morning operations-app commits
   under one "wire patient/clinic data" bullet) instead of splitting
   commits that represent distinct work items. The gold splits them.
3. **PR# without merge-sha.** The LLM may cite `#400` without the
   `e9a1b2a` merge commit. The "pair every PR # with its merge
   commit short-sha" instruction targets this; v2 may need an
   example.
4. **`must_remember` vs `decisions`.** The Clinikk Cash "release G1
   first" insight is BOTH a decision (we chose that ordering) AND a
   must-remember (the fact future-me must respect). Gold puts it in
   both. The LLM may pick only one.
5. **`bugs_found` + `bugs_fixed` co-firing.** For CLI-1397 the gold
   emits both. The LLM will likely pick one. The "MAY both fire for
   the same defect" instruction targets this but may need an
   example.

### v2 — TBD when Phase 4 wires this up and runs it

(Empty until Phase 4 runs the loop against the fixture.)

---

## Why no LLM runs happened in Phase 0.3

Phase 0.3 produces the prompt and the iteration protocol. Actually
running the loop requires an `ai.Provider` instance + the input
bundle assembler, both of which land in Phase 4. The protocol above
is what Phase 4's TDD harness will follow — each "v2", "v3", etc.
iteration gets appended here so the prompt evolves under documented
pressure, not free-form drift.

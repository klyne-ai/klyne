# Worklog "What was done" Narrative — Redesign Research

**Date:** 2026-05-20
**Status:** Research — read-only investigation. No code changed. Awaits user review before any implementation plan.
**Trigger:** the 2026-05-19 daily reflection rendered on the productivity dashboard as 4 high-level topic-summary bullets, each cited with the same UUID `e4696ed8-c2e9-416e-9a2d-a6e4860e649d`. The day's actual work (operations-app/Labstack property additions for phone / lab test name / lab name, WebEngage event work, billing-PR + PRA issues end-of-day, the refund-bug discovery in `cancel-event`, the report-from-event miss against the health vault + Maddox API) appeared nowhere.

---

## 1. Current state — code map

The "What was done" narrative is `Service.ReflectionMarkdown` (or `Report.ReflectionMarkdown` at the top level) — i.e. the persisted `worklog_reflections.body_md` for that project + day. The dashboard does not generate it; it only renders what `record_reflection` wrote.

| Stage | File:line | Role |
|---|---|---|
| Slash command → host prompt | `internal/mcpserver/slashcommands/reflect.md:1-26` | Tells Claude to bucket pending entries by UTC day, draft "2–4 insights" per bucket, "Every insight MUST cite at least one `session_id` from that day's entries as evidence" |
| Proposer (loads pending entries) | `internal/worklog/reflection_proposer.go:41-87` (`LoadPendingEntries`) | Pure SQLite read: returns `stop_summaries` rows since the last reflection for the project. Each row carries `session_id`, `cli`, `recap_topic`, `ai_drafted_summary`, `last_user`, `ts`, `importance`. **No commits, no diffs, no per-message granularity.** |
| Proposer MCP tool | `internal/mcpserver/tool_propose_reflection.go:39-68` (`handleProposeReflection`) | Calls `LoadPendingEntries`, formats `formatProposeReflectionMD` (one line per entry — `id=<session> [cli] importance=<n> topic=<…> — <ai_drafted_summary truncated to 200 runes>`), then appends `worklog.ProposalGitBrief` (`reflection_substrate.go:122-128`) which is `SalienceRankedFacts(rep, project) + "\n\n" + GroundingContractText()` |
| Proposer entry render | `internal/mcpserver/tool_propose_reflection.go:73-100` (`formatProposeReflectionMD`) | Truncates each AI-drafted summary at 200 chars (`proposeReflectionEntryBody = 21:const`). This is where the per-session granularity is collapsed. |
| Substrate (git brief shown to LLM) | `internal/worklog/reflection_substrate.go:122-128` (`ProposalGitBrief`), `:166-196` (`SalienceRankedFacts`), `:137-153` (`GroundingContractText`) | Ranks branches by commit count then net-new lines; emits per-branch lines + per-commit lines (`subject (short-sha) +N/-M on YYYY-MM-DD`) |
| Substrate build | `internal/worklog/reflection_substrate_build.go:30-76` (`BuildProjectSubstrate`) | Runs `productivity.ScanRepo` over the canonical repo + every worktree for `[since, until]`. The proposer calls this with a 7-day window (`tool_propose_reflection.go:54-56`); the recorder with the calendar-day window (`tool_record_reflection.go:55-60`). |
| Recorder (writes body_md) | `internal/worklog/reflection_recorder.go:78-147` (`recordReflection`) | **This is where `body_md` is structurally assembled.** Loops over the LLM-authored `Insight{Text, Evidence}`; for each insight writes a literal `"- " + sanitized_text + " (evidence: " + strings.Join(ins.Evidence, ", ") + ")\n"` (`:112`). Then appends `GitSubstrateSections` (open-loops, shipped-ledger, cross-project thread). |
| Recorder MCP tool | `internal/mcpserver/tool_record_reflection.go:39-66` (`handleRecordReflection`) | Builds the day-window substrate, calls `RecordReflectionWithSubstrate` |
| PR-ref guard | `internal/worklog/reflection_substrate.go:32-69` (`prRefRe`, `SanitizeArtifactIDs`) | Regex on `(?i)\bPR\s*#\s*(\d+)`; strips refs whose number is not in the evidence allowlist (commit subjects + branch names + ticket id + short SHAs from `gitEvidenceFor`, `reflection_recorder.go:149-172`) |
| Layer-1 deterministic narrative (per-branch, **not** "What was done") | `internal/productivity/report.go:398-446` (`layer1Narrative`) | Per-branch one-liner the dashboard already builds; structurally constrained — only interpolates `b.Name`, `b.TicketID`, `b.Ship`, real `c.Subject`, real `c.SHA`, real minutes |
| UI consumer | `ui/src/lib/components/productivity/DaySummary.svelte:1-110` | Renders `report.reflection_markdown` through `renderMarkdown` (marked + DOMPurify) under the header "What was done" |
| Top-level body_md selection | `internal/productivity/report.go:154-174` | The dashboard's overall `reflection_markdown` is the first non-empty per-Service `body_md` (literally the value of `worklog_reflections.body_md` for that project + day, read via `refl.HasReflection`) |

The "What was done" panel surfaces the LLM-authored insight bullets verbatim, with the `(evidence: …)` suffix appended by the recorder, plus three deterministic appended sections (open loops, shipped ledger, cross-project thread). Nothing else.

---

## 2. The hallucination — root cause

`e4696ed8-c2e9-416e-9a2d-a6e4860e649d` is a Claude Code session UUID — the value of `stop_summaries.session_id`, surfaced as `PendingEntry.SessionID` (`reflection_proposer.go:24`) and listed in the proposer markdown at `tool_propose_reflection.go:92-97`:

```
- id=%s [%s] importance=%d topic=%q — %s\n
```

The slash command (`slashcommands/reflect.md:14-19`) then tells the host:

> Every insight MUST cite at least one `session_id` from that day's entries as evidence — this is the citation invariant the system enforces.

The recorder writes the evidence array literally into the markdown (`reflection_recorder.go:112`):

```go
body.WriteString(fmt.Sprintf("- %s (evidence: %s)\n", text, strings.Join(ins.Evidence, ", ")))
```

**The UUID is therefore NOT fabricated by the LLM** — it is a real `session_id` for a session that occurred on 2026-05-19. The pathology is something different and arguably worse: **the host correctly satisfied the citation invariant by reusing one session_id under every bullet.** Several failure modes compound:

1. **The citation invariant is too weak.** It enforces only "at least one session_id". It does not require that each bullet cite a distinct or representative session, nor that the citation be the session whose work is being summarized. A model that bucketed all entries together is rewarded for citing one bucket-wide id.
2. **The substrate inputs encourage abstraction, not chronology.** The proposer markdown gives the host short topic summaries (`ai_drafted_summary` truncated at 200 runes, `tool_propose_reflection.go:21`), not the full transcript, not the messages, not the file paths the user worked on. The host has no per-event timeline to narrate — it only has a topic list. So it writes topic-level bullets.
3. **The anti-hallucination guard is shape-specific to the original failure.** `SanitizeArtifactIDs` (`reflection_substrate.go:35-69`) only matches `(?i)\bPR\s*#\s*(\d+)`. A UUID-shaped citation, a fabricated short SHA, a misattributed ticket id — none are caught. The dashboard prototype results doc (`docs/superpowers/plans/PROTOTYPE-RESULTS.md:25`) confirms the test it passes is literally "zero `\"PR #\"` substrings" — that is a regex check, not a citation-validity check.
4. **Cited session_ids are never re-validated against the day's pending entries.** Once the LLM emits a `session_id`, the recorder simply concatenates it. No check that it appears in `LoadPendingEntries` output for that bucket. (The repeated UUID would survive even if it were entirely fabricated, because `Insight.Evidence` is only screened by length > 0 — `reflection_recorder.go:102-104`.)
5. **The salience gate inverts the wrong axis.** `SalienceRankedFacts` ranks branches by commit count then net-new lines (`reflection_substrate.go:352-362`). Per the spec this fixed the "docs-cleanup-foregrounded-over-CLI-1396" failure — but for 2026-05-19 the labstack work was already pushed, generating a substrate that legitimately frontloaded "Labstack integration was the dominant thread". The bullets are not lying about the day's *aggregate*. They are *omitting* the day's *progression*.

**The root-cause sentence:** the prompt asks for thematic insights, gives only thematic inputs, validates only that one UUID per bullet exists in the LLM's output, and the recorder concatenates the LLM's word "evidence" tag verbatim into markdown — so when the host writes 4 topic bullets and reuses one session_id under all of them, the system happily renders `(evidence: <same UUID>)` four times. No part of the pipeline tells it to write a chronological day, ground each claim in a commit, or vary citations across bullets.

---

## 3. What the spec says about anti-hallucination

`docs/superpowers/specs/2026-05-19-ai-productivity-dashboard-design.md`:

**§1 (line 21)** — motivating failure:

> klyne's existing worklog reflections inverted salience (foregrounded a docs cleanup while the flagship CLI-1396 pipeline — 9 commits, committed locally but **never pushed** — was absent from all reflections) and **hallucinated a non-existent "PR #57"**. The dashboard's data model is designed specifically to make those failure modes structurally impossible.

**§7.1 Anti-Hallucination Grounding Contract** (lines 206-212):

> - **Determinism boundary:** repo discovery, commit/branch/ship-state facts, time math, state machine, identity filter, and all numbers are deterministic and never authored by the LLM. The LLM only writes prose and receives the facts as fixed inputs.
> - **Input allowlist:** `{commit: sha, subject, files_changed, ts}[]`, branch name, klyne worklog/stop-summary text for that project+window, the deterministic time figure + ship state. Nothing else.
> - **Output rules:** (1) every factual claim traceable to an allowlisted input, **cite short sha(s)**; (2) **never** emit a PR/ticket/external ID unless it literally appears in the branch name or a commit message; (3) unknowns stated as unknown; (4) time/state quoted verbatim from the deterministic layer; (5) **salience rule** — lead with the highest-code-impact work (commit count / net-new lines), not chronological or trivial work.

**§9 Success Criteria (line 252):**

> AI narrative cites commit shas and contains **no** invented PR/ticket numbers (regression guard against "PR #57").

**The gap between spec and implementation:**

- Output rule (1) says "cite short sha(s)". The implementation **does not enforce this** — citations are session_ids by the slash command's instruction.
- The input allowlist explicitly names `commit: sha, subject, files_changed, ts`. The proposer markdown (`tool_propose_reflection.go:81-97`) lists pending entries by session_id + topic + truncated body — **not by commit**. `SalienceRankedFacts` is appended but as a *separate* "git brief" section; it is not the primary input the LLM is told to narrate.
- The salience rule (5) says "lead with the highest-code-impact work, not chronological". The user is asking for the opposite — chronological — for the *user-facing* narrative. The spec's rule was written to fix the *inverted-salience* failure (docs over CLI-1396); it isn't an absolute against chronology, it's a "don't bury the lede". **The user's complaint suggests a tension between "lead with impact" and "show me the day" that the spec did not address.**
- The PR #57 regression guard is implemented as a regex on `PR #<n>` (`reflection_substrate.go:35`). It is **not** a general "every cited id must appear in the allowlist" guard, which is what §7.1(1) actually requires.

**`internal/worklog/reflection_substrate_test.go:261`** explicitly comments:

```go
// "PR #57" hallucination — it must be stripped/flagged.
```

— confirming the shape-specific check is what is implemented and tested.

---

## 4. Grounding data already available

The substrate is rich; the prompt under-uses it.

`productivity.Report` for one project+day (`internal/productivity/types.go:128-227`) carries, **per Service**:

- `Branches[]` with full `Commits[]` — each commit has `SHA` (full, short-sha is `SHA[:7]` typically), `Subject`, `Author`, `AuthorEmail`, `CommittedAt`, `Files`, `Insertions`, `Deletions`, `IsUser`, `BranchName`. **The commit subjects ARE the day's chronology** — they are the closest thing in the substrate to the granular "what I did today" the user wants.
- `Branch.Ship` (3-state), `Ahead`, `Behind`, `AttributedMinutes`, `Narrative` (the deterministic L1 one-liner), `FirstCommitAt`, `LastCommitAt`, `ShipSpanMinutes`.
- `Risks[]` with `Kind` (`unpushed` / `done-uncommitted`), `Detail`, `AgeMinutes`, `Branch`, `WorktreePath`, `Commits[] RiskCommit{SHA, Subject}`, `Files[]`.
- `MergedPRs[]` from `gh` (number, title, head_ref, merged_at, opened_at, time_to_ship_minutes).
- `MinutesByCLI`.

`Report.Sessions[]` (`SessionStat`, `types.go:186-195`) gives **per-session active intervals** for the day — `SessionID`, `CLI`, `Repo`, `StartedAt`, `EndedAt`, `ActiveMinutes`, `ActiveIntervals[]`, `MessageCount`. This is the timeline backbone.

The `stop_summaries` table (read by the proposer) has, per session:
- `recap_topic`
- `ai_drafted_summary` — **the full text is in SQLite; the proposer truncates it to 200 runes at `tool_propose_reflection.go:21`**
- `last_user`
- `importance`
- `ts`

**What the LLM currently sees (the input it actually narrates from):**
1. A list of `id=<session> [cli] importance=<n> topic=<…> — <200-rune summary>` rows.
2. The salience-ranked git brief: per branch one line, per commit one line (`subject (sha) +I/-D on date`).
3. The §7.1 grounding contract as plain prose.

**What is structurally available but NOT in the prompt today:**
- The full `ai_drafted_summary` per session (truncation strips ~80% of the content).
- The per-session `ActiveIntervals` — the LLM does not see WHEN in the day a session ran or how it overlaps with commits.
- `Risks[]` commits/files — the LLM is told there are unpushed commits via the appended deterministic block, but those commits are not in its input allowlist for the bullets themselves.
- `MergedPRs[]` — completely absent from the prompt.
- The per-branch `Branch.Narrative` (L1 deterministic one-liner) — the dashboard renders it, but the reflection LLM doesn't see it.
- Per-commit file paths (the substrate scan stops at counts — `Files`/`Insertions`/`Deletions`). The user's concrete grievance ("added properties for phone number / lab test name / lab name") is at the *file/diff* level which the substrate does not currently surface to the LLM.

**The bottleneck:** the LLM is being asked to write "What I did today" but is given (a) topic-summary bullets, not transcripts, and (b) a sha-list ranked by impact, not a timeline. The two inputs both push it toward synthesis (4 thematic bullets) and away from chronology (a per-commit / per-event narrative).

---

## 5. Prior labstack-commits fetch (if found)

**Searched, not found.** `grep -rln "labstack\|Labstack\|LabStack\|CLI-1325" docs/` returns only:
- `docs/superpowers/plans/PROTOTYPE-RESULTS.md` (mentions the labstack branches in the verification section but does not enumerate commits)
- `docs/superpowers/specs/2026-05-19-ai-productivity-dashboard-design.md` (mentions CLI-1396 — the *previous* labstack-related ticket — as a motivating failure)

No standalone "labstack commits across the 5 repos" doc exists under `docs/superpowers/` or `docs/research/`. **Recommendation:** before implementing the redesign, fetch the labstack-tagged commits from the user's 5 service repos for a representative window (last 7 days covers 2026-05-19) and save them under `docs/superpowers/specs/2026-05-20-labstack-commits-window.md` as the canonical test fixture for the new narrative. The substrate already knows how to find them (`productivity.ScanRepo` per worktree); a one-off Go harness or a `git log --author --since --until --numstat` per repo is sufficient. The user explicitly said "if not then just again do the fetching of the same thing and add it first in the doc and then observe it" — that is the path.

(The dashboard's existing read-only verify harness at `internal/api/handlers/zzz_verifyreal_test.go` — mentioned in PROTOTYPE-RESULTS.md:14 as "intentionally not committed/deleted; recreate it to dump `/tmp/prodreport.json`" — can serve as the template.)

---

## 6. Proposed redesign

### 6a. Output shape

The narrative becomes a **chronological day timeline grouped by repo**, with thematic synthesis demoted to a closing one-line "themes" footer.

Concrete shape per day:

```
## 2026-05-19

### operations-app — feature/CLI-1325-labstack-integration  (pushed, 104 min)

08:42  feat(labstack): add phone-number property to lab-order payload   (a1b2c3d)
        +24/-3 on src/labstack/order.ts
09:15  feat(labstack): add lab-test-name and lab-name to order schema   (b2c3d4e)
        +47/-8 on src/labstack/order.ts, src/labstack/types.ts
10:30  chore(webengage): plumb new lab fields into LabOrderPlaced event  (c3d4e5f)
        +18/-2 on src/integrations/webengage/events.ts

### oms-service — feat/CLI-1397-labstack-auto-refund  (pushed, 207 min)

…

### Open at end of day
- billing-PR  (uncommitted, operations-app:src/billing/)  — work continued from 17:10 session
- refund bug found in cancel-event flow — cancel does not refund when paid via "payment"
- report-from-event bug — missed saving report to health vault + missed Maddox API call

### Themes
Labstack-data plumbing dominated the day (5 commits across 3 repos); end-of-day shifted to billing/PRA reconciliation where two bugs surfaced (refund-on-cancel, report-vault save).
```

Required per timeline entry:
- Commit time (HH:MM, the user's local TZ).
- Repo + branch (or `(no branch — uncommitted)`).
- Commit subject (verbatim — never rewritten).
- **Short SHA in parens (the citation).**
- A second line with `+I/-D on <up-to-N file paths>` from the substrate.

Stop-summary content (the host's narrative ability) is used **only to**:
- Fill the "Open at end of day" block (bugs found, work-in-progress at session end — explicitly things the substrate cannot see in commits).
- Write the one-line "Themes" footer.

### 6b. Grounding contract

Rewrite the contract from "PR-ref-shaped guard" to "**every cited id must be in the allowlist, period.**"

1. **Allowlist (deterministic, computed before the LLM is called):**
   - `commit_shas`: full + short forms of every `IsUser` commit in window.
   - `branch_names`: every `Branch.Name` in the report.
   - `ticket_ids`: every `Branch.TicketID` (deterministically parsed from branch name — D3).
   - `pr_numbers`: every `MergedPR.Number` (string-formatted) for the window.
   - `session_ids`: every contributing session_id from `Report.Sessions[]`.

2. **Token-level post-generation validation (rejects, not just strips):**
   - Any token matching `^[0-9a-f]{6,40}$` (hex SHA-shaped) MUST be in `commit_shas`. Otherwise: reject the whole reflection, return error to the host.
   - Any token matching `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$` (UUID) MUST be in `session_ids` AND must not appear in more than one bullet unless that session truly spans multiple repos (cross-check `Report.Sessions[].Repo`).
   - Any `PR #<n>` MUST be in `pr_numbers` (replaces today's commit-message check; merged PRs are now in the substrate, so we have a real allowlist).
   - Any `CLI-<n>` / `FEAT-<n>` / generic-ticket-shape MUST be in `ticket_ids`.

3. **Citation positioning:**
   - Citations belong to timeline entries (one short-SHA per commit line). **No `(evidence: …)` trailers on synthesized themes.** The "Themes" footer is allowed to be uncited prose since it is honestly a synthesis, not a claim — but it must not introduce any new ids.

4. **Time figures verbatim:** the LLM is forbidden from emitting any minute/hour number that is not in the substrate (already a §7.1 rule; restate).

5. **File paths come from the substrate, never the model:** today's substrate stops at counts; the redesign must extend the scan to include `git log --numstat --name-only` per commit and surface the changed paths to the LLM as an allowlist. The LLM may name paths only from this list.

### 6c. Prompt template sketch

```
You are drafting one day's worklog reflection for {project} on {day} (UTC).

GROUND TRUTH (you MAY only reference items in these lists):
  commit_shas:    [a1b2c3d, b2c3d4e, c3d4e5f, …]
  branch_names:   [feature/CLI-1325-labstack-integration, …]
  ticket_ids:     [CLI-1325, CLI-1397, …]
  pr_numbers:     [49]
  session_ids:    [e4696ed8-…, f5a7b9c1-…]
  changed_paths:  [src/labstack/order.ts, src/labstack/types.ts, …]

TIMELINE (deterministic facts — quote times and SHAs verbatim):
  08:42  feat(labstack): add phone-number property …  (a1b2c3d) +24/-3 on src/labstack/order.ts
  09:15  feat(labstack): add lab-test-name and lab-name to order schema  (b2c3d4e) +47/-8 on src/labstack/order.ts, src/labstack/types.ts
  …

SESSION STREAM (verbatim full ai_drafted_summary per session, ordered by ts):
  [11:30, claude, session=e4696ed8-…]
    <full ai_drafted_summary — no truncation>
  [17:10, claude, session=f5a7b9c1-…]
    <full ai_drafted_summary>

OPEN LOOPS (deterministic from substrate Risks):
  - operations-app: 3 uncommitted files in src/billing/ since 17:42

WRITE:
  1. A chronological timeline grouped by repo. One bullet per commit, the bullet
     IS the verbatim subject + the short SHA in parens. No editorializing.
  2. An "Open at end of day" block: bugs found and work-in-progress that have
     NO commit (sourced from session_stream prose).
  3. A one-line "Themes" footer.

RULES:
  - You may not invent a SHA, UUID, PR number, ticket id, branch name, file path,
    or time. Any output token of those shapes will be checked against the
    allowlists above; an unknown token fails the whole reflection.
  - Citations belong to commit lines (short SHA in parens). Do not append
    "(evidence: …)" anywhere.
  - "Open at end of day" entries cite the session_id of the session in which
    they were discussed, exactly once per entry.
  - Quote times and ship state verbatim.
```

The slash command (`reflect.md`) becomes the source of these instructions; the proposer markdown becomes the timeline-formatted bundle (commits + full session summaries), not the topic-list it is today.

### 6d. Anti-hallucination guards

Implement as a single `ValidateReflection(body string, allow Allowlist) (cleaned string, []error)` function in `internal/worklog/reflection_substrate.go`, replacing `SanitizeArtifactIDs`. The new function runs **after** the recorder has the LLM's draft body and **before** persistence.

Token classes and actions:

| Shape | Regex | Action on mismatch |
|---|---|---|
| Short SHA | `\b[0-9a-f]{7,12}\b` | If not in `commit_shas`, reject reflection (hard fail — host re-drafts) |
| UUID | `\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b` | If not in `session_ids`, reject. If appears > N times where N = sessions in the day's bucket, reject. |
| PR ref | `(?i)\bPR\s*#\s*(\d+)` | If number not in `pr_numbers`, reject (today: stripped to `[unverified PR ref removed]`) |
| Ticket | `\b[A-Z]{2,5}-\d{1,5}\b` | If not in `ticket_ids`, reject |
| File path | heuristic — any `[./][\w/.-]+\.\w+` | If not in `changed_paths`, reject (path inventions are a real failure mode) |
| Minute/hour | `\b\d+\s*(?:m|min|h|hr)\b` | If not in `allowed_durations`, reject |

The behaviour difference vs. today:
- **Today:** silent strip of one shape (`PR #n`) only.
- **Proposed:** hard reject across six shapes; recorder returns a structured error listing the offending tokens so the host can re-draft instead of silently persisting bad markdown.

Additionally:
- **Distinct-citation invariant:** for the chronological timeline section, each commit bullet must cite the SHA of that commit (positional check — the parenthesised SHA must equal the substrate's SHA for the commit being narrated, matched by sequence). For "Open at end of day" entries, the cited session_id must be distinct per entry unless the substrate confirms a session spans multiple findings.
- **No-`(evidence: ...)` invariant:** the recorder no longer appends the literal `(evidence: <ids>)` trailer (`reflection_recorder.go:112`). Evidence becomes a structural field of the persisted reflection (already present — `EvidenceEntryIDs`), not a rendered markdown suffix. This kills the most visible class of "same UUID under every bullet" surface.

---

## 7. Open questions / decisions for the user

1. **Chronological vs. salience-first.** Spec §7.1 rule 5 mandates "lead with highest-code-impact work". The user wants chronological. **Decision:** chronological *within* repo, but repos ordered by impact? Or pure clock order across repos? Or two modes (toggle)?
2. **Per-session prose ingestion: full text vs. capped.** The current 200-rune cap (`proposeReflectionEntryBody`) collapses the user's actual day. Lifting it could exceed the host's prompt budget on heavy days. **Decision:** raise to 2000? Stream per-session in chunks? Drop `last_user`?
3. **File-path surfacing requires substrate change.** `productivity.ScanRepo` does not currently return changed paths per commit (only counts — `Files`/`Insertions`/`Deletions`). Surfacing per-path requires extending the scan and re-running it for every reflection draft. **Decision:** accept the substrate cost, or keep paths out of the timeline (subjects only)?
4. **Reject vs. strip.** Today the PR-ref guard silently strips. The proposal moves to hard reject + retry. Hard reject means a day's reflection can fail to persist; the host gets the error and can re-prompt. **Decision:** OK to require a retry loop in the slash command, or do we prefer silent strip-then-flag (current behaviour) at the cost of accepting bad markdown?
5. **What lives outside commits (bugs found, blockers).** The user's 2026-05-19 examples — refund bug in cancel-event, report-vault save miss — have NO commit. They live only in session prose. **Decision:** is the "Open at end of day" section purely LLM-authored from session prose (with session_id cites), or do we want a deterministic "extract `BUG:`/`TODO:` markers from session messages" pass?
6. **Run the labstack-commits fetch first?** §5 — no prior doc exists. Recommendation: fetch and save `docs/superpowers/specs/2026-05-20-labstack-commits-window.md` as the canonical fixture, then iterate on prompt design against that fixture before touching code. **Decision:** confirm we are doing the fetch as the first concrete step.

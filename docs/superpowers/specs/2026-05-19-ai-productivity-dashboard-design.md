# AI-Made Developer Productivity Dashboard — Design Spec

**Date:** 2026-05-19
**Status:** Draft — awaiting user review (brainstormed end-to-end while user asleep, per explicit instruction)
**Scope of this spec:** Data acquisition, data model, time attribution, ship/uncommitted state machine, grouping, the narrative layers, **and the shipping of the previously-identified klyne worklog-reflection improvements** (they consume the same substrate). **UI/visual presentation is explicitly OUT OF SCOPE and deferred to a separate future brainstorm** (user instruction: "You are not talking about the UI/UX... that should also be a topic of brainstorming").

**Core principle — deterministic-first:** the dashboard is fully functional from git + session telemetry alone, with **zero dependency on any AI reflection having been run**. Reflections are an optional enrichment: when one exists for a day/project it refines the narrative; when none exists the dashboard still shows everything deterministically and surfaces a **nudge to run/update the reflection**. The dashboard is never an "always-on AI" surface and never blocks on an LLM call at render. Absence of a reflection must never degrade or destabilize the deterministic data — it stays stable until a reflection changes it.

---

## 1. Purpose

A single developer works across many microservice repos using an AI coding agent (Claude Code). klyne already captures AI-session telemetry (session durations, message/token timelines, per-project time, worklog reflections, decisions) but captures **no git data**. This feature fuses the two into an **AI-narrated, per-service productivity record** answering, for a given day (and weekly rollup):

- What shipped, and in what state (committed-local / pushed / merged-to-main).
- Which services/repos were touched, on which branches.
- What topic/ticket each branch covered, narrated by AI from verifiable git + worklog evidence.
- How much *time* went into each service, anchored to real AI-session activity (not naive commit-delta guessing).
- Where work is **at risk**: committed-but-unpushed, or "AI task done but not yet committed".

This was motivated by a concrete failure: klyne's existing worklog reflections inverted salience (foregrounded a docs cleanup while the flagship CLI-1396 pipeline — 9 commits, committed locally but **never pushed** — was absent from all reflections) and hallucinated a non-existent "PR #57". The dashboard's data model is designed specifically to make those failure modes structurally impossible.

### Market position (research summary)

The *combination* is genuine whitespace; the ingredients are not. WakaTime owns IC time-per-project (no AI-session layer, no narrative). Anthropic's Claude Code Analytics already joins sessions↔commits/PRs per user (but org-admin ROI, not IC-facing, no per-service narrative). DX owns AI-impact framing (org surveys). Auto-standup tools narrate commits (no session telemetry, no time math). **No product fuses AI-session timelines + git shipping into a per-service, AI-narrated "what I did and how long" log for the individual.** Two risks shaped this design: (1) git-derived time is provably noisy — the session-timeline anchor is the mitigation; (2) per-dev time-tracking is culturally radioactive — hence strictly **local, single-IC, no team aggregation**. Platform risk: Anthropic could extend its analytics API.

---

## 2. Scope

**In scope (this spec):** repo discovery, git-data acquisition architecture, persistence model, deterministic time-attribution algorithm, ship-state + uncommitted-state machine, service→branch→topic grouping, the narrative layers (deterministic default + reflection enrichment + nudge), anti-hallucination grounding contract, **shipping the previously-identified klyne worklog-reflection improvements** (open-loops/unpushed block, salience gate, commit-date anchoring, unverified-ID suppression, shipped ledger, author/identity filter, cross-project thread — all consuming the shared substrate), decisions log.

**Out of scope (deferred / YAGNI):**

- UI, charts, layout, visual design — separate future brainstorm.
- Team / multi-user aggregation — single IC, local only (deliberate; see market risk #2).
- Linear/Jira integration — not in v1 (decision D3); ticket ID shown as raw branch-derived string.
- Proactive alerting/notification engine beyond surfacing the two risk states.
- Editor-heartbeat tracking (WakaTime-style), non-git work.
- Retroactive backfill of "uncommitted-at-session-end" history for sessions before this feature ships (unrecoverable; commit history itself is still available via live scan).

---

## 3. Locked Decisions & Alternatives Considered

Every decision below was made interactively. Alternatives are recorded so any choice can be revisited later **without re-deriving the analysis**. To revisit: change the "Chosen" line, re-run the spec self-review, regenerate the implementation plan.

### D1 — Time source: **Hybrid (session-anchored, commit-validated, AI-reconciled)**
- **Chosen:** klyne session active-time is the primary measure of time-on-service; commit timestamps cross-check and split a session across services when one session touched multiple repos; AI reconciles ambiguity.
- **Alternatives:** (a) klyne session timelines only — misses multi-repo splitting; (b) commit-timestamp deltas only — crude, ignores pre-first-commit work, can't distinguish lunch from deep work; (c) show both numbers side by side — defers the modeling decision, honest but unrefined.
- **Why:** session telemetry is the accuracy edge that makes git-derived time credible (directly answers market risk #1). Commits alone are provably noisy.

### D2 — Ship status: **Three explicit states per branch**
- **Chosen:** every branch is exactly one of `committed-local-only`, `pushed-to-remote`, `merged-to-default`.
- **Alternatives:** (a) binary merged=shipped else in-progress — hides the committed-but-unpushed risk that caused the CLI-1396 blind spot; (b) activity-only (any commit = "worked on") — loses accountability entirely; (c) three states **plus** a proactive risk-flag engine — explicitly NOT chosen, scope discipline.
- **Why:** directly fixes the documented CLI-1396 blind spot; minimal, not over-built.

### D3 — Topic/narrative source: **Git-grounded AI summary only (no Linear)**
- **Chosen:** AI narrates "what I did" purely from commit messages + files changed + klyne worklog reflections. Ticket ID is the raw string parsed from the branch name (no title lookup). Anti-hallucination rule enforced (see §7).
- **Alternatives:** (a) git-grounded + Linear ticket titles — richer, needs integration; (b) deterministic only, no AI — zero hallucination but loses the "AI-made" insight; (c) AI + Linear best-effort/optional — flexible, more surface area.
- **Why:** no external dependency, works offline, lower surface area; ticket title enrichment can be added later behind D3-revisit.

### D4 — Session↔commit gap: **Session bounds work time + explicit "uncommitted work" state**
- **Chosen:** work-time = klyne session active span only. The session→commit/push gap (e.g. task done ~11:30, pushed 12:30) is **never** counted as work. "AI task completed, not yet committed/pushed" is a **first-class tracked state** with elapsed-time-since, mirroring committed-but-unpushed. The gap is surfaced as a risk signal.
- **Alternatives:** (a) bridge the gap up to a threshold — reintroduces commit-delta guesswork; (b) AI decides per-case from evidence — unverifiable, drifts; (c) session-bounds only without elevating the uncommitted state to first-class.
- **Why:** symmetric with unpushed-risk detection; keeps time honest; surfaces the "done but uncommitted" case the user explicitly raised.

### D5 — Repo scope: **klyne sessions + their sibling worktrees**
- **Chosen:** auto-discover repos from klyne session `project_path`s in the window, AND enumerate `git worktree list` for each to also cover sibling worktrees (e.g. `labstack-docs` / `CLI-xxxx` worktrees).
- **Alternatives:** (a) sessions only — misses parallel-worktree branches; (b) configured repo list — manual upkeep, drifts; (c) scan a root directory — broad but noisy/slow.
- **Why:** matches how the user actually works (per-repo worktrees per ticket); zero config; bounded.

### D6 — Git-data acquisition architecture: **Hybrid (session-end snapshot + live scan at render)**
- **Chosen:** session-end hook records a lightweight point-in-time git snapshot (the only way to ever know "done but uncommitted at 11:30"); a live git scan at render catches post-session pushes/merges (the 12:30 push); the two are reconciled, AI-summarized, and cached per repo until git HEAD / ahead-behind changes.
- **Alternatives:** (a) live query only — structurally cannot reconstruct uncommitted-at-session-end after the fact, would force revisiting D4; (b) session-end snapshot only — misses post-session pushes/merges, undercuts "what shipped today".
- **Why:** D2 and D4 *jointly require* both halves; neither alone satisfies the locked decisions.

### D7 — Decision documentation: **Log every decision with alternatives + rationale**
- **Chosen:** this section. Decisions are revisitable without re-deriving.
- **Why:** user instruction — "document this also; later if this option isn't working we can try the other one."

### D8 — Deterministic-first; reflections enrich, never gate
- **Chosen:** the dashboard renders fully from the deterministic substrate (git + session telemetry) with **no LLM call at render and no dependency on any reflection having been run**. A klyne worklog reflection, when present for a day/project, *enriches* the branch narrative. When absent, the dashboard shows a deterministic templated summary **plus a visible nudge** to run/update the reflection. Deterministic data stays stable until a reflection changes it. The previously-identified worklog improvements are **shipped here** (not merely recommended), and the upgraded reflection generator consumes the **same substrate** as the dashboard.
- **Alternatives:** (a) AI narrative generated ad-hoc by the dashboard at render — makes it an always-on AI surface, costly, unstable, contradicts user intent; (b) reflection required before dashboard is meaningful — gates the product on a manual step; (c) critique-only (flag improvements, don't ship them) — explicitly rejected by user ("it should not be just that we are asking").
- **Why:** user instruction — dashboard "should be determined by what users do, until they do any reflection; if there is any reflection then the data can be [enriched]; if not, that should remain the same." Also reduces AI-platform dependency/cost risk from the market analysis.

---

## 4. Data Sources

| Source | Provides | Already in klyne? |
|---|---|---|
| `sessions`, `messages` tables | session spans, per-message timestamps, token timeline, `project_path`, `cli` | Yes |
| `stop_summaries`, `worklog_reflections` | deterministic + AI worklog text per project/window | Yes |
| `014_work_spans` | **existing** work-span computation — time-attribution must reuse/extend, not reinvent | Yes — investigate in plan |
| `internal/projectpath` | canonical repo-root resolution (worktree → repo identity) | Yes |
| `internal/contexthealth` | active-time / idle-gap logic for token timeline | Yes — reuse for active-span math |
| Live `git` (shell) | commits, branches, ahead/behind, merged-state, dirty tree | **No — new** |
| Session-end git snapshot | point-in-time branch/HEAD/ahead-behind/dirty at session end | **No — new** |

---

## 5. Data Model (new persistence)

Minimal, lean. Only data that is **unreconstructable later** is persisted as raw facts; everything else is computed at render from git (the source of truth) and memoized.

### Migration `017_git_dashboard.sql`

**`git_session_snapshots`** — point-in-time, written by the session-end hook. Essential and unreconstructable.

| Column | Notes |
|---|---|
| `id` PK | |
| `session_id` | FK `sessions.id` |
| `project_path` | canonical repo root (via `internal/projectpath`) |
| `repo_name` | derived |
| `worktree_path` | actual worktree (may differ from canonical root) |
| `branch` | branch at session end |
| `head_sha` | |
| `ahead_count`, `behind_count` | vs upstream/origin |
| `dirty_file_count` | uncommitted+untracked count at session end |
| `dirty_files_json` | bounded list (cap N, e.g. 50) |
| `captured_at` | session-end timestamp |

**`dashboard_cache`** — memoized fused+narrated record. Essential for AI cost/latency.

| Column | Notes |
|---|---|
| `id` PK | |
| `day` | date (local) |
| `repo_name`, `branch` | |
| `cache_key` | hash of invalidation inputs (see §6.4) |
| `payload_json` | the fully fused service→branch→topic record incl. AI narrative, time, ship state |
| `model`, `generated_at` | provenance |

Live commit/branch/ship-state data is **not** persisted in a normalized table — it is computed on demand from git at render and folded into `dashboard_cache.payload_json`. git is the historical source of truth for commits; only the uncommitted-at-session-end moment needs snapshotting.

---

## 6. Computation Pipeline (render time)

Input: a time window (default = today, local; weekly = rolling 7-day rollup over the same per-day records).

### 6.1 Repo discovery (D5)
1. Select distinct `sessions.project_path` in window; canonicalize via `internal/projectpath`.
2. For each, run `git worktree list`; add every worktree path.
3. Result: a set of (repo_name, canonical_root, worktree_path) tuples.

### 6.2 Live git scan (D6)
Per repo+worktree, within window:
- commits by author/committer date,
- branches with activity,
- per-branch `ahead`/`behind` vs origin,
- merged-into-default detection (`git branch --merged <default>` / `merge-base --is-ancestor`),
- current HEAD + dirty tree.

### 6.3 Identity filter (fixes the Ravi-Ranjan / Jenkins attribution defect)
- "User" identity = `git config user.email` ∪ a configurable alias set in klyne config (default seed: `user@example.com`).
- Commits not authored by the user are labelled `co-actor` and **excluded from the user's productivity figures** (kept only as context count). Bot/CI authors (e.g. Jenkins) excluded likewise.

### 6.4 Time attribution (D1, D4) — deterministic, no LLM
- **Primary:** per `project_path`, sum klyne session active-time in window. Active-time = Σ message-interval durations with a max idle-gap cap (reuse `internal/contexthealth` idle logic). **Reuse/extend `014_work_spans` if it already computes this** — do not reinvent (flagged for the implementation plan).
- **Multi-repo split:** if a single session's window contains commits in >1 repo (or cwd switches), split that session's active-time across those repos proportionally by in-window commit count/recency. Mark such figures as approximate (`≈, split A/B`). AI may *annotate* the ambiguity but **never computes the number**.
- **Manual work:** if a repo has in-window commits but **no** session active-time, report a separate `manual (no AI session)` commit-span figure — never folded into AI-time (D4: never silently count non-session time).
- **Gap rule (D4):** the session→commit/push gap is excluded from work-time entirely.

### 6.5 State machine (D2, D4)
Per branch:
- `committed-local-only` — local commits, `ahead > 0`, not on origin.
- `pushed-to-remote` — present on origin, not merged to default.
- `merged-to-default` — merged into the repo's default branch.

Per repo (risk signals, derived from latest `git_session_snapshots` + live scan):
- `unpushed` — `ahead > 0`; include count + age (oldest unpushed commit time).
- `done-uncommitted` — last session for repo was substantive AND (snapshot `dirty_file_count > 0` OR no commit after session end); elapsed = `now − session_end`. Surfaced as a signal; **never** added to work-time.

### 6.6 Grouping
Assemble the hierarchy:

```
Service (repo)
└─ Branch  { ship_state, ticket_id (raw, parsed from branch name), time_attributed, risk_signals }
   └─ Topic { AI narrative — §7 }
```

Weekly rollup = aggregation over the per-day per-service records (sum time, union branches, latest ship-state).

### 6.7 Cache invalidation (D6)
`cache_key = hash(repo, branch, head_sha, ahead, behind, sorted(in-window commit shas), rounded(session-active-span), latest snapshot id)`. AI narrative regenerated only when `cache_key` changes.

---

## 7. Narrative Layers (D3, D8)

The per-branch "topic / what I did" text has **three layers**, resolved in order. The dashboard always has *something* to show and never blocks on an LLM.

**Layer 1 — Deterministic templated summary (always available, default).**
Built with no LLM from: commit subjects, files-changed counts, ship state, attributed time, risk signals. Example shape: *"3 commits on `feat/CLI-1396-...` (committed-local, unpushed 4h): extractReportIdFromLink; processOneLabStackReport; … — ~2h10m AI session time."* Stable, cheap, regenerates only when the deterministic substrate changes.

**Layer 2 — Reflection enrichment (when a klyne worklog reflection exists for that day/project).**
The (upgraded) reflection's narrative replaces/augments Layer 1 as the richer "topic" text. The reflection generator is itself upgraded here (§7.1) and consumes the same substrate, so its narrative is already git-grounded and salience-correct.

**Layer 3 — Nudge (when no reflection exists for the window).**
The API record carries a `reflection_status: "missing" | "stale" | "current"` field and a human nudge string (e.g. *"No reflection yet for consultation-service today — run it to enrich this entry. Showing factual summary."*). Deterministic data is shown regardless and remains stable until a reflection is produced.

### 7.1 Anti-Hallucination Grounding Contract (applies to the reflection generator, upgraded here)

This is the explicit guard against the documented "PR #57" hallucination and salience-inversion failures. It now governs the **klyne worklog reflection generator** (which Layer 2 surfaces), not an ad-hoc render-time LLM.

- **Determinism boundary:** repo discovery, commit/branch/ship-state facts, time math, state machine, identity filter, and all numbers are deterministic and never authored by the LLM. The LLM only writes prose and receives the facts as fixed inputs.
- **Input allowlist:** `{commit: sha, subject, files_changed, ts}[]`, branch name, klyne worklog/stop-summary text for that project+window, the deterministic time figure + ship state. Nothing else.
- **Output rules:** (1) every factual claim traceable to an allowlisted input, cite short sha(s); (2) **never** emit a PR/ticket/external ID unless it literally appears in the branch name or a commit message; (3) unknowns stated as unknown; (4) time/state quoted verbatim from the deterministic layer; (5) **salience rule** — lead with the highest-code-impact work (commit count / net-new lines), not chronological or trivial work.

## 7.2 klyne worklog-reflection improvements shipped here (D8)

The seven improvements previously *recommended* are now *implemented* as part of this feature, all consuming the §6 substrate so the dashboard and the reflections agree by construction:

| # | Improvement | Powered by substrate field |
|---|---|---|
| 1 | "Open Loops / Unpushed" block in every daily reflection | `risk_signals.unpushed` (ahead count + age), `done-uncommitted` |
| 2 | Salience gate (lead with highest-impact work) | commit count / lines / net-new ranking (§7.1 rule 5) |
| 3 | Date reflections by commit timestamp, not session start | commit `committed_at` from live scan |
| 4 | Suppress unverified specific artifact IDs | grounding rule §7.1(2) |
| 5 | Terse "Shipped" ledger line per repo | ship-state machine (§6.5) + identity filter |
| 6 | Author/identity filter (exclude co-actors/bots) | §6.3 identity filter |
| 7 | Cross-project initiative thread (one umbrella when a theme spans repos same day) | grouping across repos sharing a window/ticket-id token |

These ship as modifications to the existing `internal/worklog` reflection proposer/recorder, reusing `internal/productivity`.

---

## 8. Architecture Placement in klyne

| Concern | Location |
|---|---|
| Migration | `internal/store/migrations/017_git_dashboard.sql` |
| Snapshot capture | extend `internal/hooks/sessionend.go` (shell git in project_path + worktrees; reuse `internal/projectpath`) |
| Compute engine | new package `internal/productivity` — discovery, live scan, identity filter, time attribution, state machine, grouping; depends on `store`, `projectpath`, `contexthealth`, `worklog`, and existing `work_spans` |
| Narrative L1 | deterministic templated builder in `internal/productivity` (no LLM) |
| Narrative L2/L3 | upgrade `internal/worklog` reflection proposer/recorder to consume `internal/productivity` substrate + enforce §7.1; expose `reflection_status` + nudge on the API record |
| Worklog improvements | the 7 items in §7.2, implemented in `internal/worklog` against the shared substrate |
| API | new handler in `internal/api/handlers`, registered in `internal/api/contracts.go`: `GET /api/productivity?since=&until=` → fused grouped JSON incl. `reflection_status`. **Production UI route deferred; a rough read-only prototype page is built for demo only (§11).** |
| Identity config | extend klyne config: git user.email + alias set |

---

## 9. Success Criteria

1. For 2026-05-19, the dashboard JSON correctly shows: consultation-service CLI-1396 as `committed-local-only` with the unpushed risk signal (the exact case the old worklog missed); operations-app CLI-1325 as `pushed`; oms-service PR #49 work as `merged-to-default`; klyne `init` 9 commits `committed-local-only`.
2. Ravi-Ranjan / Jenkins commits are excluded from the user's figures (identity filter).
3. Time-on-service is session-anchored; the 11:30→12:30 push gap is excluded; the "done-uncommitted" state appears with elapsed time when applicable.
4. AI narrative cites commit shas and contains **no** invented PR/ticket numbers (regression guard against "PR #57").
5. Dashboard renders fully with **zero reflections present**; the nudge appears; running a reflection enriches the entry without changing the deterministic numbers.
6. The 7 worklog improvements (§7.2) are present in generated reflections.
7. No *production* UI is built (deferred); only the rough demo prototype page of §11.

## 10. Open Questions for Implementation Plan

- Does `014_work_spans` already provide the session active-span primitive? If yes, extend it; if not, build in `internal/productivity`.
- Default-branch detection per repo (`main` vs `master` vs `init` for klyne itself) — needs a robust resolver.
- Performance ceiling: N repos × worktrees × live git per render — measure; the `dashboard_cache` should make steady-state cheap, but first-load cost needs a budget.

---

## 11. Prototype scope (what is built tonight vs. production follow-up)

The user requested a runnable prototype by morning. The prototype delivers the **deterministic-first core** end-to-end and is honest about what is stubbed.

**Built in the prototype:**
- `internal/productivity` package: repo discovery (D5), live git scan (D6 live half), identity filter (D6.3), ship-state machine (D2/§6.5), session-anchored time attribution (D1/§6.4, reusing `014_work_spans`/`contexthealth` if available), grouping (§6.6), Layer-1 deterministic narrative (§7 L1), risk signals incl. *current* unpushed / done-uncommitted.
- `GET /api/productivity?since=&until=` handler returning the fused grouped JSON with `reflection_status` + nudge (L3).
- A deliberately rough, unstyled read-only HTML/Svelte page to **view** it (the "dirt dashboard") — explicitly not the real UI/UX (separate brainstorm).
- Runs against real local repos for 2026-05-19 and validates success criteria 1–3, 5.

**Stubbed / production follow-up (documented, not silently skipped):**
- `git_session_snapshots` capture in the session-end hook — migration `017` ships the table, but live-scan-only is used for the prototype; historical "uncommitted-at-11:30" reconstruction is therefore not yet available (only *current* uncommitted state). This is the D6 snapshot half.
- Layer-2 reflection enrichment + the 7 worklog improvements (§7.2) — substrate is built and consumable; the `internal/worklog` upgrade is the next plan phase, not the prototype.
- `dashboard_cache` memoization — prototype computes live each request (acceptable at current repo count; cache is a perf follow-up).

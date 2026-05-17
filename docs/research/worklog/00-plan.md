# Plan: AI Work-Log Feature for klyne — Deep Research Synthesis

**Date:** 2026-05-17 (updated evening of same day after architectural review)
**Status:** **Direction confirmed — Option 2 (Memory + Reflection + Planning), cross-AI foundational.** Ready to start.
**Sources:** Five parallel research agents (Appendices A–E) + claude-diary verified review (F) + Generative Agents paper review (G).
**Branch / worktree:** `research/worklog` at `/Users/mohitpatel/Desktop/Project/klyne-worklog-research`.
**Parallel work:** bootstrap integration bug fix (`mcp__klyne__get_session`, `summarize_session`, bootstrap reads Claude auto-memory) dispatched to separate session.

---

## TL;DR

You face a hard call. Here is what the data actually says.

**The problem is real, but its scale is smaller than originally reported.** Agent C audited your klyne database (349 sessions, 109,122 messages over 8 months) and found behavioural evidence: 133 handoff generations, 128 pre-compact pulls, 34 auto-compacted sessions. The originally-headlined **"`record_decision` invoked 40 times but only 3 rows survived"** claim was VERIFIED 2026-05-17 and found wrong — actual invocations were **7, not 40** (the inflated count came from `deferred_tools_delta` tool-availability listings being miscounted as calls). True loss rate is ~57% of a tiny base, plausibly within normal failure modes — NOT the catastrophic 92% data-loss bug originally framed. **The other quantitative claims (133, 128, 34, etc.) have NOT been re-verified** and should be treated with caution. Claude captured a cross-repo summary into its auto-memory (`project_labstack_integration.md`) on your prompt during session `38601308` — that workaround is verified. The pain is real; just not as dramatic as Agent C made it.

**The space is crowded.** Agent B surfaced ~33 directly-adjacent OSS projects. **claude-mem (89K stars)** and **cass (761 stars)** already solve ~75% of the naive feature. SpecStory, Mantra, claude-diary, and Agent-Scribe occupy the rest. The white-space exists but is narrow and shrinking quarter-over-quarter. Anthropic itself ships per-project session memory at `~/.claude/projects/<hash>/session-memory/summary.md` and is heading further into this space.

**You have built most of this already, twice.** Agent A found that klyne has 14 migrations and most of the substrate: `stop_summaries`, `decisions`, `work_spans`, `messages_fts`, `bootstrap`, `generate_handoff`, `status_snapshot`. A naive "work log" overlaps roughly 60% with existing schema.

**The skeptic has the strongest single argument.** Agent E argues that a sixth pillar will not make the first five "wow." The wow-deficit is distribution and depth, not breadth. The recommended alternative is to ship a sharper retrieval surface (`/klyne:recap`) over what you already index, rather than a write-time summarisation layer.

**If you build it, the shape is clear.** Agent D's "Strategy C" is the only sane architecture: deterministic event taxonomy → episode-scoped triggers (commit / PR / Stop / PreCompact / idle / explicit) → 11 suppression rules → optional AI prose pass → runbook-style accept/dismiss. Cost ~$0.05/user/week. 24 of 25 event types are deterministic.

### My recommendation (CONFIRMED 2026-05-17 evening)

**Option 2: Memory + Reflection + Planning, cross-AI from day one. 3-week sprint.**

This is **not a Claude session companion**. It is a **cross-AI productivity diary** that captures what every AI tool ships for you (Claude, Codex, future tools) into one project-or-user-level log. The original ask, re-anchored: *"irrespective of codex and plot, so it would be a client log feature"* — that requirement has been central since message 1 and is the differentiator vs every existing tool in the market scan.

Three-week structure:

- **Parallel (separate session)** — Bootstrap integration bug fix (`mcp__klyne__get_session`, `summarize_session`, bootstrap reads Claude auto-memory). Lands when it lands; doesn't block this plan.
- **Track 1 (definitely-ship, ~5 days)** — Sharpen existing: 1-hour decision-rot spot-check, promote `stop_summaries` into `bootstrap`, ship `/klyne:recap` on-demand.
- **Track 2 (Memory + Reflection, cross-AI, ~9 days)** — Extended `stop_summaries` capturing **Claude AND Codex** episodes, conditional weekly per-project Markdown export with cli tags, **plus** the Reflection layer (importance scoring, `worklog_reflections` table, weekly synthesis with citation invariants) directly inspired by claude-diary's three-tier architecture and the Generative Agents paper (Park et al. 2023, Appendix G).
- **Dogfood + kill criteria (~5 days)** — Run the six pre-committed kill criteria. If they all pass, the feature stays. If any fire, Track 1 still shipped (clean win).

**Pitch when this ships:** *"klyne is the only tool that gives you one cross-AI productivity diary. Use Claude, Codex, Cursor, whatever — the work lands in one log per project. Ask 'what did I ship this week' and get the answer across all your AI tools combined. Auto-captured, auditable, local-first, queryable by AI on demand."*

---

## The two problems, separated

Tonight's discussion made the scope clear by separating two overlapping problems:

| Problem | Who solves it | Status |
|---|---|---|
| **Session context for next Claude session** | Claude itself (via handoff) + the bootstrap integration bug fix | **Being handled in a parallel session** (the bug ticket) |
| **"What has AI shipped across all my tools this week"** | **Nothing currently solves this. This is what klyne uniquely can.** | **This plan** |

The first problem is being fixed elsewhere. The plan below focuses on the second — the cross-AI productivity diary that nothing in the market scan covers.

---

## The core tension

Your stated goal was a "wow feature." The five-agent research found four findings that pull in different directions:

1. **The pain is real** (Agent C, high confidence) — so doing *nothing* is wrong.
2. **The space is crowded and shrinking** (Agent B) — so doing the *naive thing* loses to claude-mem.
3. **klyne already covers most of it** (Agent A + E) — so adding a *new pillar* duplicates existing code.
4. **Pre-summarised logs may not actually beat raw-transcript retrieval** (Agent E) — so the *load-bearing assumption* of the feature is unproven.

The only path that resolves all four is a small, deterministic, evidence-tested incremental extension to what exists, with explicit kill criteria. That is the plan below.

---

## What the five lenses found (one paragraph each)

### A — klyne internals
You have 14 migrations and most of the work-log substrate. `work_spans` (migration 014, ~10 days old) is the natural episode primitive — it already groups messages into outcomes by git commit. `stop_summaries` (migration 011) is structurally close to a per-session work-log row. `decisions`, `messages_fts`, `bootstrap`, `generate_handoff`, `status_snapshot` cover adjacent functionality. The cleanest architectural fit if a full feature shipped: a new `internal/worklog/` package, ~1500 lines of Go, building *on top of* `work_spans` rather than parallel to it. New code is ~20% of the feature; the other 80% reuses substrate. Full inventory in appendix A.

### B — OSS / market
33 directly-adjacent projects, eight categories, ~85% confidence on the open-source picture. **claude-mem (89K stars)** is the gorilla — local-first, tool-agnostic, captures via 5 hooks → SQLite + FTS + vectors, auto-summarises, injects on resume. **cass (761 stars)** is the search foundation across 20+ providers. **SpecStory, Mantra, claude-diary** occupy the rest. None do all of: structured ledger (decisions/files/tasks separated) + cross-tool intent aggregation + deterministic-first extraction + audit-mode surface. **That is the narrow white-space — but it is shrinking.** Anthropic itself has session-memory primitives and is heading there. Full landscape in appendix B.

### C — user-pain validation
349 sessions, 109,122 messages over 8 months. **133 handoff generations vs. only 3 surviving decision rows** (per agent — needs independent verification per Appendix C correction note). The agent claims `record_decision` was fired 40 times — if confirmed, **persistence is broken or routes wrong**. Claude captured `project_labstack_integration.md` into its **auto-memory** (NOT klyne's — these are distinct systems) on your prompt during session `38601308`, tracking branch state across 4 LabStack repos as a manual workaround for the missing worklog. 34 sessions auto-compacted. 12 days in the last 30 had both Claude AND Codex active on the same project. The trackIt monetisation Q&A (2026-05-16) produced ten "locked" decisions and persisted zero. **Verdict: high confidence the problem is real.** But the sharpest version is *decisions and deferreds* (severely lost) + *cross-repo what-shipped* (severely lost), not "what was built" (git knows). Top 5 scenarios with verbatim excerpts in appendix C.

### D — trigger design
**Use Strategy C: deterministic skeleton + AI for prose only + user accept/dismiss (runbook-style).** 25-event taxonomy, 24 deterministic events, 1 AI-judged catch-all. Primary trigger = `git commit` lands. Five secondary triggers in strict precedence (PR open, `/klyne:log`, Stop, PreCompact, idle-resume). 11 named suppression rules. Silent-draft default, 7-day auto-accept. AI input capped at 2K tokens, output at 150. Steady-state cost ~$0.05/user/week with prompt-caching. Single-writer rule: only `close_episode` writes a row. 15 acceptance test scenarios. Full design in appendix D.

### E — adversarial skeptic
**Strongest five arguments against:** (1) you've built 60–70% twice; (2) "AI reads pre-summary > raw transcripts" is unproven and probably wrong given long-context models; (3) the problem may not be real outside your machine (N=1); (4) you'd be centralising a continuous surveillance log with no threat model; (5) opportunity cost — making one existing pillar 5× better beats adding a sixth horizontal pillar. Cheaper alternatives: promote `stop_summaries` into `bootstrap`; ship on-demand `/klyne:recap`; sharpen `generate_handoff` with cross-session aggregation; rank `search_messages`; ship session-diff view. Six pre-committed kill criteria. Minimum viable concession: extend `stop_summaries` with two columns; add one MCP tool `mcp__klyne__recap_project`; no new table, no new file format, no UI. Full review in appendix E.

---

## Recommended path: the 2-week dual-track sprint

Do **both** tracks in parallel. They are independent.

### Track 1 — Sharpen what exists (the "this would have worked anyway" track)

These are wins regardless of whether the work-log experiment succeeds.

**T1.1. ~~Diagnose decision rot.~~ DOWNGRADED 2026-05-17 — verification showed the bug is much smaller than claimed.** Real numbers: 7 actual tool_use calls, 3 surviving rows (~57% loss, not 92%). 4 missed writes plausibly normal (permission denials, validation, dismissals). **Action item:** still worth a 1-hour investigation to confirm the 4 missing calls have normal failure reasons (check daemon logs, MCP error responses). Do NOT block other Track 1 work on it. ~1 hour, not 1 day.

**T1.2. Promote `stop_summaries` into `bootstrap`.** It already exists and is project-keyed. Inject the last 5 `stop_summaries` rows for the active project into the bootstrap brief, alongside the last 3 decisions and the touched-files heatmap. ~1 day.

**T1.3. Build `/klyne:recap`.** An on-demand, pull-based slash command: `/klyne:recap [--since 7d] [--project X] [--topic auth]`. Runs over `messages` + FTS + `stop_summaries` + `decisions` + `work_spans` at query time, anchored on the current question. Returns a tailored summary. **No write-time summarisation.** This is Agent E's recommended alternative to the work log. ~3 days.

These three together cost ~5 days, address ~70% of the underlying pain that Agent C surfaced, and ship before the kill-criteria experiment finishes.

### Track 2 — Cross-AI Memory + Reflection layer (~9 days)

**Three layers from the Generative Agents paper (Park et al. 2023), adapted from claude-diary's three-tier architecture:**

- **Memory** — every Claude AND Codex session writes structured entries to extended `stop_summaries`. The Memory layer is cli-agnostic by design.
- **Reflection** — weekly synthesis across entries with importance-sum trigger, "what questions can we answer" prompt, mandatory citation invariants; lives in new `worklog_reflections` table.
- **Planning loop** — synthesized reflections surface in bootstrap so the next session inherits curated context (not raw turn snapshots).

**T2.1. Schema (migrations 015 + 016).** Extend `stop_summaries`:
```sql
ALTER TABLE stop_summaries ADD COLUMN recap_visible INTEGER NOT NULL DEFAULT 1;
ALTER TABLE stop_summaries ADD COLUMN recap_topic TEXT;
ALTER TABLE stop_summaries ADD COLUMN ai_drafted_summary TEXT;
ALTER TABLE stop_summaries ADD COLUMN draft_state TEXT NOT NULL DEFAULT 'proposed';
ALTER TABLE stop_summaries ADD COLUMN signature TEXT;
ALTER TABLE stop_summaries ADD COLUMN importance INTEGER NOT NULL DEFAULT 5;     -- 1-10, set at write time via deterministic heuristics
ALTER TABLE stop_summaries ADD COLUMN last_accessed_at INTEGER NOT NULL DEFAULT 0; -- powers recency decay in retrieval
```
Plus migration 016 — new `worklog_reflections` table (per Appendix G schema: `tier`, `evidence_entry_ids_json`, `importance`, `state`). ~1 day.

**T2.2a. Claude Stop hook writer.** Extend the existing Claude Stop hook to populate the new columns. Compute `importance` deterministically from event-tag heuristics (commit_landed=7, security_relevant_change=9, dependency_change=6, lint-only=2, decision_recorded=8, etc.). Run AI prose pass only when entry survives suppression. ~1 day.

**T2.2b. Codex episode-boundary detector (cross-AI capture — the differentiator).** Klyne's ingestion daemon already tails Codex JSONL files. Add: when a Codex session has been idle > 30 min (no new messages), treat as ended → write the same `stop_summaries` row shape with `cli='codex'`. Same suppression rules, same importance scoring, same downstream surfaces. **This is what makes the worklog cross-AI, not Claude-only.** ~1 day.

**T2.3. Suppression rules.** Implement Agent D's 11 rules as `internal/insights/worklog/suppress.go`. Applied identically to Claude and Codex entries. ~2 days.

**T2.4. New MCP tool: `mcp__klyne__recap_project(project_path, since_days, topic?)`.** Returns visible entries for a project, **cli-agnostic** (mixes Claude + Codex in one timeline). ~½ day.

**T2.4b. New MCP tool: `mcp__klyne__user_recap(since_days, group_by?)`.** **User-level surface** — aggregates across ALL projects and ALL CLIs. Returns "this week across 4 projects: 12 entries — Claude 8, Codex 3, ...". This is the "monitor my AI productivity" pitch. ~½ day.

**T2.5. Auto-inject into `bootstrap`.** When a fresh session starts, surface the last 3 visible entries for this project from **both CLIs**, tagged with `cli` source. ~½ day.

**T2.6. Conditional weekly Markdown export with cli tags.** Command: `klyne worklog export-week --project X --week 2026-W20 [--all-projects]`. Renders `<project_root>/docs/worklog/YYYY-WW.md`. **Conditional rule:** writes the file ONLY if the (project, week) has ≥ 1 visible entry. Each entry in the rendered MD is tagged with its source (`[claude]` / `[codex]`). Quiet weeks produce no file. Mirrors release-please's "no commits → no release" pattern. ~1 day.

**T2.7. Reflection layer (the synthesis tier — Generative Agents pattern).**
- **Trigger:** importance-sum threshold (paper uses 150) OR weekly cron, whichever fires first
- **Process:** query last-N unreflected entries, prompt LM with *"what 3 high-level questions can we answer from these entries?"*, use those as retrieval queries, extract insights with **mandatory citation invariants** — every reflection row's `evidence_entry_ids_json` must be non-empty
- **Storage:** `worklog_reflections` table (tier=1 daily, 2 weekly, 3 quarterly)
- **Surface:** reflections appear in `recap_project` / `user_recap` output **AND** get injected into bootstrap. This closes the **Planning loop** — synthesized rules from past sessions shape future session context, which was the gap in the old Track 2 design.
- ~2 days.

**T2.8. Dogfood for 5 working days.** Use the cross-AI worklog on real work — including reading the generated weekly MD files. Cross-test by running both Claude AND Codex sessions in the same project and verifying both surface in the recap. Run the kill-criteria tests at end of week 3.

---

## Kill criteria (commit to these now)

Pre-commit so you cannot rationalise around them. If **any one** fires at end of week 2, scrap Track 2 and double down on Track 1.

1. **Dogfood usefulness rate.** Try to answer 20 questions you'd normally ask about past work using **only** the new recap surface. If useful answer rate is < 60%, kill.
2. **Counterfactual A/B.** For the same 20 questions, answer using only existing klyne (`search_messages` + `list_decisions` + `generate_handoff`). If the new recap surface does not **strictly** beat the existing stack on ≥ 12 of 20, kill — you have a `/klyne:recap` win, not a work-log win.
3. **Hallucination audit.** Sample 30 random AI-drafted summaries. Have a fresh Claude check each against the underlying transcript. If > 4 contain a factual hallucination, kill.
4. **Schema-drift test.** If > 60% of the value of the new columns can be reconstructed from existing data via a `SELECT`, kill — you had a view problem, not a write problem.
5. **Boredom signal.** If during week 2 you reach for `/klyne:search` or `git log` instead of the recap surface, kill.
6. **External validation.** Show recap output to two unrelated devs who use Claude Code daily. If neither says "yes, I'd want this," demote to personal dotfile.

---

## What we're explicitly NOT building (and why)

These were all on the table in the original prompt. They are now off-limits unless kill-criteria pass.

- **No new top-level `worklog_entries` table.** Use `stop_summaries` extended. (One new table is unavoidable: `worklog_reflections` for the synthesis tier — that's distinct in purpose from per-episode entries, per the Generative Agents architecture.)
- **Hybrid storage: SQLite source of truth + conditional weekly Markdown export.** Internal storage stays SQLite. Per-project weekly Markdown at `<project_root>/docs/worklog/YYYY-WW.md`, **conditional on activity** (no file for projects with zero entries that week). Each entry tagged with source CLI. SQLite remains canonical; Markdown is regenerable.
- **No new directory tree under `~/.klyne/`.** The per-project export at `<project_root>/docs/worklog/` is the new directory tree, and it lives at the project root by design.
- **No new UI route in the cockpit.** UI ships only if kill-criteria pass.
- **No cross-machine sync.** Local-first, period. Reconsider only if three external users ask.
- **AI is for prose + reflection synthesis only, never for trigger decisions.** Memory-layer triggers are 100% deterministic per Agent D. The Reflection layer uses AI for *synthesis* over deterministically-selected entries (paper's pattern: deterministic importance score gates the AI call). Never AI-as-trigger.
- **No write-time summary blob (claude-mem style).** Structured ledger with separated decision/file/task types, not a compressed memory blob. Backed by the Reflection layer instead.

---

## What's open — questions only you can answer

1. **Is "wow" really the goal, or is depth?** Agent E argues making `/klyne:precompact` or context-health undeniably best-in-world is higher-leverage than adding a sixth pillar. You said yourself existing features "aren't wow." Decide: is the missing thing breadth (new pillar) or depth (sharpen one)?
2. **Do you have three external users?** Agent E's kill criterion #6 depends on this. If klyne is meaningfully N=1 today, the answer to (1) shifts toward depth.
3. **The cross-CLI angle.** This is the most differentiated piece. Is "klyne unifies Claude + Codex + future CLIs across one project" the narrative you want?
4. **The decision-rot bug (DOWNGRADED 2026-05-17).** Original framing was wrong — verified numbers are 3 rows from 7 actual calls (~57% loss, not 92%). Likely not a bug at all, more likely normal failure modes. ~1 hour spot-check, not a Monday-morning critical fix. **The bigger lesson:** Agent C's other quantitative claims (133, 128, 34, etc.) should be similarly re-verified before any are used to justify decisions.
5. **Anthropic's roadmap.** They already ship per-project session memory at `~/.claude/projects/<hash>/session-memory/summary.md`. If they ship native cross-session memory in the next 3 months, every OSS player in this space gets compressed. How much does that change the risk profile?

---

## Day-by-day for the 3-week sprint

| Day | Track 1 (sharpen, definitely ship) | Track 2 (cross-AI worklog + Reflection) |
|---|---|---|
| **Week 1 Mon** | Decision-rot 1h spot-check + T1.2 (stop_summaries → bootstrap, partial) | Migrations 015 + 016 schema (T2.1) |
| **Week 1 Tue** | T1.3 start (`/klyne:recap`) | T2.2a Claude Stop writer + T2.2b Codex detector (parallel) |
| **Week 1 Wed** | T1.3 continue | T2.3 suppression rules |
| **Week 1 Thu** | T1.3 finish | T2.3 finish + T2.4 `recap_project` MCP tool |
| **Week 1 Fri** | Track 1 polish | T2.4b `user_recap` + T2.5 bootstrap inject + T2.6 MD export with cli tags |
| **End of Week 1** | **✅ Track 1 shipped** | **✅ Memory layer complete (cross-AI capture, both CLIs writing)** |
| **Week 2 Mon** | — | T2.7 Reflection layer — `worklog_reflections` table + importance scoring writer |
| **Week 2 Tue** | — | T2.7 weekly synthesis runner + citation invariants enforcement |
| **Week 2 Wed–Fri** | — | T2.8 dogfood starts (real work in both Claude AND Codex; weekly MD generated Friday night) |
| **Week 3 Mon–Fri** | — | T2.8 dogfood continues, run all 6 kill criteria, make build/scrap decision |

**End of Week 3:** build/scrap decision based on kill criteria. Track 1 wins are already shipped either way. Parallel bug-fix work from the separately-dispatched ticket lands whenever it lands — does not block this timeline.

---

## Tension preserved — read this last

The skeptic (Agent E) thinks you should not build *any* of Track 2. The pain validator (Agent C) thinks you should. The market scanner (Agent B) thinks the white-space is real but narrow. The internals mapper (Agent A) thinks the substrate makes it cheap. The trigger designer (Agent D) thinks if you do it, it's tractable.

The dual-track design is built so that **even if Agent E is fully right**, you ship Track 1 (which is independently valuable) and the only loss is Track 2's ~5 days. If **Agent C is right**, you've started learning the right thing cheaply. If **Agent B is right and the window is closing**, 2 weeks of experiment is the right speed.

The mistake to avoid is the one Agent E warned about: spending 6 weeks on a fully-baked work-log and then discovering the load-bearing assumption ("AI on logs > AI on transcripts") was wrong all along.

---

## Appendices (curated set — non-essential research files were pruned 2026-05-17 evening)

- **A** — `a-klyne-internals.md` — full inventory of klyne's existing substrate, schema snapshot, reuse map, risks. *Engineering reference for the implementing session.*
- **C** — `c-user-pain-validation.md` — quantitative profile, evidence table, top 5 scenarios with verbatim excerpts. *Empirical justification for the feature, with verified-number corrections at the top.*
- **D** — `d-trigger-design.md` — 25-event taxonomy, three strategies compared, schema DDL, 15 test scenarios, cross-AI capture (§5.2), Reflection layer (§5.3), budget analysis. *The actual design specs being implemented.*
- **F** — `f-claude-diary-verified.md` — first-hand verified deep-dive on claude-diary (the project closest in intent). *Inspiration source with verified scoring.*
- **G** — `g-generative-agents-paper.md` — first-hand review of Park et al. 2023, including a plain-English summary at the top. *Architecture source; patterns adopted in Reflection layer.*

**Implementation plan:**
- **`plans/2026-05-17-worklog-end-to-end.md`** — Single-phase end-to-end implementation plan covering Memory + Surfaces + Reflection. 19 tasks with bite-sized TDD steps. Agent-driven execution recommended. **Start here when ready to build.**

**Pruned 2026-05-17 evening (recoverable via git history if needed):**
- ~~`b-market-scan.md`~~ — 33-project OSS scan. Key findings (claude-mem dominates; claude-diary closest in intent; white-space narrow) absorbed into this plan's TL;DR and into appendices F/G.
- ~~`e-adversarial-review.md`~~ — adversarial case. Six kill criteria + minimum-viable concession are folded into this plan's Track 2 design and "Kill criteria" section above.
- ~~`plans/2026-05-17-worklog-memory-capture.md`~~ — sub-project A plan, superseded by the single-phase end-to-end plan above.

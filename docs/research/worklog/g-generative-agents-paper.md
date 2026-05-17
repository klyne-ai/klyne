# Appendix G — Generative Agents Paper (Park et al. 2023)

**Source:** Park, O'Brien, Cai, Morris, Liang, Bernstein. *Generative Agents: Interactive Simulacra of Human Behavior.* UIST '23. arXiv 2304.03442.
**Local copy:** `/Users/mohitpatel/Desktop/Project/Generative Agents.pdf`
**Why in this research:** claude-diary's three-tier architecture explicitly draws from this paper. The paper is also the canonical academic source for the memory + reflection + retrieval pattern that any work-log design will inherit ideas from.
**Verification:** Read first-hand (pages 1–20 of the 22-page PDF) on 2026-05-17.

---

## Plain-English summary (read this first)

> *This section is for any reader (including future-you or a teammate) who wants the intuition before the technical depth. The rest of this appendix is the rigorous version.*

The paper is about making AI characters in a Sims-style town feel like real people, not scripted robots. To pull that off, each character needs the three things every real person uses to live their life: **memory, reflection, and planning.**

Forget AI for a second. These three words describe how a thoughtful person handles their week.

### 1. Memory — the diary

**Just a list of every event the character noticed, written in plain words.**

Like someone keeping a daily journal: *"8:00 am — made coffee. 9:15 am — saw Maria at the cafe, she looked tired. 11:00 am — boss said the deadline moved to Friday."* Nothing clever. Just raw notes about what happened.

### 2. Reflection — the Sunday think

**Sitting back, reading old diary entries, and writing down what you notice across them.**

Sunday evening, you read this week's journal and think: *"I notice I'm tired every Wednesday — what's going on? Maria has been quiet for three weeks, something's up. I keep saying yes to projects I regret — I need a rule about that."*

These insights are not in any single diary entry. They appear only when you look across many entries and **connect dots**. You then write those insights back into the journal so future-you remembers them.

### 3. Planning — what you'll do tomorrow

**Using both raw memory AND your reflections to decide what to do next.**

Based on memory ("deadline moved to Friday") + reflection ("I'm tired on Wednesdays") + current situation ("it's Monday"), you plan: Monday start early, Tuesday work hard, Wednesday light day, Thursday push, Friday ship. The plan is shaped by both raw facts and learned patterns.

### The paper's real example — Isabella's Valentine's Day party

The researchers told ONE character (Isabella, the cafe owner) "you want to throw a Valentine's Day party." That was it. They let the AI run for 2 sim-days. What happened:

- **Memory:** Isabella noticed Maria came into the cafe → wrote it down. Noticed Maria mentioned Klaus → wrote it down. Noticed Sam was busy with mayoral campaign → wrote it down.
- **Reflection:** Reading her memory, Isabella concluded *"Maria is dependable and helpful"* and *"I throw parties; I'm a social organizer."*
- **Planning:** Based on both, she planned — *morning: invite friends, afternoon: ask Maria to help decorate, evening: party 5–7pm.* She then went and did it. Maria invited her secret crush Klaus. 5 of 12 invited people showed up. **Nobody wrote that script.** The whole party emerged from the three-component loop.

That's the paper's point: with just memory + reflection + planning, AI characters generate believable life on their own.

### How this maps to klyne's work-log

| | Real person | Generative Agents paper | klyne work-log |
|---|---|---|---|
| **Memory** | Daily journal entries | Raw observations: *"John ate breakfast"* | Every commit, file edit, decision automatically recorded per session |
| **Reflection** | Sunday "what patterns do I see" | *"Klaus is dedicated to research"* | Weekly AI digest: *"you shipped 3 commits to payments, decided on Razorpay, deferred refunds to Q3"* |
| **Planning** | "Don't schedule big meetings Wednesdays" | *"Tomorrow: invite friends, decorate, host party"* | Next Monday's fresh session opens with: *"open question carried forward: build refund mechanism"* |

### The deep idea (one sentence)

**Raw notes alone aren't useful** (too much noise). **Reflection turns notes into wisdom.** **Wisdom shapes future behavior.** Skip any one of the three and the system feels dumb — exactly what the paper's ablation study proved (believability scores dropped sharply when reflection was removed).

That is why this paper matters for klyne's work-log decision: if klyne only saves raw session logs (memory) but never reflects on them, the logs become a junk pile. If klyne reflects but never feeds those reflections back into future sessions (planning), the wisdom is wasted. You need all three for the system to actually *help* you next Monday.

*(For the rigorous version of all of the above — retrieval formulas, ablation numbers, schema deltas, failure modes — keep reading.)*

---

## What the paper actually proposes

The paper introduces a *generative agent architecture* for believable human behavior in a sandbox simulation (25 agents in a Sims-style town). The architecture has three load-bearing components:

### 1. Memory Stream
> "A long-term memory module that records, in natural language, a comprehensive list of the agent's experiences."

Each memory is a `memory object`:
- Natural-language description
- Creation timestamp
- Most-recent-access timestamp

Three types live in the stream: **observations** (raw events perceived), **reflections** (higher-level synthesised insights), **plans** (intended future actions).

### 2. Retrieval (the key formula)
Final retrieval score combines three signals, each normalised 0–1 via min-max, then weighted sum:

```
score = α_recency · recency + α_importance · importance + α_relevance · relevance
```

- **Recency** — exponential decay over time since last retrieval (decay factor 0.995 per sandbox-hour in the paper)
- **Importance** — LLM-rated 1–10 *at memory creation time* (so it's stored, not recomputed). The exact prompt is:
  > "On the scale of 1 to 10, where 1 is purely mundane (e.g., brushing teeth, making bed) and 10 is extremely poignant (e.g., a break up, college acceptance), rate the likely poignancy of the following piece of memory. Memory: <text> Rating: <fill in>"
- **Relevance** — cosine similarity between memory's embedding and the query's embedding

All αs set to 1 in the paper. Top-N retrieved memories that fit the LM context window are included in the next prompt.

### 3. Reflection
The synthesis layer. Two things to know:

**Trigger:** *not* time-based. Fires when **sum of importance scores of the 100 most recent observations exceeds a threshold (150 in the paper)** — i.e., the agent reflects when enough notable things have happened. In practice this fires "roughly two or three times a day."

**Process:**
1. Prompt LM with the 100 most recent records: *"Given only the information above, what are 3 most salient high-level questions we can answer about the subjects?"*
2. Use those questions as retrieval queries against memory stream
3. Prompt LM to *extract insights AND cite the specific records that served as evidence*
4. Store the insight as a reflection, with pointers to its evidence memories
5. **Reflections can themselves be reflected on**, forming a tree (leaves = observations, internal nodes = progressively more abstract synthesised reflections — see Figure 7 of the paper)

### 4. Planning
Hierarchical: starts with day-level plan (5–8 chunks), recursively decomposes into hour-level then 5–15-minute level. Plans live in the memory stream alongside observations and reflections, so subsequent retrieval considers them.

---

## What the paper empirically proves (Section 6 ablation)

This is the part most relevant to the work-log decision.

The authors ran a controlled ablation — same agents, same 2-day simulation, different memory architectures — and measured believability via interviews scored on a TrueSkill rating.

| Condition | TrueSkill rating |
|---|---|
| Full architecture (memory + reflection + planning) | **μ = 29.89** |
| No reflection | μ = 26.88 |
| No reflection, no planning | μ = 25.64 |
| Human crowdworker baseline | μ = 22.95 |
| No memory, no reflection, no planning | μ = 21.21 |

Cohen's *d* between the no-memory baseline and full architecture: **8.16** (eight standard deviations). Every ablation step degrades performance significantly.

**The single most important finding for klyne's work-log decision:** reflection (i.e., AI synthesis over accumulated memories) measurably beats raw memory access *in their domain*. This directly contradicts a piece of Agent E's argument that "AI on pre-summary < AI on raw transcripts." It does *not* fully refute Agent E (see below) but it tilts the empirical evidence.

---

## Failure modes observed (also empirical)

Even with the full architecture, the paper documents three failure classes:

1. **Hallucinated embellishments.** Quote: *"Isabella also added that 'he's going to make an announcement tomorrow,' even though Sam and Isabella had not discussed any such plans."* This is exactly the user's stated fear ("junk buries important things") and the paper validates it as a real risk.
2. **Misclassification from missing world-norms.** Things implicit in the environment (e.g. "this is a one-person bathroom") not captured in NL led to wrong behaviour.
3. **Instruction-tuning artefacts.** Agents became overly polite/cooperative, drifting from their stated personalities.

The paper also notes: *"synthesizing an increasingly larger set of memory not only posed a challenge in retrieving the most relevant pieces of information but also in determining the appropriate space to execute an action"* — i.e., **more memory makes retrieval harder**. Confirms the suppression-rule discipline in Agent D's design.

---

## Cost candor

The paper says directly:
> "The present study required substantial time and resources to simulate 25 agents for two days, costing thousands of dollars in token credits and taking multiple days to complete."

For klyne this is reassuring — one human + N sessions is much cheaper than 25 agents × 2 sandbox-days. But it does mean: every architectural extravagance (per-memory LLM importance scoring, hierarchical reflection trees, embedding-based relevance) costs tokens. Strategy C's bounded LLM use is consistent with the paper's "future work: cost-effective architecture" callout.

---

## Direct mappings to klyne's work-log design

| Generative Agents pattern | klyne equivalent / opportunity |
|---|---|
| Memory stream | Already exists: `messages` + `stop_summaries` + `work_spans` + `decisions` |
| Observation memory | `messages`, `stop_summaries` rows |
| Reflection memory | The proposed `worklog_entries.ai_drafted_summary` field (Agent D's schema) |
| Plan memory | Open question — klyne does not currently store intended-future-actions as first-class memory. Could be a separate "open question / deferred" table. |
| Recency score | Trivial — `ts` columns already exist |
| Importance score | **NOT in current design.** Major missed pattern. See "Borrow #1" below. |
| Relevance via embeddings | Could add — klyne has FTS but no vector index. Could be cheap with a local ONNX model. |
| Reflection trigger via importance-sum threshold | **NOT in current design.** Currently Agent D fires on commits/PR/stop/precompact (event-driven). The importance-sum pattern is *complementary* — see "Borrow #2." |
| "What questions can we answer?" reflection prompt | Better than naive "summarize this." See "Borrow #3." |
| Evidence citation in reflections | Already partially in Agent D's schema (`session_ids_json`, `decision_ids_json`, `files_json`). **Should be promoted to first-class invariant.** |
| Reflection-of-reflections (tree) | Maps to: per-session entries → daily digest → weekly synthesis → quarterly architectural patterns |

---

## Five patterns klyne should borrow from the paper

### Borrow #1 — Importance score per work-log candidate, stored at write time
Add an `importance` integer 1–10 column to `worklog_entries`. Use deterministic heuristics (commit landed = 7, security-relevant change = 9, lint-only commit = 2, decision_recorded = 8, runbook_accepted = 5, etc.) rather than an LLM call. Use it for:
- **Throttling** — only entries above threshold are surfaced in default views
- **Retrieval ranking** — `/klyne:recap` results sorted by `α_recency · recency + α_importance · importance + α_relevance · relevance`
- **Reflection triggering** — see #2

### Borrow #2 — Importance-sum threshold as a secondary trigger
Agent D currently triggers reflection/digest on event boundaries (commit, PR, Stop, PreCompact). Add a third trigger: **when the importance sum of recent unreflected entries exceeds a threshold (klyne's analogue of 150), fire a digest**. Means:
- Slow weeks don't produce weekly digests just because Friday arrived
- A single load-bearing decision (importance 9) can fire a reflection on its own without waiting for stop hook
- High-decision days get reflected immediately; routine days don't

### Borrow #3 — "What questions can we answer?" as the synthesis prompt
The paper's reflection step does NOT just say "summarise this." It says "what 3 high-level questions can we answer from these records?" then uses those as retrieval queries. For klyne's AI-enrichment pass (Agent D Strategy C, step 2), use the same pattern:
- Don't prompt: "summarise this episode"
- Do prompt: "what 2–3 questions could a future session usefully ask about this episode?" → use those as title/sections of the entry
- Result: entries are pre-shaped for retrieval, not pre-shaped for narration

### Borrow #4 — Citation as a first-class invariant
Every reflection in the paper carries pointers to its evidence memories. klyne should treat this as a hard invariant: every `ai_drafted_summary` field carries a non-empty `evidence_ids_json` referencing the underlying session/message/decision/commit IDs. Same audit benefit as Agent D's `require_signal` rule, applied to the AI-generated text layer. If the LM claims a fact without an evidence pointer, the row is rejected.

### Borrow #5 — Reflection tree, not flat reflections
The paper synthesises reflections from reflections. claude-diary stops at one layer. klyne should plan for three layers from day one:
- L0: per-episode worklog entries (observations, what happened)
- L1: per-day digest (synthesised from L0 — what shipped today, what was decided)
- L2: per-week / per-project summary (synthesised from L1 — themes, drift, open questions)

L1 and L2 are rendered, not stored — but the *prompting* pipeline should be designed to be recursive from the start.

---

## What the paper challenges in Agent E's adversarial review

Agent E's argument 1.2 was: "AI on pre-summary ≤ AI on raw transcripts." The paper directly tests this:
- "Full architecture" = AI on retrieved memories *including reflections*
- "No reflection" = AI on retrieved raw observations only
- Full architecture *measurably* beats no-reflection (μ=29.89 vs μ=26.88, Cohen's d substantial)

**However the transfer is partial:**
- The paper's success metric is *believable behaviour* (e.g., does Isabella spread Valentine's Day invites convincingly?). klyne's success metric is *useful fact retrieval* (e.g., did the AI correctly recall the payment-flow decision from last Tuesday?). These are different tasks. The paper's evidence does NOT directly prove that pre-summaries beat transcripts *for fact recall*.
- The paper's no-reflection baseline still had access to raw memory with recency/importance retrieval. So the win is "reflection-augmented retrieval > raw-retrieval", not "summary blob > raw transcripts."

**Net effect on Agent E's argument:** weakened, not refuted. The empirical case for adding *reflection* (a synthesised layer over raw memory) is now stronger than Agent E allowed. But the empirical case for replacing transcripts with summaries is still untested and still risky. **The dual-track sprint plan survives unchanged** — Track 1's `/klyne:recap` is "AI on raw transcripts via retrieval," Track 2 adds the reflection layer to test if it beats Track 1.

---

## What the paper validates in Agent D's design

- **Strategy C (deterministic skeleton + AI prose) is consistent with the paper's pattern**: deterministic event detection (the importance heuristic) gates the LLM synthesis call. klyne can be *more* efficient than the paper because we have stronger deterministic signal (commits, file edits, hooks) than the paper had in its sandbox.
- **Suppression discipline is empirically necessary**: the paper observed "larger memory → harder retrieval" — failure mode #3 cited in Agent D's failure-mode table.
- **Reflection-tree design**: the paper's Figure 7 is the visual template for L0 → L1 → L2 rollups.
- **Citation requirement**: the paper's reflection-with-evidence pattern directly motivates Agent D's `session_ids_json`/`decision_ids_json`/`files_json` arrays in the schema.

---

## What the paper validates in the user's concerns

The user said: *"if it has a lot of junk and a lot of things get logged, then it could be complicated and an important thing can get missed."*

Paper Section 7.2: *"As a result, some agents chose less typical locations for their actions, potentially making their behavior less believable over time."* — i.e., as memory grew, retrieval degraded and behaviour got worse. **The user's concern is empirically supported.**

The user also said: *"Important things can get missed if we just dump everything."*

Paper hallucination observation: agents *embellish* memories — claim facts they don't have evidence for. **The user's concern is empirically supported** — the hallucination risk is real even with the full architecture.

---

## What the paper does NOT solve for klyne

1. **Cross-CLI / cross-tool merging.** The paper is one-agent-per-character in one sandbox. Nothing about merging Claude + Codex into a single intent stream.
2. **Project-rooted artifacts.** The paper has one memory stream per agent, no project boundary.
3. **Deterministic-first extraction.** The paper uses LLM for importance scoring at every memory write — expensive and non-reproducible. klyne can do better with deterministic heuristics for most events and reserve LLM for the genuinely-ambiguous ones.
4. **Load-bearing change detection.** Nothing in the paper says "if file matches `**/migrations/**`, flag as schema change." That kind of code-aware classification is klyne's to design.

---

## Recommended schema additions (from this paper, on top of Agent D's schema)

Two additions to Agent D's proposed `worklog_entries` schema:

```sql
ALTER TABLE worklog_entries ADD COLUMN importance INTEGER NOT NULL DEFAULT 5;     -- 1-10, set at write time, deterministic via event-tag heuristics
ALTER TABLE worklog_entries ADD COLUMN last_accessed_at INTEGER NOT NULL DEFAULT 0; -- updated when retrieved; powers recency decay
```

And one new table for the reflection tier:

```sql
-- 018_worklog_reflections.sql
CREATE TABLE IF NOT EXISTS worklog_reflections (
    id              TEXT    PRIMARY KEY,
    ts              INTEGER NOT NULL,
    project_path    TEXT    NOT NULL DEFAULT '',
    tier            INTEGER NOT NULL,            -- 1 = daily, 2 = weekly, 3 = quarterly
    title           TEXT    NOT NULL,
    body_md         TEXT    NOT NULL,
    evidence_entry_ids_json    TEXT NOT NULL DEFAULT '[]',  -- L1 cites L0 entries
    evidence_reflection_ids_json TEXT NOT NULL DEFAULT '[]', -- L2 cites L1 reflections
    importance      INTEGER NOT NULL DEFAULT 5,
    summary_source  TEXT    NOT NULL DEFAULT 'ai',
    state           TEXT    NOT NULL DEFAULT 'proposed',
    state_changed_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_worklog_reflections_project_ts ON worklog_reflections(project_path, ts DESC);
CREATE INDEX IF NOT EXISTS idx_worklog_reflections_tier      ON worklog_reflections(tier);
```

**Citation invariant:** rows in `worklog_reflections` MUST have at least one non-empty evidence array. Enforce via CHECK constraint or write-path predicate. This is the paper's audit pattern made enforceable in SQLite.

---

## Bottom line for the work-log decision

The paper does NOT change the dual-track sprint recommendation in `00-plan.md`. It does:

1. **Strengthen the case for adding a reflection layer** (Track 2), because the paper empirically shows reflections-over-memories beats raw-memories in their domain — partial transfer to klyne's domain but the evidence is real.
2. **Provide concrete patterns to borrow** in Track 2's design: importance scoring, importance-sum triggers, "what questions can we answer" synthesis prompts, citation invariants, reflection trees.
3. **Validate the user's core concern** (junk degrades retrieval; LLMs hallucinate) with empirical evidence from a peer-reviewed study.
4. **Validate Agent D's suppression-rule discipline** as empirically necessary.
5. **NOT solve** the cross-CLI, project-rooted artifact, deterministic-first, or change-class-detection gaps — those remain klyne's white-space.

If Track 2 happens, do the importance-score + reflection-tree pattern from day one. If it doesn't, the "what questions can we answer" prompt pattern should still inform `/klyne:recap`'s synthesis prompt in Track 1.

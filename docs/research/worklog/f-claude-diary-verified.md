# Appendix F — claude-diary Verified Comparison

**Status:** First-hand verified 2026-05-17 (read repo README + design blog directly, not via agent).
**Repo:** https://github.com/rlancemartin/claude-diary
**Design blog:** https://rlancemartin.github.io/2025/12/01/claude_diary/
**Stars / license:** 368 ⭐ / MIT
**Why this appendix exists:** Of the projects surfaced by the original market scan, claude-diary is the closest in *intent* to the work-log feature being designed. The original chat synthesis scored it 75% against the user's shape. This appendix re-scores it against the user's 10 stated criteria using verified facts and finds the real match is **~55%**.

---

## What claude-diary actually does (verified)

**Three-tier architecture** (Generative-Agents-inspired):

1. **Capture (Tier 1 — Diary Creation)**
   - Triggered by `/diary` slash command (manual) OR auto-fires on Claude Code's **PreCompact hook** (right before context compaction)
   - Each entry is a Markdown file with structured sections:
     - task summary
     - work done
     - **design decisions**
     - user preferences
     - code review feedback
     - challenges
     - solutions
     - code patterns
   - Stored as `~/.claude/memory/diary/YYYY-MM-DD-session-N.md`

2. **Reflect (Tier 2 — Pattern Analysis)**
   - Triggered by `/reflect` slash command (**manual only** — author's deliberate choice: *"I wanted to review the proposed updates before writing them to the CLAUDE.md file"*)
   - Reads accumulated diary entries
   - Identifies recurring patterns and rule violations
   - Reads current `~/.claude/CLAUDE.md` and proposes updates
   - Output stored as `~/.claude/memory/reflections/YYYY-MM-reflection-N.md`
   - Uses `~/.claude/memory/reflections/processed.log` to avoid duplicate analysis

3. **Persist (Tier 3 — Instruction Updates)**
   - Synthesized rules merged as one-line bullets into `~/.claude/CLAUDE.md` (**user-level**, not per-project)
   - This file is auto-loaded by Claude Code into every session

**Extraction method:** Pure LLM. The slash commands prompt Claude to reflect on conversation history and write entries.

**Supported CLIs:** Claude Code ONLY. The plugin is built specifically for Claude Code's hook system and memory architecture.

**Retrieval:** Via Claude Code's native CLAUDE.md auto-loading. No MCP, no FTS, no API, no situational/proactive injection.

**Author-stated limitations** (direct from repo notes):
- No project-level memory tiers
- No official session metadata API (uses JSONL parsing as workaround)
- No adaptive automatic triggering of reflection
- No proactive memory retrieval based on current context
- No cross-project pattern detection

---

## Scorecard against your 10 criteria

| # | Criterion | Score | Verified notes |
|---|---|---|---|
| R1 | **Auto-capture** | ◐ | Diary entries auto-fire on PreCompact hook. Reflection step is intentionally MANUAL — author wants to review before persistence. So half-auto. |
| R2 | **Cross-session synthesis** | ✓ | `/reflect` is exactly this — analyses accumulated diary entries to find patterns. |
| R3 | **Root-level Markdown artifact** | ✗ | Everything lives in `~/.claude/memory/`. The CLAUDE.md it updates is **user-level**, NOT per-project. Cannot have a `WORKLOG.md` at repo root. Author explicitly lists "no project-level memory tiers" as a limitation. |
| R4 | **Cross-CLI** | ✗ | Claude Code only. Codex sessions invisible. |
| R5 | **Structured types** | ◐ | Sections inside the Markdown (decisions, challenges, solutions, code patterns) — but these are document sections, not independently queryable first-class types in a database. |
| R6 | **Deterministic-first** | ✗ | Pure LLM. Re-running the same session can produce different entries. Not auditable, not reproducible. |
| R7 | **Local-first** | ✓ | All data on disk, no cloud. |
| R8 | **AI-queryable later** | ◐ | Only via Claude Code's CLAUDE.md auto-load mechanism. No FTS, no MCP query tool, no situational retrieval. A fresh session cannot ask "what did we decide about payments last week?" and get back structured facts — it only gets whatever made it into CLAUDE.md. |
| R9 | **Decisions as first-class** | ✓ | "Design decisions" is an explicit section in every diary entry. |
| R10 | **Load-bearing change detection** | ✗ | Does NOT flag schema/payment/auth/dependency changes specifically. Captures broad behavioural patterns instead (commit style, testing practices, agent design preferences, git workflows). |

**Tally:** 3× ✓, 3× ◐, 4× ✗ → **~55% match against your stated shape.**

(The chat synthesis from the original research called it 75% — that was a generic "AI memory feature" score, not a score against your specific requirements. Corrected here.)

---

## What claude-diary covers from your wish-list

1. **Auto-capture on PreCompact** — clever timing: it fires precisely when context is about to be lost. klyne could borrow this trigger pattern.
2. **"Design decisions" as a dedicated section** in every entry — the closest existing approach to "log when we decided to add a new payment method."
3. **Cross-session synthesis via `/reflect`** — accumulated raw entries → curated patterns → persistent rules. The three-tier architecture itself is well-designed and worth borrowing.
4. **User reviews proposed updates before persistence** — no surprise CLAUDE.md rewrites. This matches your "ask the user, or have it as a hybrid" intuition from the original prompt.
5. **One-line rule distillation** — keeps the persistent memory file compact and readable instead of bloated.
6. **MIT license** — could be forked / studied freely.

---

## What claude-diary does NOT cover that you wanted

1. **No project-root artifact.** Everything is user-level (`~/.claude/memory/`). You cannot share the log with teammates. You cannot have per-project diaries. You cannot `cat WORKLOG.md` in a repo. **This is explicitly a stated limitation** by the author ("no project-level memory tiers as current capability"). Your "client.md at the root level" instinct is unsupported.

2. **Claude Code only.** Your Codex sessions are invisible. Your stated requirement for cross-CLI ("irrespective of codex and plot, so it would be a client log feature, whatever it is, irrespective") is unmet. Adding Codex support would require a fork + significant rework of the hook system.

3. **No cross-project pattern detection.** Author-stated limitation. You said "across every project also" — claude-diary cannot do this. Each session is captured but rules synthesised into one user-wide CLAUDE.md don't track which project they came from.

4. **No load-bearing change detection.** Will NOT flag "you touched a migration", "you changed payment flow", "you modified auth code", or "you added a new dependency." Captures broad behavioural patterns instead. Your example use case ("if we have introduced a certain new payment method or if it is the payment method or any schema change we have made, a feature we introduce") is not what this tool does.

5. **No structured query API / no MCP.** A fresh AI session cannot ask "what decisions did we make about payments in this repo over the last 30 days?" and get back facts. It can only read whatever happens to be in CLAUDE.md at session start. Your "user will use AI to analyse all these logs" workflow has no programmatic surface.

6. **No deterministic extraction.** Pure LLM. The same session re-processed can yield different entries. Not auditable. If you wanted to prove to a teammate or a compliance reviewer "this is what the AI did," you cannot — the extraction is non-reproducible.

7. **Reflection is manual.** `/reflect` only runs when you remember to run it. Easy to forget for weeks. Nothing is scheduled or threshold-triggered. The synthesised rules in CLAUDE.md will drift behind reality.

8. **No proactive retrieval based on current context.** Author-stated limitation. If you're about to make a payment-related change, claude-diary does NOT notice and surface prior payment decisions. It only loads CLAUDE.md (which is broad behavioural rules, not retrievable facts).

9. **No teammate / multi-user story.** User-level `~/.claude/` is fundamentally single-user. If "a new developer comes" (your original phrasing — "When a new developer comes, it should not be a hard thing for them to get the download"), claude-diary cannot transfer the project's log to them.

---

## Implications for klyne's design

If klyne builds the work-log feature, claude-diary establishes a useful **architectural prior** but does NOT remove the white-space:

| Pattern to borrow from claude-diary | Pattern klyne needs to add beyond it |
|---|---|
| Three-tier capture → reflect → persist | Per-project storage (project-rooted artifact, not user-rooted) |
| PreCompact hook auto-fires capture | Cross-CLI ingestion (already in klyne via the Codex connector) |
| Structured Markdown sections per entry | Structured database rows queryable via MCP — not just a Markdown document |
| User-review-before-write for persistence | Deterministic-first extraction with LLM only for paraphrase (per Agent D Strategy C) |
| One-line rule distillation | First-class change-class detection (migration / dependency / auth / payment-flow) |
| `/diary` and `/reflect` slash commands | Proactive context-aware retrieval (surface relevant past decisions when you're about to repeat them) |

claude-diary is the project klyne can **learn the most from architecturally** while NOT being competed with directly — the two would coexist if both shipped, with claude-diary as a user-level Claude-only behavioural-rule synthesiser and klyne as a per-project cross-CLI structured ledger.

---

## How I verified this

Two WebFetch calls on 2026-05-17:
- `https://github.com/rlancemartin/claude-diary` — extracted README facts (stars, license, triggers, storage paths, sections, supported CLIs, retrieval mechanism, stated limitations)
- `https://rlancemartin.github.io/2025/12/01/claude_diary/` — extracted design philosophy (three-tier architecture description, manual/auto split, file paths, stated limitations)

Both fetches returned consistent facts. No fabrication risk that I can identify.

---

## Bottom line

claude-diary is the **closest existing project in intent**, but its **shape is wrong for your stated requirement** in three load-bearing ways: user-level storage (not per-project), Claude-only (no cross-CLI), and pure-LLM extraction (not auditable). At ~55% match it does not solve your problem out of the box, but it provides the strongest architectural reference for what klyne should build *differently*.

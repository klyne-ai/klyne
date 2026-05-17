# Appendix D — Trigger Design and Signal-vs-Noise

**Agent:** general-purpose, design-research focused
**Goal:** Design the WHEN and WHAT of capture, optimised for signal not noise. Address the user's stated North Star concern: "if a lot of junk gets logged, important things get buried."

---

# Designing the AI Work-Log: When and What to Log

**Research deliverable for klyne, 2026-05-17.** The brief: design the trigger and content model for an auto-captured cross-project, cross-tool AI work log, optimised for signal over noise.

This study leans on klyne's existing detectors (`internal/insights/runbooks.go`, `internal/insights/patterns.go`, `internal/contexthealth/classifier.go`, `internal/store/stop_summaries.go`, `internal/store/work_spans.go`, `cmd/klyne/session_end.go`) and on external prior art from Conventional Commits / release-please, ActivityWatch / WakaTime, and the Claude Code hooks surface.

---

## 1. Event Taxonomy — what counts as a "log-worthy moment"

The first design principle is: **the unit of logging is not the tool call**. A single Edit means almost nothing. The unit is the **work episode** — a chain of related turns that ended in a *commitment* (commit, PR, decision, abandonment, hand-off). Klyne already has the right primitive — `work_spans` (migration 014) — where a span runs from session-start (or previous close event) to a git commit / PR / exploration fallback. The taxonomy below feeds *episodes*, not entries; events promote an in-flight episode to log-worthy and decide its content.

Detectability codes: **D** = deterministic from JSONL / SQLite alone, **H** = deterministic heuristic with a tunable threshold, **A** = needs an LLM judge, **U** = needs user confirmation.

| # | Event | What it indicates | Detection method | D/H/A/U | FP risk | FN risk |
|---|---|---|---|---|---|---|
| 1 | `commit_landed` | A coherent unit of work shipped | Watch `git log --since=session_start` from session end, or hook into `Bash` calls whose normalised form is `git commit`. Re-use the work_spans commit bucket. | D | low | low — git is the ground truth |
| 2 | `pr_opened` | Larger shipped unit | Detect `gh pr create` / `git push -u origin HEAD` followed by GitHub URL in tool output. | D | low | medium — non-`gh` flows |
| 3 | `decision_recorded` | User pinned a load-bearing decision | Hook `mcp__klyne__record_decision` call (already exists). | D | low | low |
| 4 | `runbook_accepted` | User confirmed a workflow as canonical | `mcp__klyne__accept_runbook` outcome row. | D | low | low |
| 5 | `task_completion_marker` | User said "done", "ship it", "looks good" | Lexical match on last user turn at session end + assistant tool-state inactive ≥ 60s. | H | medium — phatic agreement | medium — silent satisfaction |
| 6 | `file_significantly_edited` | Real change, not a typo | (#edits to same path ≥ 2 AND total added-lines ≥ 20 AND not in suppression globs) OR (single Write of a new file ≥ 30 lines). | H | medium | low |
| 7 | `novel_pattern_introduced` | New module / new top-level dir / new dependency | Diff against `git ls-files` snapshot at session start; or detect `package.json` / `go.mod` / `requirements.txt` Edit. | D | low | medium |
| 8 | `scope_change` | User pivoted topics inside one session | Re-use `contexthealth.topicShifted` (Jaccard < 0.30 between first-N and last-N user prompts). | H | medium | low |
| 9 | `dead_end_abandoned` | Files touched then reverted, or branch abandoned without commit | Edits on path X followed by `git restore X` / `git checkout -- X` / a contradicting Edit reverting >70% of added lines. Or session ends with `git status --porcelain` empty after edits were performed. | H | medium | medium |
| 10 | `error_resolved` | Repeated tool error → ultimately succeeded | `ToolErrors` count on a tool ≥ 3 in a window, followed by ≥ 1 success of the same tool name targeting an overlapping file. Re-use `insights.ComputeSessionStats` ToolErrors. | H | medium | medium |
| 11 | `debug_loop_resolved` | Long failure chain, then green | Re-use `KindTightLoop` (`LongestRun ≥ 5`) AND final test/build command exits 0. | H | low | medium |
| 12 | `test_added_or_changed` | Verifiable behaviour | Edit/Write on path matching `*_test.{go,py,ts,rs}` or `tests/**` or `__tests__/**`. | D | low | low |
| 13 | `dependency_change` | Risk surface change | Edit to `go.mod`, `package.json`, `Cargo.toml`, `pyproject.toml`, `requirements*.txt`, lockfiles. | D | low | low |
| 14 | `migration_or_schema_change` | DB / wire-format change | Edit/Write under `**/migrations/**`, `*.sql`, `*.proto`, `openapi*.yaml`. | D | low | low |
| 15 | `security_relevant_change` | Crypto / auth / secrets surface | Path or content match against `auth`, `crypto`, `password`, `token`, `secret`, `oauth`, `permission`, `policy` (lexical, file paths first). | H | medium | medium |
| 16 | `runtime_config_change` | Could affect operator runbook | Edit to `Dockerfile`, `*.yaml` under `k8s/` `helm/`, `terraform/**`, `.github/workflows/**`, `Makefile`. | D | low | low |
| 17 | `tool_used_for_first_time` | New superpower entered the workflow | `mcp__klyne__list_decisions`-style: first occurrence of a tool name not seen in this project's last 90 days. | D | low | low |
| 18 | `compact_event` | Context was lost (or saved) | Already captured in `compact_events` table. Treat as an episode close. | D | low | low |
| 19 | `handoff_generated` | User explicitly closed an episode | `mcp__klyne__generate_handoff` invocation. | D | low | low |
| 20 | `long_idle_resume` | Crossing a session boundary | Gap > 30 min in message timestamps inside same `session_id`, or new SessionStart in same project. | D | low | low |
| 21 | `cost_spike` | Episode cost > $X or > N% of week | Already computable from `work_spans.tokens_*`. | D | low | low |
| 22 | `explicit_user_log` | User said "log this" / "remember this" | Slash command `/klyne:log <text>` or MCP tool. | U | none | none |
| 23 | `ai_judged_notable` | Catch-all for moments that don't fit a rule | One LLM call per episode close, asked "is anything in this episode worth keeping?". | A | medium — model is generous | medium — model is timid |
| 24 | `revert_or_rollback` | Negative-knowledge moment | `git revert`, `git reset --hard`, or `git checkout <older-sha> -- path` on a path edited in-session. | D | low | low |
| 25 | `external_artefact_produced` | Something left the box | Tool output URL (`https://github.com/.../pull/...`, `https://*.deploy*`, `https://*.vercel*`, `https://*.netlify*`). | D | low | medium |

**Reuse statement.** Rows 1, 2, 6, 9, 11–14, 16, 18, 20, 21, 24 are detectable from data klyne already stores. Rows 3, 4, 17, 19, 22 need only a hook into existing klyne calls. Rows 5, 8, 10, 15 are heuristic and warrant per-rule tests in `insights/`. Only row 23 needs an LLM. That ratio — 24 deterministic events vs. 1 AI-judged — is the *foundation* of the noise-suppression argument.

---

## 2. Recommended Trigger Architecture

The single most important design choice: **anchor logging on the closing event of an episode, not on individual turns.** This mirrors what release-please does — commits accumulate, the release event is when noise becomes signal. The same logic applies here.

### Primary trigger: `git commit` lands

A commit is the closest thing to ground truth a developer ever produces. Conventional Commits research is unambiguous on this point — structured commits give downstream tools (and AIs) "clear signal about which changes are user-facing features, which are bug fixes, and which are internal noise." When a commit lands inside a session, klyne should:

1. Read the commit message and changed files.
2. Walk back from commit-time to the previous commit / session-start to identify the episode boundary (already implemented in `work_spans` "commit bucket").
3. Roll up every event (#1–#25) that fired inside that window into one log entry.

### Secondary triggers (each writes a log entry on close)

In strict precedence order, so two triggers can never fire for the same window:

1. **PR opened** (#2) — closes a multi-commit episode at the PR boundary.
2. **`/klyne:log` explicit user command** (#22) — always wins, never deduped.
3. **Stop hook with non-trivial activity** (the existing `klyne session-end` path) — closes any in-flight episode that did not end at a commit. This is the "exploration bucket" that already exists in `work_spans`.
4. **PreCompact** — captures what would otherwise be lost. Klyne already has `hook_precompact.go`; reuse it.
5. **Long idle resume** (#20) — implicit close of the previous episode at 30-minute idle.

### Anti-triggers (suppress writing a log entry)

* **Episode contains zero file edits AND zero bash commands beyond `ls` / `cd` / `git status`** → drop. Just navigation, not work.
* **Episode wall-time < 90 s AND tool count < 5** → drop. Too small.
* **Episode contains only Read tool calls** → drop. Browsing, not work.
* **Episode ends with `git status --porcelain` empty AND no commit landed** → drop or downgrade to a tiny "explored X" entry (see noise rules below).

### Why not per-turn or per-tool-call

Per-turn triggers fail because most turns are noise. A user asking "what files are in this dir?" is one turn that should not generate a log entry. Per-tool-call triggers fail for the same reason at higher volume. Per-session triggers fail because branch-switching developers do three logical episodes in one Claude session, and one-shot scripters do half an episode across two sessions. The episode model — closed by commit / PR / explicit / Stop — is the only granularity that survives both patterns.

### Decision flow

```
on every PostToolUse event:
    if tool == Bash and command parses as `git commit`:
        close_episode(reason="commit")
    elif tool == Bash and command parses as `gh pr create`:
        close_episode(reason="pr")
    elif tool == mcp__klyne__generate_handoff:
        close_episode(reason="handoff")
    elif tool == mcp__klyne__record_decision:
        attach_to_episode(decision_id)

on every UserPromptSubmit:
    if idle_gap > 30min:
        close_episode(reason="idle_resume")
    if prompt matches /^\/klyne:log\b/:
        write_explicit_entry(text)

on Stop hook:
    if episode_open and not_trivial(episode):
        close_episode(reason="stop")

on PreCompact:
    close_episode(reason="precompact")
```

`close_episode` is the *only* function that writes a log row. Everything else just accumulates state. This single-writer rule is what keeps the log from filling with junk: there are five total places in the codebase where a log row can be created, all centralised in one function with one suppression policy.

---

## 3. Three Strategies Compared

### Strategy A — Deterministic-only

Every log entry is a roll-up of #1–#22 and #24–#25, generated by walking the session's messages, tool calls, and git events. Zero LLM calls. The entry's title is auto-generated from commit message (when present) or from the dominant verbs of the bash commands run (already implemented in `suggestRunbookName`).

* **Pros.** Free, fast, deterministic, replayable. Fully consistent with klyne's "local-first, no AI calls" charter that the insights package already enforces.
* **Cons.** Titles and summaries read mechanically. Cannot capture the "why" of a decision unless the user wrote it into the commit message or a `record_decision` call.
* **Predicted S/N.** High signal, low *expressive* signal. The log is correct but feels like a build log.

### Strategy B — AI-only

Every Stop hook runs an LLM call: "given this session transcript, write a work-log entry or return null." The model is the judge of both trigger and content.

* **Pros.** Captures intent, nuance, the "why".
* **Cons.** Token cost per session-end (estimated below — ~$0.01–$0.04). Latency on the Stop hook. Hallucination risk — model can claim "fixed the auth bug" when nothing in fact was fixed. Hardest to test because the model floats. Two identical sessions can produce different logs. Cuts against klyne's positioning ("the only thing Claude can do is in-session summary" — per the user's memory note).
* **Predicted S/N.** Medium signal, medium noise — the model will be *too generous* with what counts as notable, exactly the user's stated fear.

### Strategy C — Hybrid with deterministic skeleton + AI-drafted prose + user confirmation

This mirrors klyne's existing runbook flow (proposer is deterministic; user accepts/edits/dismisses). The work-log version:

1. Deterministic trigger (§2) decides *whether* an episode is log-worthy and writes a "skeleton entry" — facts only: commit SHA, files, decision IDs, runbooks accepted, fired-event tags.
2. *Optional* AI pass enriches title and one-paragraph "what & why" using only the data already in the skeleton + the last user prompt + the commit message. Capped at ~2K input tokens / ~150 output tokens per episode. The AI never *adds* facts; it only paraphrases.
3. *Optional* user-facing card surfaces the draft at next SessionStart (or in a dashboard) for accept / edit / dismiss. Dismiss writes the signature to a `worklog_dismissals` table — same pattern as `runbook_dismissals`.

* **Pros.** Skeleton can never hallucinate; AI never decides what counts; user is the final arbiter. The cost cap is small and predictable. Failure mode "AI invented a fact" is structurally prevented because the AI input is the skeleton, not the raw transcript.
* **Cons.** Three moving parts to test. AI pass requires a token budget. User has to accept/dismiss occasionally (klyne already trains users for this with runbooks, so adoption cost is near zero).
* **Predicted S/N.** Highest. The skeleton is the floor; the AI lifts only readability; the user is the noise filter of last resort.

### Recommendation: Strategy C.

The user's stated concern — "important things get missed in junk" — is *not* about expressiveness. It is about the trigger being too generous. Strategy C's deterministic skeleton makes the trigger the cheapest possible to audit: every entry can be explained by "X event fired at time Y on commit Z." The AI pass is opt-in and bounded, and the user-confirmation step matches an interaction pattern they already know.

---

## 4. Capture Mode Recommendation

The three spec candidates restated:

* **(a)** Ask user at session end.
* **(b)** Skill / MCP decides autonomously.
* **(c)** AI drafts candidate, user accepts/edits/dismisses (runbook-style).

**Pick (c)**, with two refinements:

1. **Default to silent draft.** The user does not get prompted *at session end*. The draft is written to `worklog_entries` in `state=proposed`. The user sees pending proposals when they next run `/klyne:bootstrap` or open the dashboard. This avoids the friction failure mode where the user disables the feature because it interrupts at the worst possible moment (right after a flow state).
2. **Auto-accept after N days OR Y entries** with the option to bulk-edit. After 7 days unread, a `proposed` entry rolls up into the weekly summary as accepted. This is the only way the log scales — if the user has to manually accept 30 entries a week, they will not.

Mode (a) fails on the "friction" failure mode. Mode (b) fails on the "AI hallucination disagrees with reality" failure mode — there is no human in the loop to catch a fabricated outcome. Mode (c), with silent-draft and timed auto-accept, threads both needles.

---

## 5. Schema Proposal

Minimal viable schema. Single primary table, two summary tables, one suppression table. Mirrors klyne's existing migration conventions.

```sql
-- 015_worklog_entries.sql
CREATE TABLE IF NOT EXISTS worklog_entries (
    id              TEXT    PRIMARY KEY,          -- uuid, generated by the writer
    ts              INTEGER NOT NULL,             -- epoch-ms, end of episode
    opened_at       INTEGER NOT NULL,             -- epoch-ms, start of episode
    project_path    TEXT    NOT NULL DEFAULT '',
    cli             TEXT    NOT NULL DEFAULT 'claude',
    git_branch      TEXT    NOT NULL DEFAULT '',
    commit_sha      TEXT    NOT NULL DEFAULT '',  -- '' for non-commit episodes
    pr_number       INTEGER NOT NULL DEFAULT 0,
    work_span_id    INTEGER NOT NULL DEFAULT 0,   -- FK to work_spans.id when present

    -- Closing event in §2 precedence order: commit | pr | handoff | explicit | stop | precompact | idle_resume
    close_reason    TEXT    NOT NULL,

    -- Skeleton — deterministic, never written by AI.
    title           TEXT    NOT NULL,             -- 1 line, ≤ 80 chars
    summary         TEXT    NOT NULL DEFAULT '',  -- optional AI-drafted paragraph
    summary_source  TEXT    NOT NULL DEFAULT 'deterministic',  -- 'deterministic' | 'ai' | 'user'
    body_md         TEXT    NOT NULL,             -- full Markdown render

    -- Roll-up data, all JSON arrays of strings/ints.
    session_ids_json    TEXT NOT NULL DEFAULT '[]',
    decision_ids_json   TEXT NOT NULL DEFAULT '[]',
    files_json          TEXT NOT NULL DEFAULT '[]',  -- final, deduped list, capped at 25
    runbook_ids_json    TEXT NOT NULL DEFAULT '[]',
    event_tags_json     TEXT NOT NULL DEFAULT '[]',  -- e.g. ["commit_landed","test_added","scope_change"]
    external_urls_json  TEXT NOT NULL DEFAULT '[]',

    -- Cost roll-up — keep raw tokens; render USD via live pricing table.
    tokens_in       INTEGER NOT NULL DEFAULT 0,
    tokens_out      INTEGER NOT NULL DEFAULT 0,
    tokens_cached   INTEGER NOT NULL DEFAULT 0,
    msg_count       INTEGER NOT NULL DEFAULT 0,

    -- State machine for the runbook-style accept/dismiss flow.
    state           TEXT    NOT NULL DEFAULT 'proposed',  -- proposed | accepted | dismissed | auto_accepted
    state_changed_at INTEGER NOT NULL,

    -- Suppression key. SHA1(close_reason || ":" || commit_sha || ":" || sorted_files).
    signature       TEXT    NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_worklog_project_ts     ON worklog_entries(project_path, ts DESC);
CREATE INDEX IF NOT EXISTS idx_worklog_state          ON worklog_entries(state);
CREATE INDEX IF NOT EXISTS idx_worklog_signature      ON worklog_entries(signature);
CREATE INDEX IF NOT EXISTS idx_worklog_commit_sha     ON worklog_entries(commit_sha);

-- 016_worklog_dismissals.sql -- mirrors runbook_dismissals
CREATE TABLE IF NOT EXISTS worklog_dismissals (
    signature    TEXT NOT NULL,
    project_path TEXT NOT NULL DEFAULT '',
    ts           INTEGER NOT NULL,
    PRIMARY KEY (signature, project_path)
);

-- 017_worklog_rollups.sql -- daily/weekly/project summaries are VIEWS, not tables.
CREATE VIEW IF NOT EXISTS worklog_daily AS
  SELECT
    project_path,
    date(ts/1000, 'unixepoch')           AS day,
    count(*)                              AS entries,
    sum(tokens_in)                        AS tokens_in,
    sum(tokens_out)                       AS tokens_out,
    group_concat(distinct commit_sha,' ') AS commits,
    group_concat(title, char(10))         AS titles
  FROM worklog_entries
  WHERE state IN ('accepted','auto_accepted')
  GROUP BY project_path, day;
```

Rollups are *views*, not denormalised tables — klyne's existing rendering layer (see `insights/status.go::RenderStatusAsMarkdown`) renders these on demand. No second source of truth.

The `body_md` field is what users see. Markdown is mandatory because the existing `stop_summaries.summary` already renders as Markdown into `/klyne:bootstrap`, and the new log needs to chain into the same pipeline. JSON is the storage form for arrays, never the render form.

### 5.1 Storage strategy: SQLite source of truth + conditional weekly Markdown export

*(Added 2026-05-17 after architectural review with the maintainer.)*

**Internal storage stays SQLite.** The `worklog_entries` table above is the single source of truth. All MCP queries, FTS search, indexes, JOINs to `decisions`/`work_spans`/`messages`, and atomic writes use the DB. Klyne's whole stack is SQLite-native; this stays consistent.

**External surface adds a per-project weekly Markdown export.** Path: `<project_root>/docs/worklog/YYYY-WW.md` (ISO week). Rendered from the `body_md` column for that (project, week) window.

**Conditional generation rule (this is the load-bearing part).** An export file is written ONLY if the (project, week) pair has ≥ 1 entry meeting both:
- `recap_visible = 1`
- `state IN ('accepted', 'auto_accepted')`

If a project had zero qualifying entries that week, **no Markdown file is created for that project that week.** Example: in week 2026-W20, oms-service has 8 entries → `oms-service/docs/worklog/2026-W20.md` written. partner-service had no commits and no decisions that week → `partner-service/docs/worklog/2026-W20.md` does not exist. This mirrors release-please's "no commits since last release → no release" convention and prevents the `docs/worklog/` folder from filling with empty placeholder files.

**Why hybrid, not pure-SQLite or pure-Markdown:**

| Lost if pure SQLite | Lost if pure Markdown |
|---|---|
| Survives klyne uninstall (logs stay as readable .md in git) | Sub-ms queries with indexes |
| Git-trackable per-project artifact | Atomic concurrent writes |
| Grep-able + Obsidian/Notion-ingestible | FTS5, JOIN to decisions/work_spans |
| Original "WORKLOG.md at repo root" instinct from the user | Schema enforcement |

Hybrid keeps both column. Cost: ~30 lines of Go for the renderer.

**Implementation surface:**
- CLI: `klyne worklog export-week --project X --week YYYY-WW` and `--all-projects` variant
- Scheduler: Sunday night per project, only fires for projects with activity that week
- Renderer is pure — reads from DB, writes to disk, never writes back. DB drift impossible.
- File is regenerable at any time from the DB — losing or hand-editing the .md is recoverable.

**Edge cases handled by the conditional rule:**
- A project goes dormant for months → no empty files accumulate
- A new entry lands after a week's file was already exported → re-export overwrites with the now-complete week
- A dismissed entry (`state='dismissed'`) is excluded from the export (won't pollute the human-facing surface)
- A user-edited .md is regenerable; losing manual edits is acceptable since the .md is treated as a view, not a source

### 5.2 Cross-AI capture — Claude Stop hook AND Codex episode detector

*(Added 2026-05-17 evening after architectural review confirmed cross-AI is foundational, not optional.)*

The Memory layer's single most important property is that **both Claude AND Codex sessions write entries to the same `stop_summaries` table**, distinguished only by the `cli` column. Downstream consumers (`recap_project`, `user_recap`, bootstrap injection, weekly MD export) are cli-agnostic by design.

| CLI | Capture mechanism | Implementation |
|---|---|---|
| **Claude Code** | Stop hook fires at end of every turn | Already exists. Extend the writer (T2.2a) to populate the new worklog columns (`recap_topic`, `importance`, `signature`, `ai_drafted_summary`) when suppression rules pass. |
| **Codex** | No hook system. Use idle-detection in the ingestion daemon. | New (T2.2b). Klyne's daemon already tails `~/.codex/sessions/*.jsonl`. When a session has been idle > 30 min, treat as ended → run the same suppression + importance scoring + writer as the Claude path. Write row with `cli='codex'`. |
| **Future CLIs** | Pattern is extensible — any tool whose transcripts klyne can ingest can be added by writing a detector. | Reuse the writer; only the detector is per-CLI. |

The downstream MCP tools query by `project_path` and time window — no `cli` filter unless asked. The MD export tags each entry with its source (`[claude]` / `[codex]`). This is the property that makes the worklog the *productivity diary* described in the original ask, not a Claude session companion.

### 5.3 Reflection layer (Generative Agents pattern)

*(Added 2026-05-17 evening; Option 2 confirmed by maintainer. Adopts the importance-scoring + reflection-tree + citation-invariant patterns from Appendix G.)*

The Memory layer alone produces entries. The Reflection layer produces *insights across entries*. Without it, the worklog is a list-of-things-that-happened — useful for recovery but not the wisdom-extraction pattern the Generative Agents paper proves measurably better (Cohen's d=8.16 between no-memory baseline and full architecture).

**New table `worklog_reflections`** (migration 016):
```sql
CREATE TABLE worklog_reflections (
    id              TEXT    PRIMARY KEY,
    ts              INTEGER NOT NULL,
    project_path    TEXT    NOT NULL DEFAULT '',
    tier            INTEGER NOT NULL,           -- 1 = daily, 2 = weekly, 3 = quarterly
    title           TEXT    NOT NULL,
    body_md         TEXT    NOT NULL,
    evidence_entry_ids_json    TEXT NOT NULL DEFAULT '[]',  -- CITATION INVARIANT: must be non-empty
    evidence_reflection_ids_json TEXT NOT NULL DEFAULT '[]',
    importance      INTEGER NOT NULL DEFAULT 5,
    summary_source  TEXT    NOT NULL DEFAULT 'ai',
    state           TEXT    NOT NULL DEFAULT 'proposed',
    state_changed_at INTEGER NOT NULL
);
```

**Trigger:** importance-sum threshold (paper's value: 150) OR weekly cron (Sunday night), whichever fires first per project.

**Synthesis prompt** (from paper §4.2, adapted): *"Given the following work-log entries from the past week, what 3 high-level insights can we infer? For each insight, cite the specific entries (by id) that serve as evidence."*

**Citation invariant:** rows MUST have non-empty `evidence_entry_ids_json`. Enforce via CHECK constraint OR write-path predicate. If the LM fails to cite, the row is rejected. This makes reflections **auditable** — every insight is traceable to source entries.

**Closing the Planning loop:** Reflections surface in `recap_project` / `user_recap` (alongside raw entries, distinguished as tier-1/2/3), **AND** get injected into bootstrap. Next session sees not just last-N entries but also the most recent weekly reflection. This is what completes Memory → Reflection → Planning. The AI-on-Monday reads last week's reflections (curated, cited) instead of (or in addition to) raw entries.

---

## 6. Noise-Suppression Rules — concrete, quantified

These are tested as plain Go predicates in `internal/insights/worklog/suppress.go` (proposed file). Every rule is named so the dismissal reason can be cited back to the user.

1. **`skip_trivial_size`** — Drop episodes with: (wall-time < 90 s) AND (tool calls < 5) AND (commit landed = false). 90 s is enough to do anything *real* and short enough to catch typo-fixes-then-commit (which keep their entry).
2. **`skip_read_only`** — Drop episodes with: `count(Edit) + count(Write) + count(MultiEdit) == 0` AND `count(Bash where command starts with git commit|gh|test|build) == 0`. Plain browsing.
3. **`skip_navigation_bash`** — When summarising bash commands inside an episode, drop those whose normalised primary verb is in `{ls, cd, pwd, cat, head, tail, file, stat, which, type, echo, pushd, popd}`. Reuse `insights.primaryVerb` + `shellBuiltinSkip` (already exists).
4. **`dedup_file_edits`** — Same `file_path` edited N times in one episode → one entry in `files_json` with `(N edits, +A −B lines)`. Cap `files_json` length at 25; surplus rolls up to `... +K more`.
5. **`skip_dependency_lock_noise`** — Files matching `**/package-lock.json`, `pnpm-lock.yaml`, `yarn.lock`, `go.sum`, `Cargo.lock`, `poetry.lock`, `*.snap` never count as "significantly edited" — they go into a `dep_lock_touched: true` boolean flag, not the files list.
6. **`skip_irrelevant_paths`** — Exclude from `files_json` and from `file_significantly_edited` detection: anything under `node_modules/`, `.git/`, `dist/`, `build/`, `target/`, `__pycache__/`, `.next/`, `.svelte-kit/`, `.venv/`, `vendor/`, `**/coverage/**`, `**/.DS_Store`. Same set used by file-heat detector — reuse.
7. **`skip_routine_lint_fix`** — Episode = ONE commit AND commit message matches `(?i)^(chore|style|fmt|lint|format)(\(|:)`. Downgrade to `state=auto_accepted` immediately with `summary_source=deterministic` and one-line `body_md` — no AI pass. Mirrors Conventional Commits' "hidden sections" pattern.
8. **`skip_dismissed_signature`** — If a prior identical signature is in `worklog_dismissals` for this project, do not write the entry. Same algorithm as `DismissedSignatureSet`.
9. **`dedup_repeated_signature`** — If the new signature matches the most recent `worklog_entries` row for this project AND that row is < 60 min old, *update* the existing row's `closed_at`, `tokens_*`, `files_json` rather than insert a new one. Stops e.g. an amend-commit from doubling the log.
10. **`cap_episode_span`** — A single episode may not span more than 24 wall-clock hours. If the open span exceeds that, force-close at the 24h mark with `close_reason=stop` and start a fresh episode. Prevents one stale open episode from absorbing days of unrelated work.
11. **`require_signal`** — Final gate: an entry must carry at least one of {commit_sha != "", pr_number > 0, decision_ids ≥ 1, runbook_ids ≥ 1, files_significantly_edited ≥ 1, explicit_user_log}. Else drop. This is the single hard rule the user can cite when asking "why is my log so quiet?" — every entry must point to a real artefact.

Predicted effect when run on the maintainer's own transcripts (extrapolating from `insights.DetectPatterns` calibration data): rule #1 alone drops ~60% of would-be entries; rules #2–#3 together drop another ~20%; rules #5–#7 trim the surviving entries down to a readable shape. End state: roughly **3–8 entries per active dev day**, vs. ~30–50 per session if we logged per tool-call.

---

## 7. Test Scenarios (acceptance tests)

These are the scenarios `internal/insights/worklog/worklog_test.go` must cover. Expected outcomes shown.

1. **50-turn auth refactor ending in one commit** — One entry. Title from commit message. `event_tags`: `[commit_landed, file_significantly_edited, test_added_or_changed, security_relevant_change]`. Files capped at 25, rest in `... +K more`. AI pass writes one-paragraph "why".
2. **3-turn typo fix, commit lands** — One entry. `event_tags`: `[commit_landed, routine_lint_fix]` if commit msg matches `chore|fmt|style`; auto-accepted immediately, no AI pass.
3. **45-min explore-then-abandon, no commit, ends with `git status` clean** — Zero entries. Suppressed by `skip_read_only` + `require_signal`.
4. **Long debug loop: 30 turns, `npm test` fails 6× then passes, no commit** — One entry. `event_tags`: `[debug_loop_resolved, error_resolved, test_added_or_changed?]`. No `commit_sha`. `close_reason=stop`. AI pass useful here.
5. **User switches between three feature branches in one Claude session** — Three entries (one per branch episode), if each ended at either a commit or a 30-min idle. Episodes split on `git checkout <branch>` boundary. Branch field populated.
6. **User runs `npm install` adding a new dep, no other change, commits** — One entry. `event_tags`: `[commit_landed, dependency_change]`. Lockfile suppressed; `package.json` in files; AI summary mentions the dep name.
7. **User invokes `/klyne:log "we decided to ditch Redis"`** — One entry, `close_reason=explicit`, never deduped, never suppressed.
8. **A 4-hour session where the user paused for lunch (90 min idle) and resumed** — Two entries. Idle-resume splits the episode at 30-min mark. The post-lunch span gets its own row.
9. **`git revert <sha>` of an earlier in-session commit** — Two entries. The original commit's entry stays; a new entry with `event_tags=[revert_or_rollback]` is added. Both rows reference each other via `commit_sha`.
10. **`/compact` fires mid-episode** — Episode closes at the PreCompact boundary with `close_reason=precompact`. Body includes "context was about to be lost — N tokens before, M after." If the post-compact work later commits, that's a fresh entry.
11. **Two repeated identical commits within 5 min (amend)** — One entry. Second close updates the first row's `closed_at` and `commit_sha`. `dedup_repeated_signature` fired.
12. **User accepts a runbook proposal in the middle of a session, no commit** — Entry only if other signal exists (file edits etc). If runbook-accept is the *only* signal, write an entry with `event_tags=[runbook_accepted]` because runbook acceptance is a curated user gesture.
13. **Session that touches only `node_modules/` (impossible normally, but a misconfigured agent)** — Zero entries. `skip_irrelevant_paths` zeros out the files list, then `require_signal` drops the row.
14. **Pure documentation PR — only `README.md` edited, commits, pushes, opens PR** — One entry. Tags include `pr_opened`; no `test_added` tag; AI pass produces a tight title.
15. **A session that produces a security-relevant change but no commit (user is exploring an auth bug)** — One entry, `close_reason=stop`, tag `security_relevant_change`. Surfaces in the weekly summary even without a commit because the security tag bumps it past `require_signal`.

These scenarios double as the marketing claim: "no junk, no misses." Each scenario can be cited in docs alongside the rule(s) that produced it.

---

## 8. Failure-Mode Table

| Failure mode | How design prevents it | Residual risk |
|---|---|---|
| Log fills with junk → important entries buried | Episode-not-turn granularity + 11 suppression rules + `require_signal` final gate + per-project `worklog_dismissals`. 24-of-25 event types are deterministic. | Heuristic rules can mis-tune; calibration tracked in `docs/eval/worklog/` per the contexthealth rubric precedent. |
| Trigger too rare → key moments missed | Five secondary triggers cover the gaps a primary-only design would leave. `/klyne:log` is an always-on escape hatch. Stop hook is the last-resort safety net. | A long session that never commits, never compacts, never idles, never stops cleanly. Mitigated by `cap_episode_span` 24h force-close. |
| Capture introduces friction → user disables | Silent-draft default + timed auto-accept. No interactive prompt at session-end. Surface in `/klyne:bootstrap` and the cockpit, where the user is already looking. | Users who never run bootstrap and never check the cockpit won't see drafts. Acceptable — auto-accept after 7d means the log still gets written. |
| AI-judged trigger costs tokens / latency | Triggers are 100% deterministic. Only the *prose draft* is AI, and only when the skeleton already exists. Per-episode AI input capped at 2K tokens, output at 150. Estimated $0.02 / 100 episodes / week per active user. | Heavy users hit a couple dollars a month for prose. If unwanted, `worklog.ai_enrich=false` config kills the call entirely; entries still get a deterministic title. |
| AI hallucinates outcomes that disagree with reality | AI's input is the skeleton (commit SHA, files, tags), not the raw transcript. The model paraphrases facts, never adds them. `summary_source` is recorded; user-edits flip it to `user` and bypass AI on future regenerations. | Model could still phrase "fixed the bug" when in truth the bug remains. Phrasing-only risk; user-accept loop catches it. |
| Identical signature logged twice (amend-commit, retry, etc) | `dedup_repeated_signature` rule, signed by close-reason + commit-sha + sorted-files. Updates instead of inserts when ≤ 60 min old. | None significant. |
| Stale episode absorbs unrelated work | `cap_episode_span` 24h force-close; `long_idle_resume` 30m split; topic-shift detector (#8) flags but doesn't split (would be confusing if a one-session two-topic refactor became two rows). | Single-session two-topic refactors land in one row with a `scope_change` tag rather than two rows. Acceptable per scenario #5. |
| User can't find a specific entry | Indexes on `(project_path, ts)`, `commit_sha`, and `signature`. Re-use the existing FTS table (migration 002) on `body_md`. | None — FTS already exists. |

---

## 9. Budget Analysis — token cost if AI-judged triggers were used

Two questions to budget: cost of the *trigger* AI call, and cost of the *enrichment* AI call. Both at Haiku-class pricing (the only sensible class for high-frequency local-first work), using approximate May-2026 numbers from Anthropic's pricing JSON (klyne already maintains `internal/cost/pricing.json`).

### If we used AI as the trigger (Strategy B — rejected, but bounded)

* Input per session-end: full transcript trimmed to ~10K tokens (typical session at klyne's cost-curve median).
* Output: 50 tokens of "yes/no + 1-line title".
* Sessions per active user per day: ~6 (`status.go` calibration).
* Haiku-tier cost: ~$0.001 per 1K input + ~$0.005 per 1K output.
* Per session: 10K × $0.001 + 50 × $0.005 ≈ $0.0103.
* Per user per week: 6 × 7 × $0.0103 ≈ **$0.43**.
* Per user per year: ~$22.

For a tool sold as "local-first, no telemetry, no AI calls in the hot path," $22/user/yr is meaningful — not catastrophic but visible. And it cuts directly against klyne's positioning (a Claude auto-memory entry — `project_klyne_positioning.md`, written by Claude per the auto-memory rules — captures the user's view that "in-session summary is the only thing Claude can do").

### Strategy C (recommended) — AI enrichment only

* Input per *accepted* episode: skeleton JSON (~1.5K tokens) + commit message + last user prompt ≈ 2K input total.
* Output: ~150 tokens of prose.
* Episodes per active user per day: ~5 (after suppression).
* Per episode: 2K × $0.001 + 150 × $0.005 ≈ $0.00275.
* Per user per week: 5 × 7 × $0.00275 ≈ **$0.10**.
* Per user per year: ~$5.

A 4× reduction vs. Strategy B, with strictly better signal-to-noise because the suppression rules already weeded out 80% of would-be episodes before the model is called. And if `worklog.ai_enrich=false`, the cost is exactly $0 and the entry still has a deterministic title and a fact-listing body.

### Cache wins to factor in

Strategy C's input is JSON skeleton — perfect for prompt-caching across episodes since the schema is identical and the model is constant. With Anthropic prompt-caching, the ~1K token "render this skeleton as a one-paragraph entry" system prompt is paid once per 5 min, so the marginal cost per episode drops further (a 60% reduction on input tokens is documented in the 2026 prompt-caching guides). Steady-state real-world cost is more like **$0.05 / user / week**.

This is the only place klyne should spend tokens — at the moment when the work has *already happened* and has *already been judged worth keeping by a deterministic rule*. Spending tokens on the trigger means paying to decide whether to pay; the recommended design pays only for readability.

---

## Closing principle

The user said: "junk buries important things." The deepest implication of that statement is that **the cost of a false-positive log entry is far higher than the cost of a false-negative**. A missed entry can be re-captured by `/klyne:log` or read off git. A logged-junk entry erodes the whole product's credibility — the next time the user opens the log and the third entry is "user ran `ls`," they stop trusting the whole table.

The recommended design treats every entry as having to *earn its row*. Twenty-four out of twenty-five event types are deterministic. Five triggers in strict precedence, one writer function, eleven suppression rules, one final `require_signal` gate. AI gets to suggest words, never facts, and never gets to decide whether an entry is born. The user is the final editor, the runbook flow they already know is the interaction model, and the cost stays under five cents a week.

That is what "signal not noise" looks like when written as code.

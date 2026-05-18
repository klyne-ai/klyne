# Claude Scenario Harness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Node-based scenario harness that drives real `claude -p` sessions through multi-turn flows and asserts that klyne's **worklog**, **memory**, **reflection**, and **runbooks** features work end-to-end. Output is PASS/FAIL per feature with evidence (transcripts, SQL state, markdown report).

**Architecture:** Pure Node (built-ins only, no native bindings). Each scenario is a Node module that (1) sets up an isolated project dir, (2) spawns one or more `claude -p` invocations, (3) queries `~/.klyne/klyne.db` via `sqlite3` CLI, (4) asserts expected state, (5) writes a markdown report. Worklog Stop hook already wired in user's env (proven via JSONL transcripts) — harness just provides cwd + waits for hook to fire.

**Tech Stack:** Node 20, sqlite3 CLI, `claude -p`, no npm dependencies.

**Location:** `test/scenarios/` at klyne repo root (sibling to `internal/`, `cmd/`). Self-contained with own `package.json` + README.

**Scope decisions:**
- **"Planning" feature → tested as `/klyne:runbooks`** (no `planning` surface exists in the codebase; runbooks is closest match). Flagged in handoff for user confirmation.
- **Tests run against user's live `~/.klyne/klyne.db`**, scoped by `project_path` to throwaway tmp dirs. Cleanup deletes own rows.
- **`claude -p` costs real $$.** Harness includes a `--smoke` flag that runs 1 cheap scenario only. Full sweep is the user's call.

---

## File Structure

```
test/scenarios/
├── package.json              # type:module, "test" script, no deps
├── README.md                 # what each scenario proves; how to run
├── runner.js                 # entry point: discovers scenarios, runs, reports
├── lib/
│   ├── claude.js             # spawn `claude -p`, capture text + session_id
│   ├── klyne.js              # sqlite3 helpers (project rows, reflections, cleanup)
│   ├── tmpdir.js             # mktemp project, git init, cleanup
│   ├── assert.js             # assertions with rich error context
│   └── report.js             # render scenario result -> markdown
├── scenarios/
│   ├── 01-worklog-signal-not-noise.js
│   ├── 02-worklog-suppression-rules.js
│   ├── 03-memory-cross-session.js
│   ├── 04-reflection-synthesis.js
│   └── 05-runbooks-recurring-commands.js
└── reports/                  # generated markdown per run (gitignored)
```

---

### Task 1: Scaffold `test/scenarios/` package

**Files:**
- Create: `test/scenarios/package.json`
- Create: `test/scenarios/.gitignore`
- Create: `test/scenarios/README.md`

- [ ] **Step 1:** Write `package.json` declaring `type: module`, Node 20+, `test` script invoking `node runner.js`, `test:smoke` for one cheap scenario.
- [ ] **Step 2:** Write `.gitignore` for `reports/` and `tmp/`.
- [ ] **Step 3:** Write README explaining purpose, scenarios, run commands, cost warning, and how to interpret reports.

### Task 2: Build `lib/claude.js` — claude -p wrapper

- [ ] **Step 1:** Function `runClaude({cwd, prompt, settings, allowedTools, timeoutMs})` that spawns `claude -p --output-format json --no-session-persistence --max-budget-usd 0.50 ...`, captures stdout, parses JSON, returns `{sessionId, text, costUsd, error}`.
- [ ] **Step 2:** Function `runClaudeChained({cwd, turns: [string]})` to allow multi-turn but each turn is a new `claude -p` (Stop hook fires between turns, which is what worklog tests need).
- [ ] **Step 3:** Defensive: surface any non-zero exit, kill on timeout, redact `--max-budget-usd` overrun.

### Task 3: Build `lib/klyne.js` — SQLite query helpers

- [ ] **Step 1:** `sql(query, params)` — shell out to `sqlite3 -json ~/.klyne/klyne.db`, parse result. Reject if klyne.db missing.
- [ ] **Step 2:** `getStopSummariesForProject(projectPath)` returning `[{session_id, ts, recap_visible, recap_topic, importance, signature, files_json, summary}]`.
- [ ] **Step 3:** `getReflectionsForProject(projectPath)` returning `[{id, title, body_md, evidence_entry_ids, importance, state}]`.
- [ ] **Step 4:** `cleanupProject(projectPath)` — delete from stop_summaries, worklog_reflections, decisions, sessions, messages where project_path matches.
- [ ] **Step 5:** `waitForStopHook(projectPath, sinceTs, {timeoutMs, expectRows=1})` — poll until N rows appear, fail with timeout context.

### Task 4: Build `lib/tmpdir.js` + `lib/assert.js` + `lib/report.js`

- [ ] **Step 1:** `tmpdir.js`: `makeProject({name})` → mktemp `/tmp/klyne-showcase-<name>-<random>`, git init, return path. `removeProject(p)`.
- [ ] **Step 2:** `assert.js`: `eq`, `truthy`, `match`, `gt`/`lt`, `includes` — all with custom error class that includes scenario name + actual/expected.
- [ ] **Step 3:** `report.js`: `renderReport({scenario, status, steps:[{name,status,detail,evidence}], elapsedMs, costUsd})` → markdown blob.

### Task 5: Build `runner.js`

- [ ] **Step 1:** Discover scenarios in `scenarios/*.js`, run each in sequence, isolate exceptions per scenario.
- [ ] **Step 2:** Support `--smoke`, `--only=<glob>`, `--keep-tmp` (skip cleanup for debug), `--no-claude` (dry-run, prints intent only).
- [ ] **Step 3:** On finish, write `reports/<timestamp>.md` aggregating all scenario reports + top-line PASS/FAIL counts.
- [ ] **Step 4:** Exit code: 0 if all pass, 1 if any fail. Print summary table to stdout.

### Task 6: Scenario 01 — Worklog signal-not-noise

**Premise:** A trivial session should produce no visible worklog entry; a meaningful session should produce one with importance ≥ 5.

- [ ] **Step 1:** Make 2 tmp projects (trivial-proj, meaningful-proj).
- [ ] **Step 2:** Trivial run: `claude -p "what is 2+2? answer in one sentence"` in trivial-proj. Wait. Query: assert either no row OR `recap_visible=0`.
- [ ] **Step 3:** Meaningful run: `claude -p "create hello.js with a greet(name) function, then add a second function farewell(name). Both should console.log."` in meaningful-proj. Wait. Query: assert `recap_visible=1`, importance ≥ 5, files_json non-empty.
- [ ] **Step 4:** Cleanup both project rows.
- [ ] **Step 5:** Report each assertion with the actual SQL row.

### Task 7: Scenario 02 — Suppression rules

**Premise:** Exercise specific rules from `internal/worklog/suppress.go`: skip-trivial-size (<90s + <5 tools + no commit), skip-read-only (zero edits without whitelisted bash).

- [ ] **Step 1:** Read-only session: `claude -p "list the words in the sentence 'fast brown fox'"`. Expect: no visible entry.
- [ ] **Step 2:** Lockfile-only edit session: simulate by asking claude to write only `package-lock.json` (unrealistic via prompt, so this case may need to be marked SKIP with reason if not testable through prompt alone).
- [ ] **Step 3:** Document SKIP cases clearly in report so user can decide if they need extra harness affordances.

### Task 8: Scenario 03 — Memory cross-session

**Premise:** klyne MCP server exposes `remember_memory` / `recall_memory`. Session A stores a fact via tool call. Session B (new session, same project dir) recalls it via tool call (or via `/klyne:bootstrap` injection if hooks enabled).

- [ ] **Step 1:** Session A: `claude -p "use the klyne MCP tool to remember that the staging API URL is https://api.staging.acme.test"` (project dir = tmp).
- [ ] **Step 2:** Verify klyne DB `decisions` table (or wherever memory is stored — verify schema first) has the row scoped to that project.
- [ ] **Step 3:** Session B (fresh): `claude -p "what's our staging API URL? use klyne tools to recall if needed."` in same dir.
- [ ] **Step 4:** Assert text response includes the URL string.
- [ ] **Step 5:** Cleanup.

### Task 9: Scenario 04 — Reflection synthesis

**Premise:** Given pending worklog entries, `/klyne:reflect` produces a `worklog_reflections` row with non-empty evidence IDs (citation invariant).

- [ ] **Step 1:** Seed 3-4 meaningful sessions in the same tmp project (re-use Scenario 01's "meaningful" prompt variants).
- [ ] **Step 2:** Verify `stop_summaries` has ≥3 visible entries.
- [ ] **Step 3:** Run `claude -p "/klyne:reflect"` in that project dir (slash command resolves to reflect skill).
- [ ] **Step 4:** Query `worklog_reflections` for the project. Assert ≥1 row exists, `evidence_entry_ids_json` is not `[]`, body_md is ≥ 100 chars.
- [ ] **Step 5:** Cleanup.

### Task 10: Scenario 05 — Runbooks (treating as planning surface)

**Premise:** When sessions repeat the same bash command pattern, `/klyne:runbooks` should surface it as a candidate.

- [ ] **Step 1:** Run 3 sessions in same tmp project, each performing `npm install && npm test`-like sequence (prompt claude to run them).
- [ ] **Step 2:** Run `claude -p "/klyne:runbooks"`.
- [ ] **Step 3:** Assert response text includes a runbook suggestion mentioning at least one of the repeated commands.
- [ ] **Step 4:** Cleanup.
- [ ] **Step 5:** Mark in report: "Treating runbooks as planning-like feature; please confirm in morning."

### Task 11: Smoke test + commit

- [ ] **Step 1:** Run `node runner.js --smoke` (executes Scenario 01 only). Verify PASS.
- [ ] **Step 2:** Inspect `reports/<timestamp>.md` for completeness.
- [ ] **Step 3:** Commit on worktree branch with descriptive message.

### Task 12: Handoff summary

- [ ] **Step 1:** Write `HANDOFF.md` at repo root listing: what was built, where, how to run, smoke results, open questions for user (planning vs runbooks; full sweep cost estimate; any blockers).

---

## Self-Review Notes

- Spec coverage: worklog (signal+suppression) ✓, memory ✓, reflection ✓, planning-as-runbooks ✓.
- Open: "planning" surface needs user confirmation — flagged in handoff and report.
- Risk: Slash commands like `/klyne:reflect` only resolve when MCP server is registered for that session. Need to verify klyne MCP is auto-loaded for `claude -p` runs in test dirs (or pass `--mcp-config`).
- Risk: Each `claude -p` run with hooks costs $0.05–0.30. Full sweep estimate: 5 scenarios × ~3 runs × ~$0.15 = ~$2.25. Acceptable for review; flag in README.

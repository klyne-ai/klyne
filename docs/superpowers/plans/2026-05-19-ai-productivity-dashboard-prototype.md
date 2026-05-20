# AI Productivity Dashboard — Prototype Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development. Steps use `- [ ]` checkboxes. Work happens in the worktree `/Users/mohitpatel/Desktop/Project/klyne/.worktrees/productivity-dashboard` on branch `feat/productivity-dashboard`. All `go`/`git` commands run from that worktree root.

**Goal:** A deterministic-first dashboard that fuses live git activity with klyne session telemetry into a Service → Branch → Topic productivity view for 2026-05-19, viewable in a rough page, with no LLM call at render.

**Architecture:** New `internal/productivity` package computes everything deterministically (repo discovery from sessions + worktrees, live git scan, identity filter, ship-state machine, session-anchored time, L1 templated narrative, risk signals). A new read-only `GET /api/productivity` handler serves the fused JSON. Migration `017` ships the two new tables (capture stubbed for prototype). A deliberately rough Svelte route renders it. Spec: `docs/superpowers/specs/2026-05-19-ai-productivity-dashboard-design.md` (authoritative — read it first).

**Tech Stack:** Go (stdlib + existing klyne `internal/store`, `internal/projectpath`, `internal/contexthealth`, `internal/api`), SQLite, SvelteKit (existing `ui/`).

**Prototype boundary (from spec §11):** BUILD = substrate + endpoint + migration tables + rough page + verification. STUB/FOLLOW-UP = session-end snapshot capture, Layer-2 reflection enrichment + the 7 worklog improvements, `dashboard_cache` memoization. Stubs must be explicit (commented `// PROTOTYPE STUB (spec §11): ...`), never silent.

---

## Pre-flight (Task 0): Learn the conventions

**Files to read (no changes):**
- `internal/store/migrations/016_worklog_reflections.sql` and `014_work_spans.sql` — migration style; does `work_spans` already give per-session active span? If yes, the time-attribution task reuses it.
- `internal/api/handlers/` — pick one read handler (e.g. `project_insights*.go`) + `internal/api/contracts.go` + the mounter — copy its registration/response pattern exactly.
- `internal/projectpath/projectpath.go` — canonical repo-root resolution API.
- `internal/contexthealth/` — active-time / idle-gap helper to reuse for session active span.
- `internal/store/` — how sessions are queried by `project_path` + time window.
- `ui/src/routes/insights/+page.svelte` and `ui/src/lib/api.js` — page + fetch-wrapper pattern.

- [ ] **Step 1:** Read the files above; write a 10-line note at top of the implementation summarizing: the migration pattern, the handler registration call, the sessions-by-window query function name, the contexthealth active-span function name, and whether `work_spans` is reusable. This note guides every later task.

---

## Task 1: Migration 017 (tables only; capture stubbed)

**Files:**
- Create: `internal/store/migrations/017_git_dashboard.sql`
- Test: `internal/store/migrations_test.go` (existing test that asserts all migrations apply cleanly — extend if it enumerates)

- [ ] **Step 1: Write the migration** (follow 016's exact style):

```sql
-- 017_git_dashboard.sql
CREATE TABLE IF NOT EXISTS git_session_snapshots (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id   TEXT,
  project_path TEXT NOT NULL,
  repo_name    TEXT NOT NULL,
  worktree_path TEXT,
  branch       TEXT,
  head_sha     TEXT,
  ahead_count  INTEGER DEFAULT 0,
  behind_count INTEGER DEFAULT 0,
  dirty_file_count INTEGER DEFAULT 0,
  dirty_files_json TEXT,
  captured_at  TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_gss_project_time ON git_session_snapshots(project_path, captured_at);

CREATE TABLE IF NOT EXISTS dashboard_cache (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  day        TEXT NOT NULL,
  repo_name  TEXT NOT NULL,
  branch     TEXT,
  cache_key  TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  model      TEXT,
  generated_at TIMESTAMP NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_dashcache_key ON dashboard_cache(cache_key);
```

- [ ] **Step 2:** Run the migrations test. Run: `go test ./internal/store/ -run Migration -v`. Expected: PASS (all migrations including 017 apply).
- [ ] **Step 3: Commit.** `git add internal/store/migrations/017_git_dashboard.sql internal/store/migrations_test.go && git commit -m "feat(store): migration 017 git dashboard tables"`

---

## Task 2: `internal/productivity` types + git scan

**Files:**
- Create: `internal/productivity/types.go`, `internal/productivity/gitscan.go`
- Test: `internal/productivity/gitscan_test.go`

Types (exact names used by all later tasks):

```go
package productivity

type ShipState string
const ( ShipLocal ShipState="committed-local-only"; ShipPushed="pushed-to-remote"; ShipMerged="merged-to-default" )

type Commit struct{ SHA, Subject, Author, AuthorEmail string; CommittedAt time.Time; Files, Insertions, Deletions int; IsUser bool }
type Branch struct{ Name, TicketID string; Ship ShipState; Ahead, Behind int; Commits []Commit; AttributedMinutes int; Narrative string }
type RiskSignal struct{ Kind string; Detail string; AgeMinutes int } // Kind: "unpushed" | "done-uncommitted"
type Service struct{ Repo, ProjectPath string; Branches []Branch; Risks []RiskSignal; ManualOnly bool }
type Report struct{ Day string; Services []Service; ReflectionStatus, Nudge string }
```

- [ ] **Step 1: Failing test** — `gitscan_test.go`: create a temp git repo, 2 commits, assert `ScanRepo(dir, since, until, userEmails)` returns 2 `Commit`s with correct subjects, `IsUser` true when author email in set, and `Ship==ShipLocal` when no remote.
- [ ] **Step 2:** Run `go test ./internal/productivity/ -run Scan -v` → FAIL (undefined).
- [ ] **Step 3: Implement** `ScanRepo`: shell `git -C <dir> log --since --until --pretty=...|--numstat`, parse; `git rev-parse --abbrev-ref HEAD`, `git rev-list --count @{u}..HEAD` / `..@{u}` for ahead/behind (handle no-upstream → ahead=all, ShipLocal); default-branch via `git symbolic-ref refs/remotes/origin/HEAD` fallback `main`→`master`→current; merged check `git merge-base --is-ancestor HEAD <default>`. Parse `TicketID` from branch via regex `(?i)[a-z]+-\d+`.
- [ ] **Step 4:** Run test → PASS.
- [ ] **Step 5: Commit.** `git add internal/productivity/ && git commit -m "feat(productivity): git scan + ship-state types"`

---

## Task 3: Repo discovery (sessions + worktrees) + identity

**Files:** Create `internal/productivity/discover.go`; Test `internal/productivity/discover_test.go`

- [ ] **Step 1: Failing test** — given a store stub returning two `project_path`s for a window, `DiscoverRepos(store, since, until)` returns canonicalized repo roots; for a repo with `git worktree list` output it also includes sibling worktree paths (use a temp repo + real `git worktree add`).
- [ ] **Step 2:** `go test ./internal/productivity/ -run Discover -v` → FAIL.
- [ ] **Step 3: Implement:** query distinct session `project_path` in window (reuse store fn found in Task 0); canonicalize via `internal/projectpath`; for each run `git worktree list --porcelain`, add worktree paths. `UserEmails()` = `git config user.email` ∪ config aliases (seed `mohitpatel9753@gmail.com`); document `// PROTOTYPE: alias set hardcoded seed; config wiring is follow-up`.
- [ ] **Step 4:** test → PASS. **Step 5: Commit.** `git commit -am "feat(productivity): repo discovery + identity"`

---

## Task 4: Session-anchored time attribution

**Files:** Create `internal/productivity/timeattrib.go`; Test `timeattrib_test.go`

- [ ] **Step 1: Failing test** — sessions with message timestamps spanning gaps; `AttributeMinutes(sessions, idleCapMin=30)` sums active intervals excluding gaps > cap; a session whose window has commits in 2 repos splits proportionally by in-window commit count; a repo with commits but no session yields `ManualOnly=true` and 0 AI minutes.
- [ ] **Step 2:** `go test ./internal/productivity/ -run Attribute -v` → FAIL.
- [ ] **Step 3: Implement** reusing `internal/contexthealth` active-span helper if found in Task 0 (else compute from message ts). Gap rule: session→commit gap NOT added. Multi-repo split proportional by commit count; mark `AttributedMinutes` and annotate split in `Branch.Narrative` later.
- [ ] **Step 4:** test → PASS. **Step 5: Commit.** `git commit -am "feat(productivity): session-anchored time attribution"`

---

## Task 5: Risk signals + L1 narrative + Report assembly

**Files:** Create `internal/productivity/report.go`; Test `report_test.go`

- [ ] **Step 1: Failing test** — `BuildReport(...)` over a fixture with: a branch ahead>0 not pushed → `RiskSignal{Kind:"unpushed",AgeMinutes:...}` and `Ship==ShipLocal`; a repo with current dirty tree + recent session, no commit after session end → `RiskSignal{Kind:"done-uncommitted"}`; L1 `Narrative` is non-empty, contains short SHAs and the attributed minutes, contains NO "PR #" substring; `ReflectionStatus=="missing"` and `Nudge` non-empty when no reflection row exists.
- [ ] **Step 2:** `go test ./internal/productivity/ -run Report -v` → FAIL.
- [ ] **Step 3: Implement** assembly: group commits→Branch→Service; ship-state from ahead/behind+merged; risks; L1 templated narrative `fmt`-built (lead with highest commit-count branch — salience rule); reflection lookup via store (`worklog_reflections` by project+day) → status `missing|stale|current` + nudge string; no LLM.
- [ ] **Step 4:** test → PASS. **Step 5: Commit.** `git commit -am "feat(productivity): risk signals + L1 narrative + report"`

---

## Task 6: `GET /api/productivity` handler

**Files:** Create `internal/api/handlers/productivity.go`; Modify `internal/api/contracts.go` (register route, follow Task 0 pattern exactly) + the mounter; Test `internal/api/handlers/productivity_test.go`

- [ ] **Step 1: Failing test** — httptest the handler with `?since=&until=`; assert 200, JSON decodes into `productivity.Report`, `Day` set.
- [ ] **Step 2:** `go test ./internal/api/... -run Productivity -v` → FAIL.
- [ ] **Step 3: Implement** handler: parse `since/until` (default = today local 00:00→now), call `productivity.BuildReport`, JSON-encode. Register exactly like the sibling read handler from Task 0.
- [ ] **Step 4:** test → PASS. **Step 5: Commit.** `git commit -am "feat(api): GET /api/productivity"`

---

## Task 7: Rough view page ("dirt dashboard")

**Files:** Create `ui/src/routes/productivity/+page.svelte`; add fetch in `ui/src/lib/api.js`

- [ ] **Step 1:** Add `fetchProductivity(since,until)` to `api.js` mirroring an existing wrapper.
- [ ] **Step 2:** Create the route: minimal, intentionally unstyled. For each Service: `<h2>repo</h2>`, risks as a red `<ul>`, then per Branch a block showing `ship state · ticketId · ~Xh Ym · narrative` and a `<details>` of commit subjects+SHAs. Top banner shows `reflectionStatus` + `nudge`. Add a one-line comment: `<!-- PROTOTYPE: deliberately rough; real UI/UX is a separate brainstorm (spec scope) -->`.
- [ ] **Step 3:** Build UI: `cd ui && npm run build` (or the repo's build script) → succeeds.
- [ ] **Step 4: Commit.** `git add ui && git commit -m "feat(ui): rough productivity view page (prototype)"`

---

## Task 8: End-to-end verification (verification-before-completion)

- [ ] **Step 1:** `go build ./...` from worktree root → succeeds.
- [ ] **Step 2:** `go test ./internal/productivity/... ./internal/api/... ./internal/store/...` → all PASS.
- [ ] **Step 3:** Run the klyne binary/daemon from the worktree; `curl 'localhost:8888/api/productivity'` (use the repo's actual port from config). Capture JSON.
- [ ] **Step 4: Verify against spec §9 success criteria 1–3, 5 using REAL data:** consultation-service CLI-1396 shows `committed-local-only` + unpushed risk; klyne `init` shows committed-local-only with ahead count; Ravi/Jenkins commits excluded (IsUser=false); time is session-anchored; `reflection_status:"missing"` + nudge present. Record actual observed values.
- [ ] **Step 5:** Open the page in a browser (or note inability) and confirm it renders the grouped data.
- [ ] **Step 6: Commit** any fixes. Then write `docs/superpowers/plans/PROTOTYPE-RESULTS.md` with: what works (with real observed values), what's stubbed (spec §11), exact run instructions, and known gaps. `git commit -am "docs: prototype results + run instructions"`

---

## Self-Review (completed by plan author)

- **Spec coverage:** D1 time→T4; D2 ship-state→T2/T5; D3/§7 L1→T5 (L2/3 enrichment = documented follow-up T-future, not prototype per §11); D4 done-uncommitted→T5; D5 discovery→T3; D6 live half→T2, snapshot half stubbed (T1 ships table, capture is follow-up — explicit per §11); D6.3 identity→T3; §6.6 grouping→T5; §7.2 worklog improvements = follow-up (spec §11 lists as stubbed; substrate built so consumable). Endpoint→T6; view→T7; verification→T8. Covered.
- **Placeholder scan:** no TBD/TODO; stubs are explicit and spec-referenced, not vague.
- **Type consistency:** `Report/Service/Branch/Commit/RiskSignal/ShipState` defined once in Task 2, referenced unchanged in T4–T7. `BuildReport`, `ScanRepo`, `DiscoverRepos`, `AttributeMinutes` names consistent across tasks.

# Worklog Rich-Entry Pipeline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the dashboard's "What was done" narrative source. Today an LLM reads N truncated 200-rune session summaries at *reflection time* and hallucinates (the same UUID cited as evidence under every bullet, vague topic abstractions, missing the actual day's work). Move generation to *session-end time* — when full context is still in memory — gated on session worthiness, producing a structured 15-category rich entry per session. Reflection then becomes a mechanical chronological join over those rich entries; no second LLM pass, no opportunity to re-hallucinate.

**Backward compatibility:** historical sessions (written before migration 019) carry empty entries forever. Phase 6's reflection consumer falls back to the legacy LLM writer on any day where ALL contributing sessions have empty entries, AND Phase 5 runs a one-time 30-day backfill worker to upgrade recent history.

**Architecture:**
- New columns on `stop_summaries` (migration 019) — `worklog_entry_json` + `worklog_gate_verdict` — following 015's parallel-write-function + selective-ON-CONFLICT pattern. The Stop hook writes base + deterministic fields; a separate pass writes the rich entry without clobbering anything.
- New package `internal/worklog/richentry/` holds: gate (hybrid heuristic+LLM), writer (LLM call with full session context), allowlist validator (6 token shapes — SHA / UUID / PR# / ticket / path / duration).
- Gate verdict is persisted so "no entry" is auditable (`admitted-heuristic` / `admitted-llm` / `skipped-heuristic` / `skipped-llm` / empty).
- Reflection consumer rewritten to be a deterministic merge — no LLM in the join.
- Dashboard "What was done" renders structured timeline directly from rich entries.

**Tech Stack:** Go (Stop hook, store, gate, writer, validator), SQLite (storage), the existing AI runner (`internal/ai/`) for the writer LLM call, SvelteKit for the dashboard "What was done" view.

**Scope of THIS plan:** Bite-sized executable tasks for Phases 0–3 (fixture, data layer, validator, gate). Phases 4–8 are sketched at roadmap level — a follow-up plan slices those into tasks once 0–3 land. Splitting this way matches the user's stated workflow ("fixture first; iterate the prompt manually before touching code").

**Cardinality clarification (from in-flight diagnosis of the existing pipeline):**
- The Stop hook fires **per turn**, not per session — composite key `(session_id, ts)` on `stop_summaries` already reflects this (multiple rows per session_id).
- `internal/worklog/suppress.go` already runs heuristic worthiness filtering via `ruleSkipReadOnly`, setting `recap_visible=0` on chat-only turns (no Edit/Write tool calls AND `last_bash` doesn't start with a build/test/commit verb).
- **Consequence for this plan:** the rich-entry writer runs **per turn**, not per session. The gate in Phase 3 is *additive* to `ruleSkipReadOnly`, not a replacement — many discussion turns currently suppressed (`recap_visible=0`) actually contain worklog-worthy content (decisions, bugs found, blockers) and should be rescued by the LLM tiebreak.
- Per-turn cardinality means cost: 30 turns × 5 sessions/day ≈ 150 LLM calls/day per dev for the writer. The hybrid gate's purpose is to keep that bounded by only invoking the writer when admitted; the heuristic admits cheaply, so the LLM is called only for borderline turns + every admitted turn's writer pass.

**Anti-hallucination contract (carried through every phase):**
- The writer LLM gets full session prose + commit list + diff stats — never a truncated summary.
- Every cited token in the entry MUST be in the allowlist derived from the session context (validator rejects anything else).
- The 7 token shapes the validator recognizes: short SHA (`[0-9a-f]{7,12}`), UUID (rejected unless in the session-id allowlist AND not reused across >1 bullet — the actual regression pattern), PR number (`#\d+` cross-checked against the gh PR cache for the repo; when cache is empty/stale the check is skipped not failed), ticket (`[A-Z]{2,}-\d+` cross-checked against branch names), file path (must exist in the session's touched-files), duration (must match an interval the session actually had, canonical form `~Xh Ym`), clock time (`\d{1,2}:\d{2}` must match a commit time or interval endpoint in the session).

---

## Phase 0 — Fixture & gold narrative

**Why first:** without ground truth, prompt iteration is vibes. We build one canonical fixture for 2026-05-19 and write the narrative we want by hand. Every later phase is measured against this.

### Task 0.1: Pull labstack commits across the 5 repos for 2026-05-19

**Files:**
- Create: `docs/superpowers/specs/2026-05-20-labstack-fixture.md`

- [ ] **Step 1: For each of consultation-service / oms-service / operations-app / product-service / klyne, run `git log` for 2026-05-19 IST**

For each repo, capture (in the fixture doc):
```
## <repo>
### Branches active on 2026-05-19
- branch-name | last commit ts | ship-state (merged/pushed/local)

### Commits (newest first)
- <short-sha> <author> <ts> <subject>
  files: <list>
  +N -M
```

- [ ] **Step 2: For each repo, list the contributing klyne sessions on 2026-05-19**

For each session, capture: session_id, cli (claude/codex), started_at, ended_at, message_count, the FULL `summary` body from stop_summaries (NOT the 200-rune `ai_drafted_summary`). This is the prose ground truth.

- [ ] **Step 3: Cross-reference any merged PRs from the productivity dashboard**

Pull the `MergedPR` list for the window (we already cache it) and copy into the fixture so the prompt has the same source the dashboard does.

- [ ] **Step 4: Save and commit**

```bash
git add docs/superpowers/specs/2026-05-20-labstack-fixture.md
git commit -m "docs(worklog): canonical 2026-05-19 fixture for rich-entry iteration"
```

### Task 0.2: Hand-write the gold narrative for 2026-05-19

**Files:**
- Create: `docs/superpowers/specs/2026-05-20-labstack-gold-narrative.md`

- [ ] **Step 1: For each session in the fixture, hand-write the rich entry JSON**

Use the 13-category schema. Real example for an operations-app afternoon session:
```json
{
  "schema_version": 1,
  "session_id": "ShpjTloRJ2-000xxx",
  "categories": {
    "features_worked_on": [
      { "summary": "Phone/lab-test/lab-name properties on lab_payload",
        "repo": "operations-app",
        "refs": ["c5c97a86", "e2850e29"] }
    ],
    "shipped": [
      { "summary": "Merged PR #400 Feature/cli 1325 labstack integration",
        "repo": "operations-app", "refs": ["#400"], "ticket": "CLI-1325" }
    ],
    "bugs_found": [
      { "summary": "Cancel-event refund path does not refund users who paid through payment provider",
        "repo": "operations-app", "refs": [] }
    ],
    "investigations": [
      { "summary": "Traced report-from-event flow — found we miss the health-vault save AND the Maddox API share (unlike healthi which does both)",
        "repo": "operations-app", "refs": [] }
    ],
    "reviews_given":         [],  // PRs I reviewed (gh-derivable)
    "blocked_on":            [],  // external dependency (distinct from blockers = self-stuck)
    "features_picked":       [],
    "bugs_fixed":            [],
    "decisions":             [],
    "blockers":              [],
    "pending":               [],
    "followups_for_others":  [],
    "must_remember":         [],
    "config_changes":        [],
    "mistakes_or_dead_ends": []
  },
  "gate_verdict": "admitted-heuristic"
}
```

**15 categories total:** features_worked_on, shipped, features_picked, bugs_found, bugs_fixed, investigations, decisions, blockers, blocked_on, pending, followups_for_others, must_remember, config_changes, mistakes_or_dead_ends, reviews_given.

- [ ] **Step 2: Stitch the gold per-day reflection from the per-session entries**

For 2026-05-19, write the markdown "What was done" we want the dashboard to render. It should be a chronological-per-repo merge of the per-session entries; bullets grouped by category; never a category bucket if empty. This is the deterministic-merge target Phase 6 must produce.

- [ ] **Step 3: Save and commit**

```bash
git commit -m "docs(worklog): hand-written gold narrative for 2026-05-19 — the target"
```

### Task 0.3: Manually iterate a writer prompt against the fixture

**Files:**
- Create: `docs/superpowers/specs/2026-05-20-writer-prompt-v1.md`

- [ ] **Step 1: Draft prompt v1**

Inputs the prompt will receive at runtime:
- Full session `summary` (the deterministic body — NOT ai_drafted_summary)
- Session's commit list with subjects + short SHAs + +/− stats + touched files
- Session's project_path + repo name
- Linked PR data from gh cache (for the day)
- Branch name + ticket id (if any)
- The 13-category schema

Output contract: emit ONLY the JSON object; no prose around it; every `refs` element must be findable in the inputs.

- [ ] **Step 2: Run prompt manually against 3+ representative sessions from the fixture**

Use Claude or whatever LLM klyne plans to call. Compare the output against the hand-written gold for those sessions. Note what's missing, what's hallucinated, what's verbose.

- [ ] **Step 3: Iterate**

Refine the prompt until ≥80% of bullets match the gold in topic and citation. Document the prompt + the iteration notes (what changed and why) in the file. This becomes the source-of-truth prompt that the Phase 4 writer code embeds.

- [ ] **Step 4: Commit**

```bash
git commit -m "docs(worklog): writer prompt v1 + iteration notes against gold"
```

---

## Phase 1 — Data layer

### Task 1.1: Migration 019 + write function

**Files:**
- Create: `internal/store/migrations/019_worklog_rich_entry.sql`
- Modify: `internal/store/migrations/migrations_test.go` (extend the expected-files list)
- Modify: `internal/store/stop_summaries.go` (new struct + new write fn)
- Test: `internal/store/stop_summaries_rich_entry_test.go`

- [ ] **Step 1: Write the failing test**

Follows the existing `internal/store/stop_summaries_test.go` conventions: `package store_test`, exported-symbol calls via `store.`, the existing `openStopSummariesDB(t)` helper (do NOT introduce a new helper).

```go
package store_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/klyne-ai/klyne/internal/store"
)

func TestUpsertStopSummaryWithEntry_RoundTrip(t *testing.T) {
	db := openStopSummariesDB(t)
	ctx := context.Background()

	base := store.StopSummary{
		SessionID:   "test-sess",
		Ts:          1000,
		ProjectPath: "/repo",
		Summary:     "base body",
	}
	entry := store.WorklogEntryJSON{
		SchemaVersion: 1,
		Categories: map[string][]store.WorklogItem{
			"features_worked_on": {{Summary: "phone/lab props", Repo: "operations-app", Refs: []string{"c5c97a86"}}},
		},
	}
	if err := store.UpsertStopSummaryWithEntry(ctx, db, base, entry, "admitted-heuristic", 0); err != nil {
		t.Fatalf("UpsertStopSummaryWithEntry: %v", err)
	}

	var gotJSON, gotVerdict string
	var gotAttempts int
	if err := db.Read().QueryRowContext(ctx,
		`SELECT worklog_entry_json, worklog_gate_verdict, worklog_attempts FROM stop_summaries WHERE session_id=? AND ts=?`,
		"test-sess", int64(1000)).Scan(&gotJSON, &gotVerdict, &gotAttempts); err != nil {
		t.Fatalf("select: %v", err)
	}
	if gotVerdict != "admitted-heuristic" {
		t.Errorf("gate verdict: got %q want %q", gotVerdict, "admitted-heuristic")
	}
	if gotAttempts != 0 {
		t.Errorf("attempts: got %d want 0", gotAttempts)
	}
	var roundTrip store.WorklogEntryJSON
	if err := json.Unmarshal([]byte(gotJSON), &roundTrip); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := roundTrip.Categories["features_worked_on"][0].Refs[0]; got != "c5c97a86" {
		t.Errorf("ref: got %q", got)
	}
}

func TestUpsertStopSummaryWithEntry_DoesNotClobberBase(t *testing.T) {
	db := openStopSummariesDB(t)
	ctx := context.Background()

	// First write the base via InsertStopSummary (gives a deterministic body).
	if err := store.InsertStopSummary(ctx, db, &store.StopSummary{
		SessionID: "s2", Ts: 2000, ProjectPath: "/r", Summary: "DETERMINISTIC BODY",
	}); err != nil {
		t.Fatalf("insert base: %v", err)
	}

	// Now write a rich entry on the same row. ON CONFLICT must only touch
	// the migration-019 columns — summary/last_user/last_bash/files_json
	// stay untouched even though we pass a different Summary value here.
	if err := store.UpsertStopSummaryWithEntry(ctx, db,
		store.StopSummary{SessionID: "s2", Ts: 2000, Summary: "WRONG IF WRITTEN"},
		store.WorklogEntryJSON{SchemaVersion: 1}, "admitted-llm", 1); err != nil {
		t.Fatalf("upsert entry: %v", err)
	}

	var gotSummary string
	if err := db.Read().QueryRowContext(ctx,
		`SELECT summary FROM stop_summaries WHERE session_id=? AND ts=?`,
		"s2", int64(2000)).Scan(&gotSummary); err != nil {
		t.Fatalf("select summary: %v", err)
	}
	if gotSummary != "DETERMINISTIC BODY" {
		t.Errorf("base summary was clobbered: got %q", gotSummary)
	}
}
```

- [ ] **Step 2: Run the tests — expect failure**

```bash
GOTOOLCHAIN=go1.25.3 go test ./internal/store/ -run TestUpsertStopSummaryWithEntry -count=1
```
Expected: FAIL (table column does not exist OR function not defined).

- [ ] **Step 3: Write the migration**

`internal/store/migrations/019_worklog_rich_entry.sql`:
```sql
-- 019_worklog_rich_entry.sql — Rich worklog entry on each stop_summaries row.
--
-- The dashboard's "What was done" narrative used to be re-generated at
-- reflection time from N truncated 200-rune session summaries, which
-- starved the LLM and produced hallucinations (the same UUID cited as
-- evidence under every bullet, vague topic abstractions). Move
-- generation to session-end time when the full transcript is in
-- memory: a hybrid gate (heuristic admit + LLM tiebreak, additive to
-- the existing ruleSkipReadOnly worthiness filter) decides whether
-- a turn is worklog-worthy, then a writer produces a structured
-- 15-category JSON entry.
--
-- The Stop hook fires PER TURN (composite key (session_id, ts)), so
-- one stop_summaries row = one rich entry. The reflection consumer
-- aggregates all turns for a day/session at read time.
--
-- Following the migration 015 pattern (re-use stop_summaries instead
-- of a parallel worklog_entries table; one parallel write function;
-- selective ON CONFLICT so a later AI prose pass does not clobber the
-- deterministic body): we add three columns and a NEW write function
-- (UpsertStopSummaryWithEntry, ON CONFLICT only updates these three).
--
-- Columns:
--   * worklog_entry_json     full 15-category WorklogEntryJSON; '{}' default
--   * worklog_gate_verdict   audit trail: why a turn has / lacks an entry
--                            ('' = never processed, equivalent to 'pending';
--                             'pending' = explicitly enqueued, awaiting worker;
--                             'admitted-heuristic' | 'admitted-llm' |
--                             'skipped-heuristic' | 'skipped-llm' |
--                             'skipped-validator' | 'failed-permanent')
--   * worklog_attempts       worker-attempt counter; on attempts >= MAX
--                            the worker sets verdict='failed-permanent' and stops

ALTER TABLE stop_summaries ADD COLUMN worklog_entry_json   TEXT    NOT NULL DEFAULT '{}';
ALTER TABLE stop_summaries ADD COLUMN worklog_gate_verdict TEXT    NOT NULL DEFAULT '';
ALTER TABLE stop_summaries ADD COLUMN worklog_attempts     INTEGER NOT NULL DEFAULT 0;

-- Two indexes back the two hot reads: the worker queue scan (verdict='' OR
-- 'pending' with attempts < MAX, ordered by ts) and the reflection
-- consumer's day-window scan (project_path + ts).
CREATE INDEX IF NOT EXISTS idx_stop_summaries_gate_verdict
    ON stop_summaries (worklog_gate_verdict, worklog_attempts, ts DESC);
```

- [ ] **Step 4: Extend `migrations_test.go` expected-files list**

```go
"018_github_pr_cache.sql",       // merged-PR `gh` enrichment cache (TTL-refreshed)
"019_worklog_rich_entry.sql",    // structured rich worklog entry per stop_summaries row
```

- [ ] **Step 5: Add the WorklogEntryJSON / WorklogItem types + the write fn**

Append to `internal/store/stop_summaries.go`:
```go
// WorklogItem is one entry inside a WorklogEntryJSON category. Uniform
// shape across all 13 categories so the reflection consumer can
// iterate without special-casing. `refs` MUST be tokens that survive
// the allowlist validator (short SHA, PR#, file path, etc.).
type WorklogItem struct {
	Summary string   `json:"summary"`
	Repo    string   `json:"repo,omitempty"`
	Refs    []string `json:"refs,omitempty"`
	Ticket  string   `json:"ticket,omitempty"`
}

// WorklogEntryJSON is the 13-category structured worklog entry the
// session-end pipeline (gate + writer + validator) attaches to a
// stop_summaries row via UpsertStopSummaryWithEntry. Empty categories
// are []; the JSON column never holds null.
type WorklogEntryJSON struct {
	SchemaVersion int                      `json:"schema_version"`
	Categories    map[string][]WorklogItem `json:"categories"`
}

// UpsertStopSummaryWithEntry writes a stop_summaries row plus the
// migration-019 rich entry, gate verdict, and worker-attempts counter.
// Following the 015 precedent: ON CONFLICT ONLY updates the
// migration-019 columns, preserving the base body
// (summary/last_user/last_bash/files_json) and the 015 worklog
// metadata so this pass can run after the Stop hook without
// clobbering anything.
//
// attempts is the worker's try-count. The gate calling this for the
// first time passes 0 on success, or N on a retry. When attempts
// >= MAX_WORKLOG_ATTEMPTS the caller is expected to pass
// verdict='failed-permanent' so the worker queue drops the row.
func UpsertStopSummaryWithEntry(
	ctx context.Context, db *DB, row StopSummary, entry WorklogEntryJSON,
	gateVerdict string, attempts int,
) error {
	if strings.TrimSpace(row.SessionID) == "" {
		return errors.New("store: upsert entry: session_id required")
	}
	if row.Ts == 0 {
		row.Ts = time.Now().UnixMilli()
	}
	if row.CLI == "" {
		row.CLI = "claude"
	}
	files := row.Files
	if files == nil {
		files = []string{}
	}
	filesJSON, err := json.Marshal(files)
	if err != nil {
		return fmt.Errorf("store: marshal files: %w", err)
	}
	entryBytes, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("store: marshal worklog entry: %w", err)
	}
	const q = `
INSERT INTO stop_summaries (
    session_id, ts, project_path, cli, summary, last_user, last_bash, files_json,
    worklog_entry_json, worklog_gate_verdict, worklog_attempts
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(session_id, ts) DO UPDATE SET
    worklog_entry_json   = excluded.worklog_entry_json,
    worklog_gate_verdict = excluded.worklog_gate_verdict,
    worklog_attempts     = excluded.worklog_attempts`
	_, err = db.Write().ExecContext(ctx, q,
		row.SessionID, row.Ts, row.ProjectPath, row.CLI, row.Summary,
		row.LastUser, row.LastBash, string(filesJSON),
		string(entryBytes), gateVerdict, attempts)
	if err != nil {
		return fmt.Errorf("store: upsert stop summary with entry: %w", err)
	}
	return nil
}
```

The `MAX_WORKLOG_ATTEMPTS` constant + the worker-queue scan function are introduced in Phase 5 — Phase 1 only ships the column and the write fn.

- [ ] **Step 6: Run the tests — expect pass**

```bash
GOTOOLCHAIN=go1.25.3 go test ./internal/store/ -run "TestUpsertStopSummaryWithEntry|TestMigrationsList" -count=1 -v
```
Expected: PASS — both round-trip and don't-clobber-base tests green.

- [ ] **Step 7: Commit**

```bash
git add internal/store/migrations/019_worklog_rich_entry.sql \
        internal/store/migrations/migrations_test.go \
        internal/store/stop_summaries.go \
        internal/store/stop_summaries_rich_entry_test.go
git commit -m "feat(store): migration 019 + rich worklog-entry write fn"
```

### Task 1.2: WorklogEntryJSON read helper

**Files:**
- Modify: `internal/store/stop_summaries.go` (add `ReadWorklogEntry`)
- Test: same `stop_summaries_rich_entry_test.go`

- [ ] **Step 1: Write the failing test**

```go
// In the same package store_test file as Task 1.1.
func TestReadWorklogEntry(t *testing.T) {
	db := openStopSummariesDB(t)
	ctx := context.Background()
	entry := store.WorklogEntryJSON{SchemaVersion: 1, Categories: map[string][]store.WorklogItem{
		"shipped": {{Summary: "PR #400", Refs: []string{"#400"}}},
	}}
	_ = store.UpsertStopSummaryWithEntry(ctx, db,
		store.StopSummary{SessionID: "s", Ts: 1, Summary: "x"}, entry, "admitted-heuristic", 0)
	got, verdict, err := store.ReadWorklogEntry(ctx, db, "s", 1)
	if err != nil { t.Fatal(err) }
	if verdict != "admitted-heuristic" { t.Errorf("verdict: %q", verdict) }
	if got.Categories["shipped"][0].Summary != "PR #400" { t.Errorf("missing shipped") }
}
```

- [ ] **Step 2: Run — expect failure (no function)**

```bash
GOTOOLCHAIN=go1.25.3 go test ./internal/store/ -run TestReadWorklogEntry -count=1
```

- [ ] **Step 3: Add `ReadWorklogEntry`**

```go
// ReadWorklogEntry returns the migration-019 rich entry + gate verdict
// for one (session_id, ts). Zero entry + empty verdict on no-row.
func ReadWorklogEntry(ctx context.Context, db *DB, sessionID string, ts int64) (WorklogEntryJSON, string, error) {
	const q = `SELECT worklog_entry_json, worklog_gate_verdict FROM stop_summaries WHERE session_id=? AND ts=?`
	var raw, verdict string
	err := db.Read().QueryRowContext(ctx, q, sessionID, ts).Scan(&raw, &verdict)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return WorklogEntryJSON{}, "", nil
		}
		return WorklogEntryJSON{}, "", fmt.Errorf("store: read worklog entry: %w", err)
	}
	var entry WorklogEntryJSON
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &entry)
	}
	return entry, verdict, nil
}
```

- [ ] **Step 4: Run — expect pass**

```bash
GOTOOLCHAIN=go1.25.3 go test ./internal/store/ -run TestReadWorklogEntry -count=1
```

- [ ] **Step 5: Commit**

```bash
git commit -am "feat(store): ReadWorklogEntry for the 019 rich entry"
```

---

## Phase 2 — Allowlist validator

This is the structural anti-hallucination guard. It's small, pure, easily TDD'd, and protects every downstream step.

### Task 2.1: Define the validator + 6 token shapes

**Files:**
- Create: `internal/worklog/richentry/validator.go`
- Test: `internal/worklog/richentry/validator_test.go`

**Allowlist source (built per session from real inputs the writer saw):**
- `short_shas`     — set of `[0-9a-f]{7,12}` from the commit list
- `pr_numbers`     — set of `#\d+` from the gh PR cache for the repo
- `ticket_ids`     — set of `[A-Z]{2,}-\d+` from the branch names
- `file_paths`     — set of paths from the session's `files_json` + commit file lists
- `durations`      — bag of duration strings (e.g. `~2h 14m`) derivable from session active intervals, canonical form `~Xh Ym` / `~Nm`
- `clocks`         — bag of `HH:MM` timestamps from commit times + interval endpoints (the gold narrative format uses these)
- `session_ids`    — set of UUIDs from prior klyne sessions explicitly referenced in the writer's input bundle (rare — only when the LLM is told "this session continues from session X")

**Token shapes that bypass the allowlist** (deliberately allowed without lookup):
- Plain English (no constraint)
- The repo name itself when it appears as `service.repo` in inputs

**UUID handling (the regression class — fixed precisely, not blanket-rejected):**
- A UUID is allowed ONLY if it's in `session_ids` AND it appears across at most 1 bullet's `refs[]`. Either condition fails → reject.
- That covers the actual regression: the LLM picked one real `session_id` and pasted it under every bullet. Blanket-rejecting all UUIDs would break the legitimate "continuing from session X" case the research doc raised.

**PR-data staleness guard:**
- When the gh PR cache for the repo is empty / stale (e.g., fresh repo, never fetched), the validator SKIPS the PR-number check rather than rejecting — a `#123` ref then passes structurally without a positive match. Phase 4's writer logs this so we can detect "passing because cache missing" vs "passing because PR exists".

- [ ] **Step 1: Write the failing tests (TDD)**

Tests in `validator_test.go`:
```go
func TestValidator_AllowsRealSHA(t *testing.T)      // refs ["c5c97a86"] when in allowlist → ok
func TestValidator_RejectsFakeSHA(t *testing.T)     // refs ["aaaaaaaa"] not in allowlist → reject
func TestValidator_RejectsUUID(t *testing.T)        // refs ["e4696ed8-c2e9-416e-9a2d-a6e4860e649d"] → reject (the regression class)
func TestValidator_AllowsKnownPR(t *testing.T)      // refs ["#400"] when allowlist has it → ok
func TestValidator_RejectsUnknownPR(t *testing.T)   // refs ["#999"] not in allowlist → reject
func TestValidator_AllowsKnownTicket(t *testing.T)  // refs ["CLI-1325"] when branch had it → ok
func TestValidator_AllowsKnownPath(t *testing.T)    // refs ["src/components/foo.tsx"] in files → ok
func TestValidator_RejectsInventedPath(t *testing.T)// refs ["nonsense/path.go"] → reject
func TestValidator_AcceptsDurationFromIntervals(t)  // refs ["~2h 14m"] when intervals support → ok
```

- [ ] **Step 2: Run — expect compile failure / undefined symbols**

```bash
GOTOOLCHAIN=go1.25.3 go test ./internal/worklog/richentry/ -count=1
```

- [ ] **Step 3: Implement `validator.go`**

```go
package richentry

import (
	"regexp"
	"strings"

	"github.com/klyne-ai/klyne/internal/store"
)

// Allowlist is the per-session set of tokens any `refs` element in the
// WorklogEntryJSON is permitted to cite. Anything outside this set is
// rejected (with the UUID exception below). All sets are built from
// real session inputs — no synthesis.
type Allowlist struct {
	ShortSHAs   map[string]bool
	PRNumbers   map[string]bool // keys include the "#" prefix
	PRCacheLive bool            // false → skip PR check entirely (fresh repo / cache miss)
	TicketIDs   map[string]bool
	FilePaths   map[string]bool
	Durations   map[string]bool // canonical "~Xh Ym" / "~Nm"
	Clocks      map[string]bool // "HH:MM" from commit times + interval endpoints
	SessionIDs  map[string]bool // UUIDs of prior klyne sessions explicitly referenced
}

var (
	shaRe      = regexp.MustCompile(`^[0-9a-f]{7,12}$`)
	uuidRe     = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	prRefRe    = regexp.MustCompile(`^#\d+$`)
	ticketRe   = regexp.MustCompile(`^[A-Z]{2,}-\d+$`)
	durationRe = regexp.MustCompile(`^~?\d+h(\s\d+m)?$|^~?\d+m$`)
	clockRe    = regexp.MustCompile(`^\d{1,2}:\d{2}$`)
)

// ValidationError describes one bad citation found inside an entry.
type ValidationError struct {
	Category string
	ItemIdx  int
	Ref      string
	Reason   string
}

// Validate walks every refs[] across every category and returns the
// list of rejected citations. Empty list = entry is allowlist-clean.
// Also rejects any UUID that's in allow.SessionIDs but appears in
// >1 bullet's refs (the actual hallucination pattern — one valid id
// pasted under every bullet).
func Validate(entry store.WorklogEntryJSON, allow Allowlist) []ValidationError {
	uuidUses := countUUIDUses(entry)
	var errs []ValidationError
	for cat, items := range entry.Categories {
		for i, it := range items {
			for _, ref := range it.Refs {
				if reason := classify(ref, allow, uuidUses); reason != "" {
					errs = append(errs, ValidationError{Category: cat, ItemIdx: i, Ref: ref, Reason: reason})
				}
			}
		}
	}
	return errs
}

// countUUIDUses tallies how many distinct bullets reference each UUID.
// The hallucination pattern was one UUID cited under every bullet —
// uniqueness across bullets is the structural guard against it.
func countUUIDUses(entry store.WorklogEntryJSON) map[string]int {
	out := map[string]int{}
	for _, items := range entry.Categories {
		for _, it := range items {
			seenInThisBullet := map[string]bool{}
			for _, ref := range it.Refs {
				r := strings.TrimSpace(ref)
				if uuidRe.MatchString(r) && !seenInThisBullet[r] {
					out[r]++
					seenInThisBullet[r] = true
				}
			}
		}
	}
	return out
}

func classify(ref string, allow Allowlist, uuidUses map[string]int) string {
	r := strings.TrimSpace(ref)
	if r == "" {
		return "empty ref"
	}
	switch {
	case uuidRe.MatchString(r):
		// UUIDs need both: known session id AND used in only one bullet.
		if !allow.SessionIDs[r] {
			return "uuid not in session-id allowlist (regression class)"
		}
		if uuidUses[r] > 1 {
			return "uuid reused across multiple bullets (regression class)"
		}
	case prRefRe.MatchString(r):
		// Skip PR-number check when the gh cache is empty — passes structurally.
		if allow.PRCacheLive && !allow.PRNumbers[r] {
			return "PR not in allowlist"
		}
	case shaRe.MatchString(r):
		if !allow.ShortSHAs[r] { return "SHA not in commit list" }
	case ticketRe.MatchString(r):
		if !allow.TicketIDs[r] { return "ticket not on any branch in this session" }
	case durationRe.MatchString(r):
		if !allow.Durations[r] { return "duration not derivable from session intervals" }
	case clockRe.MatchString(r):
		if !allow.Clocks[r] { return "clock time not at any commit or interval endpoint" }
	default:
		// Treat as a file-path citation.
		if !allow.FilePaths[r] { return "path not in session's touched-files" }
	}
	return ""
}
```

- [ ] **Step 4: Run — expect pass**

```bash
GOTOOLCHAIN=go1.25.3 go test ./internal/worklog/richentry/ -count=1
```

- [ ] **Step 5: Commit**

```bash
git add internal/worklog/richentry/
git commit -m "feat(worklog): allowlist validator for rich-entry citations"
```

### Task 2.2: Builder — derive allowlist from session inputs

**Files:**
- Modify: `internal/worklog/richentry/validator.go` (add `BuildAllowlist`)
- Test: same file

- [ ] **Step 1: Write the failing test**

```go
func TestBuildAllowlist_FromSessionInputs(t *testing.T) {
	// Inputs the writer would receive at runtime
	cmts := []CommitRef{{SHA: "c5c97a86", Files: []string{"src/foo.ts"}}, {SHA: "e2850e29"}}
	prs := []int{400, 142}
	branches := []string{"feature/CLI-1325-labstack", "main"}
	files := []string{"package.json"}
	intervals := []time.Duration{2*time.Hour + 14*time.Minute, 30*time.Minute}

	al := BuildAllowlist(cmts, prs, branches, files, intervals)
	if !al.ShortSHAs["c5c97a86"] { t.Error("missing sha") }
	if !al.PRNumbers["#400"] { t.Error("missing pr") }
	if !al.TicketIDs["CLI-1325"] { t.Error("missing ticket") }
	if !al.FilePaths["src/foo.ts"] { t.Error("missing path from commit") }
	if !al.FilePaths["package.json"] { t.Error("missing path from session files") }
	if !al.Durations["~2h 14m"] { t.Errorf("missing duration; got %v", al.Durations) }
}
```

- [ ] **Step 2: Run — expect failure**

- [ ] **Step 3a: Build ShortSHAs**

Iterate `cmts`, insert each `CommitRef.SHA` into `Allowlist.ShortSHAs`.

- [ ] **Step 3b: Build FilePaths**

Iterate `cmts`, insert each path from `CommitRef.Files`. Then iterate `files` (session-level touched files from `stop_summaries.files_json`) and insert those too.

- [ ] **Step 3c: Build PRNumbers**

Iterate `prs []int`, format each as `"#%d"` and insert. Set `Allowlist.PRCacheLive = true` only when the gh PR cache lookup for the repo succeeded (non-nil result, including empty list); fresh-repo / cache-miss → `false` and the validator skips PR checks.

- [ ] **Step 3d: Build TicketIDs**

Iterate `branches []string`, regex-match `[A-Z]{2,}-\d+` (case-insensitive then upper-case), insert each unique match.

- [ ] **Step 3e: Build Durations**

Iterate `intervals []time.Duration`, canonicalize each via the existing `productivity.hm` helper convention: `~Xh Ym` when `d >= 1h`, `~Nm` otherwise. Insert each canonical string.

- [ ] **Step 3f: Build Clocks**

For each commit time and each interval start/end (passed as a separate `clocks []time.Time` argument), format as `HH:MM` in local time. Insert each.

- [ ] **Step 3g: Build SessionIDs**

Accept `priorSessionIDs []string` argument (will be empty for most calls — only non-empty when the writer's prompt explicitly references continuation from a prior session). UUID-validate each and insert. Empty set ⇒ all UUID citations rejected (default safe).

- [ ] **Step 4: Run — expect pass**

- [ ] **Step 5: Commit**

```bash
git commit -am "feat(worklog): BuildAllowlist derives validator inputs from session"
```

---

## Phase 3 — Gate (hybrid heuristic + LLM tiebreak)

The gate decides "is this session worklog-worthy?" — keeps noise out, keeps borderline cases honest via a tiny LLM call.

### Task 3.1: Heuristic admit/deny rules

**Files:**
- Create: `internal/worklog/richentry/gate.go`
- Test: `internal/worklog/richentry/gate_test.go`

**Context:** runs PER TURN (one stop_summaries row = one turn). The existing `internal/worklog/suppress.go` already sets `recap_visible=0` on turns with no Edit/Write tool calls AND no build/test/commit `last_bash`. This gate is **additive** — it can rescue worthy discussion turns the existing rule suppresses (e.g. a turn where the user found a refund bug but didn't commit), and it can reject trivial admits (a one-line typo commit).

**Heuristic contract:**
- **Admit immediately:** (≥1 user-commit with `lines_changed ≥ 10` OR at least one non-doc file touched) OR (active duration ≥15 min AND ≥3 user messages with at least one decision/bug/blocker keyword present)
- **Deny immediately:** active duration <3 min OR (0 commits AND <3 user messages AND duration <5 min) OR (only doc-file commits with `lines_changed < 10` total)
- **Otherwise:** borderline → defer to LLM tiebreak

Reviewer pushed back on the original "≥1 commit" rule because a typo-fix commit would admit; the `lines_changed ≥ 10` qualifier sets a reasonable floor without losing real work. The non-doc-file gate keeps README-tweak commits from auto-admitting.

- [ ] **Step 1: Write failing tests** for each branch (admit-by-commit, admit-by-duration, deny-too-short, borderline-defer)

- [ ] **Step 2: Run — expect failure**

- [ ] **Step 3: Implement `HeuristicGate` returning `Admit | Deny | Borderline`**

```go
type HeuristicVerdict int
const (
	HeuristicBorderline HeuristicVerdict = iota
	HeuristicAdmit
	HeuristicDeny
)

// SessionMetrics holds the per-turn signals the gate reads. Built by
// the caller from the stop_summaries row + the matching session and
// message data the daemon already has.
type SessionMetrics struct {
	UserCommits         int
	ActiveDuration      time.Duration
	UserMessages        int
	LinesChanged        int  // sum of insertions+deletions across user commits in this turn
	NonDocFilesTouched  int  // count of non-{.md,.txt,docs/} files in commits
	HasDecisionKeyword  bool // user msg contains "decided"/"chose"/"picked"/"will use"/etc.
	HasBugKeyword       bool // user msg contains "bug"/"broken"/"failing"/"issue"/etc.
	HasBlockerKeyword   bool // user msg contains "blocked"/"stuck"/"can't"/"won't"/etc.
}

func HeuristicGate(m SessionMetrics) HeuristicVerdict { ... }
```

- [ ] **Step 4: Run — expect pass**

- [ ] **Step 5: Commit**

```bash
git commit -am "feat(worklog): heuristic admit/deny rules for the gate"
```

### Task 3.2: LLM tiebreak for borderline sessions

**Files:**
- Modify: `internal/worklog/richentry/gate.go`
- Test: same

**Contract:** tiny prompt that gets session metrics + the deterministic Stop-hook summary body + the last user message, returns `worklog_worthy: true|false`. Mocked in tests via the local `AIClient` interface defined below.

**`AIClient` interface — defined locally in `gate.go`:**

```go
// AIClient is the narrow interface this package needs from the AI
// runner — a single Chat call. Production wires
// internal/ai.Provider (which already implements Chat via
// internal/ai/tasks/runner.go); tests pass a fake. Defined locally
// (not imported from internal/ai) so the worklog package stays
// decoupled from the runner's larger surface.
type AIClient interface {
	Chat(ctx context.Context, req ai.ChatRequest) (*ai.ChatResponse, error)
}
```

- [ ] **Step 1: Write failing test with a mock LLM that returns true → gate should output `admitted-llm`**

```go
type fakeAI struct{ reply string }
func (f *fakeAI) Chat(_ context.Context, _ ai.ChatRequest) (*ai.ChatResponse, error) {
	return &ai.ChatResponse{Content: f.reply}, nil
}

func TestDecide_BorderlineLLMAdmits(t *testing.T) {
	// metrics put us in HeuristicBorderline
	m := SessionMetrics{ActiveDuration: 10*time.Minute, UserMessages: 2}
	verdict, admit := Decide(context.Background(), &fakeAI{reply: `{"worklog_worthy": true}`}, m, "found refund bug, didn't commit yet")
	if !admit { t.Errorf("admit=false") }
	if verdict != "admitted-llm" { t.Errorf("verdict=%q", verdict) }
}
```

- [ ] **Step 2: Run — expect failure**

- [ ] **Step 3: Implement `Decide(ctx, llm AIClient, m SessionMetrics, prose string) (verdict string, admit bool)`** — wraps `HeuristicGate`, only calls `llm` on `HeuristicBorderline`, returns one of `admitted-heuristic`/`admitted-llm`/`skipped-heuristic`/`skipped-llm`. The verdict `pending` is reserved for the worker queue (set by Phase 5 when the writer enqueues but hasn't run yet); the gate itself never returns `pending`.

- [ ] **Step 4: Run — expect pass**

- [ ] **Step 5: Commit**

```bash
git commit -am "feat(worklog): LLM tiebreak for borderline gate verdicts"
```

---

## Roadmap — Phases 4-8 (next plan, after 0-3 land)

Sketched so reviewers can pressure-test the overall architecture, but not yet sliced into bite-sized tasks.

### Phase 4 — Rich-entry writer
- Build per-turn context bundle (full `stop_summaries.summary` body, commits in this turn with files+stats, gh PRs visible to repo from migration 018 cache, branch info, active intervals around the turn's ts)
- **PR-cache freshness guard:** when the gh cache for the repo is empty/stale, set `Allowlist.PRCacheLive = false` so the validator skips PR-number checks rather than rejecting (fresh-repo case is legitimate)
- LLM call using the prompt locked in Phase 0
- Run validator (Phase 2) on output; on validation failures, retry the LLM call ONCE with a corrected prompt (citing the specific rejected refs); on second failure, persist `gate_verdict='skipped-validator'` + empty entry + `attempts++`
- Persist via `UpsertStopSummaryWithEntry(ctx, db, row, entry, verdict, attempts)`
- Tests: replay Phase 0 fixture sessions, assert output matches gold within tolerance; assert PR-cache-empty path passes structurally

### Phase 5 — Pipeline integration
- **Decision (settled per architecture review):** background worker — NOT a synchronous Stop-hook call. The Stop hook stays as today (writes the base `stop_summaries` row); the worker drains the queue and runs gate+writer asynchronously so per-turn Stop never blocks on an LLM call.
- **Queue signal:** `worklog_gate_verdict IN ('', 'pending')` AND `worklog_attempts < MAX_WORKLOG_ATTEMPTS` — persistent (crash-safe), no extra table needed. The Stop hook sets `verdict='pending'` to differentiate "explicitly enqueued" from "never processed at all" (`''`).
- **Worker placement:** in the existing daemon process (`cmd/klyne`), polls every N seconds for queue rows, processes oldest-first.
- **Failure modes:** LLM unavailable → leave `verdict='pending'`, `attempts++`, retry on next tick; validator fails twice in Phase 4 → `verdict='skipped-validator'`, drops from queue; `attempts >= MAX_WORKLOG_ATTEMPTS` → `verdict='failed-permanent'`, drops from queue with an error log.
- **One-time 30-day backfill** (carries over the reviewer's blocker): on daemon startup, mark every `stop_summaries` row from the last 30 days with `verdict=''` and `gate_verdict<>'skipped-*'` as `verdict='pending'`. The worker picks them up. Bounded one-shot; subsequent restarts only enqueue rows still at `verdict=''`.
- Tests: end-to-end against the gold fixture, including failure paths and the backfill

### Phase 6 — Reflection consumer rewrite
- `internal/worklog/reflection_*` rewritten to call `ReadWorklogEntry` for EVERY stop_summaries row (every turn) in the window, then mechanically merge categories chronologically per repo per session.
- NO LLM in the merge — pure structured combination + render-to-markdown.
- Output is the same `body_md` shape current reflections produce so downstream readers don't change.
- **Empty-entry fallback (carries over the reviewer's blocker):** when ALL contributing rows for a day have `worklog_entry_json='{}'`, fall back to the legacy LLM reflection writer. This covers (a) historical sessions before migration 019, (b) days where every session was suppressed by `ruleSkipReadOnly` + `gate_verdict='skipped-*'`, (c) the transition period before the 30-day backfill finishes. The fallback path is the existing `reflection_proposer.go` — kept for backward compatibility, not removed.
- Deprecation path for the LLM-driven reflection writer: keep it as the fallback indefinitely until we're confident every rich-entry path produces complete days.
- Tests: replay 2026-05-19 gold fixture → expect the hand-written gold-narrative markdown; assert fallback runs when entries are empty

### Phase 7 — Dashboard "What was done"
- `internal/api/handlers/productivity.go` reads rich entries for the window + the merged reflection
- UI: structured timeline of categories per repo per session OR the joined reflection markdown
- Keep current rendering as fallback when no rich entries exist (older sessions)
- Tests: protoserve + Playwright against the 2026-05-19 window, verify operations-app section shows the refund bug + health-vault investigation

### Phase 8 — Deprecation
- Mark `ai_drafted_summary` deprecated in code comments + spec
- Continue dual-writing during transition for read-side safety
- Migration 020 (much later, separate decision) drops `ai_drafted_summary` after grep confirms zero readers

---

## Self-review

Updated after architecture + executability review pass. Outstanding fix-list is now empty.

**Spec coverage:** 15-category schema (added `reviews_given`, `blocked_on`), hybrid gate (additive to existing `ruleSkipReadOnly`), allowlist validator (7 token shapes including SessionIDs and Clocks), full-session prose ingestion (per turn, not 200-rune summaries), mechanical reflection merge with legacy-LLM fallback, dashboard rendering — every requirement covered. ✓

**Placeholder scan:** No TBDs. All Phase 0-3 tasks have complete failing-test code + implementation snippets + exact commands. Task 2.2 Step 3 broken into 7 sub-steps (was the reviewer's blocker). Phase 4-8 explicitly roadmap, labeled. ✓

**Type consistency:** `WorklogEntryJSON`/`WorklogItem`/`Allowlist`/`SessionMetrics`/`HeuristicVerdict`/`AIClient` names used consistently. `UpsertStopSummaryWithEntry(ctx, db, row, entry, verdict, attempts)` signature matches its test calls everywhere. The `gate_verdict` vocabulary (`pending`/`admitted-heuristic`/`admitted-llm`/`skipped-heuristic`/`skipped-llm`/`skipped-validator`/`failed-permanent`) is consistent between migration 019, the write fn, and the Phase 3/5 decisions. ✓

**Migration safety:** 019 is purely additive (three new columns with DEFAULT). Existing readers use explicit column lists — invisible to them. Rollback = drop the three columns. ✓

**Cardinality:** Plan now correctly assumes one rich-entry-per-turn (matching the existing `stop_summaries` composite PK), with per-day aggregation deferred to the reflection consumer. ✓

**Test harness:** Plan uses `package store_test` + `openStopSummariesDB(t)` + `store.` prefix matching the existing `stop_summaries_test.go` conventions (the executability blocker). ✓

**External dependencies:** `AIClient` interface defined inline in `gate.go` matching `ai.Provider`'s shape — production wires the real provider, tests pass fakes. No new package import surface. ✓

**Reviewer findings addressed:**
- [x] Arch #1 Historical-session fallback — Phase 6 fallback + Phase 5 backfill
- [x] Arch #2 Queue durability — `worklog_attempts` column + `pending` verdict + `failed-permanent` terminal
- [x] Arch #3 UUID rule nuance — session_ids allowlist + uniqueness check (not blanket reject)
- [x] Arch #4 Gate over-admit — `lines_changed ≥ 10 OR non-doc file` qualifier
- [x] Arch #5 Missing categories — `reviews_given`, `blocked_on` added (15 total)
- [x] Arch #6 Validator HH:MM — `clockRe` + `Allowlist.Clocks`
- [x] Arch #7 Verdict `pending` — added explicitly
- [x] Exec #1 migrations_test.go expected list — instruction stands; no extra count assertion
- [x] Exec #2 Test harness — `package store_test`, `openStopSummariesDB(t)`, `store.` prefix
- [x] Exec #5 `AIClient` definition — inline in `gate.go` with `ai.Provider`-shape `Chat`
- [x] Exec #6 PR-cache freshness — `Allowlist.PRCacheLive` flag, validator skips when false
- [x] Exec #7 Task 2.2 Step 3 — broken into Steps 3a–3g

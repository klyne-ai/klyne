# Productivity Snapshots Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the productivity dashboard deterministic on reload by persisting per-day snapshots, replacing the 7d/14d/30d ranges with Today / Yesterday / This Week / Last Week, and sourcing past-day data from snapshots written when reflections are recorded.

**Architecture:** A new `daily_productivity_snapshot` table is keyed by `(project_path, day)` and stores the full single-day `productivity.Report` payload as JSON. The reflection writer (`recordReflection`) writes a snapshot for the reflected day after the reflection row is inserted (source=`reflection`). The HTTP handler `/api/productivity` slices the requested window into days; for each past day it prefers the snapshot, lazily backfilling one (source=`live`) when missing; `today` is always live-computed. A new aggregation helper (`internal/productivity/aggregate.go`) merges N single-day reports into one composite Report (sum minutes, union sessions, merge services-by-repo). The Svelte UI is reduced to four preset chips (Today / Yesterday / This Week / Last Week) — date inputs are removed.

**Tech Stack:** Go 1.25 (modernc.org/sqlite, chi router), Svelte 5 (runes), SQLite.

---

### Task 1: Migration `021_daily_productivity_snapshot.sql`

**Files:**
- Create: `internal/store/migrations/021_daily_productivity_snapshot.sql`

- [ ] Schema: `daily_productivity_snapshot(project_path TEXT, day TEXT, payload_json TEXT, total_active_minutes INT, source TEXT, created_at INT, updated_at INT, PRIMARY KEY (project_path, day))`
- [ ] Comment block explains: keyed by canonical project_path + local YYYY-MM-DD; source is `reflection` (authoritative, written by `recordReflection`) or `live` (auto-backfill from handler).
- [ ] Single unique index implied by PK; add covering index `(project_path, day)` (already PK) — skip extra.

### Task 2: Store layer for snapshots

**Files:**
- Create: `internal/store/productivity_snapshots.go`

- [ ] `type DailyProductivitySnapshot struct { ProjectPath, Day, PayloadJSON, Source string; TotalActiveMinutes int; CreatedAt, UpdatedAt int64 }`
- [ ] `UpsertDailyProductivitySnapshot(ctx, db, s) error` — INSERT … ON CONFLICT(project_path, day) DO UPDATE SET payload_json=excluded, total_active_minutes=excluded, source=excluded, updated_at=excluded.
- [ ] `GetDailyProductivitySnapshot(ctx, db, projectPath, day) (DailyProductivitySnapshot, bool, error)`
- [ ] `ListDailyProductivitySnapshotsRange(ctx, db, projectPath, fromDay, toDay) ([]DailyProductivitySnapshot, error)` — inclusive YYYY-MM-DD lexicographic compare.

### Task 3: Aggregation helper

**Files:**
- Create: `internal/productivity/aggregate.go`

- [ ] `AggregateReports(reports []Report, day string) Report` — returns one composite Report:
  - `Day` = caller-provided range label
  - `Services` = merged by `ProjectPath`: union Branches, union Risks, union MergedPRs (by Number dedup), sum/union MinutesByCLI; preserve newest GitFetchedAt
  - `Sessions` = concatenated, sorted by StartedAt; dedup by SessionID (last wins)
  - `TotalActiveMinutes` = sweep-line union of every Session's ActiveIntervals across all days (NOT a plain sum — preserves §6.4 semantics)
  - `MinutesByCLI` = same sweep-line union per CLI
  - `ReflectionGroups` / `ReflectionMarkdown` = concatenated from each day, newest day first
  - `ReflectionStatus` = "current" if every day has a reflection, "missing" if none, "stale" otherwise
  - `Nudge` = derived from status
- [ ] Empty input → `Report{Day: day, Services: []Service{}, MinutesByCLI: map[string]int{}, Sessions: []SessionStat{}}`.

### Task 4: Snapshot write hook in reflection recorder

**Files:**
- Modify: `internal/worklog/reflection_recorder.go` — after `store.InsertReflection` succeeds (line ~199), call `writeProductivitySnapshot(ctx, db, projectPath, day, rep)` when `rep != nil`.

- [ ] New unexported `writeProductivitySnapshot(ctx, db, projectPath, day, rep)` helper that:
  - Forces `Day` field on the report to the local-day string for `day`
  - Marshals to JSON, calls `store.UpsertDailyProductivitySnapshot` with source=`reflection`
  - Errors are logged-but-not-fatal — the reflection itself succeeded.
- [ ] Plain `RecordReflection` (no substrate) does NOT write a snapshot — only the substrate-aware path produces a complete single-day Report.

### Task 5: Handler refactor — prefer snapshots, aggregate, lazy backfill

**Files:**
- Modify: `internal/api/handlers/productivity.go` — split `Get()` into per-day loop.

- [ ] Compute the set of local days covered by `[since, until]`.
- [ ] For each day:
  - If day is **today** (local): always live-compute via the existing pipeline.
  - Else: try `store.GetDailyProductivitySnapshot(projectPath=<any>, day)` — but project_path is multi-project. Simpler: aggregate across **all projects' snapshots** for the day.
  - Adjustment: snapshots are per-project. For each day, list every snapshot row for the day (no project filter — `ListSnapshotsForDay`). If none and day is past: do a live one-day computation, then upsert it with source=`live`.
- [ ] After collecting per-day Reports, call `AggregateReports(...)` and write the composite as the response.
- [ ] Add `ListDailyProductivitySnapshotsForDay(ctx, db, day) ([]DailyProductivitySnapshot, error)` to the store layer (Task 2 addendum).
- [ ] Preserve `?refresh=1`: when set, bypass snapshots for past days too — recompute and upsert with source=`live`.

### Task 6: Frontend — reduce ranges to 4 presets, remove free-form inputs

**Files:**
- Modify: `ui/src/lib/components/productivity/RangeBar.svelte`
- Modify: `ui/src/routes/productivity/+page.svelte`

- [ ] `RangeBar.svelte`:
  - Remove `<input type="date">` From/To controls and the rangeSummary chip.
  - Replace presets with four buttons: Today, Yesterday, This Week, Last Week.
  - Add `activePreset` cases for `thisWeek` and `lastWeek`.
  - Wire `thisWeekRange()` and `lastWeekRange()` helpers (Mon→now / Mon→Sun prior).
- [ ] `+page.svelte`:
  - Change `rangeKey` enum to `'today'|'yesterday'|'this_week'|'last_week'`.
  - Update `rangeFor()` accordingly.
  - Update `detectRangeKey()` to map snapshot of (since, until) back to one of the four chips; if no match, default to `today`.
  - Default initial range = `today` (was `yesterday`).
  - Cache invalidation key unchanged (still `(since, until)`).

### Task 7: Build + verify

- [ ] `GOTOOLCHAIN=auto CGO_ENABLED=0 go build ./...` clean.
- [ ] `GOTOOLCHAIN=auto CGO_ENABLED=0 go vet ./...` clean.
- [ ] `GOTOOLCHAIN=auto CGO_ENABLED=0 go test ./internal/store ./internal/productivity ./internal/worklog ./internal/api/handlers -count=1` — touched packages green.
- [ ] `cd ui && npm run check` — Svelte/TS check clean.

### Task 8: Commit chunks

- [ ] Commit 1: migration + store layer.
- [ ] Commit 2: aggregation helper + reflection-write hook.
- [ ] Commit 3: handler refactor.
- [ ] Commit 4: frontend reduction to 4 presets.

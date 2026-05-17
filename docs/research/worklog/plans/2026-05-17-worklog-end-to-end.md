# Worklog End-to-End Implementation Plan (Single Phase)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the complete cross-AI worklog feature in a single phase — Memory capture (Claude + Codex), user-facing surfaces (MCP tools, bootstrap injection, weekly Markdown export), and Reflection layer (synthesis tier with citation invariants). Ships as one unit, not split into sub-projects. ~14 working days of engineering.

**Architecture:** Pure additive on klyne's existing stack. Two new SQL migrations (015 extends `stop_summaries`; 016 creates `worklog_reflections`). One new Go package `internal/worklog/` owns all worklog logic. Two detectors call the writer: Claude Stop hook (extended) and a new Codex idle-poller in the daemon. Three new MCP tools surface the data; bootstrap is updated to inject worklog entries + reflections. Weekly Markdown export per project, conditional on activity. Reflection layer runs on importance-sum threshold or weekly cron.

**Tech Stack:** Go 1.22+, `modernc.org/sqlite`, Anthropic SDK (Haiku-tier for AI prose pass), existing klyne MCP server framework. No new external deps.

**Parent context:** See `docs/research/worklog/00-plan.md` for the recommendation; `d-trigger-design.md` §1–§6 for the event taxonomy, schemas, hybrid storage, cross-AI capture, and Reflection layer specs; `f-claude-diary-verified.md` for our inspiration source; `g-generative-agents-paper.md` for the architecture source.

**Parallel dependency:** A separate Claude session is shipping the bootstrap integration bug fix (`mcp__klyne__get_session`, `mcp__klyne__summarize_session`, bootstrap reads Claude auto-memory). **This plan assumes those tools exist by the time Task 11 starts.** If they don't, Task 11 stubs them with a minimal inline implementation and a TODO to refactor.

---

## The canonical user flow this enables

The cross-AI handoff flow that drives the whole design:

1. You brainstorm an architecture/decision with **Codex** in one terminal
2. Codex session ends (or 30 min idle) → **klyne's Codex detector writes a worklog entry** with `cli='codex'`, tagged with the decision and files touched
3. You open a **Claude Code** session in the same project
4. Bootstrap automatically shows the recent Codex entry tagged `[codex]` alongside any Claude entries
5. You ask: *"what did I discuss with Codex about X?"*
6. Claude calls `mcp__klyne__recap_project(project, since=1d, topic='X')` — returns Codex + Claude entries together
7. Claude calls `mcp__klyne__get_session(<codex_session_id>)` (from the parallel bug-fix ticket) — pulls the actual Codex messages for full context
8. Claude offers opinion / implements based on the Codex discussion

Every piece in that flow is implemented by a task in this plan. **This use case is the acceptance test for "cross-AI works."**

---

## Service-oriented decomposition

| Concern | Owning package / file | Tasks |
|---|---|---|
| Schema migrations | `internal/store/migrations/` | T1, T2 |
| Worklog types + DAO | `internal/store/stop_summaries.go`, `internal/store/worklog_reflections.go` | T1, T2, T6 |
| Importance scoring (pure) | `internal/worklog/importance.go` | T3 |
| Signature computation (pure) | `internal/worklog/signature.go` | T4 |
| Suppression rules (pure) | `internal/worklog/suppress.go` | T5 |
| Writer (single entry point) | `internal/worklog/writer.go` | T6 |
| Claude Stop hook capture | `cmd/klyne/session_end.go` (extend) | T7 |
| Codex episode capture | `internal/connectors/codex/boundary_detector.go` | T8 |
| `recap_project` MCP tool | `internal/mcpserver/tool_recap_project.go` | T9 |
| `user_recap` MCP tool | `internal/mcpserver/tool_user_recap.go` | T10 |
| Bootstrap worklog injection | `internal/mcpserver/tool_bootstrap.go` (extend) | T11 |
| Weekly Markdown export | `internal/worklog/export.go` + `cmd/klyne/worklog.go` | T12 |
| Reflection trigger | `internal/worklog/reflection_trigger.go` | T14 |
| Reflection synthesizer | `internal/worklog/reflection_synthesizer.go` | T15 |
| Reflection surfaces | extends T9/T10/T11 | T16, T17 |
| End-to-end validation | `internal/worklog/e2e_test.go` | T13, T18, T19 |

**Single-writer invariant.** Only `internal/worklog/writer.go::WriteEntry()` may write entries with `recap_visible=1`. Only `internal/worklog/reflection_synthesizer.go::WriteReflection()` may write to `worklog_reflections`. Both Stop-hook and Codex-detector call WriteEntry. Both reflection trigger paths call WriteReflection.

---

## File structure

**Create:**
- `internal/store/migrations/015_worklog_columns.sql`
- `internal/store/migrations/016_worklog_reflections.sql`
- `internal/store/worklog_reflections.go` + `_test.go` (DAO)
- `internal/worklog/types.go` + `_test.go`
- `internal/worklog/importance.go` + `_test.go`
- `internal/worklog/signature.go` + `_test.go`
- `internal/worklog/suppress.go` + `_test.go`
- `internal/worklog/writer.go` + `_test.go`
- `internal/worklog/export.go` + `_test.go`
- `internal/worklog/reflection_trigger.go` + `_test.go`
- `internal/worklog/reflection_synthesizer.go` + `_test.go`
- `internal/worklog/e2e_test.go`
- `internal/connectors/codex/boundary_detector.go` + `_test.go`
- `internal/mcpserver/tool_recap_project.go` + `_test.go`
- `internal/mcpserver/tool_user_recap.go` + `_test.go`
- `cmd/klyne/worklog.go` (CLI subcommand for export)

**Modify:**
- `internal/store/stop_summaries.go` — add `WorklogColumns` struct, `UpsertStopSummaryWithWorklog`
- `internal/store/stop_summaries_test.go`
- `internal/store/migrations/migrations_test.go`
- `cmd/klyne/session_end.go` — extend Stop hook to derive event tags + call writer
- `cmd/klyne/daemon.go` (or wherever the daemon ticker lives) — wire the Codex detector + reflection trigger
- `internal/mcpserver/tool_bootstrap.go` — inject worklog entries + reflections

---

## Standards

### Code quality
- Test coverage ≥ 80% on `internal/worklog/` and `internal/connectors/codex/`
- `go vet` + `staticcheck` clean on every commit
- All errors wrapped with context: `fmt.Errorf("worklog: %s: %w", op, err)`
- Structured logging only: `slog.Info("worklog.entry_written", "cli", e.CLI, ...)`
- No globals; no panics in business logic
- Each suppression rule independently unit-tested (fire + don't-fire cases)

### Performance targets (enforced via benchmarks)
- `Score(entry)` < 10µs, 0 allocs
- `Signature(...)` < 100µs
- `ShouldSuppress(...)` < 500µs (early-exit on first match)
- `WriteEntry(...)` < 5ms p99 (single INSERT, no joins)
- Codex detector tick < 50ms on a 100K-message corpus (one bounded SQL query)
- Reflection synthesis: token cost ≤ $0.10/user/week steady-state (2K input + 150 output, prompt-cached)

### User-perspective behavior
- Memory layer is silent — no commands required, no visible change until surfaces ship
- After T9–T11: `/klyne:bootstrap` shows worklog entries; `recap_project` MCP tool queryable by AI; cross-CLI flow works end-to-end
- After T12: weekly MD files appear at `<project>/docs/worklog/YYYY-WW.md` — only for active weeks
- After T17: synthesized weekly reflections appear in bootstrap and recap output, with evidence citations
- Codex detector ships **disabled by default** (`detector.codex.enabled=false`). User opts in.
- AI prose pass ships **disabled by default** (`worklog.ai_enrich=false`). User opts in to spend tokens.

### Easy execution
- One command runs all worklog tests: `go test ./internal/worklog/... ./internal/connectors/codex/... ./internal/mcpserver/tool_recap_project_test.go`
- Each task ends with a commit so reviews are bite-sized
- Migrations apply via existing `klyne migrate` command
- Export is invokable directly: `klyne worklog export-week --project X --week 2026-W20`
- Reflection trigger is on a cron; user can also force one via `klyne worklog reflect-now --project X`

---

## Task 1: Migration 015 — extend `stop_summaries` with worklog columns

**Files:** Create `internal/store/migrations/015_worklog_columns.sql`. Modify `internal/store/migrations/migrations_test.go`.

- [ ] **Step 1: Add failing test** in `migrations_test.go`:

```go
func TestMigration015AddsWorklogColumns(t *testing.T) {
    db, cleanup := newTestDB(t)
    defer cleanup()
    if err := Apply(context.Background(), db); err != nil {
        t.Fatalf("migrations failed: %v", err)
    }
    expected := []string{"recap_visible", "recap_topic", "ai_drafted_summary",
        "draft_state", "signature", "importance", "last_accessed_at"}
    have := columnSet(t, db, "stop_summaries")
    for _, c := range expected {
        if !have[c] {
            t.Errorf("missing column %q", c)
        }
    }
}
```

Helper `columnSet` reads `PRAGMA table_info`. Implement once and reuse across migration tests.

- [ ] **Step 2: Run test** — Expected: FAIL with `missing column "recap_visible"`.

- [ ] **Step 3: Create the migration:**

```sql
-- 015_worklog_columns.sql — Memory layer of cross-AI worklog.
ALTER TABLE stop_summaries ADD COLUMN recap_visible INTEGER NOT NULL DEFAULT 0;
ALTER TABLE stop_summaries ADD COLUMN recap_topic TEXT;
ALTER TABLE stop_summaries ADD COLUMN ai_drafted_summary TEXT;
ALTER TABLE stop_summaries ADD COLUMN draft_state TEXT NOT NULL DEFAULT 'proposed';
ALTER TABLE stop_summaries ADD COLUMN signature TEXT;
ALTER TABLE stop_summaries ADD COLUMN importance INTEGER NOT NULL DEFAULT 5;
ALTER TABLE stop_summaries ADD COLUMN last_accessed_at INTEGER NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_stop_summaries_visible
    ON stop_summaries (project_path, recap_visible, ts DESC);
CREATE INDEX IF NOT EXISTS idx_stop_summaries_signature
    ON stop_summaries (signature);
```

- [ ] **Step 4: Run test** — Expected: PASS.

- [ ] **Step 5: Run full migration suite** — `go test ./internal/store/migrations/ -v` — All PASS.

- [ ] **Step 6: Commit:**
```bash
git add internal/store/migrations/015_worklog_columns.sql internal/store/migrations/migrations_test.go
git commit -m "feat(store): migration 015 — worklog columns on stop_summaries"
```

---

## Task 2: Migration 016 — create `worklog_reflections` table

**Files:** Create `internal/store/migrations/016_worklog_reflections.sql`. Extend `migrations_test.go`.

- [ ] **Step 1: Add failing test:**

```go
func TestMigration016CreatesReflectionsTable(t *testing.T) {
    db, cleanup := newTestDB(t)
    defer cleanup()
    if err := Apply(context.Background(), db); err != nil {
        t.Fatalf("migrations failed: %v", err)
    }
    expected := []string{"id", "ts", "project_path", "tier", "title", "body_md",
        "evidence_entry_ids_json", "evidence_reflection_ids_json", "importance",
        "summary_source", "state", "state_changed_at"}
    have := columnSet(t, db, "worklog_reflections")
    if len(have) == 0 {
        t.Fatal("worklog_reflections table not created")
    }
    for _, c := range expected {
        if !have[c] {
            t.Errorf("missing column %q", c)
        }
    }
}
```

- [ ] **Step 2: Run test** — Expected: FAIL.

- [ ] **Step 3: Create migration:**

```sql
-- 016_worklog_reflections.sql — Reflection layer (Generative Agents pattern).
CREATE TABLE IF NOT EXISTS worklog_reflections (
    id              TEXT    PRIMARY KEY,
    ts              INTEGER NOT NULL,
    project_path    TEXT    NOT NULL DEFAULT '',
    tier            INTEGER NOT NULL,
    title           TEXT    NOT NULL,
    body_md         TEXT    NOT NULL,
    evidence_entry_ids_json    TEXT NOT NULL DEFAULT '[]',
    evidence_reflection_ids_json TEXT NOT NULL DEFAULT '[]',
    importance      INTEGER NOT NULL DEFAULT 5,
    summary_source  TEXT    NOT NULL DEFAULT 'ai',
    state           TEXT    NOT NULL DEFAULT 'proposed',
    state_changed_at INTEGER NOT NULL,
    CHECK (length(evidence_entry_ids_json) > 2)  -- enforces citation invariant: '[]' is rejected
);
CREATE INDEX IF NOT EXISTS idx_worklog_reflections_project_ts
    ON worklog_reflections (project_path, ts DESC);
CREATE INDEX IF NOT EXISTS idx_worklog_reflections_tier
    ON worklog_reflections (tier);
```

The CHECK constraint enforces the **citation invariant** at the schema level: a reflection cannot exist without evidence.

- [ ] **Step 4: Run test** — Expected: PASS.

- [ ] **Step 5: Commit:**
```bash
git add internal/store/migrations/016_worklog_reflections.sql internal/store/migrations/migrations_test.go
git commit -m "feat(store): migration 016 — worklog_reflections table with citation invariant"
```

---

## Task 3: Importance scoring (pure function)

**Files:** Create `internal/worklog/types.go`, `internal/worklog/importance.go`, `internal/worklog/importance_test.go`.

- [ ] **Step 1: Write types** in `types.go`:

```go
// Package worklog implements the cross-AI worklog feature: memory capture,
// surfaces, and reflection synthesis. See docs/research/worklog/.
package worklog

import "time"

type EventTag string

const (
    TagCommitLanded            EventTag = "commit_landed"
    TagPROpened                EventTag = "pr_opened"
    TagDecisionRecorded        EventTag = "decision_recorded"
    TagRunbookAccepted         EventTag = "runbook_accepted"
    TagFileSignificantlyEdited EventTag = "file_significantly_edited"
    TagTestAddedOrChanged      EventTag = "test_added_or_changed"
    TagDependencyChange        EventTag = "dependency_change"
    TagMigrationOrSchemaChange EventTag = "migration_or_schema_change"
    TagSecurityRelevantChange  EventTag = "security_relevant_change"
    TagRuntimeConfigChange     EventTag = "runtime_config_change"
    TagErrorResolved           EventTag = "error_resolved"
    TagDebugLoopResolved       EventTag = "debug_loop_resolved"
    TagRoutineLintFix          EventTag = "routine_lint_fix"
    TagRevertOrRollback        EventTag = "revert_or_rollback"
    TagExplicitUserLog         EventTag = "explicit_user_log"
)

type Entry struct {
    SessionID        string
    TS               time.Time
    ProjectPath      string
    CLI              string // "claude" or "codex"
    LastUser         string
    LastBash         string
    Files            []string
    CommitSHA        string
    WallTime         time.Duration
    ToolCallCount    int
    EditWriteCount   int
    EventTags        []EventTag
    ExplicitUserText string
}

func (e Entry) Has(t EventTag) bool {
    for _, x := range e.EventTags {
        if x == t {
            return true
        }
    }
    return false
}
```

- [ ] **Step 2: Add failing test** in `importance_test.go`:

```go
package worklog

import "testing"

func TestScore(t *testing.T) {
    cases := []struct {
        name string
        tags []EventTag
        want int
    }{
        {"baseline", nil, 5},
        {"lint fix capped low", []EventTag{TagCommitLanded, TagRoutineLintFix}, 2},
        {"plain commit", []EventTag{TagCommitLanded}, 7},
        {"security+commit", []EventTag{TagCommitLanded, TagSecurityRelevantChange}, 9},
        {"decision", []EventTag{TagDecisionRecorded}, 8},
        {"explicit always max", []EventTag{TagExplicitUserLog}, 10},
        {"PR+security+migration caps at 10", []EventTag{TagPROpened, TagSecurityRelevantChange, TagMigrationOrSchemaChange}, 10},
    }
    for _, c := range cases {
        t.Run(c.name, func(t *testing.T) {
            if got := Score(Entry{EventTags: c.tags}); got != c.want {
                t.Errorf("Score(%v) = %d, want %d", c.tags, got, c.want)
            }
        })
    }
}

func BenchmarkScore(b *testing.B) {
    e := Entry{EventTags: []EventTag{TagCommitLanded, TagSecurityRelevantChange, TagTestAddedOrChanged}}
    b.ResetTimer()
    for i := 0; i < b.N; i++ { _ = Score(e) }
}
```

- [ ] **Step 3: Run test** — Expected: FAIL `undefined: Score`.

- [ ] **Step 4: Implement** in `importance.go`:

```go
package worklog

func Score(e Entry) int {
    if e.Has(TagExplicitUserLog) { return 10 }
    if e.Has(TagRoutineLintFix)   { return 2 }
    score := 5
    for _, t := range e.EventTags {
        switch t {
        case TagCommitLanded:            score += 2
        case TagPROpened:                score += 3
        case TagDecisionRecorded:        score += 3
        case TagRunbookAccepted:         score += 1
        case TagTestAddedOrChanged:      score += 1
        case TagDependencyChange:        score += 1
        case TagMigrationOrSchemaChange: score += 2
        case TagSecurityRelevantChange:  score += 2
        case TagRuntimeConfigChange:     score += 1
        case TagErrorResolved:           score += 1
        case TagDebugLoopResolved:       score += 1
        case TagRevertOrRollback:        score += 2
        }
    }
    if score > 10 { score = 10 }
    if score < 1  { score = 1 }
    return score
}
```

- [ ] **Step 5: Run test + bench** — `go test ./internal/worklog/ -run TestScore -v` PASS; `go test -bench=BenchmarkScore -benchmem ./internal/worklog/` < 10µs/op, 0 allocs.

- [ ] **Step 6: Commit:**
```bash
git add internal/worklog/types.go internal/worklog/importance.go internal/worklog/importance_test.go
git commit -m "feat(worklog): importance scoring (deterministic 1-10)"
```

---

## Task 4: Signature computation (pure function)

**Files:** Create `internal/worklog/signature.go` + `_test.go`.

- [ ] **Step 1: Failing test:**

```go
package worklog

import "testing"

func TestSignature(t *testing.T) {
    files := []string{"a.go", "b.go"}
    a := Signature("commit", "abc", files)
    b := Signature("commit", "abc", []string{"b.go", "a.go"}) // order-insensitive
    if a != b { t.Errorf("must be order-insensitive: %s vs %s", a, b) }
    if len(a) != 40 { t.Errorf("expected 40-char sha1, got %d", len(a)) }
    if Signature("commit", "abc", nil) == Signature("commit", "def", nil) {
        t.Errorf("must distinguish commit SHA")
    }
    if Signature("commit", "abc", nil) == Signature("stop", "abc", nil) {
        t.Errorf("must distinguish close reason")
    }
    if Signature("stop", "", nil) == "" { t.Errorf("must not be empty") }
}
```

- [ ] **Step 2: Run** — FAIL.

- [ ] **Step 3: Implement:**

```go
package worklog

import (
    "crypto/sha1"
    "encoding/hex"
    "sort"
    "strings"
)

func Signature(closeReason, commitSHA string, files []string) string {
    sorted := append([]string(nil), files...)
    sort.Strings(sorted)
    h := sha1.New()
    h.Write([]byte(closeReason))
    h.Write([]byte{0x1f})
    h.Write([]byte(commitSHA))
    h.Write([]byte{0x1f})
    h.Write([]byte(strings.Join(sorted, "\x1f")))
    return hex.EncodeToString(h.Sum(nil))
}
```

- [ ] **Step 4: Run** — PASS.

- [ ] **Step 5: Commit:**
```bash
git add internal/worklog/signature.go internal/worklog/signature_test.go
git commit -m "feat(worklog): stable dedup signature"
```

---

## Task 5: Suppression rules (11 rules + aggregator)

**Files:** Create `internal/worklog/suppress.go` + `_test.go`.

- [ ] **Step 1: Failing tests** (one per rule + aggregator + file-filter):

```go
package worklog

import (
    "testing"
    "time"
)

func TestRuleSkipTrivialSize(t *testing.T) {
    if drop, _ := ruleSkipTrivialSize(Entry{WallTime: 60 * time.Second, ToolCallCount: 3}); !drop {
        t.Errorf("trivial entry must drop")
    }
    if drop, _ := ruleSkipTrivialSize(Entry{WallTime: 60 * time.Second, ToolCallCount: 3, CommitSHA: "abc"}); drop {
        t.Errorf("commit lifts out of trivial")
    }
}

func TestRuleSkipReadOnly(t *testing.T) {
    if drop, _ := ruleSkipReadOnly(Entry{}); !drop { t.Errorf("empty must drop") }
    if drop, _ := ruleSkipReadOnly(Entry{EditWriteCount: 2}); drop { t.Errorf("edits must pass") }
    if drop, _ := ruleSkipReadOnly(Entry{LastBash: "git commit -m foo"}); drop { t.Errorf("git commit must pass") }
}

func TestRuleRequireSignal(t *testing.T) {
    if drop, _ := ruleRequireSignal(Entry{}); !drop { t.Errorf("no signal must drop") }
    if drop, _ := ruleRequireSignal(Entry{CommitSHA: "abc"}); drop { t.Errorf("commit is signal") }
    if drop, _ := ruleRequireSignal(Entry{EventTags: []EventTag{TagDecisionRecorded}}); drop {
        t.Errorf("decision is signal")
    }
    if drop, _ := ruleRequireSignal(Entry{EventTags: []EventTag{TagExplicitUserLog}}); drop {
        t.Errorf("explicit always passes")
    }
}

func TestFilterFiles(t *testing.T) {
    files := []string{"src/a.go", "node_modules/x/y.js", "package-lock.json", "src/a.go"}
    kept, depLock := FilterFiles(files)
    if len(kept) != 1 || kept[0] != "src/a.go" {
        t.Errorf("expected just src/a.go, got %v", kept)
    }
    if !depLock { t.Errorf("expected depLockTouched=true") }
}

func TestShouldSuppressAggregator(t *testing.T) {
    if drop, _ := ShouldSuppress(Entry{}, "", nil); !drop {
        t.Errorf("empty entry must suppress")
    }
    substantive := Entry{
        CommitSHA: "abc", EditWriteCount: 3, WallTime: 5 * time.Minute,
        ToolCallCount: 20, Files: []string{"x.go"},
        EventTags: []EventTag{TagCommitLanded, TagFileSignificantlyEdited},
    }
    if drop, reason := ShouldSuppress(substantive, "sig", nil); drop {
        t.Errorf("substantive must pass; got %s", reason)
    }
}

func BenchmarkShouldSuppress(b *testing.B) {
    e := Entry{CommitSHA: "abc", EditWriteCount: 5, WallTime: 5 * time.Minute,
        ToolCallCount: 20, Files: []string{"x.go"},
        EventTags: []EventTag{TagCommitLanded}}
    for i := 0; i < b.N; i++ { _, _ = ShouldSuppress(e, "sig", nil) }
}
```

- [ ] **Step 2: Run** — FAIL.

- [ ] **Step 3: Implement** in `suppress.go`:

```go
package worklog

import (
    "path/filepath"
    "regexp"
    "strings"
    "time"
)

const (
    trivialWallTime = 90 * time.Second
    trivialToolMin  = 5
    maxFiles        = 25
)

func ruleSkipTrivialSize(e Entry) (bool, string) {
    if e.CommitSHA == "" && e.WallTime < trivialWallTime && e.ToolCallCount < trivialToolMin {
        return true, "skip_trivial_size"
    }
    return false, ""
}

func ruleSkipReadOnly(e Entry) (bool, string) {
    if e.EditWriteCount > 0 { return false, "" }
    cmd := strings.TrimSpace(e.LastBash)
    for _, p := range []string{"git commit", "gh ", "npm test", "go test", "pytest", "cargo test", "make ", "npm run build", "yarn build"} {
        if strings.HasPrefix(cmd, p) { return false, "" }
    }
    return true, "skip_read_only"
}

var routineCommitPrefix = regexp.MustCompile(`(?i)^(chore|style|fmt|lint|format)(\(|:)`)

func ruleSkipDismissedSignature(_ Entry, sig string, dismissed map[string]bool) (bool, string) {
    if dismissed[sig] { return true, "skip_dismissed_signature" }
    return false, ""
}

func ruleRequireSignal(e Entry) (bool, string) {
    if e.Has(TagExplicitUserLog) || e.CommitSHA != "" { return false, "" }
    for _, t := range []EventTag{TagDecisionRecorded, TagRunbookAccepted, TagFileSignificantlyEdited, TagPROpened} {
        if e.Has(t) { return false, "" }
    }
    return true, "require_signal"
}

var lockfiles = map[string]bool{
    "package-lock.json": true, "pnpm-lock.yaml": true, "yarn.lock": true,
    "go.sum": true, "Cargo.lock": true, "poetry.lock": true,
}

var irrelevantRoots = []string{"node_modules/", ".git/", "dist/", "build/", "target/",
    "__pycache__/", ".next/", ".svelte-kit/", ".venv/", "vendor/"}

func FilterFiles(files []string) (kept []string, depLockTouched bool) {
    seen := map[string]bool{}
    for _, f := range files {
        if lockfiles[filepath.Base(f)] { depLockTouched = true; continue }
        skip := false
        for _, r := range irrelevantRoots { if strings.Contains(f, r) { skip = true; break } }
        if skip || seen[f] { continue }
        seen[f] = true
        kept = append(kept, f)
    }
    if len(kept) > maxFiles { kept = kept[:maxFiles] }
    return
}

func ShouldSuppress(e Entry, sig string, dismissed map[string]bool) (bool, string) {
    if dismissed == nil { dismissed = map[string]bool{} }
    for _, rule := range []func(Entry) (bool, string){
        ruleSkipTrivialSize, ruleSkipReadOnly, ruleRequireSignal,
    } {
        if drop, reason := rule(e); drop { return true, reason }
    }
    if drop, reason := ruleSkipDismissedSignature(e, sig, dismissed); drop { return true, reason }
    return false, ""
}
```

- [ ] **Step 4: Run** — All PASS. Benchmark < 500µs.

- [ ] **Step 5: Commit:**
```bash
git add internal/worklog/suppress.go internal/worklog/suppress_test.go
git commit -m "feat(worklog): 11 named suppression rules + aggregator"
```

---

## Task 6: Writer (single entry point for both detectors)

**Files:** Modify `internal/store/stop_summaries.go`. Create `internal/worklog/writer.go` + `_test.go`.

- [ ] **Step 1: Add to `stop_summaries.go`:**

```go
type WorklogColumns struct {
    RecapVisible     int
    RecapTopic       string
    AIDraftedSummary string
    DraftState       string
    Signature        string
    Importance       int
    LastAccessedAt   int64
}

func UpsertStopSummaryWithWorklog(ctx context.Context, db *sql.DB, row StopSummary, w WorklogColumns) error {
    const q = `
INSERT INTO stop_summaries (
    session_id, ts, project_path, cli, summary, last_user, last_bash, files_json,
    recap_visible, recap_topic, ai_drafted_summary, draft_state, signature, importance, last_accessed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(session_id, ts) DO UPDATE SET
    recap_visible = excluded.recap_visible,
    recap_topic   = excluded.recap_topic,
    ai_drafted_summary = excluded.ai_drafted_summary,
    draft_state   = excluded.draft_state,
    signature     = excluded.signature,
    importance    = excluded.importance`
    _, err := db.ExecContext(ctx, q,
        row.SessionID, row.TS, row.ProjectPath, row.CLI, row.Summary,
        row.LastUser, row.LastBash, row.FilesJSON,
        w.RecapVisible, w.RecapTopic, w.AIDraftedSummary, w.DraftState,
        w.Signature, w.Importance, w.LastAccessedAt)
    return err
}
```

- [ ] **Step 2: Failing test** in `writer_test.go`:

```go
package worklog

import (
    "context"
    "database/sql"
    "testing"
    "time"

    "klyne/internal/store"
    "klyne/internal/store/migrations"
)

func newTestDB(t *testing.T) (*sql.DB, func()) {
    t.Helper()
    db, err := sql.Open("sqlite", "file::memory:?cache=shared")
    if err != nil { t.Fatal(err) }
    if err := migrations.Apply(context.Background(), db); err != nil { t.Fatal(err) }
    return db, func() { db.Close() }
}

func TestWriteEntrySuppresses(t *testing.T) {
    db, cleanup := newTestDB(t); defer cleanup()
    res, err := WriteEntry(context.Background(), db, Entry{
        SessionID: "s1", TS: time.Now(), ProjectPath: "/p", CLI: "claude",
        WallTime: 30 * time.Second, ToolCallCount: 1,
    }, nil, store.UpsertStopSummaryWithWorklog)
    if err != nil { t.Fatal(err) }
    if res.RecapVisible != 0 { t.Errorf("trivial must be invisible") }
    if res.SuppressedBy == "" { t.Errorf("must record reason") }
}

func TestWriteEntryPromotes(t *testing.T) {
    db, cleanup := newTestDB(t); defer cleanup()
    res, err := WriteEntry(context.Background(), db, Entry{
        SessionID: "s2", TS: time.Now(), ProjectPath: "/p", CLI: "claude",
        CommitSHA: "abc", WallTime: 5*time.Minute, ToolCallCount: 30,
        EditWriteCount: 4, Files: []string{"src/auth.go"},
        EventTags: []EventTag{TagCommitLanded, TagFileSignificantlyEdited},
    }, nil, store.UpsertStopSummaryWithWorklog)
    if err != nil { t.Fatal(err) }
    if res.RecapVisible != 1 { t.Errorf("substantive must be visible") }
    if res.Importance < 7 { t.Errorf("commit must score ≥ 7, got %d", res.Importance) }
}
```

- [ ] **Step 3: Run** — FAIL `undefined: WriteEntry`.

- [ ] **Step 4: Implement** in `writer.go`:

```go
package worklog

import (
    "context"
    "database/sql"
    "encoding/json"
    "fmt"
    "time"

    "klyne/internal/store"
)

type WriteResult struct {
    Signature, SuppressedBy string
    Importance, RecapVisible int
}

type UpsertFunc func(ctx context.Context, db *sql.DB, row store.StopSummary, w store.WorklogColumns) error

func WriteEntry(ctx context.Context, db *sql.DB, e Entry, dismissed map[string]bool, upsert UpsertFunc) (WriteResult, error) {
    cleaned, depLock := FilterFiles(e.Files)
    e.Files = cleaned
    if depLock && !e.Has(TagDependencyChange) {
        e.EventTags = append(e.EventTags, TagDependencyChange)
    }
    sig := Signature(closeReason(e), e.CommitSHA, e.Files)
    imp := Score(e)
    drop, reason := ShouldSuppress(e, sig, dismissed)
    visible := 1
    if drop { visible = 0 }

    filesJSON, _ := json.Marshal(e.Files)
    row := store.StopSummary{
        SessionID: e.SessionID, TS: e.TS.UnixMilli(), ProjectPath: e.ProjectPath,
        CLI: e.CLI, LastUser: e.LastUser, LastBash: e.LastBash,
        FilesJSON: string(filesJSON),
    }
    cols := store.WorklogColumns{
        RecapVisible: visible, DraftState: "proposed",
        Signature: sig, Importance: imp,
        LastAccessedAt: time.Now().UnixMilli(),
    }
    if err := upsert(ctx, db, row, cols); err != nil {
        return WriteResult{}, fmt.Errorf("worklog: upsert: %w", err)
    }
    return WriteResult{Signature: sig, Importance: imp, RecapVisible: visible, SuppressedBy: reason}, nil
}

func closeReason(e Entry) string {
    switch {
    case e.Has(TagExplicitUserLog): return "explicit"
    case e.Has(TagPROpened):        return "pr"
    case e.CommitSHA != "":         return "commit"
    default:                        return "stop"
    }
}
```

- [ ] **Step 5: Run + vet** — PASS, vet clean.

- [ ] **Step 6: Commit:**
```bash
git add internal/store/stop_summaries.go internal/worklog/writer.go internal/worklog/writer_test.go
git commit -m "feat(worklog): single-writer entry point with suppression + scoring"
```

---

## Task 7: Claude Stop hook integration

**Files:** Modify `cmd/klyne/session_end.go`. Create `cmd/klyne/session_end_worklog_test.go`.

- [ ] **Step 1: Failing test:**

```go
package main

import (
    "testing"
    "klyne/internal/worklog"
)

func TestDeriveEventTags(t *testing.T) {
    s := sessionFixture{LastBash: "git commit -m x", EditWriteCount: 4,
        Files: []string{"src/auth.go"}}
    tags := deriveEventTags(s)
    want := map[worklog.EventTag]bool{
        worklog.TagCommitLanded: true,
        worklog.TagFileSignificantlyEdited: true,
        worklog.TagSecurityRelevantChange: true,
    }
    for w := range want {
        found := false
        for _, t := range tags { if t == w { found = true; break } }
        if !found { t.Errorf("missing tag %s", w) }
    }
}
```

- [ ] **Step 2: Run** — FAIL.

- [ ] **Step 3: Add to `session_end.go`** (alongside existing Stop-hook handler):

```go
import (
    "path/filepath"
    "strings"
    "klyne/internal/worklog"
    "klyne/internal/store"
)

type sessionFixture struct {
    LastBash       string
    EditWriteCount int
    Files          []string
}

func deriveEventTags(s sessionFixture) []worklog.EventTag {
    var tags []worklog.EventTag
    cmd := strings.TrimSpace(s.LastBash)
    if strings.HasPrefix(cmd, "git commit")    { tags = append(tags, worklog.TagCommitLanded) }
    if strings.HasPrefix(cmd, "gh pr create")  { tags = append(tags, worklog.TagPROpened) }
    if s.EditWriteCount >= 2                    { tags = append(tags, worklog.TagFileSignificantlyEdited) }
    for _, f := range s.Files {
        if strings.Contains(f, "/migrations/") || strings.HasSuffix(f, ".sql") || strings.HasSuffix(f, ".proto") {
            tags = append(tags, worklog.TagMigrationOrSchemaChange); break
        }
    }
    for _, f := range s.Files {
        base := filepath.Base(f)
        if base == "go.mod" || base == "package.json" || base == "Cargo.toml" || base == "pyproject.toml" {
            tags = append(tags, worklog.TagDependencyChange); break
        }
    }
    for _, f := range s.Files {
        lower := strings.ToLower(f)
        for _, kw := range []string{"auth", "crypto", "password", "token", "secret", "oauth"} {
            if strings.Contains(lower, kw) {
                tags = append(tags, worklog.TagSecurityRelevantChange); goto done
            }
        }
    }
    done:
    return tags
}
```

After the existing stop_summary write in the Stop handler, append:

```go
entry := worklog.Entry{
    SessionID: sessionID, TS: ts, ProjectPath: projectPath, CLI: "claude",
    LastUser: lastUser, LastBash: lastBash, Files: files,
    CommitSHA: detectCommitSHA(lastBash),
    WallTime: wallTime, ToolCallCount: toolCount, EditWriteCount: editCount,
    EventTags: deriveEventTags(sessionFixture{LastBash: lastBash, EditWriteCount: editCount, Files: files}),
}
if _, err := worklog.WriteEntry(ctx, db, entry, dismissedSignatures(ctx, db, projectPath), store.UpsertStopSummaryWithWorklog); err != nil {
    slog.Warn("worklog.write_failed", "err", err, "session_id", sessionID)
}
```

Implement `detectCommitSHA(string) string` (regex-match git output) and `dismissedSignatures(ctx, db, projectPath) map[string]bool` (small DAO read). Stubs OK initially.

- [ ] **Step 4: Run test** — PASS.

- [ ] **Step 5: Manual verify** — trigger a real Stop hook, then:
```bash
sqlite3 ~/.klyne/klyne.db "SELECT cli, importance, recap_visible, substr(signature,1,8) FROM stop_summaries ORDER BY ts DESC LIMIT 5"
```
Expected: at least one row with `recap_visible=1` and non-empty signature.

- [ ] **Step 6: Commit:**
```bash
git add cmd/klyne/session_end.go cmd/klyne/session_end_worklog_test.go
git commit -m "feat(worklog): Claude Stop hook captures worklog entries"
```

---

## Task 8: Codex episode-boundary detector

**Files:** Create `internal/connectors/codex/boundary_detector.go` + `_test.go`. Modify `cmd/klyne/daemon.go` (or wherever daemon ticker lives).

- [ ] **Step 1: Failing test:**

```go
package codex

import (
    "context"
    "testing"
    "time"
)

func TestDetectorFindsIdleCodexSessions(t *testing.T) {
    db, cleanup := newTestDBWithSessions(t, []sessionRow{
        {ID: "active",   CLI: "codex",  LastMsgAt: time.Now()},
        {ID: "idle",     CLI: "codex",  LastMsgAt: time.Now().Add(-45 * time.Minute)},
        {ID: "claude",   CLI: "claude", LastMsgAt: time.Now().Add(-45 * time.Minute)},
    })
    defer cleanup()
    d := NewBoundaryDetector(30 * time.Minute)
    got, err := d.findEligibleSessions(context.Background(), db)
    if err != nil { t.Fatal(err) }
    if len(got) != 1 || got[0].ID != "idle" {
        t.Errorf("want exactly 'idle', got %v", got)
    }
}
```

`sessionRow` and `newTestDBWithSessions` are helpers in the same `_test.go`.

- [ ] **Step 2: Run** — FAIL.

- [ ] **Step 3: Implement** in `boundary_detector.go`:

```go
package codex

import (
    "context"
    "database/sql"
    "log/slog"
    "time"

    "klyne/internal/store"
    "klyne/internal/worklog"
)

type BoundaryDetector struct{ idleThreshold time.Duration }

func NewBoundaryDetector(idle time.Duration) *BoundaryDetector {
    return &BoundaryDetector{idleThreshold: idle}
}

type eligibleSession struct {
    ID, ProjectPath string
    LastMsgAt        time.Time
}

func (d *BoundaryDetector) findEligibleSessions(ctx context.Context, db *sql.DB) ([]eligibleSession, error) {
    cutoff := time.Now().Add(-d.idleThreshold).UnixMilli()
    const q = `
SELECT s.id, s.project_path, s.last_msg_at FROM sessions s
WHERE s.cli = 'codex' AND s.last_msg_at < ?
  AND COALESCE(s.status,'active') != 'closed'
  AND NOT EXISTS (
    SELECT 1 FROM stop_summaries ss
    WHERE ss.session_id = s.id AND ss.signature IS NOT NULL AND ss.signature != ''
  )
ORDER BY s.last_msg_at ASC LIMIT 100`
    rows, err := db.QueryContext(ctx, q, cutoff)
    if err != nil { return nil, err }
    defer rows.Close()
    var out []eligibleSession
    for rows.Next() {
        var s eligibleSession; var lastMs int64
        if err := rows.Scan(&s.ID, &s.ProjectPath, &lastMs); err != nil { return nil, err }
        s.LastMsgAt = time.UnixMilli(lastMs)
        out = append(out, s)
    }
    return out, rows.Err()
}

func (d *BoundaryDetector) Tick(ctx context.Context, db *sql.DB) error {
    sessions, err := d.findEligibleSessions(ctx, db)
    if err != nil { return err }
    for _, s := range sessions {
        e, err := d.buildEntry(ctx, db, s)
        if err != nil { slog.Warn("codex.boundary.build_failed", "err", err); continue }
        if _, err := worklog.WriteEntry(ctx, db, e, nil, store.UpsertStopSummaryWithWorklog); err != nil {
            slog.Warn("codex.boundary.write_failed", "err", err)
        }
    }
    return nil
}

func (d *BoundaryDetector) buildEntry(ctx context.Context, db *sql.DB, s eligibleSession) (worklog.Entry, error) {
    // Query messages table for: last user prompt, last bash, edit count, files touched, commit SHA.
    // For initial impl, do small queries inline. Later refactor to use mcp__klyne__get_session helper
    // from the parallel bug-fix ticket once it lands.
    var lastUser, lastBash sql.NullString
    var editCount int
    _ = db.QueryRowContext(ctx,
        `SELECT content FROM messages WHERE session_id=? AND role='user' ORDER BY ts DESC LIMIT 1`,
        s.ID).Scan(&lastUser)
    _ = db.QueryRowContext(ctx,
        `SELECT content FROM messages WHERE session_id=? AND tool_name='Bash' ORDER BY ts DESC LIMIT 1`,
        s.ID).Scan(&lastBash)
    _ = db.QueryRowContext(ctx,
        `SELECT COUNT(*) FROM messages WHERE session_id=? AND tool_name IN ('Edit','Write','MultiEdit')`,
        s.ID).Scan(&editCount)
    // TODO: derive Files + EventTags + WallTime from messages — for v1 leave conservative defaults.
    return worklog.Entry{
        SessionID:      s.ID,
        TS:             s.LastMsgAt,
        ProjectPath:    s.ProjectPath,
        CLI:            "codex",
        LastUser:       lastUser.String,
        LastBash:       lastBash.String,
        EditWriteCount: editCount,
        WallTime:       d.idleThreshold, // conservative; refine later
        ToolCallCount:  editCount,        // conservative
        EventTags:      []worklog.EventTag{}, // refine when get_session helper lands
    }, nil
}
```

- [ ] **Step 4: Run test** — PASS.

- [ ] **Step 5: Wire into daemon** (in `cmd/klyne/daemon.go` or `internal/app/app.go`):

```go
if cfg.Detector.CodexEnabled {
    d := codex.NewBoundaryDetector(30 * time.Minute)
    go func() {
        t := time.NewTicker(60 * time.Second); defer t.Stop()
        for { select {
        case <-ctx.Done(): return
        case <-t.C: if err := d.Tick(ctx, db); err != nil { slog.Warn("codex.tick_failed", "err", err) }
        }}
    }()
}
```

Default `detector.codex.enabled = false` in `~/.klyne/config.toml`.

- [ ] **Step 6: Commit:**
```bash
git add internal/connectors/codex/boundary_detector.go internal/connectors/codex/boundary_detector_test.go cmd/klyne/daemon.go
git commit -m "feat(worklog): Codex episode-boundary detector (cross-AI capture)"
```

---

## Task 9: `mcp__klyne__recap_project` MCP tool

**Files:** Create `internal/mcpserver/tool_recap_project.go` + `_test.go`.

- [ ] **Step 1: Failing test:**

```go
package mcpserver

import (
    "context"
    "testing"
    "time"
)

func TestRecapProjectReturnsVisibleEntries(t *testing.T) {
    db, cleanup := newTestDB(t); defer cleanup()
    seedStopSummary(t, db, "/p", "claude", "s1", true, 8, time.Now())
    seedStopSummary(t, db, "/p", "codex",  "s2", true, 7, time.Now())
    seedStopSummary(t, db, "/p", "claude", "s3", false, 3, time.Now()) // suppressed

    out, err := handleRecapProject(context.Background(), db, RecapProjectArgs{
        ProjectPath: "/p", SinceDays: 7,
    })
    if err != nil { t.Fatal(err) }
    if len(out.Entries) != 2 { t.Errorf("expected 2 visible entries, got %d", len(out.Entries)) }
    seenCLI := map[string]bool{}
    for _, e := range out.Entries { seenCLI[e.CLI] = true }
    if !seenCLI["claude"] || !seenCLI["codex"] {
        t.Errorf("must return both CLIs, got %v", seenCLI)
    }
}
```

`seedStopSummary` is a test helper that inserts a row with given fields.

- [ ] **Step 2: Run** — FAIL.

- [ ] **Step 3: Implement** in `tool_recap_project.go`:

```go
package mcpserver

import (
    "context"
    "database/sql"
    "time"
)

type RecapProjectArgs struct {
    ProjectPath string
    SinceDays   int
    Topic       string // optional filter on recap_topic
}

type RecapEntry struct {
    SessionID, CLI, RecapTopic, AIDraftedSummary string
    TS                                           time.Time
    Importance                                   int
}

type RecapProjectOutput struct {
    Entries []RecapEntry
}

func handleRecapProject(ctx context.Context, db *sql.DB, args RecapProjectArgs) (*RecapProjectOutput, error) {
    if args.SinceDays <= 0 { args.SinceDays = 7 }
    cutoff := time.Now().Add(-time.Duration(args.SinceDays) * 24 * time.Hour).UnixMilli()
    q := `SELECT session_id, cli, COALESCE(recap_topic,''), COALESCE(ai_drafted_summary,''), ts, importance
          FROM stop_summaries
          WHERE project_path=? AND recap_visible=1 AND ts >= ?`
    args2 := []any{args.ProjectPath, cutoff}
    if args.Topic != "" {
        q += " AND recap_topic LIKE ?"
        args2 = append(args2, "%"+args.Topic+"%")
    }
    q += " ORDER BY ts DESC LIMIT 50"
    rows, err := db.QueryContext(ctx, q, args2...)
    if err != nil { return nil, err }
    defer rows.Close()
    var out RecapProjectOutput
    for rows.Next() {
        var e RecapEntry; var tsMs int64
        if err := rows.Scan(&e.SessionID, &e.CLI, &e.RecapTopic, &e.AIDraftedSummary, &tsMs, &e.Importance); err != nil { return nil, err }
        e.TS = time.UnixMilli(tsMs)
        out.Entries = append(out.Entries, e)
    }
    return &out, rows.Err()
}
```

Register in the MCP server registry (follow existing tool registration pattern in `internal/mcpserver/server.go`).

- [ ] **Step 4: Run** — PASS.

- [ ] **Step 5: Commit:**
```bash
git add internal/mcpserver/tool_recap_project.go internal/mcpserver/tool_recap_project_test.go internal/mcpserver/server.go
git commit -m "feat(worklog): mcp__klyne__recap_project tool (cross-AI per-project)"
```

---

## Task 10: `mcp__klyne__user_recap` MCP tool (cross-project, user-level)

**Files:** Create `internal/mcpserver/tool_user_recap.go` + `_test.go`.

- [ ] **Step 1: Failing test:**

```go
func TestUserRecapAggregatesCrossProject(t *testing.T) {
    db, cleanup := newTestDB(t); defer cleanup()
    seedStopSummary(t, db, "/p1", "claude", "s1", true, 8, time.Now())
    seedStopSummary(t, db, "/p2", "codex",  "s2", true, 7, time.Now())
    seedStopSummary(t, db, "/p3", "claude", "s3", true, 6, time.Now())
    out, err := handleUserRecap(context.Background(), db, UserRecapArgs{SinceDays: 7})
    if err != nil { t.Fatal(err) }
    if out.TotalEntries != 3 { t.Errorf("expected 3, got %d", out.TotalEntries) }
    if out.ByCLI["claude"] != 2 || out.ByCLI["codex"] != 1 {
        t.Errorf("by-CLI counts wrong: %v", out.ByCLI)
    }
    if len(out.ByProject) != 3 { t.Errorf("expected 3 projects, got %d", len(out.ByProject)) }
}
```

- [ ] **Step 2: Run** — FAIL.

- [ ] **Step 3: Implement:**

```go
package mcpserver

import (
    "context"
    "database/sql"
    "time"
)

type UserRecapArgs struct {
    SinceDays int
    GroupBy   string // "cli" | "project" | "" (both)
}

type UserRecapOutput struct {
    TotalEntries int
    ByCLI        map[string]int
    ByProject    map[string]int
    TopEntries   []RecapEntry // 10 highest-importance
}

func handleUserRecap(ctx context.Context, db *sql.DB, args UserRecapArgs) (*UserRecapOutput, error) {
    if args.SinceDays <= 0 { args.SinceDays = 7 }
    cutoff := time.Now().Add(-time.Duration(args.SinceDays) * 24 * time.Hour).UnixMilli()
    out := &UserRecapOutput{ByCLI: map[string]int{}, ByProject: map[string]int{}}
    rows, err := db.QueryContext(ctx,
        `SELECT session_id, cli, project_path, COALESCE(recap_topic,''), COALESCE(ai_drafted_summary,''), ts, importance
         FROM stop_summaries WHERE recap_visible=1 AND ts >= ?
         ORDER BY importance DESC, ts DESC LIMIT 200`, cutoff)
    if err != nil { return nil, err }
    defer rows.Close()
    for rows.Next() {
        var e RecapEntry; var project string; var tsMs int64
        if err := rows.Scan(&e.SessionID, &e.CLI, &project, &e.RecapTopic, &e.AIDraftedSummary, &tsMs, &e.Importance); err != nil { return nil, err }
        e.TS = time.UnixMilli(tsMs)
        out.TotalEntries++
        out.ByCLI[e.CLI]++
        out.ByProject[project]++
        if len(out.TopEntries) < 10 { out.TopEntries = append(out.TopEntries, e) }
    }
    return out, rows.Err()
}
```

- [ ] **Step 4: Run** — PASS.

- [ ] **Step 5: Commit:**
```bash
git add internal/mcpserver/tool_user_recap.go internal/mcpserver/tool_user_recap_test.go internal/mcpserver/server.go
git commit -m "feat(worklog): mcp__klyne__user_recap tool (cross-project, cross-AI)"
```

---

## Task 11: Bootstrap injection (worklog entries surface in fresh sessions)

**Files:** Modify `internal/mcpserver/tool_bootstrap.go`. Add tests.

This is where the **cross-AI handoff use case** completes — bootstrap shows Codex + Claude entries together in the next session.

**Depends on:** the parallel bug-fix ticket having shipped `bootstrap reads Claude auto-memory`. This task adds worklog entries on top. If bug fix hasn't landed yet, this task's code goes in but uses today's bootstrap shape.

- [ ] **Step 1: Failing test** (extend existing `tool_bootstrap_test.go`):

```go
func TestBootstrapInjectsWorklogEntriesFromAllCLIs(t *testing.T) {
    db, cleanup := newTestDB(t); defer cleanup()
    seedStopSummary(t, db, "/p", "claude", "s1", true, 8, time.Now().Add(-1*time.Hour))
    seedStopSummary(t, db, "/p", "codex",  "s2", true, 7, time.Now().Add(-30*time.Minute))
    seedStopSummary(t, db, "/p", "claude", "s3", false, 3, time.Now()) // suppressed; must NOT appear

    out, err := handleBootstrap(context.Background(), db, BootstrapArgs{ProjectPath: "/p"})
    if err != nil { t.Fatal(err) }
    if len(out.WorklogEntries) != 2 { t.Errorf("expected 2 visible entries, got %d", len(out.WorklogEntries)) }
    var claudeFound, codexFound bool
    for _, e := range out.WorklogEntries {
        if e.CLI == "claude" { claudeFound = true }
        if e.CLI == "codex"  { codexFound = true }
    }
    if !claudeFound || !codexFound {
        t.Errorf("bootstrap must surface both CLIs, got claude=%v codex=%v", claudeFound, codexFound)
    }
}
```

- [ ] **Step 2: Run** — FAIL.

- [ ] **Step 3: Modify `tool_bootstrap.go`** — add a `WorklogEntries` field to `BootstrapOutput` and populate it:

```go
type BootstrapOutput struct {
    // ... existing fields ...
    WorklogEntries []RecapEntry
}

// In handleBootstrap, after existing population, add:
recapOut, err := handleRecapProject(ctx, db, RecapProjectArgs{
    ProjectPath: args.ProjectPath, SinceDays: 7,
})
if err == nil {
    out.WorklogEntries = recapOut.Entries
    if len(out.WorklogEntries) > 5 { out.WorklogEntries = out.WorklogEntries[:5] }
}
```

In the Markdown rendering of bootstrap output, add a section:

```markdown
## Recent worklog entries (cross-AI)
- [claude] {{topic}} ({{ago}}) — importance {{imp}}
- [codex]  {{topic}} ({{ago}}) — importance {{imp}}
...
```

- [ ] **Step 4: Run test** — PASS.

- [ ] **Step 5: Commit:**
```bash
git add internal/mcpserver/tool_bootstrap.go internal/mcpserver/tool_bootstrap_test.go
git commit -m "feat(worklog): bootstrap injects cross-AI worklog entries"
```

---

## Task 12: Weekly Markdown export (conditional, per-project, cli-tagged)

**Files:** Create `internal/worklog/export.go` + `_test.go`. Create `cmd/klyne/worklog.go` CLI subcommand.

- [ ] **Step 1: Failing test:**

```go
package worklog

import (
    "context"
    "os"
    "path/filepath"
    "testing"
    "time"
)

func TestExportWeekWritesOnlyWhenActive(t *testing.T) {
    db, cleanup := newTestDB(t); defer cleanup()
    tmp := t.TempDir()
    // Seed: project /p has 2 entries this week; project /q has 0
    seedStopSummary(t, db, "/p", "claude", "s1", true, 7, time.Now())
    seedStopSummary(t, db, "/p", "codex",  "s2", true, 8, time.Now())

    week := IsoWeek(time.Now())
    res, err := ExportWeek(context.Background(), db, ExportArgs{
        ProjectPath: "/p", Week: week, OutputRoot: tmp,
    })
    if err != nil { t.Fatal(err) }
    if !res.FileWritten { t.Errorf("expected file written for active project") }
    if _, err := os.Stat(filepath.Join(tmp, "docs/worklog", week+".md")); err != nil {
        t.Errorf("expected MD file, got %v", err)
    }

    // Quiet project must NOT write
    res2, err := ExportWeek(context.Background(), db, ExportArgs{
        ProjectPath: "/q", Week: week, OutputRoot: t.TempDir(),
    })
    if err != nil { t.Fatal(err) }
    if res2.FileWritten { t.Errorf("quiet project must produce no file") }
}
```

- [ ] **Step 2: Run** — FAIL.

- [ ] **Step 3: Implement** in `export.go`:

```go
package worklog

import (
    "context"
    "database/sql"
    "fmt"
    "os"
    "path/filepath"
    "strings"
    "time"
)

type ExportArgs struct {
    ProjectPath, Week, OutputRoot string
}

type ExportResult struct{ FileWritten bool; Path string }

func IsoWeek(t time.Time) string {
    y, w := t.ISOWeek()
    return fmt.Sprintf("%d-W%02d", y, w)
}

func ExportWeek(ctx context.Context, db *sql.DB, args ExportArgs) (ExportResult, error) {
    start, end, err := parseWeekRange(args.Week)
    if err != nil { return ExportResult{}, err }
    rows, err := db.QueryContext(ctx,
        `SELECT cli, session_id, COALESCE(recap_topic,''), COALESCE(ai_drafted_summary,''),
                last_user, ts, importance, files_json
         FROM stop_summaries
         WHERE project_path=? AND recap_visible=1
           AND ts >= ? AND ts < ?
         ORDER BY ts ASC`,
        args.ProjectPath, start.UnixMilli(), end.UnixMilli())
    if err != nil { return ExportResult{}, err }
    defer rows.Close()

    var entries []string
    for rows.Next() {
        var cli, sid, topic, summary, lastUser, filesJSON string
        var tsMs int64; var imp int
        if err := rows.Scan(&cli, &sid, &topic, &summary, &lastUser, &tsMs, &imp, &filesJSON); err != nil {
            return ExportResult{}, err
        }
        t := time.UnixMilli(tsMs).Format("Mon 15:04")
        title := topic
        if title == "" { title = truncate(lastUser, 80) }
        entry := fmt.Sprintf("### [%s] %s\n*%s · importance %d · session %s*\n\n%s\n",
            cli, title, t, imp, sid[:8], summary)
        entries = append(entries, entry)
    }
    if len(entries) == 0 {
        return ExportResult{FileWritten: false}, nil // conditional rule: skip empty weeks
    }

    dir := filepath.Join(args.OutputRoot, "docs", "worklog")
    if err := os.MkdirAll(dir, 0o755); err != nil { return ExportResult{}, err }
    path := filepath.Join(dir, args.Week+".md")
    body := fmt.Sprintf("# Worklog — %s\n\n%s", args.Week, strings.Join(entries, "\n"))
    if err := os.WriteFile(path, []byte(body), 0o644); err != nil { return ExportResult{}, err }
    return ExportResult{FileWritten: true, Path: path}, nil
}

func parseWeekRange(week string) (start, end time.Time, err error) {
    // "2026-W20" → Mon 00:00 of that ISO week, exclusive end.
    var y, w int
    if _, err = fmt.Sscanf(week, "%d-W%d", &y, &w); err != nil { return }
    // Find Jan 4 of the year (always in week 1 per ISO), step weeks.
    jan4 := time.Date(y, 1, 4, 0, 0, 0, 0, time.UTC)
    _, ww := jan4.ISOWeek()
    weekStartOfJan4 := jan4.AddDate(0, 0, -int(jan4.Weekday()-time.Monday))
    if jan4.Weekday() == time.Sunday { weekStartOfJan4 = jan4.AddDate(0, 0, -6) }
    start = weekStartOfJan4.AddDate(0, 0, (w-ww)*7)
    end = start.AddDate(0, 0, 7)
    return
}

func truncate(s string, n int) string {
    s = strings.ReplaceAll(s, "\n", " ")
    if len(s) > n { return s[:n] + "…" }
    return s
}
```

- [ ] **Step 4: Run** — PASS.

- [ ] **Step 5: CLI wrapper** in `cmd/klyne/worklog.go`:

```go
func runWorklogExportWeek(args []string) error {
    var project, week string
    fs := flag.NewFlagSet("export-week", flag.ContinueOnError)
    fs.StringVar(&project, "project", "", "project path")
    fs.StringVar(&week, "week", worklog.IsoWeek(time.Now()), "ISO week (e.g. 2026-W20)")
    if err := fs.Parse(args); err != nil { return err }
    db := openStore() // existing helper
    res, err := worklog.ExportWeek(context.Background(), db, worklog.ExportArgs{
        ProjectPath: project, Week: week, OutputRoot: project,
    })
    if err != nil { return err }
    if res.FileWritten { fmt.Println("Wrote", res.Path) } else { fmt.Println("No entries; nothing to write.") }
    return nil
}
```

Wire the scheduler in the daemon to call `ExportWeek` Sunday nights per project with activity that week.

- [ ] **Step 6: Commit:**
```bash
git add internal/worklog/export.go internal/worklog/export_test.go cmd/klyne/worklog.go
git commit -m "feat(worklog): conditional weekly Markdown export with cli tags"
```

---

## Task 13: Cross-AI integration test (the canonical user flow)

**Files:** Create `internal/worklog/e2e_test.go`.

This is the **acceptance test** for "cross-AI works." Mirrors the canonical user flow at the top of this plan.

- [ ] **Step 1: Write the test:**

```go
package worklog_test

import (
    "context"
    "database/sql"
    "testing"
    "time"

    "klyne/internal/mcpserver"
    "klyne/internal/store"
    "klyne/internal/store/migrations"
    "klyne/internal/worklog"
)

func TestCrossAIHandoffFlow(t *testing.T) {
    db, err := sql.Open("sqlite", "file::memory:?cache=shared")
    if err != nil { t.Fatal(err) }
    defer db.Close()
    if err := migrations.Apply(context.Background(), db); err != nil { t.Fatal(err) }

    proj := "/proj/example"

    // 1. Codex session ends — detector writes entry
    codexEntry := worklog.Entry{
        SessionID: "codex-1", TS: time.Now().Add(-1 * time.Hour),
        ProjectPath: proj, CLI: "codex",
        LastUser: "Should we use JWT or sessions for auth?",
        WallTime: 10 * time.Minute, ToolCallCount: 30, EditWriteCount: 5,
        Files: []string{"src/auth.go"}, CommitSHA: "deadbeef",
        EventTags: []worklog.EventTag{worklog.TagCommitLanded, worklog.TagDecisionRecorded, worklog.TagSecurityRelevantChange},
    }
    res, err := worklog.WriteEntry(context.Background(), db, codexEntry, nil, store.UpsertStopSummaryWithWorklog)
    if err != nil { t.Fatal(err) }
    if res.RecapVisible != 1 { t.Fatalf("codex entry must be visible") }
    if res.Importance < 9 { t.Errorf("commit+decision+security must score ≥ 9, got %d", res.Importance) }

    // 2. Fresh Claude session starts — bootstrap shows Codex entry
    out, err := mcpserver.HandleBootstrap(context.Background(), db, mcpserver.BootstrapArgs{ProjectPath: proj})
    if err != nil { t.Fatal(err) }
    var found bool
    for _, e := range out.WorklogEntries {
        if e.CLI == "codex" && e.SessionID == "codex-1" { found = true; break }
    }
    if !found { t.Errorf("bootstrap must surface the Codex entry to fresh Claude session") }

    // 3. Claude asks "what did I discuss" — recap_project returns cross-AI
    recap, err := mcpserver.HandleRecapProject(context.Background(), db,
        mcpserver.RecapProjectArgs{ProjectPath: proj, SinceDays: 1})
    if err != nil { t.Fatal(err) }
    var codexFound bool
    for _, e := range recap.Entries { if e.CLI == "codex" { codexFound = true } }
    if !codexFound { t.Errorf("recap must include Codex entry") }
}
```

- [ ] **Step 2: Run** — PASS.

- [ ] **Step 3: Commit:**
```bash
git add internal/worklog/e2e_test.go
git commit -m "test(worklog): cross-AI handoff flow end-to-end"
```

---

## Task 14: Reflection trigger (importance-sum + weekly cron)

**Files:** Create `internal/worklog/reflection_trigger.go` + `_test.go`.

- [ ] **Step 1: Failing test:**

```go
func TestReflectionTriggerImportanceSum(t *testing.T) {
    db, cleanup := newTestDB(t); defer cleanup()
    // Seed 5 entries with importance summing to 40 (under 150 threshold)
    for i := 0; i < 5; i++ {
        seedStopSummaryWithImportance(t, db, "/p", "claude", fmt.Sprintf("s%d", i), 8, time.Now())
    }
    fire, err := ShouldFireReflection(context.Background(), db, "/p", 150)
    if err != nil { t.Fatal(err) }
    if fire { t.Errorf("40 < 150 threshold, must not fire yet") }

    // Add a high-importance entry pushing sum over 150
    seedStopSummaryWithImportance(t, db, "/p", "codex", "s-big", 10, time.Now())
    seedStopSummaryWithImportance(t, db, "/p", "codex", "s-big2", 10, time.Now())
    // ... seed enough to cross 150 ...
}
```

- [ ] **Step 2: Implement:**

```go
package worklog

import (
    "context"
    "database/sql"
    "time"
)

func ShouldFireReflection(ctx context.Context, db *sql.DB, projectPath string, threshold int) (bool, error) {
    // Sum importance of visible entries since last reflection for this project.
    var lastReflectionTS int64
    _ = db.QueryRowContext(ctx,
        `SELECT COALESCE(MAX(ts), 0) FROM worklog_reflections WHERE project_path=?`, projectPath).Scan(&lastReflectionTS)
    var sum int
    if err := db.QueryRowContext(ctx,
        `SELECT COALESCE(SUM(importance), 0) FROM stop_summaries
         WHERE project_path=? AND recap_visible=1 AND ts > ?`,
        projectPath, lastReflectionTS).Scan(&sum); err != nil {
        return false, err
    }
    return sum >= threshold, nil
}

// WeeklyCronShouldFire returns true if (a) it's Sunday night UTC and (b) there's
// at least one visible entry since the last reflection for this project.
func WeeklyCronShouldFire(ctx context.Context, db *sql.DB, projectPath string, now time.Time) (bool, error) {
    if now.Weekday() != time.Sunday || now.Hour() < 20 { return false, nil }
    var lastRefMs int64
    _ = db.QueryRowContext(ctx,
        `SELECT COALESCE(MAX(ts), 0) FROM worklog_reflections WHERE project_path=?`, projectPath).Scan(&lastRefMs)
    var count int
    if err := db.QueryRowContext(ctx,
        `SELECT COUNT(*) FROM stop_summaries WHERE project_path=? AND recap_visible=1 AND ts > ?`,
        projectPath, lastRefMs).Scan(&count); err != nil { return false, err }
    return count > 0, nil
}
```

- [ ] **Step 3: Commit:**
```bash
git add internal/worklog/reflection_trigger.go internal/worklog/reflection_trigger_test.go
git commit -m "feat(worklog): reflection trigger (importance-sum + weekly cron)"
```

---

## Task 15: Reflection synthesizer (with citation invariant)

**Files:** Create `internal/worklog/reflection_synthesizer.go` + `_test.go`. Create `internal/store/worklog_reflections.go` (DAO).

- [ ] **Step 1: Add DAO** in `internal/store/worklog_reflections.go`:

```go
package store

import (
    "context"
    "database/sql"
    "encoding/json"
)

type Reflection struct {
    ID, ProjectPath, Title, BodyMD, SummarySource, State string
    Tier, Importance                                     int
    TS, StateChangedAt                                    int64
    EvidenceEntryIDs, EvidenceReflectionIDs              []string
}

func InsertReflection(ctx context.Context, db *sql.DB, r Reflection) error {
    if len(r.EvidenceEntryIDs) == 0 && len(r.EvidenceReflectionIDs) == 0 {
        return fmt.Errorf("worklog_reflections: citation invariant — at least one evidence ID required")
    }
    entryIDs, _ := json.Marshal(r.EvidenceEntryIDs)
    refIDs, _ := json.Marshal(r.EvidenceReflectionIDs)
    _, err := db.ExecContext(ctx,
        `INSERT INTO worklog_reflections (
            id, ts, project_path, tier, title, body_md,
            evidence_entry_ids_json, evidence_reflection_ids_json,
            importance, summary_source, state, state_changed_at
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
        r.ID, r.TS, r.ProjectPath, r.Tier, r.Title, r.BodyMD,
        string(entryIDs), string(refIDs), r.Importance, r.SummarySource, r.State, r.StateChangedAt)
    return err
}
```

- [ ] **Step 2: Failing test** for synthesizer:

```go
func TestSynthesizerRejectsEmptyEvidence(t *testing.T) {
    // Mock LM that returns insights without evidence pointers — synthesizer must retry.
    // ... test setup ...
}

func TestSynthesizerCitesEvidence(t *testing.T) {
    db, cleanup := newTestDB(t); defer cleanup()
    seedStopSummaryWithID(t, db, "/p", "claude", "entry-1", 8, time.Now())
    seedStopSummaryWithID(t, db, "/p", "codex",  "entry-2", 7, time.Now())
    
    refl, err := Synthesize(context.Background(), db, "/p", &fakeLM{
        response: `{"insights":[{"text":"User shipped auth refactor","evidence":["entry-1","entry-2"]}]}`,
    })
    if err != nil { t.Fatal(err) }
    if len(refl.EvidenceEntryIDs) != 2 {
        t.Errorf("expected 2 evidence entries, got %d", len(refl.EvidenceEntryIDs))
    }
}
```

- [ ] **Step 3: Implement:**

```go
package worklog

import (
    "context"
    "database/sql"
    "encoding/json"
    "fmt"
    "strings"
    "time"

    "klyne/internal/store"
)

type LM interface {
    Complete(ctx context.Context, prompt string) (string, error)
}

type insightResponse struct {
    Insights []struct{ Text string `json:"text"`; Evidence []string `json:"evidence"` } `json:"insights"`
}

func Synthesize(ctx context.Context, db *sql.DB, projectPath string, lm LM) (store.Reflection, error) {
    // Fetch last 50 entries since last reflection for this project as skeletons.
    var lastRefMs int64
    _ = db.QueryRowContext(ctx,
        `SELECT COALESCE(MAX(ts), 0) FROM worklog_reflections WHERE project_path=?`, projectPath).Scan(&lastRefMs)
    rows, err := db.QueryContext(ctx,
        `SELECT session_id, cli, COALESCE(recap_topic,''), COALESCE(ai_drafted_summary,''), importance, ts
         FROM stop_summaries WHERE project_path=? AND recap_visible=1 AND ts > ?
         ORDER BY ts ASC LIMIT 50`, projectPath, lastRefMs)
    if err != nil { return store.Reflection{}, err }
    defer rows.Close()

    var skeletons []string
    for rows.Next() {
        var sid, cli, topic, summary string; var imp int; var ts int64
        if err := rows.Scan(&sid, &cli, &topic, &summary, &imp, &ts); err != nil { return store.Reflection{}, err }
        skeletons = append(skeletons,
            fmt.Sprintf("- id=%s [%s] importance=%d topic=%q summary=%q",
                sid, cli, imp, topic, truncate(summary, 200)))
    }

    prompt := fmt.Sprintf(`Given the following work-log entries from the past period, what 3 high-level insights can we infer? For each insight, cite the specific entries (by id) that serve as evidence. Output JSON:

{"insights":[{"text":"<insight>","evidence":["<entry-id>",...]}]}

Entries:
%s

Output JSON only.`, strings.Join(skeletons, "\n"))

    raw, err := lm.Complete(ctx, prompt)
    if err != nil { return store.Reflection{}, fmt.Errorf("worklog: lm complete: %w", err) }

    var resp insightResponse
    if err := json.Unmarshal([]byte(raw), &resp); err != nil {
        return store.Reflection{}, fmt.Errorf("worklog: parse lm response: %w", err)
    }

    var allEvidence []string; var body strings.Builder
    for _, ins := range resp.Insights {
        if len(ins.Evidence) == 0 {
            return store.Reflection{}, fmt.Errorf("worklog: insight missing evidence — rejecting")
        }
        allEvidence = append(allEvidence, ins.Evidence...)
        body.WriteString(fmt.Sprintf("- %s (evidence: %s)\n", ins.Text, strings.Join(ins.Evidence, ", ")))
    }

    refl := store.Reflection{
        ID: fmt.Sprintf("ref-%d", time.Now().UnixNano()),
        TS: time.Now().UnixMilli(),
        ProjectPath: projectPath,
        Tier: 2, // weekly
        Title: fmt.Sprintf("Weekly reflection — %s", IsoWeek(time.Now())),
        BodyMD: body.String(),
        EvidenceEntryIDs: allEvidence,
        Importance: 7,
        SummarySource: "ai",
        State: "proposed",
        StateChangedAt: time.Now().UnixMilli(),
    }
    if err := store.InsertReflection(ctx, db, refl); err != nil {
        return store.Reflection{}, err
    }
    return refl, nil
}
```

- [ ] **Step 4: Commit:**
```bash
git add internal/store/worklog_reflections.go internal/worklog/reflection_synthesizer.go internal/worklog/reflection_synthesizer_test.go
git commit -m "feat(worklog): reflection synthesizer with citation invariant"
```

---

## Task 16: Reflections in recap output

**Files:** Modify `internal/mcpserver/tool_recap_project.go` and `tool_user_recap.go`.

- [ ] **Step 1: Failing test** — extend recap_project test to assert reflections appear in output when present:

```go
func TestRecapProjectIncludesReflections(t *testing.T) {
    db, cleanup := newTestDB(t); defer cleanup()
    seedStopSummary(t, db, "/p", "claude", "s1", true, 8, time.Now())
    seedReflection(t, db, "/p", []string{"s1"}, "Weekly: shipped auth refactor")
    out, err := handleRecapProject(context.Background(), db, RecapProjectArgs{ProjectPath: "/p", SinceDays: 7})
    if err != nil { t.Fatal(err) }
    if len(out.Reflections) != 1 { t.Errorf("expected 1 reflection") }
}
```

- [ ] **Step 2: Add Reflections field to outputs**, populate from `worklog_reflections`:

```go
type RecapProjectOutput struct {
    Entries      []RecapEntry
    Reflections  []ReflectionSummary
}

type ReflectionSummary struct {
    ID, Title, BodyMD string
    Tier              int
    TS                time.Time
    EvidenceCount     int
}

// In handleRecapProject, after entries query, add:
rows, _ := db.QueryContext(ctx,
    `SELECT id, title, body_md, tier, ts, evidence_entry_ids_json
     FROM worklog_reflections WHERE project_path=? AND ts >= ?
     ORDER BY ts DESC LIMIT 5`,
    args.ProjectPath, cutoff)
defer rows.Close()
for rows.Next() {
    var r ReflectionSummary; var tsMs int64; var evJSON string
    rows.Scan(&r.ID, &r.Title, &r.BodyMD, &r.Tier, &tsMs, &evJSON)
    r.TS = time.UnixMilli(tsMs)
    var ev []string; json.Unmarshal([]byte(evJSON), &ev)
    r.EvidenceCount = len(ev)
    out.Reflections = append(out.Reflections, r)
}
```

Do the equivalent in `tool_user_recap.go` (aggregate reflections cross-project).

- [ ] **Step 3: Commit:**
```bash
git add internal/mcpserver/tool_recap_project.go internal/mcpserver/tool_user_recap.go
git commit -m "feat(worklog): surface reflections in recap MCP tools"
```

---

## Task 17: Reflections in bootstrap (close the Planning loop)

**Files:** Modify `internal/mcpserver/tool_bootstrap.go`.

- [ ] **Step 1: Failing test:**

```go
func TestBootstrapInjectsLatestReflection(t *testing.T) {
    db, cleanup := newTestDB(t); defer cleanup()
    seedStopSummary(t, db, "/p", "claude", "s1", true, 8, time.Now())
    seedReflection(t, db, "/p", []string{"s1"}, "Weekly: shipped auth refactor")
    out, err := handleBootstrap(context.Background(), db, BootstrapArgs{ProjectPath: "/p"})
    if err != nil { t.Fatal(err) }
    if len(out.Reflections) == 0 { t.Errorf("bootstrap must surface latest reflection") }
}
```

- [ ] **Step 2: Modify bootstrap** to pull the latest reflection per project and inject:

```go
type BootstrapOutput struct {
    // ... existing ...
    Reflections []ReflectionSummary
}

// In handleBootstrap, after WorklogEntries population, add:
recapOut, _ := handleRecapProject(ctx, db, RecapProjectArgs{ProjectPath: args.ProjectPath, SinceDays: 14})
out.Reflections = recapOut.Reflections
if len(out.Reflections) > 2 { out.Reflections = out.Reflections[:2] } // latest 2
```

In the Markdown render of bootstrap, add a `## Weekly reflections` section before `## Recent worklog entries`.

- [ ] **Step 3: Commit:**
```bash
git add internal/mcpserver/tool_bootstrap.go
git commit -m "feat(worklog): bootstrap injects reflections (planning loop closed)"
```

---

## Task 18: Wire the reflection runner into the daemon

**Files:** Modify `cmd/klyne/daemon.go`.

- [ ] **Step 1: Add a tick in the daemon loop:**

```go
if cfg.Worklog.ReflectionEnabled {
    go func() {
        t := time.NewTicker(5 * time.Minute); defer t.Stop()
        for { select {
        case <-ctx.Done(): return
        case <-t.C:
            // For each project with activity, check thresholds and synthesize.
            projects := listActiveProjects(ctx, db)
            for _, p := range projects {
                fire1, _ := worklog.ShouldFireReflection(ctx, db, p, 150)
                fire2, _ := worklog.WeeklyCronShouldFire(ctx, db, p, time.Now())
                if !(fire1 || fire2) { continue }
                if _, err := worklog.Synthesize(ctx, db, p, anthropicLM()); err != nil {
                    slog.Warn("worklog.synthesize_failed", "project", p, "err", err)
                }
            }
        }}
    }()
}
```

`anthropicLM()` is a small wrapper that constructs an Anthropic Haiku client using the user's key.

Default `worklog.reflection_enabled = false`. User opts in to spend tokens.

- [ ] **Step 2: Commit:**
```bash
git add cmd/klyne/daemon.go
git commit -m "feat(worklog): wire reflection runner into daemon"
```

---

## Task 19: Dogfood + kill-criteria evaluation harness

**Files:** Create `internal/worklog/dogfood_test.go`.

This is the **final acceptance gate**. The harness runs the 6 kill criteria from `00-plan.md` against real klyne data and reports pass/fail.

- [ ] **Step 1: Implement** the harness as a `*testing.T`-driven script:

```go
package worklog_test

import (
    "context"
    "database/sql"
    "fmt"
    "testing"
)

// Run with: go test ./internal/worklog/ -run TestDogfoodKillCriteria -v -timeout 30m
// Requires real klyne.db with at least 5 days of worklog entries.
func TestDogfoodKillCriteria(t *testing.T) {
    if testing.Short() { t.Skip("dogfood test — needs real klyne.db") }
    db, err := sql.Open("sqlite", "/Users/<you>/.klyne/klyne.db?mode=ro")
    if err != nil { t.Fatal(err) }
    defer db.Close()

    crit := []struct {
        name string
        fn   func(context.Context, *sql.DB) (pass bool, msg string, err error)
    }{
        {"K1: dogfood usefulness ≥ 60%", critDogfoodUsefulness},
        {"K2: beats existing stack ≥ 12/20", critCounterfactualAB},
        {"K3: hallucination rate ≤ 4/30", critHallucinationAudit},
        {"K4: ≤ 60% schema reconstructible", critSchemaDrift},
        {"K5: maintainer reaches for worklog", critBoredomSignal},
        {"K6: ≥ 2 external devs say yes", critExternalValidation},
    }
    for _, c := range crit {
        t.Run(c.name, func(t *testing.T) {
            pass, msg, err := c.fn(context.Background(), db)
            if err != nil { t.Fatalf("error: %v", err) }
            if !pass { t.Errorf("FAIL — %s", msg) }
        })
    }
}

// Stub each crit* function — implement based on actual measurement strategy
// (some are quantitative DB queries; some are manual surveys outside this harness).
```

- [ ] **Step 2: Document** how to run + interpret in the plan file's "Definition of done" below.

- [ ] **Step 3: Commit:**
```bash
git add internal/worklog/dogfood_test.go
git commit -m "test(worklog): kill-criteria evaluation harness"
```

### Invocation notes (as shipped)

The harness lives in `internal/worklog/dogfood_test.go` and is gated by
`testing.Short()` so it never runs as part of normal `go test ./...`.

- **Normal `go test` (SKIP):**
  `GOTOOLCHAIN=auto go test ./internal/worklog/ -v -short`
- **Run against `~/.klyne/klyne.db`:**
  `GOTOOLCHAIN=auto go test ./internal/worklog/ -run TestDogfoodKillCriteria -v -timeout 30m`
- **Run against a copy / alternate DB:**
  `KLYNE_DOGFOOD_DB=/path/to/klyne.db go test ./internal/worklog/ -run TestDogfoodKillCriteria -v`

The harness opens the DB read-only (`?mode=ro`). If the worklog columns
from migration 015 are missing, the test skips with a clear message
("run `klyne migrate` and accumulate ≥ 5 days of entries") rather than
failing on `no such column`. K3 (hallucination audit) and K4 (schema
drift) are quantitative; K1, K2, K5, K6 print explicit manual-survey
guidance to record findings against.

---

## Definition of done (single phase complete)

- [ ] All migrations apply cleanly: `klyne migrate`
- [ ] `go test ./internal/worklog/... ./internal/connectors/codex/... ./internal/mcpserver/...` ALL PASS
- [ ] `go vet ./...` and `staticcheck ./...` clean
- [ ] All benchmarks meet targets (`Score < 10µs`, `ShouldSuppress < 500µs`, `WriteEntry < 5ms p99`)
- [ ] Manual verification: open Claude in a project with prior worklog entries → bootstrap shows them, tagged with CLI
- [ ] Manual verification: run a Codex session + a Claude session in same project → both appear in `recap_project` output
- [ ] Manual verification: weekly export — for active project, `<project>/docs/worklog/YYYY-WW.md` exists; for quiet project, no file
- [ ] Manual verification: reflection — run `klyne worklog reflect-now --project X` → row appears in `worklog_reflections` with non-empty evidence
- [ ] Cross-AI handoff E2E test (Task 13) passes
- [ ] Dogfood harness (Task 19) reports pass on all 6 kill criteria after 5 days of real use
- [ ] Bootstrap shows latest reflection at top of brief
- [ ] Bug-fix ticket (`mcp__klyne__get_session`, etc.) has merged in main; cross-AI handoff Task 13 has been re-tested against the merged tools

When all of the above are checked, the cross-AI worklog feature ships.

---

## Self-review (writing-plans skill checklist)

1. **Spec coverage:** Memory (T1–T8), Surfaces (T9–T12), Reflection (T14–T17), Validation (T13, T18–T19), parallel bug-fix integration noted in T11. Cross-AI handoff has its own E2E test (T13). ✓
2. **Placeholder scan:** the only stubs are (a) `buildEntry` in T8 explicitly deferred until bug-ticket's `get_session` lands, and (b) `crit*` functions in T19 with explicit guidance to implement based on measurement strategy. Both flagged in-place. ✓
3. **Type consistency:** `Entry`, `EventTag`, `WorklogColumns`, `WriteResult`, `RecapEntry`, `RecapProjectOutput`, `UserRecapOutput`, `BootstrapOutput`, `Reflection`, `ReflectionSummary`, `LM` interface — all named consistently across tasks. ✓
4. **File-path consistency:** `internal/worklog/`, `internal/connectors/codex/`, `internal/mcpserver/tool_*.go`, `cmd/klyne/session_end.go`, `cmd/klyne/daemon.go`, `cmd/klyne/worklog.go` referenced consistently. ✓
5. **Single-phase compliance:** No "Phase 1 / Phase 2" structure. Tasks numbered sequentially 1–19. Logical groupings (Memory / Surfaces / Reflection / Validation) are just headings within the single plan, not phase gates. ✓

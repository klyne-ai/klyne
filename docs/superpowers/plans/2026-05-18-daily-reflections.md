# Daily Reflections Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Convert klyne's reflection tier from weekly-only (tier=2) to per-day (tier=1, "Daily reflection — YYYY-MM-DD"), and add a per-project drill-in route that lists those dailies grouped by ISO-week. Single `/klyne:reflect` invocation auto-buckets all pending entries by date and writes one tier-1 reflection per date.

**Architecture:** End-to-end change touches one storage helper (`worklog.RecordReflection`), one MCP tool input (`record_reflection`), the slash command prompt (`/klyne:reflect`), one new HTTP route (`/worklog/items/project`), one new SvelteKit page (`/worklog/project?path=…`), and scenario 04 of the harness. The existing `/worklog/items` rollup keeps its shape — `latest_reflection` is now a daily, not a weekly.

**Tech Stack:** Go (backend, MCP, store), SQLite, MCP SDK, SvelteKit 5 (runes), Vitest, Node test harness.

---

## File map

**Backend (modify):**
- `internal/worklog/reflection_recorder.go` — add `day time.Time` param, write tier=1
- `internal/worklog/reflection_recorder_test.go` — update existing assertions to tier=1 + new title format, add day-bucket test
- `internal/mcpserver/tool_record_reflection.go` — add `Day` to `RecordReflectionInput`, parse YYYY-MM-DD
- `internal/mcpserver/tool_record_reflection_test.go` — update existing tests, add day-rejection test
- `internal/mcpserver/server.go` — update `record_reflection` tool description to mention `day`
- `internal/mcpserver/slashcommands/reflect.md` — rewrite synthesis steps to bucket by date
- `internal/api/contracts.go` — add `RouteWorklogProject` constant + `WorklogProjectResponse` DTO
- `internal/api/contracts_test.go` — add new route to `expected` list
- `internal/api/handlers/worklog.go` — add `Project` handler method
- `internal/api/handlers/worklog_test.go` — add tests for the new endpoint
- `internal/api/handlers/mounter.go` — register the new route

**Frontend (modify):**
- `ui/src/lib/types.ts` — add `WorklogProjectResponse` interface
- `ui/src/lib/api.ts` — add `fetchWorklogProject(path)`
- `ui/src/routes/worklog/+page.svelte` — make each card heading link to drill-in

**Frontend (create):**
- `ui/src/routes/worklog/project/+page.svelte` — drill-in page, reflections grouped by ISO-week

**Showcase harness (modify):**
- `test/scenarios/scenarios/04-reflection-synthesis.js` — assert tier=1 + title format

---

## Task 1: Make `RecordReflection` write tier-1 dailies with an explicit day

**Files:**
- Modify: `internal/worklog/reflection_recorder.go:36-77`
- Test: `internal/worklog/reflection_recorder_test.go:22-126`

The existing `RecordReflection(ctx, db, projectPath, insights)` hardcodes `Tier: 2` and a `"Weekly reflection — <ISO-week>"` title at line 64-65. We add a `day time.Time` parameter (the calendar day the reflection covers). Tier becomes 1, title becomes `"Daily reflection — YYYY-MM-DD"` using `day.UTC().Format("2006-01-02")`. UTC matches the rest of the worklog code (e.g. `IsoWeek` in `export.go:31`). A zero `day` falls back to `time.Now()` so callers in tests can omit it without breakage.

- [ ] **Step 1: Update the existing `TestRecordReflection_RoundTrip` test to assert tier=1 + daily title format**

In `internal/worklog/reflection_recorder_test.go` replace the body of `TestRecordReflection_RoundTrip` (currently lines 22-62):

```go
func TestRecordReflection_RoundTrip(t *testing.T) {
	db := newRecorderTestDB(t)
	insights := []Insight{
		{Text: "User shipped auth refactor", Evidence: []string{"entry-1", "entry-2"}},
		{Text: "Test coverage improved", Evidence: []string{"entry-3"}},
	}
	day := time.Date(2026, 5, 17, 0, 0, 0, 0, time.UTC)
	refl, err := RecordReflection(context.Background(), db, "/p", day, insights)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if refl.ID == "" {
		t.Errorf("expected non-empty id")
	}
	if refl.ProjectPath != "/p" {
		t.Errorf("project_path lost: %q", refl.ProjectPath)
	}
	if refl.Tier != 1 {
		t.Errorf("expected tier=1 (daily), got %d", refl.Tier)
	}
	if refl.Title != "Daily reflection — 2026-05-17" {
		t.Errorf("expected daily title, got %q", refl.Title)
	}
	if refl.SummarySource != "ai" {
		t.Errorf("expected summary_source=ai, got %q", refl.SummarySource)
	}
	if len(refl.EvidenceEntryIDs) != 3 {
		t.Errorf("expected 3 evidence ids, got %d (%v)", len(refl.EvidenceEntryIDs), refl.EvidenceEntryIDs)
	}
	if !strings.Contains(refl.BodyMD, "shipped auth refactor") {
		t.Errorf("body should embed insight text, got %q", refl.BodyMD)
	}
	if !strings.Contains(refl.BodyMD, "entry-1, entry-2") {
		t.Errorf("body should embed comma-joined evidence, got %q", refl.BodyMD)
	}

	rows, err := store.ListReflectionsForProject(context.Background(), db, "/p", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 persisted reflection, got %d", len(rows))
	}
}
```

Add to the `import` block at the top:

```go
"time"
```

(adjacent to existing imports).

Also update the other tests in the same file to pass a zero `time.Time{}` as the new parameter — they don't care about the day. Patch each call:

```go
// line 66 → was: _, err := RecordReflection(context.Background(), db, "/p", nil)
_, err := RecordReflection(context.Background(), db, "/p", time.Time{}, nil)

// line 80 → was: _, err := RecordReflection(context.Background(), db, "/p", insights)
_, err := RecordReflection(context.Background(), db, "/p", time.Time{}, insights)

// line 94 → was: _, err := RecordReflection(context.Background(), db, "/p", insights)
_, err := RecordReflection(context.Background(), db, "/p", time.Time{}, insights)

// line 108 → was: refl, err := RecordReflection(context.Background(), db, wt, insights)
refl, err := RecordReflection(context.Background(), db, wt, time.Time{}, insights)

// line 122 → was: _, err := RecordReflection(context.Background(), db, "", insights)
_, err := RecordReflection(context.Background(), db, "", time.Time{}, insights)
```

- [ ] **Step 2: Run the test to verify the round-trip case fails**

Run: `cd ~/Desktop/Project/klyne && go test ./internal/worklog/ -run TestRecordReflection_RoundTrip -v`

Expected: FAIL with a compile error in the test file (signature mismatch) OR — once the test file compiles after updating *all* callers in step 1 — FAIL with `expected tier=1 (daily), got 2` and `expected daily title, got "Weekly reflection — 2026-W20"`.

- [ ] **Step 3: Update `RecordReflection` to accept `day` and write tier=1**

Replace `internal/worklog/reflection_recorder.go:36-77`:

```go
func RecordReflection(ctx context.Context, db *store.DB, projectPath string, day time.Time, insights []Insight) (store.Reflection, error) {
	if strings.TrimSpace(projectPath) == "" {
		return store.Reflection{}, errors.New("worklog: project_path required")
	}
	// Roll worktrees up to the canonical main-repo path so reflections
	// triggered from a worktree appear under that repo's project, not
	// as a sibling card on the worklog page.
	projectPath = projectpath.Canonical(projectPath)
	if len(insights) == 0 {
		return store.Reflection{}, errors.New("worklog: at least one insight required")
	}
	var allEvidence []string
	var body strings.Builder
	for i, ins := range insights {
		if strings.TrimSpace(ins.Text) == "" {
			return store.Reflection{}, fmt.Errorf("worklog: insight %d has empty text", i)
		}
		if len(ins.Evidence) == 0 {
			return store.Reflection{}, fmt.Errorf("worklog: insight %d missing evidence (citation invariant)", i)
		}
		allEvidence = append(allEvidence, ins.Evidence...)
		body.WriteString(fmt.Sprintf("- %s (evidence: %s)\n", ins.Text, strings.Join(ins.Evidence, ", ")))
	}
	now := time.Now()
	if day.IsZero() {
		day = now
	}
	dayLabel := day.UTC().Format("2006-01-02")
	refl := store.Reflection{
		ID:               fmt.Sprintf("ref-%s-%d", dayLabel, now.UnixNano()),
		TS:               now.UnixMilli(),
		ProjectPath:      projectPath,
		Tier:             1, // daily
		Title:            fmt.Sprintf("Daily reflection — %s", dayLabel),
		BodyMD:           body.String(),
		EvidenceEntryIDs: allEvidence,
		Importance:       7,
		SummarySource:    "ai",
		State:            "proposed",
		StateChangedAt:   now.UnixMilli(),
	}
	if err := store.InsertReflection(ctx, db, refl); err != nil {
		return store.Reflection{}, fmt.Errorf("worklog: persist reflection: %w", err)
	}
	return refl, nil
}
```

- [ ] **Step 4: Update the file's package doc to reflect the daily tier**

Replace `internal/worklog/reflection_recorder.go:1-7`:

```go
// Package-level helper for the record_reflection MCP tool.
//
// RecordReflection persists a synthesized DAILY reflection (tier=1)
// produced by the AI host (Claude / Codex via its slash command).
// The host buckets pending entries by date and calls this once per
// date so a single /klyne:reflect run can catch up across N days.
//
// The citation invariant is enforced here AND in store.InsertReflection
// — we keep the AI-facing check tight so the host gets an immediate,
// descriptive error before the row is even attempted.
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `cd ~/Desktop/Project/klyne && go test ./internal/worklog/ -run TestRecordReflection -v`

Expected: all `TestRecordReflection_*` subtests PASS.

- [ ] **Step 6: Run the full worklog package tests to catch any other regression**

Run: `cd ~/Desktop/Project/klyne && go test ./internal/worklog/ -v`

Expected: every test PASS. The recorder change is the only signature change inside this package.

- [ ] **Step 7: Commit**

```bash
cd ~/Desktop/Project/klyne
git add internal/worklog/reflection_recorder.go internal/worklog/reflection_recorder_test.go
git commit -m "$(cat <<'EOF'
feat(worklog): RecordReflection writes tier-1 daily reflections

Adds a `day` parameter and changes the persisted tier/title to
"Daily reflection — YYYY-MM-DD". A single /klyne:reflect run will
shortly call this once per pending date to catch up across days.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: Add a day-bucket test that proves multi-day catch-up works

**Files:**
- Test: `internal/worklog/reflection_recorder_test.go`

We need a test that proves three calls with three different days produce three distinct rows with three distinct daily titles. Today the existing tests only exercise the single-day round-trip.

- [ ] **Step 1: Add `TestRecordReflection_WritesOnePerDay`**

Append to `internal/worklog/reflection_recorder_test.go`:

```go
func TestRecordReflection_WritesOnePerDay(t *testing.T) {
	db := newRecorderTestDB(t)
	days := []time.Time{
		time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC),
	}
	for i, d := range days {
		insights := []Insight{
			{Text: fmt.Sprintf("did something on day %d", i), Evidence: []string{fmt.Sprintf("e%d", i)}},
		}
		if _, err := RecordReflection(context.Background(), db, "/p", d, insights); err != nil {
			t.Fatalf("day %d: %v", i, err)
		}
	}
	rows, err := store.ListReflectionsForProject(context.Background(), db, "/p", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows (one per day), got %d", len(rows))
	}
	wantTitles := map[string]bool{
		"Daily reflection — 2026-05-15": false,
		"Daily reflection — 2026-05-16": false,
		"Daily reflection — 2026-05-17": false,
	}
	for _, r := range rows {
		if _, ok := wantTitles[r.Title]; !ok {
			t.Errorf("unexpected title %q", r.Title)
		}
		wantTitles[r.Title] = true
		if r.Tier != 1 {
			t.Errorf("row %s: tier=%d, want 1", r.Title, r.Tier)
		}
	}
	for title, seen := range wantTitles {
		if !seen {
			t.Errorf("missing reflection for %s", title)
		}
	}
}
```

Add `"fmt"` to the imports block if not already present (it should be — already used by the file).

- [ ] **Step 2: Run the new test**

Run: `cd ~/Desktop/Project/klyne && go test ./internal/worklog/ -run TestRecordReflection_WritesOnePerDay -v`

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
cd ~/Desktop/Project/klyne
git add internal/worklog/reflection_recorder_test.go
git commit -m "$(cat <<'EOF'
test(worklog): assert RecordReflection writes one row per distinct day

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: Plumb a `day` field through the `record_reflection` MCP tool

**Files:**
- Modify: `internal/mcpserver/tool_record_reflection.go`
- Test: `internal/mcpserver/tool_record_reflection_test.go`
- Modify: `internal/mcpserver/server.go:160-167` (tool description)

The MCP tool currently passes `(ctx, db, projectPath, insights)` straight through. We need to add a `Day string` field (YYYY-MM-DD) and parse it into `time.Time` before calling `worklog.RecordReflection`. Empty string keeps "use today" behavior so the AI host can omit `day` for single-day reflections.

- [ ] **Step 1: Add the failing test for the day field**

Append to `internal/mcpserver/tool_record_reflection_test.go`:

```go
func TestHandleRecordReflection_PersistsWithDay(t *testing.T) {
	withFakeHome(t)
	db := withBootstrapDB(t)

	in := RecordReflectionInput{
		ProjectPath: "/p",
		Day:         "2026-05-15",
		Insights: []worklog.Insight{
			{Text: "did the auth thing", Evidence: []string{"s1"}},
		},
	}
	out, err := handleRecordReflection(context.Background(), db, in)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if out.ReflectionID == "" {
		t.Errorf("expected non-empty reflection id")
	}
	rows, err := store.ListReflectionsForProject(context.Background(), db, "/p", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Title != "Daily reflection — 2026-05-15" {
		t.Errorf("title=%q, want Daily reflection — 2026-05-15", rows[0].Title)
	}
}

func TestHandleRecordReflection_RejectsBadDay(t *testing.T) {
	withFakeHome(t)
	db := withBootstrapDB(t)

	in := RecordReflectionInput{
		ProjectPath: "/p",
		Day:         "May 15",
		Insights: []worklog.Insight{
			{Text: "x", Evidence: []string{"e"}},
		},
	}
	_, err := handleRecordReflection(context.Background(), db, in)
	if err == nil {
		t.Fatalf("expected error on malformed day")
	}
	if !strings.Contains(err.Error(), "day") {
		t.Errorf("expected error mentioning 'day', got %v", err)
	}
}
```

- [ ] **Step 2: Run new tests to verify they fail**

Run: `cd ~/Desktop/Project/klyne && go test ./internal/mcpserver/ -run TestHandleRecordReflection -v`

Expected: compile error (unknown `Day` field on `RecordReflectionInput`) — proves the next step is needed.

- [ ] **Step 3: Add `Day` to the input + handler parse**

Replace `internal/mcpserver/tool_record_reflection.go` in full:

```go
package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/store"
	"github.com/klyne-ai/klyne/internal/worklog"
)

// RecordReflectionInput is the MCP-facing input schema. Insights MUST
// each carry at least one evidence id (session_id from the proposer
// output); the underlying worklog helper rejects empty-evidence
// insights up-front. Day is optional YYYY-MM-DD (UTC) — empty falls
// back to today, which matches the legacy single-day call shape.
type RecordReflectionInput struct {
	ProjectPath string            `json:"project_path" jsonschema:"absolute project path"`
	Day         string            `json:"day,omitempty" jsonschema:"calendar day this reflection covers, YYYY-MM-DD (UTC); empty = today"`
	Insights    []worklog.Insight `json:"insights" jsonschema:"synthesized insights — each must cite at least one entry session_id"`
}

// RecordReflectionOutput surfaces the persisted id and a citation count
// so the AI host can confirm the round-trip back to the user.
type RecordReflectionOutput struct {
	ReflectionID  string `json:"reflection_id"`
	EvidenceCount int    `json:"evidence_count"`
}

func handleRecordReflection(ctx context.Context, db *store.DB, in RecordReflectionInput) (*RecordReflectionOutput, error) {
	var day time.Time
	if in.Day != "" {
		parsed, err := time.ParseInLocation("2006-01-02", in.Day, time.UTC)
		if err != nil {
			return nil, fmt.Errorf("record_reflection: bad day %q (want YYYY-MM-DD): %w", in.Day, err)
		}
		day = parsed
	}
	refl, err := worklog.RecordReflection(ctx, db, in.ProjectPath, day, in.Insights)
	if err != nil {
		return nil, err
	}
	return &RecordReflectionOutput{ReflectionID: refl.ID, EvidenceCount: len(refl.EvidenceEntryIDs)}, nil
}

// HandleRecordReflection is the MCP entry-point. The citation invariant
// is enforced inside worklog.RecordReflection; this thin shell just
// adapts errors to the tool-log surface.
func HandleRecordReflection(ctx context.Context, _ *mcp.CallToolRequest, in RecordReflectionInput) (*mcp.CallToolResult, RecordReflectionOutput, error) {
	db, err := store.Open(config.DBPath())
	if err != nil {
		return nil, RecordReflectionOutput{}, fmt.Errorf("record_reflection: open db: %w", err)
	}
	defer db.Close()
	out, err := handleRecordReflection(ctx, db, in)
	if err != nil {
		return nil, RecordReflectionOutput{}, err
	}
	summary := fmt.Sprintf("record_reflection: stored %s with %d evidence entries", out.ReflectionID, out.EvidenceCount)
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: summary}}}, *out, nil
}
```

- [ ] **Step 4: Update the tool's MCP description so the AI host knows the new field exists**

Edit `internal/mcpserver/server.go:160-167`. Replace the existing `record_reflection` block:

```go
	mcp.AddTool(srv, &mcp.Tool{
		Name: "record_reflection",
		Description: `Persist a synthesized DAILY reflection for a project (tier=1, "Daily reflection — YYYY-MM-DD"). Enforces the citation invariant — every insight must cite at least one entry session_id from the propose_reflection output.

Call this AFTER bucketing propose_reflection's entries by date and synthesizing per-day insights. Call it ONCE per distinct date — a single /klyne:reflect run may invoke it N times to catch up across N days.

Inputs:
  - project_path (required) absolute project path
  - day (required for daily synthesis) YYYY-MM-DD in UTC, the calendar day this reflection covers
  - insights ([{text, evidence: [session_id, ...]}, ...])`,
	}, HandleRecordReflection)
```

- [ ] **Step 5: Run all `record_reflection` tests**

Run: `cd ~/Desktop/Project/klyne && go test ./internal/mcpserver/ -run TestHandleRecordReflection -v`

Expected: all `TestHandleRecordReflection_*` subtests PASS (existing 3 + new 2).

- [ ] **Step 6: Run the full mcpserver package tests to catch any regression**

Run: `cd ~/Desktop/Project/klyne && go test ./internal/mcpserver/ -count=1`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
cd ~/Desktop/Project/klyne
git add internal/mcpserver/tool_record_reflection.go internal/mcpserver/tool_record_reflection_test.go internal/mcpserver/server.go
git commit -m "$(cat <<'EOF'
feat(mcp): record_reflection accepts an explicit day (YYYY-MM-DD)

The AI host calls record_reflection once per distinct date when
synthesizing across multiple days. Empty day falls back to today.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: Rewrite the `/klyne:reflect` slash command to bucket by date

**Files:**
- Modify: `internal/mcpserver/slashcommands/reflect.md`

Today the prompt instructs the host to synthesize one global insight set per call. We change it to: bucket pending entries by UTC calendar day (from their `ts` field) and call `record_reflection` once per distinct date. This is the user-facing entry point for the whole feature.

- [ ] **Step 1: Rewrite the slash command body**

Replace `internal/mcpserver/slashcommands/reflect.md` in full:

````markdown
---
description: Synthesize one daily reflection per pending date from this project's worklog entries
---

Use your own context window to synthesize **daily reflections** over this project's pending worklog entries. A single run may produce 1–N reflections, one per distinct calendar date.

1. **Call `mcp__klyne__propose_reflection`** with `project_path` = the absolute path to the current project (use the cwd). The response includes:
   - `entries`: pending worklog entries written since the last reflection — each carries `session_id`, `cli`, `recap_topic`, `last_user`, `importance`, and a `ts` (RFC3339 timestamp).
   - `reason`: why synthesis is being proposed now.
   - `markdown`: a verbatim-renderable summary.

2. **Bucket the entries by UTC date** using each entry's `ts` field. Format each bucket key as `YYYY-MM-DD` (UTC). Entries written at e.g. `2026-05-17T03:30Z` and `2026-05-17T23:45Z` both belong to the `2026-05-17` bucket. Empty buckets (dates with zero entries) are skipped automatically.

3. **For each bucket** (process oldest → newest), synthesize 2–4 insights covering ONLY that day's entries. Each insight is a short sentence about a pattern, decision, or theme from that day. **Every insight MUST cite at least one `session_id` from that day's entries as evidence** — this is the citation invariant the system enforces. Insights without evidence are rejected.

4. **Call `mcp__klyne__record_reflection` ONCE per bucket** with:
   - `project_path` — same path
   - `day` — the bucket key, e.g. `"2026-05-17"`
   - `insights` — the array `[{text: "...", evidence: ["<session_id>", ...]}, ...]`

   The tool returns one persisted reflection id per call.

If `propose_reflection` returns zero entries, tell the user "nothing pending since the last reflection" and stop — don't fabricate insights.

After all `record_reflection` calls succeed, report back the list of dates and reflection ids you wrote (e.g. "wrote 3 daily reflections: 2026-05-15, 2026-05-16, 2026-05-17"). The reflections will surface in the next `/klyne:bootstrap` brief and on the `/worklog` page automatically.
````

- [ ] **Step 2: Verify the slash command parses (embed test)**

Run: `cd ~/Desktop/Project/klyne && go test ./internal/mcpserver/ -run TestSlashCommands -v`

Expected: PASS. The slash commands are embedded via `//go:embed` and any broken frontmatter would surface here.

If `TestSlashCommands` does not exist (verify with `grep -l TestSlashCommand internal/mcpserver/`), instead just confirm the build still succeeds: `go build ./...`.

- [ ] **Step 3: Commit**

```bash
cd ~/Desktop/Project/klyne
git add internal/mcpserver/slashcommands/reflect.md
git commit -m "$(cat <<'EOF'
feat(slashcommands): /klyne:reflect buckets pending entries by UTC date

Single invocation now writes one daily reflection per distinct date
in the pending set, catching up across multiple days with one user
gesture.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: Backend — add `/worklog/items/project` drill-in endpoint

**Files:**
- Modify: `internal/api/contracts.go:32-95` (route constants + `AllRoutes`), `internal/api/contracts.go:858-861` (new DTO)
- Modify: `internal/api/contracts_test.go:32-55` (expected list)
- Modify: `internal/api/handlers/worklog.go` (add `Project` method)
- Modify: `internal/api/handlers/worklog_test.go` (add tests)
- Modify: `internal/api/handlers/mounter.go:127` (register the route)

The new endpoint returns the full daily-reflection list for one project. The chi-router pattern is `/worklog/items/project` with the project absolute path passed as a `?path=…` query param (chi can't bind a path with slashes as a single segment, and we don't want to base64-encode in the URL). Response: `{project: WorklogProjectRollup, reflections: [Reflection]}` so the drill-in page has both the rollup context (status pill, pending count) and the full daily list in one round-trip.

- [ ] **Step 1: Add the failing handler tests**

Append to `internal/api/handlers/worklog_test.go`:

```go
func TestWorklogProject_ReturnsAllReflectionsForOneProject(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	ctx := context.Background()

	// Two reflections under /proj/x, one under /proj/y. The handler must
	// only return /proj/x rows.
	mustUpsert(t, ctx, db, "x-e1", 10_000, "/proj/x", 1)
	mustReflect(t, ctx, db, "x-r1", 20_000, "/proj/x", "x-e1")
	mustUpsert(t, ctx, db, "x-e2", 30_000, "/proj/x", 1)
	mustReflect(t, ctx, db, "x-r2", 40_000, "/proj/x", "x-e2")
	mustUpsert(t, ctx, db, "y-e1", 50_000, "/proj/y", 1)
	mustReflect(t, ctx, db, "y-r1", 60_000, "/proj/y", "y-e1")

	srv := httptest.NewServer(newWorklogRouter(t, db))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/worklog/items/project?path=" + url.QueryEscape("/proj/x"))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var body api.WorklogProjectResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Project.ProjectPath != "/proj/x" {
		t.Errorf("project=%s, want /proj/x", body.Project.ProjectPath)
	}
	if len(body.Reflections) != 2 {
		t.Fatalf("Reflections len=%d, want 2", len(body.Reflections))
	}
	// Newest first.
	if body.Reflections[0].ID != "x-r2" || body.Reflections[1].ID != "x-r1" {
		t.Errorf("order wrong: got %s,%s; want x-r2,x-r1", body.Reflections[0].ID, body.Reflections[1].ID)
	}
}

func TestWorklogProject_RejectsMissingPath(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	srv := httptest.NewServer(newWorklogRouter(t, db))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/worklog/items/project")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", resp.StatusCode)
	}
}

func TestWorklogProject_UnknownProjectReturnsEmpty(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	srv := httptest.NewServer(newWorklogRouter(t, db))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/worklog/items/project?path=" + url.QueryEscape("/no/such/proj"))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var body api.WorklogProjectResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Project.ProjectPath != "/no/such/proj" {
		t.Errorf("project=%s, want /no/such/proj (echo even when empty)", body.Project.ProjectPath)
	}
	if len(body.Reflections) != 0 {
		t.Errorf("Reflections len=%d, want 0", len(body.Reflections))
	}
}
```

Add to the import block at the top of the file (`net/url` likely missing):

```go
"net/url"
```

- [ ] **Step 2: Run the new tests to verify they fail**

Run: `cd ~/Desktop/Project/klyne && go test ./internal/api/handlers/ -run TestWorklogProject -v`

Expected: FAIL at compile — `api.WorklogProjectResponse` undefined and the route returns 404. Proves the next steps are needed.

- [ ] **Step 3: Add the route constant + DTO**

Edit `internal/api/contracts.go:32-65`. Find the existing `RouteWorklog` line:

```go
		RouteWorklog             = "/worklog/items"
```

and insert below it:

```go
		// RouteWorklogProject: per-project drill-in. Takes ?path=<abs-path>;
		// returns the project's rollup PLUS its full daily-reflection list
		// newest-first. Powers /worklog/project in the cockpit SPA.
		RouteWorklogProject      = "/worklog/items/project"
```

In the same file, edit `AllRoutes()` (lines 71-94). After the line `RouteWorklog,` add `RouteWorklogProject,`:

```go
		RouteWorklog,
		RouteWorklogProject,
		RouteInsightsProjects,
```

At the bottom of the file (after the existing `WorklogResponse` struct around line 858-860), append:

```go
// WorklogProjectResponse is GET /worklog/items/project?path=<abs>. Bundles
// the project's rollup (latest reflection, stale-ness, pending count) with
// the full daily-reflection list newest-first — enough for the drill-in
// page to render the header AND the ISO-week-grouped reflection list in
// one round-trip.
//
// When the path has no rows in either table, Project carries the echoed
// ProjectPath, Name = basename(path), LatestReflection = nil, PendingEntries
// = 0, Stale = false; Reflections is an empty (non-nil) slice. The UI
// renders an empty-state message instead of erroring.
type WorklogProjectResponse struct {
	Project     store.WorklogProjectRollup `json:"project"`
	Reflections []store.Reflection         `json:"reflections"`
}
```

- [ ] **Step 4: Add the new route to the contracts test**

Edit `internal/api/contracts_test.go:32-55`. Add `RouteWorklogProject,` after `RouteWorklog,`:

```go
		RouteWorklog,
		RouteWorklogProject,
		RouteInsightsProjects,
```

- [ ] **Step 5: Implement the `Project` handler**

Append to `internal/api/handlers/worklog.go`:

```go
// Project handles GET /worklog/items/project?path=<abs>.
//
// Returns the project's rollup (so the page header can render the stale-ness
// pill and last-activity timestamp) PLUS the full daily-reflection list for
// that project, newest-first. When the project has no rows in either table,
// Reflections is empty and the rollup carries zero counts — the caller
// receives a 200 with an empty payload so the drill-in page can render its
// "no reflections yet" empty state without a separate error path.
func (h *WorklogHandler) Project(w http.ResponseWriter, r *http.Request) {
	projectPath := r.URL.Query().Get("path")
	if projectPath == "" {
		http.Error(w, "missing required ?path=<abs>", http.StatusBadRequest)
		return
	}

	// Pull the full rollup, then pluck the matching row. We reuse the
	// existing rollup query so the staleness/pending math stays in one
	// place. For projects with no rows the rollup returns nothing — we
	// fall back to a zero-valued rollup so the response shape stays
	// predictable for the UI's empty state.
	rollup, err := store.ListWorklogRollup(r.Context(), h.db)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	var project store.WorklogProjectRollup
	found := false
	for _, p := range rollup {
		if p.ProjectPath == projectPath {
			project = p
			found = true
			break
		}
	}
	if !found {
		project = store.WorklogProjectRollup{
			ProjectPath: projectPath,
			Name:        basenameProjectPath(projectPath),
		}
	}

	reflections, err := store.ListReflectionsForProject(r.Context(), h.db, projectPath, 200)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, api.WorklogProjectResponse{
		Project:     project,
		Reflections: reflections,
	})
}

// basenameProjectPath returns the last "/"-separated segment of p. Inline
// because store.basename is unexported; duplicating one tiny helper avoids
// widening that file's API surface for one caller.
func basenameProjectPath(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[i+1:]
		}
	}
	return p
}
```

- [ ] **Step 6: Wire the route in the mounter**

Edit `internal/api/handlers/mounter.go:126-127`. Replace the existing two-line worklog block:

```go
	// /worklog — project-scoped browser over the stop_summaries table.
	// Read-only; both visible and suppressed rows returned so users can
	// audit suppression behavior.
	hWorklog := NewWorklogHandler(m.deps.DB)
	r.Get(api.RouteWorklog, hWorklog.List)
	r.Get(api.RouteWorklogProject, hWorklog.Project)
```

- [ ] **Step 7: Run the handler tests**

Run: `cd ~/Desktop/Project/klyne && go test ./internal/api/handlers/ -run TestWorklog -v`

Expected: all `TestWorklog_*` and `TestWorklogProject_*` PASS.

- [ ] **Step 8: Run the contracts test**

Run: `cd ~/Desktop/Project/klyne && go test ./internal/api/ -run TestAllRoutes -v`

Expected: PASS.

- [ ] **Step 9: Build the whole project**

Run: `cd ~/Desktop/Project/klyne && go build ./...`

Expected: clean build.

- [ ] **Step 10: Commit**

```bash
cd ~/Desktop/Project/klyne
git add internal/api/contracts.go internal/api/contracts_test.go internal/api/handlers/worklog.go internal/api/handlers/worklog_test.go internal/api/handlers/mounter.go
git commit -m "$(cat <<'EOF'
feat(api): /worklog/items/project drill-in endpoint

Returns the project's rollup + its full daily-reflection list
newest-first in one round-trip, powering the per-project drill-in
page that lists dailies grouped by ISO-week.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: Frontend — `WorklogProjectResponse` type + `fetchWorklogProject` client

**Files:**
- Modify: `ui/src/lib/types.ts`
- Modify: `ui/src/lib/api.ts`

Pure additive changes mirroring the new Go DTO and adding the fetch helper.

- [ ] **Step 1: Add the TS interface**

In `ui/src/lib/types.ts`, find the existing `WorklogResponse` block (around line 606-609). Insert below it:

```ts
/** WorklogProjectResponse is GET /worklog/items/project?path=<abs>. */
export interface WorklogProjectResponse {
  project: WorklogProjectRollup;
  reflections: Reflection[];
}
```

- [ ] **Step 2: Add the fetch helper**

In `ui/src/lib/api.ts`, find the existing `fetchWorklog` function (lines 359-361). Insert below it:

```ts
/**
 * GET /worklog/items/project?path=<abs> — per-project drill-in.
 *
 * Returns the project's rollup + its full daily-reflection list
 * newest-first. The path arg is sent verbatim as the query value;
 * the URLSearchParams machinery URL-encodes it.
 */
export async function fetchWorklogProject(path: string): Promise<WorklogProjectResponse> {
  return get<WorklogProjectResponse>('/worklog/items/project', { path });
}
```

Also update the import line near the top of the file to include the new type. Find the existing line that imports from `./types` (run `grep -n "from './types" ui/src/lib/api.ts` to locate it) and add `WorklogProjectResponse` to the import list.

- [ ] **Step 3: Run the contract check**

Run: `cd ~/Desktop/Project/klyne/ui && npm run check`

Expected: PASS (the script verifies TS contracts match the Go side; the new interface mirrors the new DTO exactly).

- [ ] **Step 4: Commit**

```bash
cd ~/Desktop/Project/klyne
git add ui/src/lib/types.ts ui/src/lib/api.ts
git commit -m "$(cat <<'EOF'
feat(ui): WorklogProjectResponse type + fetchWorklogProject client

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: Frontend — `/worklog/project` drill-in page with ISO-week grouping

**Files:**
- Create: `ui/src/routes/worklog/project/+page.svelte`
- Modify: `ui/src/routes/worklog/+page.svelte` (make project cards link to the drill-in)

The drill-in page lists every daily reflection for a project, newest first, grouped by ISO-week with a collapsible heading per week. The path comes from `?path=`.

- [ ] **Step 1: Create the drill-in page**

Create `ui/src/routes/worklog/project/+page.svelte`:

```svelte
<!--
  Worklog drill-in — every daily reflection for one project, grouped by
  ISO-week. Driven by ?path=<abs-project-path>. Linked from the top-level
  /worklog cards.
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/stores';
  import { fetchWorklogProject } from '$lib/api.js';
  import type { Reflection, WorklogProjectResponse } from '$lib/types.js';
  import { relTime } from '$lib/format.js';

  let resp = $state<WorklogProjectResponse | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let collapsed = $state<Record<string, boolean>>({});

  const path = $derived($page.url.searchParams.get('path') ?? '');

  async function load(): Promise<void> {
    if (!path) {
      error = 'missing ?path=<abs-project-path>';
      loading = false;
      return;
    }
    loading = true;
    try {
      resp = await fetchWorklogProject(path);
      error = null;
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'failed to load project';
    } finally {
      loading = false;
    }
  }

  onMount(() => { void load(); });

  // ISO 8601 week label for an epoch-ms timestamp. UTC matches the
  // backend's IsoWeek helper so server and UI agree on bucket boundaries.
  function isoWeek(ts: number): string {
    const d = new Date(ts);
    // Algorithm: ISO week containing Thursday of the same week.
    const utc = new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate()));
    const dayNum = utc.getUTCDay() === 0 ? 7 : utc.getUTCDay();
    utc.setUTCDate(utc.getUTCDate() + 4 - dayNum);
    const yearStart = new Date(Date.UTC(utc.getUTCFullYear(), 0, 1));
    const week = Math.ceil(((utc.getTime() - yearStart.getTime()) / 86_400_000 + 1) / 7);
    return `${utc.getUTCFullYear()}-W${String(week).padStart(2, '0')}`;
  }

  // Bucket reflections by ISO-week, newest week first. Within a week we
  // keep the server's newest-first order (which is already by ts DESC).
  function byWeek(rs: Reflection[]): { week: string; rows: Reflection[] }[] {
    const order: string[] = [];
    const map = new Map<string, Reflection[]>();
    for (const r of rs) {
      const w = isoWeek(r.ts);
      if (!map.has(w)) { map.set(w, []); order.push(w); }
      map.get(w)!.push(r);
    }
    return order.map((w) => ({ week: w, rows: map.get(w)! }));
  }

  function toggle(week: string): void {
    collapsed = { ...collapsed, [week]: !collapsed[week] };
  }
</script>

<div class="page">
  <header class="head">
    <p class="muted small"><a href="/worklog">← Worklog</a></p>
    <h1>{resp?.project.name ?? path}</h1>
    <p class="path muted small"><code>{path}</code></p>

    {#if resp}
      <p class="meta muted">
        {resp.reflections.length} daily reflection{resp.reflections.length === 1 ? '' : 's'}
        {#if resp.project.latest_entry_ts > 0}
          · last activity {relTime(resp.project.latest_entry_ts)}
        {/if}
        {#if resp.project.stale && resp.project.latest_reflection}
          · <span class="status stale">{resp.project.pending_entries} pending</span>
        {/if}
      </p>
    {/if}
  </header>

  {#if loading && !resp}
    <p class="muted">Loading…</p>
  {:else if error}
    <p class="error">⚠ {error}</p>
  {:else if resp}
    {#if resp.reflections.length === 0}
      <p class="muted empty">
        No daily reflections for this project yet. Run
        <code>cd "{path}" && claude -p --permission-mode bypassPermissions '/klyne:reflect'</code>
        to synthesize the first one.
      </p>
    {:else}
      {#each byWeek(resp.reflections) as bucket (bucket.week)}
        <section class="week">
          <button class="week-head" onclick={() => toggle(bucket.week)}>
            <span class="caret">{collapsed[bucket.week] ? '▸' : '▾'}</span>
            <span class="week-label">{bucket.week}</span>
            <span class="muted small">{bucket.rows.length} day{bucket.rows.length === 1 ? '' : 's'}</span>
          </button>
          {#if !collapsed[bucket.week]}
            <div class="day-list">
              {#each bucket.rows as r (r.id)}
                <article class="day-card">
                  <header class="day-head">
                    <h2>{r.title}</h2>
                    <span class="muted small">{relTime(r.ts)}</span>
                  </header>
                  <pre class="body">{r.body_md}</pre>
                  <p class="muted small footer">
                    {r.evidence_entry_ids.length} evidence · tier {r.tier} · {r.summary_source}
                  </p>
                </article>
              {/each}
            </div>
          {/if}
        </section>
      {/each}
    {/if}
  {/if}
</div>

<style>
  .page { padding: 1.5rem; max-width: 1100px; margin: 0 auto; }
  .head h1 { margin: 0.25rem 0; }
  .muted { color: var(--text-muted, #888); }
  .small { font-size: 0.85em; }
  .error { color: var(--text-error, #c33); }
  .empty { text-align: center; padding: 2rem; }
  .path { font-family: monospace; word-break: break-all; }
  .meta { margin: 0.5rem 0; }
  .status.stale { color: #e6a878; }

  .week { margin: 1.25rem 0 0.5rem; }
  .week-head {
    display: flex; align-items: baseline; gap: 0.5rem;
    width: 100%;
    background: var(--surface, #1a1a1a);
    border: 1px solid var(--border, #2a2a2a);
    border-radius: 6px;
    padding: 0.5rem 0.75rem;
    color: var(--text, #ddd);
    cursor: pointer;
    text-align: left;
    font-size: 0.95em;
  }
  .week-head:hover { background: var(--border, #2a2a2a); }
  .caret { width: 1em; }
  .week-label { font-weight: 600; }

  .day-list { display: flex; flex-direction: column; gap: 0.75rem; margin: 0.5rem 0 0; padding-left: 1rem; }

  .day-card {
    background: var(--surface, #1a1a1a);
    border: 1px solid var(--border, #2a2a2a);
    border-left: 3px solid #4a9d5b;
    border-radius: 6px;
    padding: 0.75rem 1rem;
  }
  .day-head {
    display: flex; align-items: baseline; gap: 0.75rem; flex-wrap: wrap;
  }
  .day-head h2 { margin: 0; font-size: 1rem; }

  .body {
    white-space: pre-wrap; word-break: break-word;
    background: var(--surface-2, #0d0d0d);
    padding: 0.6rem 0.85rem; border-radius: 4px;
    font-size: 0.9em; margin: 0.5rem 0 0;
    line-height: 1.55;
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
  }
  .footer { margin: 0.35rem 0 0; }
</style>
```

- [ ] **Step 2: Link top-level worklog cards to the drill-in**

Edit `ui/src/routes/worklog/+page.svelte`. In the card header (around line 101-103), change:

```svelte
            <header class="card-head">
              <h2>{p.name}</h2>
              <span class="status {s.klass}">{s.label}</span>
            </header>
```

to:

```svelte
            <header class="card-head">
              <h2><a class="drill-link" href={`/worklog/project?path=${encodeURIComponent(p.project_path)}`}>{p.name}</a></h2>
              <span class="status {s.klass}">{s.label}</span>
            </header>
```

Add at the end of the `<style>` block (before the closing `</style>` at the bottom of the file):

```css
  .drill-link {
    color: inherit;
    text-decoration: none;
  }
  .drill-link:hover { text-decoration: underline; }
```

- [ ] **Step 3: Run the contract + svelte-check**

Run: `cd ~/Desktop/Project/klyne/ui && npm run check`

Expected: PASS.

- [ ] **Step 4: Manually smoke the UI**

Start the daemon and serve the UI:

```bash
cd ~/Desktop/Project/klyne && go run ./cmd/klyne serve &
cd ~/Desktop/Project/klyne/ui && npm run dev
```

Open `http://localhost:5173/worklog` in a browser. Click any project card heading — it should navigate to `/worklog/project?path=<encoded>` and display the per-week grouped daily list (or the empty state when the project has no tier-1 reflections yet). Toggling a week heading should collapse/expand its day list.

Stop the dev server with Ctrl+C and the daemon with `pkill -f 'klyne serve'`.

If you cannot run the UI in this environment, say so explicitly and note that manual smoke is deferred to the user — DO NOT claim the page works without seeing it.

- [ ] **Step 5: Commit**

```bash
cd ~/Desktop/Project/klyne
git add ui/src/routes/worklog/project/+page.svelte ui/src/routes/worklog/+page.svelte
git commit -m "$(cat <<'EOF'
feat(ui): /worklog/project drill-in with ISO-week grouping

Per-project page listing every daily reflection newest-first, with
a collapsible heading per ISO-week. Linked from the project name on
the top-level /worklog page.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: Showcase harness — scenario 04 asserts tier=1 and daily title

**Files:**
- Modify: `test/scenarios/scenarios/04-reflection-synthesis.js:103-138`

Today the scenario asserts citation invariant + body length, but not the tier or title format. We add two more assertions so a regression to the old weekly behavior shows up in the harness.

- [ ] **Step 1: Append the new assertions to the reflection-presence branch**

In `test/scenarios/scenarios/04-reflection-synthesis.js`, find the `if (reflections.length === 0)` / `else` branch (around line 97-138). Inside the `else` (after the existing "body sanity" assertion that ends around line 137), append:

```js
      // Tier invariant — daily reflections (tier=1), not weekly (tier=2).
      const wrongTier = reflections.filter((r) => Number(r.tier) !== 1);
      steps.push({
        name: "tier invariant: every reflection is tier=1 (daily)",
        status: wrongTier.length === 0 ? STATUS.PASS : STATUS.FAIL,
        detail: wrongTier.length === 0
          ? "All reflections are tier=1."
          : `${wrongTier.length} reflection(s) wrote a non-daily tier.`,
      });

      // Title format — "Daily reflection — YYYY-MM-DD".
      const dailyTitle = /^Daily reflection — \d{4}-\d{2}-\d{2}$/;
      const wrongTitle = reflections.filter((r) => !dailyTitle.test(r.title || ""));
      steps.push({
        name: "title format: 'Daily reflection — YYYY-MM-DD'",
        status: wrongTitle.length === 0 ? STATUS.PASS : STATUS.FAIL,
        detail: wrongTitle.length === 0
          ? "All titles match the daily format."
          : `${wrongTitle.length} reflection(s) with non-matching title (e.g. ${JSON.stringify(wrongTitle[0]?.title || "")}).`,
      });
```

- [ ] **Step 2: Dry-run the scenario (no Claude calls)**

Run: `cd ~/Desktop/Project/klyne/test/scenarios && node -e "import('./scenarios/04-reflection-synthesis.js').then(m => m.run({ noClaude: true })).then(r => console.log(r.status, r.summary))"`

Expected: prints `SKIP Dry-run: would run 3 seed sessions then /klyne:reflect.` — proves the JS still parses cleanly.

- [ ] **Step 3: Commit**

```bash
cd ~/Desktop/Project/klyne
git add test/scenarios/scenarios/04-reflection-synthesis.js
git commit -m "$(cat <<'EOF'
test(scenarios): scenario 04 asserts daily tier + title format

Catches regressions to the old weekly reflection shape.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 9: Final verification + handoff update

**Files:**
- Modify: `HANDOFF.md` (mark daily reflections section shipped)

Wrap up by running the entire test suite end-to-end and updating the handoff doc so the next session sees the work as complete.

- [ ] **Step 1: Run the full Go test suite**

Run: `cd ~/Desktop/Project/klyne && go test ./... -count=1`

Expected: PASS across every package.

- [ ] **Step 2: Run the UI checks**

Run: `cd ~/Desktop/Project/klyne/ui && npm run check && npm test`

Expected: PASS.

- [ ] **Step 3: Update HANDOFF.md to mark daily reflections as shipped**

Edit `HANDOFF.md`. Find the section `## Open product decision — daily reflections` (line 67). Replace its heading and the "Work remaining to ship this" body with a shorter "shipped" note:

```markdown
## Shipped — daily reflections

Daily reflections (tier=1) are now the foundational unit. `/klyne:reflect`
auto-buckets pending entries by UTC date and writes one
`"Daily reflection — YYYY-MM-DD"` reflection per date in a single run.

Files: `internal/worklog/reflection_recorder.go`,
`internal/mcpserver/tool_record_reflection.go`,
`internal/mcpserver/slashcommands/reflect.md`. The new drill-in lives at
`/worklog/project?path=<abs>` (backend: `RouteWorklogProject` →
`internal/api/handlers/worklog.go::Project`).

Plan: `docs/superpowers/plans/2026-05-18-daily-reflections.md`.
```

- [ ] **Step 4: Commit**

```bash
cd ~/Desktop/Project/klyne
git add HANDOFF.md
git commit -m "$(cat <<'EOF'
docs: mark daily reflections section shipped in HANDOFF

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

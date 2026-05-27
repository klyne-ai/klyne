package worklog

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/store"
)

func newRecorderTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "recorder.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

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

func TestRecordReflection_RejectsEmptyInsights(t *testing.T) {
	db := newRecorderTestDB(t)
	_, err := RecordReflection(context.Background(), db, "/p", time.Time{}, nil)
	if err == nil {
		t.Errorf("expected error on nil insights")
	}
	if err == nil || !strings.Contains(err.Error(), "at least one insight required") {
		t.Errorf("expected 'at least one insight required' error, got %v", err)
	}
}

func TestRecordReflection_RejectsMissingEvidence(t *testing.T) {
	db := newRecorderTestDB(t)
	insights := []Insight{
		{Text: "vague claim", Evidence: nil},
	}
	_, err := RecordReflection(context.Background(), db, "/p", time.Time{}, insights)
	if err == nil {
		t.Errorf("expected citation-invariant error, got nil")
	}
	if err == nil || !strings.Contains(err.Error(), "citation invariant") {
		t.Errorf("expected error mentioning citation invariant, got %v", err)
	}
}

func TestRecordReflection_RejectsEmptyText(t *testing.T) {
	db := newRecorderTestDB(t)
	insights := []Insight{
		{Text: "   ", Evidence: []string{"e1"}},
	}
	_, err := RecordReflection(context.Background(), db, "/p", time.Time{}, insights)
	if err == nil {
		t.Errorf("expected error on empty insight text")
	}
}

func TestRecordReflection_CanonicalizesWorktreePath(t *testing.T) {
	// A reflection triggered from a worktree must land under the main
	// repo's project_path so it appears alongside that repo's other
	// entries, not as a sibling card on the worklog page.
	main, wt := newRepoWithWorktree(t)
	db := newRecorderTestDB(t)
	insights := []Insight{{Text: "shipped", Evidence: []string{"e1"}}}

	refl, err := RecordReflection(context.Background(), db, wt, time.Time{}, insights)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	wantAbs, _ := filepath.EvalSymlinks(main)
	gotAbs, _ := filepath.EvalSymlinks(refl.ProjectPath)
	if gotAbs != wantAbs {
		t.Errorf("project_path = %q, want canonical %q (worktree input %q must roll up)", gotAbs, wantAbs, wt)
	}
}

func TestRecordReflection_RejectsEmptyProject(t *testing.T) {
	db := newRecorderTestDB(t)
	insights := []Insight{{Text: "x", Evidence: []string{"e"}}}
	_, err := RecordReflection(context.Background(), db, "", time.Time{}, insights)
	if err == nil {
		t.Errorf("expected error on empty project_path")
	}
}

// Improvement 4: the unverified-artifact-ID guard runs inside
// RecordReflection. A "PR #57" in insight text that is not backed by any
// evidence string must never reach body_md (the documented "PR #57"
// hallucination guard).
func TestRecordReflection_StripsUnverifiedPRRef(t *testing.T) {
	db := newRecorderTestDB(t)
	insights := []Insight{
		{Text: "Shipped the labstack pipeline via PR #57", Evidence: []string{"entry-1"}},
	}
	day := time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC)
	refl, err := RecordReflection(context.Background(), db, "/p", day, insights)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if strings.Contains(refl.BodyMD, "PR #57") {
		t.Errorf("unverified 'PR #57' must be stripped from body_md, got:\n%s", refl.BodyMD)
	}
	// The rest of the insight prose must survive — only the bad ref goes.
	if !strings.Contains(refl.BodyMD, "labstack pipeline") {
		t.Errorf("guard over-stripped — insight prose lost, got:\n%s", refl.BodyMD)
	}
}

// Improvements 1, 3, 5, 7: RecordReflectionWithSubstrate appends the
// deterministic git-grounded sections (open loops, shipped ledger,
// commit-dated, cross-project thread) to body_md after the AI insights.
func TestRecordReflectionWithSubstrate_AppendsGitSections(t *testing.T) {
	db := newRecorderTestDB(t)
	insights := []Insight{
		{Text: "Built the labstack pipeline", Evidence: []string{"entry-1"}},
	}
	day := time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC)
	rep := fixtureReport()
	refl, err := RecordReflectionWithSubstrate(
		context.Background(), db, "/repos/consultation-service", day, insights, rep)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	// AI insight prose still present.
	if !strings.Contains(refl.BodyMD, "labstack pipeline") {
		t.Errorf("AI insight lost from body, got:\n%s", refl.BodyMD)
	}
	// Improvement 1: open-loops block appended.
	if !strings.Contains(refl.BodyMD, "Open Loops") {
		t.Errorf("expected Open Loops block in body, got:\n%s", refl.BodyMD)
	}
	// Improvement 5: shipped ledger appended.
	if !strings.Contains(refl.BodyMD, "Shipped") {
		t.Errorf("expected Shipped ledger in body, got:\n%s", refl.BodyMD)
	}
	// Improvement 3: commit-date present.
	if !strings.Contains(refl.BodyMD, "2026-05-19") {
		t.Errorf("expected commit date in body, got:\n%s", refl.BodyMD)
	}
}

// Improvement 4 end-to-end via the substrate path: a "PR #57" not backed
// by any commit subject/branch in the report is stripped even when the
// insight cites it.
func TestRecordReflectionWithSubstrate_GuardUsesGitEvidence(t *testing.T) {
	db := newRecorderTestDB(t)
	rep := fixtureReport()
	insights := []Insight{
		// Cites a real SHA but invents PR #57 — must be stripped.
		{Text: "wired pipeline (aaaaaaaa) and opened PR #57", Evidence: []string{"entry-1"}},
	}
	day := time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC)
	refl, err := RecordReflectionWithSubstrate(
		context.Background(), db, "/repos/consultation-service", day, insights, rep)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if strings.Contains(refl.BodyMD, "PR #57") {
		t.Errorf("unverified PR #57 must be stripped even with git evidence, got:\n%s", refl.BodyMD)
	}
}

// Graceful degradation (D8): a project with no matching Service in the
// report behaves exactly like plain RecordReflection — no git sections,
// no error.
func TestRecordReflectionWithSubstrate_DegradesWhenNoRepo(t *testing.T) {
	db := newRecorderTestDB(t)
	insights := []Insight{{Text: "did work", Evidence: []string{"e1"}}}
	day := time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC)
	rep := fixtureReport() // has no Service for /not/a/repo
	refl, err := RecordReflectionWithSubstrate(
		context.Background(), db, "/not/a/repo", day, insights, rep)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if strings.Contains(refl.BodyMD, "Open Loops") || strings.Contains(refl.BodyMD, "Shipped") {
		t.Errorf("non-git project must get no git sections, got:\n%s", refl.BodyMD)
	}
	if !strings.Contains(refl.BodyMD, "did work") {
		t.Errorf("existing reflection body must be preserved, got:\n%s", refl.BodyMD)
	}
}

// TestRecordReflectionTyped_RoundTrip is the spec §1.1 typed-payload
// happy path: a properly-formed WWDPayload round-trips through
// RecordReflectionTyped → store.InsertReflection → ListReflectionsForProject
// with BOTH BodyJSON (raw, canonical) and BodyMD (deterministically
// rendered) populated. The body_md rendering must group by the §1.2
// kind order so legacy prose readers see the same order the dashboard
// does.
func TestRecordReflectionTyped_RoundTrip(t *testing.T) {
	db := newRecorderTestDB(t)
	payload := WWDPayload{
		Service: "klyne",
		Details: []WWDDetail{
			{
				Kind:      DetailKindShipped,
				When:      "16:49",
				Text:      "Wrote ~/.codex/hooks.json with klyne-hook entries for SessionStart/UserPromptSubmit",
				Evidence:  []string{"be8cc8c8", "~/.codex/hooks.json"},
				SessionID: "sess-1",
			},
			{
				Kind:      DetailKindFixed,
				When:      "17:10",
				Text:      "Applied .dot--live modifier across 3 svelte files",
				Evidence:  []string{"620a9551"},
				SessionID: "sess-2",
			},
			{
				Kind:      DetailKindDecision,
				When:      "16:34",
				Text:      "Adopt git-substrate join in propose_reflection",
				Evidence:  []string{"sess-3"},
				SessionID: "sess-3",
			},
		},
	}
	day := time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)
	refl, err := RecordReflectionTyped(context.Background(), db, "/p", day, payload)
	if err != nil {
		t.Fatalf("record typed: %v", err)
	}
	// In-memory struct carries both columns.
	if refl.BodyJSON == "" {
		t.Errorf("expected non-empty BodyJSON on returned reflection")
	}
	if refl.BodyMD == "" {
		t.Errorf("expected non-empty BodyMD (rendered companion) on returned reflection")
	}
	if !strings.Contains(refl.BodyMD, "What was done — klyne") {
		t.Errorf("expected service header in rendered body_md, got:\n%s", refl.BodyMD)
	}
	if !strings.Contains(refl.BodyMD, "[SHIPPED]") || !strings.Contains(refl.BodyMD, "[FIXED]") || !strings.Contains(refl.BodyMD, "[DECISION]") {
		t.Errorf("expected each kind chip in rendered body_md, got:\n%s", refl.BodyMD)
	}
	// §1.2 render order: SHIPPED before FIXED before DECISION.
	shippedIdx := strings.Index(refl.BodyMD, "[SHIPPED]")
	fixedIdx := strings.Index(refl.BodyMD, "[FIXED]")
	decisionIdx := strings.Index(refl.BodyMD, "[DECISION]")
	if !(shippedIdx < fixedIdx && fixedIdx < decisionIdx) {
		t.Errorf("rendered body_md violates §1.2 kind order (SHIPPED→FIXED→DECISION), got:\n%s", refl.BodyMD)
	}
	// session_id back-pointers are at the head of evidence_entry_ids so
	// the citation invariant carries the originating stop_summary row.
	if len(refl.EvidenceEntryIDs) == 0 {
		t.Fatalf("expected non-empty evidence_entry_ids derived from typed payload")
	}

	// Round-trip via the store layer — confirms BodyJSON survives the
	// migration-023 column round-trip.
	rows, err := store.ListReflectionsForProject(context.Background(), db, "/p", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 persisted typed reflection, got %d", len(rows))
	}
	if rows[0].BodyJSON == "" {
		t.Errorf("persisted row missing body_json — typed payload did not round-trip")
	}
	if rows[0].BodyMD == "" {
		t.Errorf("persisted row missing body_md companion")
	}

	// Parse the persisted body_json and confirm shape matches what we
	// emitted (deterministic JSON marshaling preserves field order
	// inside details).
	parsed, err := store.ParseBodyJSON(rows[0])
	if err != nil {
		t.Fatalf("parse persisted body_json: %v", err)
	}
	if parsed.Service != "klyne" {
		t.Errorf("persisted body_json.service = %q, want klyne", parsed.Service)
	}
	if len(parsed.Details) != 3 {
		t.Errorf("persisted body_json.details = %d items, want 3", len(parsed.Details))
	}
}

// TestRecordReflectionTyped_UpsertsByProjectDayService confirms the
// 2026-05-26 idempotency fix: re-running RecordReflectionTyped for the
// same (project, day, service) REPLACES the prior typed payload instead
// of appending a second row. Pre-fix, a re-reflect would accumulate
// ghost rows and the dashboard would merge stale details from earlier
// runs into the latest card. The DELETE-before-INSERT path leaves
// prose-only rows for the same day untouched so the legacy panel keeps
// its insights.
func TestRecordReflectionTyped_UpsertsByProjectDayService(t *testing.T) {
	db := newRecorderTestDB(t)
	ctx := context.Background()
	day := time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)

	first := WWDPayload{
		Service: "operations-app",
		Details: []WWDDetail{
			{Kind: DetailKindMajor, When: "16:35", Text: "Stale first detail.", Evidence: []string{"PR #432", "sess-x"}, SessionID: "sess-x"},
		},
	}
	if _, err := RecordReflectionTyped(ctx, db, "/p", day, first); err != nil {
		t.Fatalf("first record: %v", err)
	}

	second := WWDPayload{
		Service: "operations-app",
		Details: []WWDDetail{
			{Kind: DetailKindShipped, When: "11:26", Text: "Shipped CLI-1473 PaymentStep canManage fix.", Evidence: []string{"CLI-1473", "sess-a"}, SessionID: "sess-a"},
			{Kind: DetailKindShipped, When: "12:17", Text: "Opened PR #428 for CLI-1340 cancelled-bill modal.", Evidence: []string{"PR #428", "CLI-1340", "sess-b"}, SessionID: "sess-b"},
		},
	}
	if _, err := RecordReflectionTyped(ctx, db, "/p", day, second); err != nil {
		t.Fatalf("second record (idempotent rewrite): %v", err)
	}

	rows, err := store.ListReflectionsForProjectDay(ctx, db, "/p", "2026-05-26")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	typedRows := 0
	for _, r := range rows {
		if strings.TrimSpace(r.BodyJSON) != "" {
			typedRows++
		}
	}
	if typedRows != 1 {
		t.Fatalf("expected exactly 1 typed row after re-reflect (idempotent), got %d (total rows=%d)", typedRows, len(rows))
	}
	parsed, err := store.ParseBodyJSON(rows[len(rows)-1])
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(parsed.Details) != 2 {
		t.Fatalf("expected the latest payload's 2 details to win, got %d", len(parsed.Details))
	}
	if !strings.Contains(parsed.Details[0].Text+parsed.Details[1].Text, "CLI-1473") {
		t.Errorf("expected the second payload to win the row; got details:\n%+v", parsed.Details)
	}
}

// TestRecordReflectionTyped_PreservesProseRowsOnUpsert confirms the
// upsert path only deletes TYPED same-day same-service rows, not
// prose-only rows. The prose panel needs to survive a typed re-reflect.
func TestRecordReflectionTyped_PreservesProseRowsOnUpsert(t *testing.T) {
	db := newRecorderTestDB(t)
	ctx := context.Background()
	day := time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)

	// Seed a prose-only row for the same project + day.
	proseInsights := []Insight{
		{Text: "Investigated CLI-1340 subscription dropdown.", Evidence: []string{"sess-prose"}},
	}
	if _, err := RecordReflection(ctx, db, "/p", day, proseInsights); err != nil {
		t.Fatalf("seed prose: %v", err)
	}

	// Then write the typed payload.
	typed := WWDPayload{
		Service: "operations-app",
		Details: []WWDDetail{
			{Kind: DetailKindShipped, When: "16:35", Text: "Shipped CLI-1452 rebase + PR #432.", Evidence: []string{"PR #432", "sess-rebase"}, SessionID: "sess-rebase"},
		},
	}
	if _, err := RecordReflectionTyped(ctx, db, "/p", day, typed); err != nil {
		t.Fatalf("typed record: %v", err)
	}

	rows, err := store.ListReflectionsForProjectDay(ctx, db, "/p", "2026-05-26")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) < 2 {
		t.Fatalf("expected prose + typed = 2 rows after typed re-reflect, got %d", len(rows))
	}
	var hasProse, hasTyped bool
	for _, r := range rows {
		if strings.TrimSpace(r.BodyJSON) == "" && strings.TrimSpace(r.BodyMD) != "" {
			hasProse = true
		}
		if strings.TrimSpace(r.BodyJSON) != "" {
			hasTyped = true
		}
	}
	if !hasProse {
		t.Errorf("prose-only row was clobbered by typed upsert — should have been preserved")
	}
	if !hasTyped {
		t.Errorf("typed row missing after upsert")
	}
}

// TestRecordReflectionTyped_RejectsEmptyEvidence is the citation-
// invariant guard for the typed path: any detail with empty evidence is
// rejected and no row is written. Spec §5.
func TestRecordReflectionTyped_RejectsEmptyEvidence(t *testing.T) {
	db := newRecorderTestDB(t)
	payload := WWDPayload{
		Service: "klyne",
		Details: []WWDDetail{
			{Kind: DetailKindShipped, When: "10:00", Text: "did a thing", Evidence: nil},
		},
	}
	day := time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)
	_, err := RecordReflectionTyped(context.Background(), db, "/p", day, payload)
	if err == nil {
		t.Fatalf("expected citation-invariant error on empty evidence")
	}
	if !strings.Contains(err.Error(), "evidence empty") {
		t.Errorf("expected 'evidence empty' diagnostic, got: %v", err)
	}
	rows, err := store.ListReflectionsForProject(context.Background(), db, "/p", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("rejected payload must not write a row, got %d row(s)", len(rows))
	}
}

// TestRecordReflectionTyped_RejectsUnbackedPRRef is the spec §5
// "don't invent PR numbers" guard: a detail whose text says "PR #57"
// without "#57" appearing in that SAME detail's evidence is rejected
// outright (no silent strip — typed details face the dashboard UI
// where an invented PR ref would be more load-bearing than in prose).
func TestRecordReflectionTyped_RejectsUnbackedPRRef(t *testing.T) {
	db := newRecorderTestDB(t)
	payload := WWDPayload{
		Service: "klyne",
		Details: []WWDDetail{
			{
				Kind:     DetailKindShipped,
				When:     "12:00",
				Text:     "Shipped the labstack pipeline via PR #57",
				Evidence: []string{"be8cc8c8"}, // no "#57" anywhere
			},
		},
	}
	day := time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)
	_, err := RecordReflectionTyped(context.Background(), db, "/p", day, payload)
	if err == nil {
		t.Fatalf("expected error on unbacked PR ref")
	}
	if !strings.Contains(err.Error(), "PR #57") {
		t.Errorf("expected diagnostic naming the offending PR number, got: %v", err)
	}
}

// TestRecordReflectionTyped_AcceptsBackedPRRef confirms the inverse:
// a "PR #57" that IS present in the detail's evidence (e.g. a commit
// subject "Merge PR #57" cited as evidence) survives validation and
// the row lands.
func TestRecordReflectionTyped_AcceptsBackedPRRef(t *testing.T) {
	db := newRecorderTestDB(t)
	payload := WWDPayload{
		Service: "klyne",
		Details: []WWDDetail{
			{
				Kind: DetailKindShipped,
				When: "12:00",
				Text: "Shipped the labstack pipeline via PR #57",
				// Evidence carries "#57" literally — the guard is satisfied.
				Evidence:  []string{"be8cc8c8", "Merge PR #57: labstack pipeline"},
				SessionID: "sess-1",
			},
		},
	}
	day := time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)
	refl, err := RecordReflectionTyped(context.Background(), db, "/p", day, payload)
	if err != nil {
		t.Fatalf("expected backed PR ref to pass validation, got: %v", err)
	}
	if !strings.Contains(refl.BodyMD, "PR #57") {
		t.Errorf("backed PR ref must survive into body_md, got:\n%s", refl.BodyMD)
	}
}

// TestRecordReflectionTyped_RejectsBadKind / BadWhen / OverlongText
// covers the §1.1 schema-validator boundary cases.
func TestRecordReflectionTyped_RejectsSchemaViolations(t *testing.T) {
	db := newRecorderTestDB(t)
	day := time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		detail WWDDetail
		want   string
	}{
		{
			name:   "bad kind",
			detail: WWDDetail{Kind: "BOGUS", When: "10:00", Text: "x", Evidence: []string{"e1"}},
			want:   "kind",
		},
		{
			name:   "bad when",
			detail: WWDDetail{Kind: DetailKindShipped, When: "10:99", Text: "x", Evidence: []string{"e1"}},
			want:   "when",
		},
		{
			name:   "overlong text",
			detail: WWDDetail{Kind: DetailKindShipped, When: "10:00", Text: strings.Repeat("x", 201), Evidence: []string{"e1"}},
			want:   "200",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := RecordReflectionTyped(context.Background(), db, "/p", day,
				WWDPayload{Service: "klyne", Details: []WWDDetail{tc.detail}})
			if err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("expected error mentioning %q, got: %v", tc.want, err)
			}
		})
	}
}

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

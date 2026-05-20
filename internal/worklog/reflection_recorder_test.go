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

package worklog

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klyne-ai/klyne/internal/store"
)

func newRecorderTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "recorder.db"))
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
	refl, err := RecordReflection(context.Background(), db, "/p", insights)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if refl.ID == "" {
		t.Errorf("expected non-empty id")
	}
	if refl.ProjectPath != "/p" {
		t.Errorf("project_path lost: %q", refl.ProjectPath)
	}
	if refl.Tier != 2 {
		t.Errorf("expected tier=2 (weekly), got %d", refl.Tier)
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

	// Confirm round-trip through the store.
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
	_, err := RecordReflection(context.Background(), db, "/p", nil)
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
	_, err := RecordReflection(context.Background(), db, "/p", insights)
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
	_, err := RecordReflection(context.Background(), db, "/p", insights)
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

	refl, err := RecordReflection(context.Background(), db, wt, insights)
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
	_, err := RecordReflection(context.Background(), db, "", insights)
	if err == nil {
		t.Errorf("expected error on empty project_path")
	}
}

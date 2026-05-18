package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/klyne-ai/klyne/internal/store"
	"github.com/klyne-ai/klyne/internal/worklog"
)

func TestHandleRecordReflection_Persists(t *testing.T) {
	withFakeHome(t)
	db := withBootstrapDB(t)

	in := RecordReflectionInput{
		ProjectPath: "/p",
		Insights: []worklog.Insight{
			{Text: "shipped auth refactor", Evidence: []string{"s1", "s2"}},
			{Text: "improved test coverage", Evidence: []string{"s3"}},
		},
	}
	out, err := handleRecordReflection(context.Background(), db, in)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if out.ReflectionID == "" {
		t.Errorf("expected non-empty reflection id")
	}
	if out.EvidenceCount != 3 {
		t.Errorf("expected 3 evidence entries, got %d", out.EvidenceCount)
	}

	rows, err := store.ListReflectionsForProject(context.Background(), db, "/p", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 persisted reflection, got %d", len(rows))
	}
}

func TestHandleRecordReflection_RejectsEmptyEvidence(t *testing.T) {
	withFakeHome(t)
	db := withBootstrapDB(t)

	in := RecordReflectionInput{
		ProjectPath: "/p",
		Insights: []worklog.Insight{
			{Text: "vague claim", Evidence: nil},
		},
	}
	_, err := handleRecordReflection(context.Background(), db, in)
	if err == nil {
		t.Errorf("expected citation-invariant error, got nil")
	}
	if err != nil && !strings.Contains(err.Error(), "citation invariant") {
		t.Errorf("expected error mentioning citation invariant, got %v", err)
	}
}

func TestHandleRecordReflection_RejectsEmptyInsights(t *testing.T) {
	withFakeHome(t)
	db := withBootstrapDB(t)

	_, err := handleRecordReflection(context.Background(), db, RecordReflectionInput{ProjectPath: "/p"})
	if err == nil {
		t.Errorf("expected error on empty insights, got nil")
	}
}

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
	if !strings.Contains(err.Error(), "bad day") {
		t.Errorf("expected error mentioning 'bad day', got %v", err)
	}
}

package mcpserver

import (
	"context"
	"testing"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/store"
)

// mustRecord calls HandleRecordDecision and returns the output id.
// Fails the test on error.
func mustRecord(t *testing.T, in RecordDecisionInput) RecordDecisionOutput {
	t.Helper()
	_, out, err := HandleRecordDecision(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("HandleRecordDecision: %v", err)
	}
	return out
}

func TestHandleRecordDecision_DedupesSameTextSameProject(t *testing.T) {
	withFakeHomeAndConfigDir(t)

	first := mustRecord(t, RecordDecisionInput{
		Text:        "Labstack runbook: deploy feat/labstack-integration to dev.",
		ProjectPath: "/p1",
		Tags:        []string{"runbook", "labstack"},
	})
	if first.ID == "" {
		t.Fatalf("first record returned no id")
	}

	// Second save with identical text + same project must NOT create a
	// new row — it must return the existing id idempotently.
	second := mustRecord(t, RecordDecisionInput{
		Text:        "Labstack runbook: deploy feat/labstack-integration to dev.",
		ProjectPath: "/p1",
		Tags:        []string{"runbook", "different-tags"},
	})
	if second.ID != first.ID {
		t.Fatalf("dedup failed: got new id %q, want existing %q", second.ID, first.ID)
	}

	// Verify the DB only has ONE row for /p1.
	db, err := store.Open(context.Background(), config.DBPath())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	rows, err := store.ListDecisions(context.Background(), db, store.DecisionFilter{
		ProjectPath: "/p1",
		Limit:       50,
	})
	if err != nil {
		t.Fatalf("ListDecisions: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 row after dedup, got %d", len(rows))
	}
}

func TestHandleRecordDecision_DedupTrimsBeforeCompare(t *testing.T) {
	withFakeHomeAndConfigDir(t)

	first := mustRecord(t, RecordDecisionInput{
		Text:        "trim-me runbook",
		ProjectPath: "/p1",
	})
	// Same text with surrounding whitespace must dedup.
	second := mustRecord(t, RecordDecisionInput{
		Text:        "   trim-me runbook\n  ",
		ProjectPath: "/p1",
	})
	if second.ID != first.ID {
		t.Errorf("whitespace-only diff should dedup; got %q vs %q", second.ID, first.ID)
	}
}

func TestHandleRecordDecision_AllowsSameTextDifferentProject(t *testing.T) {
	withFakeHomeAndConfigDir(t)

	p1 := mustRecord(t, RecordDecisionInput{
		Text:        "shared runbook body",
		ProjectPath: "/p1",
	})
	p2 := mustRecord(t, RecordDecisionInput{
		Text:        "shared runbook body",
		ProjectPath: "/p2",
	})
	if p1.ID == "" || p2.ID == "" {
		t.Fatalf("missing id: p1=%q p2=%q", p1.ID, p2.ID)
	}
	if p1.ID == p2.ID {
		t.Errorf("different projects must NOT dedup; got same id %q", p1.ID)
	}

	db, err := store.Open(context.Background(), config.DBPath())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	for _, p := range []string{"/p1", "/p2"} {
		rows, err := store.ListDecisions(context.Background(), db, store.DecisionFilter{
			ProjectPath: p, Limit: 10,
		})
		if err != nil {
			t.Fatalf("ListDecisions(%s): %v", p, err)
		}
		if len(rows) != 1 {
			t.Errorf("expected 1 row in %s, got %d", p, len(rows))
		}
	}
}

func TestHandleRecordDecision_AllowsDifferentText(t *testing.T) {
	withFakeHomeAndConfigDir(t)

	first := mustRecord(t, RecordDecisionInput{
		Text:        "Labstack runbook: original wording",
		ProjectPath: "/p1",
	})
	second := mustRecord(t, RecordDecisionInput{
		Text:        "Labstack runbook: reworded slightly",
		ProjectPath: "/p1",
	})
	if first.ID == "" || second.ID == "" {
		t.Fatalf("missing id: first=%q second=%q", first.ID, second.ID)
	}
	if first.ID == second.ID {
		t.Errorf("different text bodies should NOT dedup; got same id %q", first.ID)
	}
}

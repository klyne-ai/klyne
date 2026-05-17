package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/store"
)

func TestHandleSummarizeSession_AllSourcesPopulated(t *testing.T) {
	withFakeHome(t)
	db := withBootstrapDB(t)
	ctx := context.Background()

	// Session row so decisions can FK against it.
	sess := &connectors.Session{
		ID:          "sess-sum",
		CLI:         connectors.CLIClaude,
		ProjectPath: "/tmp/proj",
		StartedAt:   1000,
		LastMsgAt:   3000,
		Status:      connectors.SessionStatusActive,
	}
	if err := store.UpsertSession(ctx, db, sess); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Two stop_summaries (newest first after sort).
	for _, s := range []*store.StopSummary{
		{SessionID: "sess-sum", Ts: 2000, ProjectPath: "/tmp/proj", Summary: "first stop", Files: []string{"a.go", "b.go"}, LastUser: "do thing"},
		{SessionID: "sess-sum", Ts: 3000, ProjectPath: "/tmp/proj", Summary: "second stop", Files: []string{"b.go", "c.go"}, LastUser: "do other thing"},
	} {
		if err := store.InsertStopSummary(ctx, db, s); err != nil {
			t.Fatalf("insert stop summary: %v", err)
		}
	}

	// A rolling AI summary.
	if err := store.InsertSummary(ctx, db, &store.Summary{
		SessionID: "sess-sum", Text: "we built X then Y", Model: "test-model", TS: 3100,
	}); err != nil {
		t.Fatalf("insert summary: %v", err)
	}

	// Linked decisions.
	for _, d := range []*store.Decision{
		{ID: "dec-1", Ts: 2500, ProjectPath: "/tmp/proj", SessionID: "sess-sum", Text: "picked sqlite"},
		{ID: "dec-2", Ts: 2700, ProjectPath: "/tmp/proj", SessionID: "sess-sum", Text: "dropped v1"},
	} {
		if err := store.InsertDecision(ctx, db, d); err != nil {
			t.Fatalf("insert decision: %v", err)
		}
	}

	_, out, err := HandleSummarizeSession(context.Background(), nil, SummarizeSessionInput{SessionID: "sess-sum"})
	if err != nil {
		t.Fatalf("HandleSummarizeSession: %v", err)
	}
	if !out.Found {
		t.Fatalf("Found = false, want true")
	}
	if len(out.StopSummaries) != 2 {
		t.Fatalf("StopSummaries len = %d, want 2", len(out.StopSummaries))
	}
	// Newest first.
	if out.StopSummaries[0].Ts != 3000 {
		t.Errorf("StopSummaries[0].Ts = %d, want 3000", out.StopSummaries[0].Ts)
	}
	if out.LatestSummary == nil || out.LatestSummary.Text != "we built X then Y" {
		t.Errorf("LatestSummary missing or wrong: %+v", out.LatestSummary)
	}
	if len(out.Decisions) != 2 {
		t.Errorf("Decisions len = %d, want 2", len(out.Decisions))
	}
	// Files de-duplicated across all stop_summaries — expect a, b, c.
	wantFiles := map[string]bool{"a.go": true, "b.go": true, "c.go": true}
	if len(out.FilesTouched) != 3 {
		t.Errorf("FilesTouched len = %d, want 3 (de-duped union)", len(out.FilesTouched))
	}
	for _, f := range out.FilesTouched {
		if !wantFiles[f] {
			t.Errorf("unexpected file in FilesTouched: %q", f)
		}
	}
	// Markdown must surface each section header.
	for _, want := range []string{"# Session summary", "## Stop-summary timeline", "## Files touched", "## Decisions", "## Latest rolling summary"} {
		if !strings.Contains(out.Markdown, want) {
			t.Errorf("Markdown missing %q\n%s", want, out.Markdown)
		}
	}
}

func TestHandleSummarizeSession_NotFound(t *testing.T) {
	withFakeHome(t)
	_ = withBootstrapDB(t)

	_, out, err := HandleSummarizeSession(context.Background(), nil, SummarizeSessionInput{SessionID: "nope"})
	if err != nil {
		t.Fatalf("HandleSummarizeSession: %v", err)
	}
	if out.Found {
		t.Errorf("Found = true, want false")
	}
	if !strings.Contains(out.Markdown, "not found") {
		t.Errorf("Markdown should mention not-found:\n%s", out.Markdown)
	}
}

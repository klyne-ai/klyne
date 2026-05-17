package mcpserver

import (
	"context"
	"testing"
	"time"
)

func TestUserRecapAggregatesCrossProject(t *testing.T) {
	withFakeHome(t)
	db := withBootstrapDB(t)
	seedStopSummary(t, db, "/p1", "claude", "s1", true, 8, time.Now())
	seedStopSummary(t, db, "/p2", "codex", "s2", true, 7, time.Now())
	seedStopSummary(t, db, "/p3", "claude", "s3", true, 6, time.Now())

	out, err := handleUserRecap(context.Background(), db, UserRecapArgs{SinceDays: 7})
	if err != nil {
		t.Fatal(err)
	}
	if out.TotalEntries != 3 {
		t.Errorf("expected 3 total, got %d", out.TotalEntries)
	}
	if out.ByCLI["claude"] != 2 || out.ByCLI["codex"] != 1 {
		t.Errorf("by-CLI counts wrong: %v", out.ByCLI)
	}
	if len(out.ByProject) != 3 {
		t.Errorf("expected 3 projects, got %d", len(out.ByProject))
	}
	if len(out.TopEntries) == 0 {
		t.Errorf("expected at least one top entry")
	}
}

func TestUserRecapExcludesSuppressed(t *testing.T) {
	withFakeHome(t)
	db := withBootstrapDB(t)
	seedStopSummary(t, db, "/p1", "claude", "s1", true, 8, time.Now())
	seedStopSummary(t, db, "/p1", "claude", "s2", false, 3, time.Now())

	out, err := handleUserRecap(context.Background(), db, UserRecapArgs{SinceDays: 7})
	if err != nil {
		t.Fatal(err)
	}
	if out.TotalEntries != 1 {
		t.Errorf("expected only visible (1), got %d", out.TotalEntries)
	}
}

package handlers

import (
	"context"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/productivity"
	"github.com/klyne-ai/klyne/internal/store"
)

// dayForTs renders the local-zone YYYY-MM-DD that BuildFloorFromStopSummaries
// buckets ts into, so seeded summaries land on the day we hydrate.
func dayForTs(ts int64) string {
	return time.UnixMilli(ts).Local().Format("2006-01-02")
}

// seedStopSummary inserts one visible stop_summaries row carrying a ticket
// reference so BuildFloorFromStopSummaries yields a non-empty floor.
// BuildFloorFromStopSummaries reads the ai_drafted_summary column (not the
// base summary), so the ticket-bearing prose is written there.
func seedStopSummary(t *testing.T, db *store.DB, projectPath, sessionID string, ts int64, summary string) {
	t.Helper()
	if err := store.UpsertStopSummaryWithWorklog(context.Background(), db,
		store.StopSummary{
			SessionID:   sessionID,
			Ts:          ts,
			ProjectPath: projectPath,
			CLI:         "claude",
			Summary:     summary,
			LastUser:    "do the thing",
		},
		store.WorklogColumns{RecapVisible: 1, Importance: 5, DraftState: "accepted", AIDraftedSummary: summary},
	); err != nil {
		t.Fatalf("seed stop summary: %v", err)
	}
}

func TestHydrateWhatWasDone_FloorOnly_NoReflections(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	const proj = "/proj/floor"
	ts := int64(1_716_700_000_000) // arbitrary fixed instant
	day := dayForTs(ts)
	seedStopSummary(t, db, proj, "s1", ts, "CLI-1452 shipped the floor card")

	rep := productivity.Report{Services: []productivity.Service{{ProjectPath: proj, Repo: "floor"}}}
	hydrateWhatWasDone(context.Background(), db, &rep, day)

	wwd := rep.Services[0].WhatWasDone
	if wwd == nil {
		t.Fatal("WhatWasDone = nil, want a floor-derived card (no reflections present)")
	}
	if wwd.LLMCompiled {
		t.Error("LLMCompiled = true, want false for a floor-only card")
	}
}

func TestHydrateWhatWasDone_NoFloor_NoCard(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	const proj = "/proj/empty"
	rep := productivity.Report{Services: []productivity.Service{{ProjectPath: proj, Repo: "empty"}}}
	hydrateWhatWasDone(context.Background(), db, &rep, "2026-05-26")
	if rep.Services[0].WhatWasDone != nil {
		t.Errorf("WhatWasDone = %+v, want nil when no stop_summaries exist", rep.Services[0].WhatWasDone)
	}
}

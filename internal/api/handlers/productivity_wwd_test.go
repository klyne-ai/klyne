package handlers

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/productivity"
	"github.com/klyne-ai/klyne/internal/store"
)

// seedCompiledSnapshot writes a daily_productivity_snapshot row carrying an
// llm_compiled card for (projectPath, serviceKey, dayStr), with UpdatedAt
// pinned to compiledAt (the compile time the freshness check compares
// against). ticketText is placed in a Tier2 detail so the card "covers"
// that ticket — letting a stale test assert the badge drops even when the
// floor adds no NEW ticket.
func seedCompiledSnapshot(t *testing.T, db *store.DB, projectPath, serviceKey, dayStr string, compiledAt int64, ticketText string) {
	t.Helper()
	card := productivity.WhatWasDoneCard{Service: serviceKey, LLMCompiled: true}
	card.Tier1.TLDR = "compiled summary"
	card.Tier2.Details = []productivity.WWDDetail{{Kind: "SHIPPED", Text: ticketText}}
	rep := productivity.Report{
		Day:      dayStr,
		Services: []productivity.Service{{Repo: serviceKey, ProjectPath: projectPath, WhatWasDone: &card}},
	}
	payload, err := json.Marshal(rep)
	if err != nil {
		t.Fatalf("marshal compiled snapshot: %v", err)
	}
	if err := store.UpsertDailyProductivitySnapshot(context.Background(), db, store.DailyProductivitySnapshot{
		ProjectPath: projectPath,
		Day:         dayStr,
		PayloadJSON: string(payload),
		Source:      "reflection",
		CreatedAt:   compiledAt,
		UpdatedAt:   compiledAt,
	}); err != nil {
		t.Fatalf("seed compiled snapshot: %v", err)
	}
}

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
	seedStopSummaryAs(t, db, projectPath, sessionID, ts, summary, "do the thing")
}

// seedStopSummaryAs is seedStopSummary with an explicit last_user prompt, so
// tests can simulate a klyne tool-run turn (e.g. last_user
// "/klyne:productivity-sync …") that the floor must exclude.
func seedStopSummaryAs(t *testing.T, db *store.DB, projectPath, sessionID string, ts int64, summary, lastUser string) {
	t.Helper()
	if err := store.UpsertStopSummaryWithWorklog(context.Background(), db,
		store.StopSummary{
			SessionID:   sessionID,
			Ts:          ts,
			ProjectPath: projectPath,
			CLI:         "claude",
			Summary:     summary,
			LastUser:    lastUser,
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

func TestHydrateWhatWasDone_FreshCompiledCardKept(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	const proj = "/proj/fresh"
	ts := int64(1_716_700_000_000)
	day := dayForTs(ts)
	seedStopSummary(t, db, proj, "s1", ts, "CLI-1452 shipped")
	// Compiled AFTER the only summary → fresh.
	seedCompiledSnapshot(t, db, proj, "fresh", day, ts+1000, "CLI-1452 shipped")

	rep := productivity.Report{Services: []productivity.Service{{ProjectPath: proj, Repo: "fresh"}}}
	hydrateWhatWasDone(context.Background(), db, &rep, day)

	wwd := rep.Services[0].WhatWasDone
	if wwd == nil || !wwd.LLMCompiled {
		t.Fatalf("WhatWasDone = %+v, want a fresh card kept with llm_compiled=true", wwd)
	}
}

func TestHydrateWhatWasDone_StaleCompiledCardReoffered(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	const proj = "/proj/stale"
	ts := int64(1_716_700_000_000)
	day := dayForTs(ts)
	seedStopSummary(t, db, proj, "s1", ts, "CLI-1452 shipped")
	// Compiled at ts+1000...
	seedCompiledSnapshot(t, db, proj, "stale", day, ts+1000, "CLI-1452 shipped")
	// ...then a NEWER summary lands on the same ticket (no new floor ticket,
	// so only the ts-based staleness check can flip the badge).
	seedStopSummary(t, db, proj, "s2", ts+5000, "CLI-1452 follow-up work")

	rep := productivity.Report{Services: []productivity.Service{{ProjectPath: proj, Repo: "stale"}}}
	hydrateWhatWasDone(context.Background(), db, &rep, day)

	wwd := rep.Services[0].WhatWasDone
	if wwd == nil {
		t.Fatal("WhatWasDone = nil, want the stale card kept (marked pending)")
	}
	if wwd.LLMCompiled {
		t.Error("LLMCompiled = true, want false — a stale card must be re-offered for compile")
	}
	if countPendingCompile(rep.Services) != 1 {
		t.Errorf("countPendingCompile = %d, want 1 (stale day is pending)", countPendingCompile(rep.Services))
	}
}

func TestLatestFloorTurnTs_ExcludesToolOnlyAndEmptyDay(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	const proj = "/proj/ts"
	ts := int64(1_716_700_000_000)
	day := dayForTs(ts)
	seedStopSummary(t, db, proj, "s1", ts, "CLI-1 real work")
	// A LATER tool-only turn (prose-detected) must NOT advance latest.
	seedStopSummary(t, db, proj, "s2", ts+9000, "Ran /klyne:productivity-sync compile")
	// A LATER klyne tool-run turn detected by last_user (prose looks like
	// real work) must ALSO be excluded — the load-bearing fix against the
	// compile's own session summary restaling the day.
	seedStopSummaryAs(t, db, proj, "s3", ts+12000,
		"Wrote 2 narrative cards covering CLI-9 and CLI-8",
		"/klyne:productivity-sync project_path="+proj+" day="+day)

	got, err := productivity.LatestFloorTurnTs(context.Background(), db.Read(), proj, day)
	if err != nil {
		t.Fatalf("LatestFloorTurnTs: %v", err)
	}
	if got != ts {
		t.Errorf("latest = %d, want %d (newest FLOOR-ADMITTED turn; tool-only + klyne tool-run excluded)", got, ts)
	}
	empty, err := productivity.LatestFloorTurnTs(context.Background(), db.Read(), proj, "2000-01-01")
	if err != nil {
		t.Fatalf("LatestFloorTurnTs empty: %v", err)
	}
	if empty != 0 {
		t.Errorf("latest for empty day = %d, want 0", empty)
	}
}

func TestHydrateWhatWasDone_CompileRunSummaryDoesNotRestale(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	const proj = "/proj/loop"
	ts := int64(1_716_700_000_000)
	day := dayForTs(ts)
	seedStopSummary(t, db, proj, "s1", ts, "CLI-1452 shipped")
	seedCompiledSnapshot(t, db, proj, "loop", day, ts+1000, "CLI-1452 shipped")
	// The headless /klyne:productivity-sync compile ends its OWN session,
	// writing a stop_summary (ts > compiledAt) with ticket-bearing prose.
	// Keyed off last_user it must be excluded from the floor, else the
	// just-compiled day would look perpetually stale (recompile loop).
	seedStopSummaryAs(t, db, proj, "compile-run", ts+5000,
		"Wrote 3 narrative cards for loop covering CLI-1452, CLI-1340",
		"/klyne:productivity-sync project_path=/proj/loop day="+day)

	rep := productivity.Report{Services: []productivity.Service{{ProjectPath: proj, Repo: "loop"}}}
	hydrateWhatWasDone(context.Background(), db, &rep, day)

	wwd := rep.Services[0].WhatWasDone
	if wwd == nil || !wwd.LLMCompiled {
		t.Fatalf("WhatWasDone = %+v, want fresh (kept compiled) — the compile's own session summary must not restale the day", wwd)
	}
}

func TestHydrateWhatWasDone_MatchesCompiledCardByRepoBasename(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	const proj = "/work/klyne"
	day := "2026-05-26"
	// Snapshot card persisted under an org-qualified repo key; the live
	// service resolves to the bare repo name. Hydrate must still match it
	// via basename (else dashboard shows pending while discovery says fresh
	// → dead Compile button).
	seedCompiledSnapshot(t, db, proj, "klyne-ai/klyne", day, 1_716_600_000_000, "CLI-1452 shipped")

	rep := productivity.Report{Services: []productivity.Service{{ProjectPath: proj, Repo: "klyne"}}}
	hydrateWhatWasDone(context.Background(), db, &rep, day)

	wwd := rep.Services[0].WhatWasDone
	if wwd == nil || !wwd.LLMCompiled {
		t.Fatalf("WhatWasDone = %+v, want the org-qualified compiled card matched by basename", wwd)
	}
}

func TestHydrateWhatWasDone_CompiledAtBoundaryIsFresh(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	const proj = "/proj/boundary"
	ts := int64(1_716_700_000_000)
	day := dayForTs(ts)
	seedStopSummary(t, db, proj, "s1", ts, "CLI-1452 shipped")
	// Compiled at EXACTLY the summary ts → latest == compiledAt → fresh
	// (boundary is `<=`, so the card stays compiled, not re-offered).
	seedCompiledSnapshot(t, db, proj, "boundary", day, ts, "CLI-1452 shipped")

	rep := productivity.Report{Services: []productivity.Service{{ProjectPath: proj, Repo: "boundary"}}}
	hydrateWhatWasDone(context.Background(), db, &rep, day)

	wwd := rep.Services[0].WhatWasDone
	if wwd == nil || !wwd.LLMCompiled {
		t.Fatalf("WhatWasDone = %+v, want fresh (kept compiled) at the latest==compiledAt boundary", wwd)
	}
}

package productivity

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// openFloorTestDB creates a minimal stop_summaries table the floor
// extractor reads from. Keeps the test independent of the full
// store.DB initialization (migrations, indexes, WAL setup) — the
// extractor only needs a few columns by name.
func openFloorTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "floor.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`
CREATE TABLE stop_summaries (
  session_id TEXT NOT NULL,
  ts INTEGER NOT NULL,
  project_path TEXT NOT NULL,
  cli TEXT NOT NULL DEFAULT '',
  summary TEXT NOT NULL DEFAULT '',
  last_user TEXT NOT NULL DEFAULT '',
  last_bash TEXT NOT NULL DEFAULT '',
  files_json TEXT NOT NULL DEFAULT '[]',
  recap_visible INTEGER NOT NULL DEFAULT 1,
  ai_drafted_summary TEXT,
  importance INTEGER NOT NULL DEFAULT 5,
  PRIMARY KEY (session_id, ts)
);`); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

type floorRowFixture struct {
	sessionID string
	ts        time.Time
	visible   int
	summary   string
	bash      string
}

func seedFloorRows(t *testing.T, db *sql.DB, project string, rows []floorRowFixture) {
	t.Helper()
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO stop_summaries
            (session_id, ts, project_path, last_bash, recap_visible, ai_drafted_summary)
          VALUES (?, ?, ?, ?, ?, ?)`,
			r.sessionID, r.ts.UnixMilli(), project, r.bash, r.visible, r.summary,
		); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

// TestBuildFloor_OperationsApp2026_05_26 reproduces the actual data
// shape from the dogfood bug: 5 distinct CLI tickets touched in a day,
// many turns marked recap_visible=0 by over-aggressive suppression.
// The floor MUST surface one detail per ticket regardless of visibility.
func TestBuildFloor_OperationsApp2026_05_26(t *testing.T) {
	db := openFloorTestDB(t)
	at := func(h, m int) time.Time { return time.Date(2026, 5, 26, h, m, 0, 0, time.Local) }

	seedFloorRows(t, db, "/p/ops", []floorRowFixture{
		// Visible CLI-1473 work — already passes suppression.
		{"sess-1473", at(10, 47), 1, "Investigated medicine-order Payment dead-end and traced to commit 461fb231. Branched feature/CLI-1473-medicine-orders-opd-fully-covered-payment and edited PaymentStep.tsx.", ""},
		{"sess-1473", at(11, 26), 1, "Committed CLI-1473 OPD-fully-covered fix and pushed to origin. PR not opened yet.", "git push"},
		// SUPPRESSED CLI-1340 implementation — recap_visible=0 but substantial.
		{"sess-1340", at(11, 44), 0, "Implemented CLI-1340 fix on operations-app in worktree. PR #428 opened on clinikk/operations-app.", ""},
		// SUPPRESSED CLI-1340 follow-up commit + push.
		{"sess-1340b", at(15, 31), 0, "Committed the poll interval change (ORDER_CANCELLATION_POLL_MS 15s→10s) as e88e57a0 and pushed to origin/feature/CLI-1340-prevent-pcc-edits-on-cancelled-bill", "git push"},
		// Visible CLI-1452 rebase.
		{"sess-1452", at(16, 35), 1, "Rebased feature/CLI-1452-followup-cta-banner onto origin/main. Opened PR #432 as follow-up to merged PR #421.", ""},
		// Suppressed investigation with no ticket — should still surface via session bucket.
		{"sess-investig", at(13, 18), 0, "Confirmed the dropdown's empty state stems from a data-shape mismatch: subscription has customer 4252296 only in top-level members[].", ""},
		// Tool-only turn — MUST be excluded.
		{"sess-meta", at(15, 32), 0, "Ran /klyne:reflect on operations-app — bucketed 13 pending worklog entries.", ""},
	})

	details, err := BuildFloorFromStopSummaries(context.Background(), db, "/p/ops", "2026-05-26")
	if err != nil {
		t.Fatalf("build floor: %v", err)
	}

	tickets := map[string]L1FloorDetail{}
	var sessionsBucketed []L1FloorDetail
	for _, d := range details {
		if d.TicketID != "" {
			tickets[d.TicketID] = d
		} else {
			sessionsBucketed = append(sessionsBucketed, d)
		}
	}

	for _, want := range []string{"CLI-1473", "CLI-1340", "CLI-1452"} {
		if _, ok := tickets[want]; !ok {
			t.Errorf("floor missing detail for ticket %s; got tickets=%v", want, mapKeys(tickets))
		}
	}
	// Tool-only summary must not appear.
	for _, d := range details {
		if strings.Contains(d.Text, "/klyne:reflect") {
			t.Errorf("tool-only turn leaked into floor: %+v", d)
		}
	}
	// Substantial investigation summary should surface as a session bucket.
	if len(sessionsBucketed) == 0 {
		t.Errorf("substantial investigation summary should produce a non-ticket bucket; got 0")
	}
	// PR #432 evidence is attached to CLI-1452.
	if cli1452, ok := tickets["CLI-1452"]; ok {
		joined := strings.Join(cli1452.Evidence, " ")
		if !strings.Contains(joined, "PR #432") {
			t.Errorf("CLI-1452 floor detail missing PR #432 evidence; got %v", cli1452.Evidence)
		}
	}
	// commit e88e57a0 evidence is attached to CLI-1340.
	if cli1340, ok := tickets["CLI-1340"]; ok {
		joined := strings.Join(cli1340.Evidence, " ")
		if !strings.Contains(joined, "e88e57a0") && !strings.Contains(joined, "PR #428") {
			t.Errorf("CLI-1340 floor detail missing commit/PR evidence; got %v", cli1340.Evidence)
		}
	}
}

func TestIsToolOnlySummary(t *testing.T) {
	toolOnly := []string{
		"Ran /klyne:reflect — found 1 pending worklog entry for operations-app",
		"Ran productivity-sync for trackIt on 2026-05-26",
		"Ran the productivity-sync second pass for operations-app",
		"Compiled 1 productivity card for oms-service on 2026-05-26",
		"Synthesized 1 typed reflection for operations-app on 2026-05-26",
		"Synthesized one typed reflection for 2026-05-26 klyne",
		"Synthesized 9 afternoon worklog entries for 2026-05-26",
		"Called propose_reflection for klyne project (39 pending entries)",
		"Generated strict-JSON productivity dashboard output for 2026-05-26",
		"Generated a service-anchored compact JSON panel for the klyne productivity dashboard",
		"Audited stop_summaries vs productivity dashboard for 2026-05-26",
	}
	realWork := []string{
		"Implemented CLI-1340 fix on operations-app in worktree",
		"Shipped CLI-1473 OPD payment fix and pushed PR #429",
		"Investigated medicine-order Payment step dead-end and traced to commit 461fb231",
		"Fixed dropdown empty-state in subscription-service members lookup",
	}
	for _, s := range toolOnly {
		if !isToolOnlySummary(s) {
			t.Errorf("expected isToolOnlySummary=true for: %q", s)
		}
	}
	for _, s := range realWork {
		if isToolOnlySummary(s) {
			t.Errorf("real work flagged as tool-only: %q", s)
		}
	}
}

func TestBuildFloor_KindClassification(t *testing.T) {
	cases := []struct {
		summary string
		want    string
	}{
		{"Shipped CLI-1450 helper and opened PR #500", "SHIPPED"},
		{"Fixed regression in CLI-1450 hydrateWhatWasDone path", "FIXED"},
		{"Decided to adopt Opus 4.7 for reflect synthesis on CLI-1450", "DECISION"},
		{"Investigated CLI-1450 dropdown empty state and traced to subscription-service", "INVESTIGATED"},
		{"In progress: drafting CLI-1450 retry logic", "IN_PROGRESS"},
		{"CLI-1450 — no verb match here", "MAJOR"},
	}
	for _, c := range cases {
		got := classifyFloorKind(c.summary)
		if got != c.want {
			t.Errorf("classifyFloorKind(%q) = %s, want %s", c.summary, got, c.want)
		}
	}
}

func TestMergeFloorIntoCard_AddsMissingTickets(t *testing.T) {
	llm := &WhatWasDoneCard{
		Service:     "operations-app",
		LLMCompiled: true,
		Tier1:       WWDTier1{TLDR: "1 major · latest: Rebased CLI-1452…"},
		Tier2: struct {
			Details []WWDDetail `json:"details"`
		}{
			Details: []WWDDetail{
				{Kind: "MAJOR", When: "16:35", Text: "Rebased CLI-1452 and opened PR #432.", Evidence: []string{"CLI-1452", "PR #432"}, SessionID: "sess-rebase"},
			},
		},
	}
	floor := []L1FloorDetail{
		{TicketID: "CLI-1473", Kind: "SHIPPED", When: "11:26", Text: "Shipped CLI-1473 OPD-fully-covered fix.", Evidence: []string{"CLI-1473"}, SessionID: "sess-1473"},
		{TicketID: "CLI-1340", Kind: "SHIPPED", When: "12:17", Text: "Opened PR #428 for CLI-1340.", Evidence: []string{"CLI-1340", "PR #428"}, SessionID: "sess-1340"},
		// Duplicate ticket — should be skipped because llm card already covers CLI-1452.
		{TicketID: "CLI-1452", Kind: "MAJOR", When: "16:35", Text: "Other CLI-1452 work.", Evidence: []string{"CLI-1452"}, SessionID: "sess-x"},
	}
	merged := MergeFloorIntoCard("operations-app", llm, floor)
	if merged == nil {
		t.Fatal("merge returned nil")
	}
	if len(merged.Tier2.Details) != 3 {
		t.Errorf("expected 3 details after merge (LLM 1 + 2 missing tickets), got %d: %+v", len(merged.Tier2.Details), merged.Tier2.Details)
	}
	seen := map[string]bool{}
	for _, d := range merged.Tier2.Details {
		for _, ev := range d.Evidence {
			if strings.HasPrefix(ev, "CLI-") {
				seen[ev] = true
			}
		}
	}
	for _, want := range []string{"CLI-1473", "CLI-1340", "CLI-1452"} {
		if !seen[want] {
			t.Errorf("merged card missing ticket %s; got %v", want, seen)
		}
	}
	// Merged card must no longer claim llm_compiled — partial determinism.
	if merged.LLMCompiled {
		t.Errorf("merged card should not be llm_compiled when floor added details")
	}
}

func TestMergeFloorIntoCard_PreservesLLMTLDRWhenFullyCovered(t *testing.T) {
	llm := &WhatWasDoneCard{
		Service:     "klyne",
		LLMCompiled: true,
		Tier1:       WWDTier1{TLDR: "3 shipped · 1 fixed — full coverage."},
		Tier2: struct {
			Details []WWDDetail `json:"details"`
		}{
			Details: []WWDDetail{
				{Kind: "SHIPPED", When: "16:34", Text: "Shipped CLI-1900.", Evidence: []string{"CLI-1900"}, SessionID: "sess-1"},
			},
		},
	}
	floor := []L1FloorDetail{
		{TicketID: "CLI-1900", Kind: "SHIPPED", When: "16:30", Text: "duplicate of llm", Evidence: []string{"CLI-1900"}, SessionID: "sess-x"},
	}
	merged := MergeFloorIntoCard("klyne", llm, floor)
	if merged == nil {
		t.Fatal("merge nil")
	}
	if !merged.LLMCompiled {
		t.Errorf("expected LLMCompiled=true when no floor was added")
	}
	if merged.Tier1.TLDR != llm.Tier1.TLDR {
		t.Errorf("expected LLM TLDR preserved, got %q", merged.Tier1.TLDR)
	}
}

func TestMergeFloorIntoCard_StartsFromNil(t *testing.T) {
	floor := []L1FloorDetail{
		{TicketID: "CLI-1234", Kind: "SHIPPED", When: "10:30", Text: "Shipped CLI-1234.", Evidence: []string{"CLI-1234"}, SessionID: "s1"},
	}
	merged := MergeFloorIntoCard("svc", nil, floor)
	if merged == nil || len(merged.Tier2.Details) != 1 {
		t.Fatalf("expected 1-detail card from nil LLM, got %+v", merged)
	}
	if merged.LLMCompiled {
		t.Errorf("nil-LLM start cannot be LLMCompiled")
	}
}

func mapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

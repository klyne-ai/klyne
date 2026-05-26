package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/klyne-ai/klyne/internal/productivity"
	"github.com/klyne-ai/klyne/internal/store"
)

// TestHandleRecordProductivityCard_Persists exercises the happy path:
// a well-formed LLM payload lands a snapshot row with llm_compiled=true,
// the mechanical Tier 1 fields are derived from the details, and the
// LLM-authored tier1_tldr is preserved verbatim.
func TestHandleRecordProductivityCard_Persists(t *testing.T) {
	withFakeHome(t)
	db := withBootstrapDB(t)

	in := RecordProductivityCardInput{
		ProjectPath: "/p",
		Day:         "2026-05-26",
		Service:     "klyne",
		Tier1Tldr:   "Wired the klyne-hook stop summaries end-to-end and stripped the legacy bullet path",
		Details: []store.WWDDetail{
			{Kind: "SHIPPED", When: "16:49", Text: "Wrote ~/.codex/hooks.json", Evidence: []string{"be8cc8c8", "~/.codex/hooks.json"}, SessionID: "s1"},
			{Kind: "FIXED", When: "17:10", Text: "Applied dot--live across 3 svelte files", Evidence: []string{"620a9551"}, SessionID: "s2"},
		},
	}
	out, err := handleRecordProductivityCard(context.Background(), db, in)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if out.DetailCount != 2 {
		t.Errorf("DetailCount = %d, want 2", out.DetailCount)
	}
	if out.ShaCount != 2 {
		t.Errorf("ShaCount = %d, want 2", out.ShaCount)
	}

	snap, found, err := store.GetDailyProductivitySnapshot(context.Background(), db, "/p", "2026-05-26")
	if err != nil {
		t.Fatalf("get snapshot: %v", err)
	}
	if !found {
		t.Fatal("snapshot not persisted")
	}
	var rep productivity.Report
	if err := json.Unmarshal([]byte(snap.PayloadJSON), &rep); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if len(rep.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(rep.Services))
	}
	svc := rep.Services[0]
	if svc.WhatWasDone == nil {
		t.Fatal("WhatWasDone card not attached")
	}
	if !svc.WhatWasDone.LLMCompiled {
		t.Errorf("LLMCompiled = false, want true")
	}
	if svc.WhatWasDone.Tier1.TLDR != in.Tier1Tldr {
		t.Errorf("TLDR = %q, want %q", svc.WhatWasDone.Tier1.TLDR, in.Tier1Tldr)
	}
	// Mechanical Tier 1 fields must be Go-derived from the details.
	if svc.WhatWasDone.Tier1.CommitCount != 2 {
		t.Errorf("CommitCount = %d, want 2", svc.WhatWasDone.Tier1.CommitCount)
	}
	if svc.WhatWasDone.Tier1.TurnCount != 2 {
		t.Errorf("TurnCount = %d, want 2", svc.WhatWasDone.Tier1.TurnCount)
	}
	if svc.WhatWasDone.Tier1.PillCounts["shipped"] != 1 {
		t.Errorf("PillCounts[shipped] = %d, want 1", svc.WhatWasDone.Tier1.PillCounts["shipped"])
	}
}

// TestHandleRecordProductivityCard_RejectsLongTldr enforces the
// ≤120-char Tier-1 prose cap.
func TestHandleRecordProductivityCard_RejectsLongTldr(t *testing.T) {
	withFakeHome(t)
	db := withBootstrapDB(t)

	long := strings.Repeat("x", tldrMaxLen+1)
	in := RecordProductivityCardInput{
		ProjectPath: "/p",
		Day:         "2026-05-26",
		Service:     "klyne",
		Tier1Tldr:   long,
		Details: []store.WWDDetail{
			{Kind: "SHIPPED", When: "10:00", Text: "ok", Evidence: []string{"e1"}},
		},
	}
	_, err := handleRecordProductivityCard(context.Background(), db, in)
	if err == nil {
		t.Fatalf("expected error on over-cap tldr")
	}
	if !strings.Contains(err.Error(), "120") {
		t.Errorf("expected error mentioning the cap (120), got %v", err)
	}
}

// TestHandleRecordProductivityCard_RejectsEmptyEvidence is the §5
// citation-invariant boundary.
func TestHandleRecordProductivityCard_RejectsEmptyEvidence(t *testing.T) {
	withFakeHome(t)
	db := withBootstrapDB(t)

	in := RecordProductivityCardInput{
		ProjectPath: "/p",
		Day:         "2026-05-26",
		Service:     "klyne",
		Tier1Tldr:   "Did stuff",
		Details: []store.WWDDetail{
			{Kind: "SHIPPED", When: "10:00", Text: "vague", Evidence: nil},
		},
	}
	_, err := handleRecordProductivityCard(context.Background(), db, in)
	if err == nil {
		t.Fatalf("expected error on empty evidence")
	}
	if !strings.Contains(err.Error(), "evidence empty") {
		t.Errorf("expected diagnostic mentioning 'evidence empty', got %v", err)
	}
}

// TestHandleRecordProductivityCard_RejectsUnbackedPRRef mirrors the
// record_reflection PR-#<n> guard.
func TestHandleRecordProductivityCard_RejectsUnbackedPRRef(t *testing.T) {
	withFakeHome(t)
	db := withBootstrapDB(t)

	in := RecordProductivityCardInput{
		ProjectPath: "/p",
		Day:         "2026-05-26",
		Service:     "klyne",
		Tier1Tldr:   "Did stuff",
		Details: []store.WWDDetail{
			{Kind: "SHIPPED", When: "10:00", Text: "Shipped via PR #57", Evidence: []string{"be8cc8c8"}},
		},
	}
	_, err := handleRecordProductivityCard(context.Background(), db, in)
	if err == nil {
		t.Fatalf("expected error on unbacked PR ref")
	}
	if !strings.Contains(err.Error(), "PR #57") {
		t.Errorf("expected diagnostic naming PR #57, got %v", err)
	}
}

// TestHandleRecordProductivityCard_RequiresFields runs the required-
// field gauntlet so a malformed call from a future caller fails fast.
func TestHandleRecordProductivityCard_RequiresFields(t *testing.T) {
	withFakeHome(t)
	db := withBootstrapDB(t)

	cases := []struct {
		name string
		in   RecordProductivityCardInput
		want string
	}{
		{
			name: "missing project_path",
			in: RecordProductivityCardInput{
				Day: "2026-05-26", Service: "klyne", Tier1Tldr: "ok",
				Details: []store.WWDDetail{{Kind: "SHIPPED", When: "10:00", Text: "x", Evidence: []string{"e"}}},
			},
			want: "project_path",
		},
		{
			name: "missing day",
			in: RecordProductivityCardInput{
				ProjectPath: "/p", Service: "klyne", Tier1Tldr: "ok",
				Details: []store.WWDDetail{{Kind: "SHIPPED", When: "10:00", Text: "x", Evidence: []string{"e"}}},
			},
			want: "day",
		},
		{
			name: "missing service",
			in: RecordProductivityCardInput{
				ProjectPath: "/p", Day: "2026-05-26", Tier1Tldr: "ok",
				Details: []store.WWDDetail{{Kind: "SHIPPED", When: "10:00", Text: "x", Evidence: []string{"e"}}},
			},
			want: "service",
		},
		{
			name: "missing tier1_tldr",
			in: RecordProductivityCardInput{
				ProjectPath: "/p", Day: "2026-05-26", Service: "klyne",
				Details: []store.WWDDetail{{Kind: "SHIPPED", When: "10:00", Text: "x", Evidence: []string{"e"}}},
			},
			want: "tier1_tldr",
		},
		{
			name: "no details",
			in: RecordProductivityCardInput{
				ProjectPath: "/p", Day: "2026-05-26", Service: "klyne", Tier1Tldr: "ok",
			},
			want: "detail",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := handleRecordProductivityCard(context.Background(), db, tc.in)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(strings.ToLower(err.Error()), tc.want) {
				t.Errorf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

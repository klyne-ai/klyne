package insights_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/insights"
	"github.com/klyne-ai/klyne/internal/store"
)

// openTestDB opens a fresh on-disk SQLite DB in a tempdir.
func openTestDB(t *testing.T) *store.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func newSession(id, project, model string) *connectors.Session {
	return &connectors.Session{
		ID:          id,
		CLI:         connectors.CLIClaude,
		ProjectPath: project,
		StartedAt:   1_700_000_000_000,
		LastMsgAt:   1_700_000_000_000,
		Model:       model,
		Status:      connectors.SessionStatusActive,
		RawPath:     "/tmp/" + id + ".jsonl",
	}
}

func newMessage(id, sessionID string, ts int64, calls []connectors.ToolCall, results []connectors.ToolResult, tokIn, tokOut, cachedRead int64, cost float64) *connectors.Message {
	return &connectors.Message{
		ID:                id,
		SessionID:         sessionID,
		CLI:               connectors.CLIClaude,
		Role:              connectors.RoleAssistant,
		Content:           "",
		ToolCalls:         calls,
		ToolResults:       results,
		TokensIn:          tokIn,
		TokensOut:         tokOut,
		CachedReadTokens:  cachedRead,
		CachedWriteTokens: 0,
		CostUSD:           cost,
		Model:             "claude-sonnet-4-5",
		Ts:                ts,
	}
}

// TestCollectStats_FromRealDB exercises the full read path with real
// SQLite rows: one session, three messages with mixed tool calls.
func TestCollectStats_FromRealDB(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	s := newSession("sess-A", "/proj/a", "claude-sonnet-4-5")
	if err := store.UpsertSession(ctx, db, s); err != nil {
		t.Fatalf("UpsertSession: %v", err)
	}

	// Three messages: Bash, Bash, Read — longest run for Bash = 2.
	msgs := []*connectors.Message{
		newMessage("m1", "sess-A", 1_700_000_000_001,
			[]connectors.ToolCall{{ID: "c1", Name: "Bash", Input: "{}"}},
			nil, 1000, 200, 500, 0.10),
		newMessage("m2", "sess-A", 1_700_000_000_002,
			[]connectors.ToolCall{{ID: "c2", Name: "Bash", Input: "{}"}},
			[]connectors.ToolResult{{ID: "c2", Output: "boom", IsError: true}},
			500, 100, 300, 0.05),
		newMessage("m3", "sess-A", 1_700_000_000_003,
			[]connectors.ToolCall{{ID: "c3", Name: "Read", Input: "{}"}},
			nil, 200, 50, 100, 0.02),
	}
	for _, m := range msgs {
		if err := store.InsertMessage(ctx, db, m); err != nil {
			t.Fatalf("InsertMessage %q: %v", m.ID, err)
		}
	}

	stats, err := insights.CollectStats(ctx, db, insights.Filter{})
	if err != nil {
		t.Fatalf("CollectStats: %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("expected 1 session, got %d", len(stats))
	}
	got := stats[0]

	if got.ID != "sess-A" {
		t.Errorf("ID = %q; want sess-A", got.ID)
	}
	if got.ToolCounts["Bash"] != 2 || got.ToolCounts["Read"] != 1 {
		t.Errorf("ToolCounts = %v; want Bash=2 Read=1", got.ToolCounts)
	}
	if got.ToolErrors["Bash"] != 1 {
		t.Errorf("ToolErrors[Bash] = %d; want 1", got.ToolErrors["Bash"])
	}
	if got.TotalToolCalls != 3 {
		t.Errorf("TotalToolCalls = %d; want 3", got.TotalToolCalls)
	}
	if got.LongestRun != 2 || got.LongestRunTool != "Bash" {
		t.Errorf("LongestRun = %d/%s; want 2/Bash", got.LongestRun, got.LongestRunTool)
	}
	// Cumulative cost from the session table — store layer aggregates these
	// via the message inserts.
	wantCost := 0.10 + 0.05 + 0.02
	if got.CostUSD < wantCost-1e-9 || got.CostUSD > wantCost+1e-9 {
		t.Errorf("CostUSD = %v; want ~%v", got.CostUSD, wantCost)
	}
}

// TestAggregateAndPatterns_RealDB feeds a small but realistic workload
// through CollectStats + AggregateTools + DetectPatterns to make sure the
// surfaces compose.
func TestAggregateAndPatterns_RealDB(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	for _, sid := range []string{"sA", "sB"} {
		if err := store.UpsertSession(ctx, db, newSession(sid, "/proj/x", "claude-sonnet-4-5")); err != nil {
			t.Fatalf("UpsertSession %q: %v", sid, err)
		}
	}
	// sA: a tight loop of Bash (6 consecutive).
	ts := int64(1_700_000_000_000)
	for i := 0; i < 6; i++ {
		ts++
		if err := store.InsertMessage(ctx, db, newMessage(
			"sA-m"+string(rune('0'+i)), "sA", ts,
			[]connectors.ToolCall{{ID: "c", Name: "Bash"}},
			nil, 200_000, 100, 10_000, 0.50,
		)); err != nil {
			t.Fatalf("InsertMessage: %v", err)
		}
	}
	// sB: Read-heavy, big input but well-cached.
	for i := 0; i < 5; i++ {
		ts++
		if err := store.InsertMessage(ctx, db, newMessage(
			"sB-m"+string(rune('0'+i)), "sB", ts,
			[]connectors.ToolCall{{ID: "c", Name: "Read"}},
			nil, 50_000, 100, 40_000, 0.05,
		)); err != nil {
			t.Fatalf("InsertMessage: %v", err)
		}
	}

	stats, err := insights.CollectStats(ctx, db, insights.Filter{})
	if err != nil {
		t.Fatalf("CollectStats: %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(stats))
	}

	tools := insights.AggregateTools(stats)
	if len(tools) == 0 {
		t.Fatal("expected tools, got none")
	}
	if tools[0].Name != "Bash" || tools[0].Count != 6 {
		t.Errorf("top tool = %+v; want Bash=6", tools[0])
	}

	patterns := insights.DetectPatterns(stats, insights.DefaultThresholds())
	gotKinds := map[string]bool{}
	for _, p := range patterns {
		if p.SessionID == "sA" {
			gotKinds[p.Kind] = true
		}
	}
	if !gotKinds[insights.KindTightLoop] {
		t.Errorf("expected tight_loop on sA, got patterns: %+v", patterns)
	}
}

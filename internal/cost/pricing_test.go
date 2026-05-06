package cost

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/mohitpatell/agentdeck/internal/config"
	_ "modernc.org/sqlite" // register "sqlite" driver for rollup tests
)

// knownModels lists models expected to be in the embedded pricing.json.
var knownModels = []string{
	"claude-sonnet-4-5",
	"claude-haiku-4",
	"claude-opus-4-6",
	"gpt-5",
	"gpt-5-mini",
	"gpt-5-nano",
	"gemini-2.5-flash",
	"gemini-2.5-flash-lite",
	"text-embedding-3-small",
	"llama3.1:8b",
}

func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	cfg := config.Defaults()
	// Point the pricing override at a nonexistent path so only embedded JSON is used.
	cfg.Paths.PricingOverride = filepath.Join(t.TempDir(), "nonexistent.json")
	e, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	return e
}

// ---------------------------------------------------------------------------
// TestLookup_KnownModel — all required models are present with non-zero rates
// (except llama3.1:8b which is intentionally free).
// ---------------------------------------------------------------------------

func TestLookup_KnownModel(t *testing.T) {
	e := newTestEngine(t)
	for _, model := range knownModels {
		rates, ok := e.Lookup(model)
		if !ok {
			t.Errorf("Lookup(%q): expected ok=true, got false", model)
			continue
		}
		// llama3.1:8b is free; all others must have non-zero prompt rate.
		if model != "llama3.1:8b" && rates.PromptPerMtok == 0 {
			t.Errorf("Lookup(%q): expected non-zero PromptPerMtok, got 0", model)
		}
	}
}

// ---------------------------------------------------------------------------
// TestLookup_UnknownModel — returns false for models not in the table.
// ---------------------------------------------------------------------------

func TestLookup_UnknownModel(t *testing.T) {
	e := newTestEngine(t)
	_, ok := e.Lookup("not-a-real-model-xyz")
	if ok {
		t.Error("Lookup(unknown): expected ok=false, got true")
	}
}

// ---------------------------------------------------------------------------
// TestCost_KnownModel — table-driven; asserts correct USD output.
// Formula: (tokensIn/1e6 * promptRate) + (tokensOut/1e6 * completionRate)
// ---------------------------------------------------------------------------

func TestCost_KnownModel(t *testing.T) {
	e := newTestEngine(t)

	tests := []struct {
		model     string
		tokensIn  int64
		tokensOut int64
		wantUSD   float64
	}{
		{
			// 1 000 000 input tokens @ $3.00/mtok = $3.00
			// 500 000 output tokens @ $15.00/mtok = $7.50
			model:     "claude-sonnet-4-5",
			tokensIn:  1_000_000,
			tokensOut: 500_000,
			wantUSD:   3.00 + 7.50,
		},
		{
			// 200 000 input @ $0.80/mtok = $0.16
			// 100 000 output @ $4.00/mtok = $0.40
			model:     "claude-haiku-4",
			tokensIn:  200_000,
			tokensOut: 100_000,
			wantUSD:   0.16 + 0.40,
		},
		{
			// 500 000 input @ $10.00/mtok = $5.00
			// 250 000 output @ $30.00/mtok = $7.50
			model:     "gpt-5",
			tokensIn:  500_000,
			tokensOut: 250_000,
			wantUSD:   5.00 + 7.50,
		},
		{
			// 1 000 000 input @ $0.075/mtok = $0.075
			// 1 000 000 output @ $0.30/mtok = $0.30
			model:     "gemini-2.5-flash",
			tokensIn:  1_000_000,
			tokensOut: 1_000_000,
			wantUSD:   0.075 + 0.30,
		},
		{
			// llama3.1:8b is free
			model:     "llama3.1:8b",
			tokensIn:  1_000_000,
			tokensOut: 1_000_000,
			wantUSD:   0.00,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.model, func(t *testing.T) {
			got := e.Cost(tc.tokensIn, tc.tokensOut, tc.model)
			if math.Abs(got-tc.wantUSD) > 1e-9 {
				t.Errorf("Cost(%d, %d, %q) = %v; want %v",
					tc.tokensIn, tc.tokensOut, tc.model, got, tc.wantUSD)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestCost_UnknownModel_LogsAndReturnsZero — unknown model returns 0.
// The warning log is a side-effect we cannot easily intercept without a log
// hook; we just assert the return value is 0.
// ---------------------------------------------------------------------------

func TestCost_UnknownModel_LogsAndReturnsZero(t *testing.T) {
	e := newTestEngine(t)
	got := e.Cost(100_000, 50_000, "totally-fake-model")
	if got != 0 {
		t.Errorf("Cost(unknown model): expected 0, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// TestOverride_FromConfigDir — override file takes precedence over embedded.
// ---------------------------------------------------------------------------

func TestOverride_FromConfigDir(t *testing.T) {
	dir := t.TempDir()

	// Write an override that changes claude-haiku-4 rates to known values.
	override := PricingFile{
		Version: 1,
		Models: map[string]PerTokenRates{
			"claude-haiku-4": {
				PromptPerMtok:     99.99,
				CompletionPerMtok: 199.99,
			},
			"override-only-model": {
				PromptPerMtok:     1.23,
				CompletionPerMtok: 4.56,
			},
		},
	}
	data, err := json.Marshal(override)
	if err != nil {
		t.Fatalf("marshal override: %v", err)
	}
	overridePath := filepath.Join(dir, "pricing.json")
	if err := os.WriteFile(overridePath, data, 0o600); err != nil {
		t.Fatalf("write override: %v", err)
	}

	cfg := config.Defaults()
	cfg.Paths.PricingOverride = overridePath

	e, err := New(cfg)
	if err != nil {
		t.Fatalf("New() with override: %v", err)
	}

	// Overridden model: check that new rates are used.
	rates, ok := e.Lookup("claude-haiku-4")
	if !ok {
		t.Fatal("Lookup(claude-haiku-4) after override: expected ok=true")
	}
	if rates.PromptPerMtok != 99.99 {
		t.Errorf("PromptPerMtok after override: got %v, want 99.99", rates.PromptPerMtok)
	}
	if rates.CompletionPerMtok != 199.99 {
		t.Errorf("CompletionPerMtok after override: got %v, want 199.99", rates.CompletionPerMtok)
	}

	// Model only in override file: should be found.
	_, ok = e.Lookup("override-only-model")
	if !ok {
		t.Error("Lookup(override-only-model): expected ok=true")
	}

	// Model only in embedded (not overridden): should still be found.
	_, ok = e.Lookup("claude-sonnet-4-5")
	if !ok {
		t.Error("Lookup(claude-sonnet-4-5): expected ok=true after partial override")
	}
}

// ---------------------------------------------------------------------------
// TestCost_ZeroTokens — both inputs zero yields zero cost.
// ---------------------------------------------------------------------------

func TestCost_ZeroTokens(t *testing.T) {
	e := newTestEngine(t)
	got := e.Cost(0, 0, "claude-sonnet-4-5")
	if got != 0 {
		t.Errorf("Cost(0, 0, model): expected 0, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// TestCost_LargeTokenCounts — verify no overflow with large token counts.
// ---------------------------------------------------------------------------

func TestCost_LargeTokenCounts(t *testing.T) {
	e := newTestEngine(t)
	// 200 million input + 100 million output tokens for claude-opus-4-6
	// prompt: 200 * 15.00 = $3000, completion: 100 * 75.00 = $7500
	got := e.Cost(200_000_000, 100_000_000, "claude-opus-4-6")
	want := 200.0*15.00 + 100.0*75.00
	if math.Abs(got-want) > 1e-6 {
		t.Errorf("Cost large: got %v, want %v", got, want)
	}
}

// ---------------------------------------------------------------------------
// Rollup tests — require a live in-memory SQLite DB.
// We use the store package's Open() with a temp file.
// ---------------------------------------------------------------------------

// openTestDB opens an in-memory SQLite database with migrations applied.
// Returns nil if the store package is not yet wired (deferred integration).
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=journal_mode%3DWAL&_pragma=foreign_keys%3DON")
	if err != nil {
		t.Skipf("cannot open in-memory sqlite: %v (deferred to integration)", err)
		return nil
	}
	if err := db.Ping(); err != nil {
		t.Skipf("cannot ping sqlite: %v (deferred to integration)", err)
		return nil
	}
	// Apply schema manually so we don't depend on the store package.
	schema := `
CREATE TABLE IF NOT EXISTS sessions (
    id           TEXT    NOT NULL PRIMARY KEY,
    cli          TEXT    NOT NULL,
    project_path TEXT    NOT NULL,
    encoded_cwd  TEXT,
    started_at   INTEGER NOT NULL,
    last_msg_at  INTEGER NOT NULL,
    msg_count    INTEGER NOT NULL DEFAULT 0,
    tokens_in    INTEGER NOT NULL DEFAULT 0,
    tokens_out   INTEGER NOT NULL DEFAULT 0,
    cost_usd     REAL    NOT NULL DEFAULT 0,
    model        TEXT,
    status       TEXT,
    raw_path     TEXT    NOT NULL
);
CREATE TABLE IF NOT EXISTS messages (
    id          TEXT    NOT NULL PRIMARY KEY,
    session_id  TEXT    NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    parent_uuid TEXT,
    role        TEXT    NOT NULL,
    content     TEXT    NOT NULL,
    tool_name   TEXT,
    tokens_in   INTEGER,
    tokens_out  INTEGER,
    cost_usd    REAL,
    model       TEXT,
    ts          INTEGER NOT NULL
);
`
	if _, err := db.ExecContext(context.Background(), schema); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	return db
}

func insertTestSession(t *testing.T, db *sql.DB, id, project string, startedAt int64) {
	t.Helper()
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO sessions (id, cli, project_path, started_at, last_msg_at, raw_path)
		 VALUES (?, 'claude', ?, ?, ?, '')`,
		id, project, startedAt, startedAt)
	if err != nil {
		t.Fatalf("insertTestSession(%q): %v", id, err)
	}
}

func insertTestMessage(t *testing.T, db *sql.DB, id, sessionID, model string, tokensIn, tokensOut int64, ts int64) {
	t.Helper()
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO messages (id, session_id, role, content, tokens_in, tokens_out, model, ts)
		 VALUES (?, ?, 'assistant', '', ?, ?, ?, ?)`,
		id, sessionID, tokensIn, tokensOut, model, ts)
	if err != nil {
		t.Fatalf("insertTestMessage(%q): %v", id, err)
	}
}

// ---------------------------------------------------------------------------
// TestRollupSession_BasicSum — single session with multiple messages.
// ---------------------------------------------------------------------------

func TestRollupSession_BasicSum(t *testing.T) {
	db := openTestDB(t)
	if db == nil {
		return
	}
	defer db.Close()

	e := newTestEngine(t)
	ctx := context.Background()

	sessionID := "sess-001"
	insertTestSession(t, db, sessionID, "/project/alpha", 1_000_000)

	// 3 messages with claude-haiku-4:
	// msg1: 100k in, 50k out  => 0.08 + 0.20 = $0.08 + $0.20 = $0.28 ... wait let's recalc
	// claude-haiku-4: prompt=$0.80/mtok, completion=$4.00/mtok
	// msg1: 100_000/1e6*0.80 + 50_000/1e6*4.00 = 0.08 + 0.20 = $0.28
	// msg2: 200_000/1e6*0.80 + 100_000/1e6*4.00 = 0.16 + 0.40 = $0.56
	// msg3: 50_000/1e6*0.80 + 25_000/1e6*4.00 = 0.04 + 0.10 = $0.14
	// total = 0.28 + 0.56 + 0.14 = $0.98
	insertTestMessage(t, db, "msg-001", sessionID, "claude-haiku-4", 100_000, 50_000, 1_000_001)
	insertTestMessage(t, db, "msg-002", sessionID, "claude-haiku-4", 200_000, 100_000, 1_000_002)
	insertTestMessage(t, db, "msg-003", sessionID, "claude-haiku-4", 50_000, 25_000, 1_000_003)

	got, err := e.RollupSession(ctx, db, sessionID)
	if err != nil {
		t.Fatalf("RollupSession: %v", err)
	}

	want := 0.28 + 0.56 + 0.14
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("RollupSession = %v; want %v", got, want)
	}
}

// ---------------------------------------------------------------------------
// TestRollupProject_MultiSession — 3 sessions in same project; sum correct.
// ---------------------------------------------------------------------------

func TestRollupProject_MultiSession(t *testing.T) {
	db := openTestDB(t)
	if db == nil {
		return
	}
	defer db.Close()

	e := newTestEngine(t)
	ctx := context.Background()

	project := "/project/beta"
	since := int64(0)

	// Session A: 1M in, 0.5M out with gpt-5-mini ($0.40/mtok, $1.60/mtok)
	// = 1*0.40 + 0.5*1.60 = 0.40 + 0.80 = $1.20
	insertTestSession(t, db, "proj-sess-A", project, 1_000_000)
	insertTestMessage(t, db, "proj-msg-A1", "proj-sess-A", "gpt-5-mini", 1_000_000, 500_000, 1_000_001)

	// Session B: 500k in, 250k out with gpt-5-mini
	// = 0.5*0.40 + 0.25*1.60 = 0.20 + 0.40 = $0.60
	insertTestSession(t, db, "proj-sess-B", project, 2_000_000)
	insertTestMessage(t, db, "proj-msg-B1", "proj-sess-B", "gpt-5-mini", 500_000, 250_000, 2_000_001)

	// Session C: different project — should NOT be included.
	insertTestSession(t, db, "proj-sess-C", "/project/gamma", 3_000_000)
	insertTestMessage(t, db, "proj-msg-C1", "proj-sess-C", "gpt-5-mini", 1_000_000, 500_000, 3_000_001)

	got, err := e.RollupProject(ctx, db, project, since)
	if err != nil {
		t.Fatalf("RollupProject: %v", err)
	}

	want := 1.20 + 0.60
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("RollupProject = %v; want %v", got, want)
	}
}

// ---------------------------------------------------------------------------
// TestRollupDaily_BoundaryHandling — events at boundary of two days land
// in the correct bucket.
// ---------------------------------------------------------------------------

func TestRollupDaily_BoundaryHandling(t *testing.T) {
	db := openTestDB(t)
	if db == nil {
		return
	}
	defer db.Close()

	e := newTestEngine(t)
	ctx := context.Background()

	// Day boundary: 2025-01-15 00:00:00 UTC = 1736899200000 ms
	// Last ms of 2025-01-14: 1736899199999
	// First ms of 2025-01-15: 1736899200000
	dayBoundaryMs := int64(1_736_899_200_000)

	// Session spanning the boundary.
	insertTestSession(t, db, "boundary-sess", "/project/alpha", dayBoundaryMs-10_000)

	// Message 1: last millisecond of 2025-01-14 (should NOT count for 2025-01-15).
	insertTestMessage(t, db, "boundary-msg-prev", "boundary-sess", "gpt-5-mini",
		100_000, 50_000, dayBoundaryMs-1)

	// Message 2: first millisecond of 2025-01-15 (SHOULD count).
	// 100k in + 50k out @ gpt-5-mini: 0.10*0.40 + 0.05*1.60 = 0.040 + 0.080 = $0.12
	insertTestMessage(t, db, "boundary-msg-day", "boundary-sess", "gpt-5-mini",
		100_000, 50_000, dayBoundaryMs)

	// Message 3: end of 2025-01-15 23:59:59.999 — should count.
	// 200k in + 100k out @ gpt-5-mini: 0.20*0.40 + 0.10*1.60 = 0.080 + 0.160 = $0.24
	insertTestMessage(t, db, "boundary-msg-eod", "boundary-sess", "gpt-5-mini",
		200_000, 100_000, dayBoundaryMs+86_399_999)

	// Use 2025-01-15 as the target day.
	targetDay := int64(1_736_899_200_000) // 2025-01-15 00:00:00 UTC in ms (used as epoch-ms for day start)

	got, err := e.RollupDaily(ctx, db, targetDay)
	if err != nil {
		t.Fatalf("RollupDaily: %v", err)
	}

	// Should include msg2 ($0.12) + msg3 ($0.24) = $0.36
	// Should NOT include msg1 (previous day).
	want := 0.04 + 0.08 + 0.08 + 0.16
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("RollupDaily = %v; want %v (boundary day)", got, want)
	}
}

// ---------------------------------------------------------------------------
// TestRollupByModel_MultiModel — messages from different models aggregated.
// ---------------------------------------------------------------------------

func TestRollupByModel_MultiModel(t *testing.T) {
	db := openTestDB(t)
	if db == nil {
		return
	}
	defer db.Close()

	e := newTestEngine(t)
	ctx := context.Background()

	insertTestSession(t, db, "model-sess-1", "/project/delta", 1_000_000)

	// claude-haiku-4: 100k in + 50k out = $0.08 + $0.20 = $0.28
	insertTestMessage(t, db, "model-msg-1", "model-sess-1", "claude-haiku-4",
		100_000, 50_000, 1_000_001)

	// gpt-5-mini: 200k in + 100k out = 0.20*0.40 + 0.10*1.60 = $0.08 + $0.16 = $0.24
	insertTestMessage(t, db, "model-msg-2", "model-sess-1", "gpt-5-mini",
		200_000, 100_000, 1_000_002)

	// claude-haiku-4 again: 50k in + 25k out = $0.04 + $0.10 = $0.14
	insertTestMessage(t, db, "model-msg-3", "model-sess-1", "claude-haiku-4",
		50_000, 25_000, 1_000_003)

	byModel, err := e.RollupByModel(ctx, db, 0)
	if err != nil {
		t.Fatalf("RollupByModel: %v", err)
	}

	wantHaiku := 0.28 + 0.14
	wantMini := 0.24

	if math.Abs(byModel["claude-haiku-4"]-wantHaiku) > 1e-9 {
		t.Errorf("RollupByModel[claude-haiku-4] = %v; want %v",
			byModel["claude-haiku-4"], wantHaiku)
	}
	if math.Abs(byModel["gpt-5-mini"]-wantMini) > 1e-9 {
		t.Errorf("RollupByModel[gpt-5-mini] = %v; want %v",
			byModel["gpt-5-mini"], wantMini)
	}
}

// ---------------------------------------------------------------------------
// TestRollupSession_MatchesCCusage — stub for D7 acceptance.
// Deferred to W16 (bench) / W17 (golden file refresh).
// This test documents the expected contract without asserting exact values.
// ---------------------------------------------------------------------------

func TestRollupSession_MatchesCCusage_Stub(t *testing.T) {
	// TODO(W16/W17): populate a golden file from `ccusage` output and assert
	// that RollupSession returns a value within 0.5% of the ccusage total.
	// For now we verify the return type and error contract only.
	db := openTestDB(t)
	if db == nil {
		return
	}
	defer db.Close()

	e := newTestEngine(t)
	ctx := context.Background()

	insertTestSession(t, db, "ccusage-stub-sess", "/stub/project", 1_000_000)
	insertTestMessage(t, db, "ccusage-stub-msg", "ccusage-stub-sess",
		"claude-sonnet-4-5", 1_000, 500, 1_000_001)

	cost, err := e.RollupSession(ctx, db, "ccusage-stub-sess")
	if err != nil {
		t.Fatalf("RollupSession: unexpected error: %v", err)
	}
	if cost < 0 {
		t.Errorf("RollupSession: expected non-negative cost, got %v", cost)
	}
	// D7 acceptance: within 0.5% of ccusage output.
	// Deferred: ccusage comparison requires W16 fixture + golden file.
	t.Logf("D7 stub: RollupSession returned $%.6f — ccusage parity deferred to W16/W17", cost)
}

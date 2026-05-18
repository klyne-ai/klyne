package mcpserver_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klyne-ai/klyne/internal/mcpserver"
	"github.com/klyne-ai/klyne/internal/store"
)

func openShieldDB(t *testing.T) *store.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "shield.db"))
	if err != nil {
		t.Fatalf("open shield db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// seedSnapshot inserts a shield_snapshot for sessionID so the handler can
// find it during the block path.
func seedSnapshot(t *testing.T, ctx context.Context, db *store.DB, sessionID string, fillPct float64) {
	t.Helper()
	s := &store.ShieldSnapshot{
		SessionID:     sessionID,
		DecisionsJSON: `["decision-A"]`,
		OpenFilesJSON: `["main.go"]`,
		TurnsJSON:     `["turn-1"]`,
		FillPct:       fillPct,
		PreTokens:     50000,
	}
	if err := store.InsertShieldSnapshot(ctx, db, s); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}
}

// preCompactPayload builds a minimal JSON PreCompact hook payload.
func preCompactPayload(sessionID string, fillPct float64, maxTokens int64) string {
	currentTokens := int64(float64(maxTokens) * fillPct)
	payload := map[string]any{
		"session_id":      sessionID,
		"hook_event_name": "PreCompact",
		"trigger":         "auto",
		"context_window": map[string]any{
			"current_tokens": currentTokens,
			"max_tokens":     maxTokens,
			"fill_pct":       fillPct * 100.0,
		},
	}
	b, _ := json.Marshal(payload)
	return string(b)
}

// TestHandlePreCompact_BelowBand_Allows verifies that fill < 70% → allow.
func TestHandlePreCompact_BelowBand_Allows(t *testing.T) {
	ctx := context.Background()
	db := openShieldDB(t)

	r := strings.NewReader(preCompactPayload("sess-low", 0.50, 200000))
	res, err := mcpserver.HandlePreCompact(ctx, r, db)
	if err != nil {
		t.Fatalf("HandlePreCompact: %v", err)
	}
	if res.Decision != "" {
		t.Errorf("expected allow (empty decision), got %q", res.Decision)
	}
	if res.Output != "" {
		t.Errorf("expected empty output for below-band fill, got %q", res.Output)
	}
}

// TestHandlePreCompact_AboveCeiling_Folds verifies fill ≥ 92% → fold (allow).
func TestHandlePreCompact_AboveCeiling_Folds(t *testing.T) {
	ctx := context.Background()
	db := openShieldDB(t)
	seedSnapshot(t, ctx, db, "sess-full", 92.0)

	r := strings.NewReader(preCompactPayload("sess-full", 0.95, 200000))
	res, err := mcpserver.HandlePreCompact(ctx, r, db)
	if err != nil {
		t.Fatalf("HandlePreCompact: %v", err)
	}
	// At ceiling the handler should NOT block (graceful fold).
	if res.Decision == "block" {
		t.Errorf("expected allow at ceiling, got block (reason=%q)", res.Reason)
	}
	if res.Output != "" {
		t.Errorf("expected empty output for fold, got %q", res.Output)
	}
}

// TestHandlePreCompact_InBandWithSnapshot_Blocks verifies fill in [70%,92%)
// with a snapshot present → block.
func TestHandlePreCompact_InBandWithSnapshot_Blocks(t *testing.T) {
	ctx := context.Background()
	db := openShieldDB(t)
	seedSnapshot(t, ctx, db, "sess-band", 75.0)

	r := strings.NewReader(preCompactPayload("sess-band", 0.80, 200000))
	res, err := mcpserver.HandlePreCompact(ctx, r, db)
	if err != nil {
		t.Fatalf("HandlePreCompact: %v", err)
	}
	if res.Decision != "block" {
		t.Fatalf("expected block in-band with snapshot, got %q", res.Decision)
	}
	if res.SnapshotID == 0 {
		t.Error("expected non-zero snapshot id in block result")
	}
	if res.Output == "" {
		t.Error("expected non-empty block output")
	}

	// Output must be valid JSON with the "decision" key.
	var doc map[string]any
	if err := json.Unmarshal([]byte(res.Output), &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, res.Output)
	}
	if doc["decision"] != "block" {
		t.Errorf("decision field = %v, want block", doc["decision"])
	}
}

// TestHandlePreCompact_InBandNoSnapshot_Allows verifies that when fill is in
// the band but no snapshot was armed, klyne gracefully allows.
func TestHandlePreCompact_InBandNoSnapshot_Allows(t *testing.T) {
	ctx := context.Background()
	db := openShieldDB(t)
	// No seedSnapshot call → table empty for this session.

	r := strings.NewReader(preCompactPayload("sess-no-snap", 0.80, 200000))
	res, err := mcpserver.HandlePreCompact(ctx, r, db)
	if err != nil {
		t.Fatalf("HandlePreCompact: %v", err)
	}
	if res.Decision == "block" {
		t.Errorf("expected allow when no snapshot exists, got block")
	}
}

// TestHandlePreCompact_NilDB_Allows verifies the handler is safe with a nil db.
func TestHandlePreCompact_NilDB_Allows(t *testing.T) {
	ctx := context.Background()

	r := strings.NewReader(preCompactPayload("sess-no-db", 0.80, 200000))
	res, err := mcpserver.HandlePreCompact(ctx, r, nil)
	if err != nil {
		t.Fatalf("HandlePreCompact: %v", err)
	}
	// Without a DB we can never find a snapshot → always allow.
	if res.Decision == "block" {
		t.Errorf("expected allow with nil db, got block")
	}
}

// TestHandlePreCompact_EmptyInput_Allows verifies graceful pass-through on empty stdin.
func TestHandlePreCompact_EmptyInput_Allows(t *testing.T) {
	ctx := context.Background()
	res, err := mcpserver.HandlePreCompact(ctx, strings.NewReader(""), nil)
	if err != nil {
		t.Fatalf("HandlePreCompact: %v", err)
	}
	if res.Output != "" {
		t.Errorf("expected empty output for empty input, got %q", res.Output)
	}
}

// TestHandlePreCompact_MalformedJSON_Allows verifies graceful pass-through.
func TestHandlePreCompact_MalformedJSON_Allows(t *testing.T) {
	ctx := context.Background()
	res, err := mcpserver.HandlePreCompact(ctx, strings.NewReader("{bad json"), nil)
	if err != nil {
		t.Fatalf("HandlePreCompact: %v", err)
	}
	if res.Output != "" {
		t.Errorf("expected empty output for malformed JSON")
	}
}

// TestHandlePreCompact_ExactBoundaries tests the 70% and 92% thresholds exactly.
func TestHandlePreCompact_ExactBoundaries(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name        string
		fillFrac    float64
		wantBlock   bool
		needsSnap   bool
	}{
		// At exactly 70% → arm band (needs snapshot to block).
		{"at-70pct", 0.70, true, true},
		// Just below 70% → below band → allow.
		{"below-70pct", 0.699, false, false},
		// At exactly 92% → fold → allow.
		{"at-92pct", 0.92, false, false},
		// Just below 92% → in band → block (if snapshot).
		{"below-92pct", 0.919, true, true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			db := openShieldDB(t)
			sessionID := "sess-" + tc.name
			if tc.needsSnap {
				seedSnapshot(t, ctx, db, sessionID, tc.fillFrac*100)
			}
			r := strings.NewReader(preCompactPayload(sessionID, tc.fillFrac, 200000))
			res, err := mcpserver.HandlePreCompact(ctx, r, db)
			if err != nil {
				t.Fatalf("HandlePreCompact: %v", err)
			}
			gotBlock := res.Decision == "block"
			if gotBlock != tc.wantBlock {
				t.Errorf("fill=%.3f: wantBlock=%v gotBlock=%v (decision=%q reason=%q)",
					tc.fillFrac, tc.wantBlock, gotBlock, res.Decision, res.Reason)
			}
		})
	}
}

// TestHandlePreCompact_BlockOutput_ContainsSnapshotID verifies the system
// message embeds the snapshot id for traceability.
func TestHandlePreCompact_BlockOutput_ContainsSnapshotID(t *testing.T) {
	ctx := context.Background()
	db := openShieldDB(t)
	seedSnapshot(t, ctx, db, "sess-trace", 75.0)

	r := strings.NewReader(preCompactPayload("sess-trace", 0.80, 200000))
	res, err := mcpserver.HandlePreCompact(ctx, r, db)
	if err != nil {
		t.Fatalf("HandlePreCompact: %v", err)
	}
	if res.Decision != "block" {
		t.Fatalf("expected block, got %q", res.Decision)
	}
	if !strings.Contains(res.Output, "snapshot=") {
		t.Errorf("expected output to mention snapshot id: %s", res.Output)
	}
}

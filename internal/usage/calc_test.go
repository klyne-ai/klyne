package usage_test

import (
	"context"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/store"
	"github.com/klyne-ai/klyne/internal/usage"
)

func openTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), t.TempDir() + "/usage.db")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// seed inserts a session and one message under the supplied CLI/model/ts.
// Token counts are split evenly between in and out.
func seed(t *testing.T, db *store.DB, sessionID string, cli connectors.CLI, model string, ts int64, tokens int64) {
	t.Helper()
	ctx := context.Background()
	sess := &connectors.Session{
		ID:          sessionID,
		CLI:         cli,
		ProjectPath: "/p/" + sessionID,
		StartedAt:   ts,
		LastMsgAt:   ts,
		Status:      connectors.SessionStatusIdle,
	}
	if err := store.UpsertSession(ctx, db, sess); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	half := tokens / 2
	msg := &connectors.Message{
		ID:        sessionID + "-m-" + model,
		SessionID: sessionID,
		CLI:       cli,
		Role:      connectors.RoleAssistant,
		Content:   "x",
		TokensIn:  half,
		TokensOut: tokens - half,
		Model:     model,
		Ts:        ts,
	}
	if err := store.InsertMessage(ctx, db, msg); err != nil {
		t.Fatalf("insert: %v", err)
	}
}

func TestCompute_RollingWindows(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)

	// "now" — pick a fixed wall clock so tests are deterministic.
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	nowMs := now.UnixMilli()

	// Inside 5h, Sonnet
	seed(t, db, "c-recent-sonnet", connectors.CLIClaude, "claude-sonnet-4-6",
		nowMs-30*60*1000, 1000)
	// Inside 5h, Opus (should NOT count toward 7d Sonnet)
	seed(t, db, "c-recent-opus", connectors.CLIClaude, "claude-opus-4-5",
		nowMs-2*60*60*1000, 500)
	// Inside 7d, outside 5h, Sonnet
	seed(t, db, "c-old-sonnet", connectors.CLIClaude, "claude-3-5-sonnet-20241022",
		nowMs-2*24*60*60*1000, 200)
	// Outside 7d (8 days) — should NOT appear in any window
	seed(t, db, "c-ancient", connectors.CLIClaude, "claude-sonnet-4-6",
		nowMs-8*24*60*60*1000, 9999)
	// Codex inside 5h
	seed(t, db, "x-recent", connectors.CLICodex, "gpt-5-codex",
		nowMs-1*60*60*1000, 700)

	got, err := usage.Compute(context.Background(), db, now, nil, nil)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	if got.Now != nowMs {
		t.Errorf("Now = %d, want %d", got.Now, nowMs)
	}

	// Claude 5h: recent-sonnet (1000) + recent-opus (500) = 1500.
	if got.Claude.Window5h.Tokens != 1500 {
		t.Errorf("Claude.5h.Tokens = %d, want 1500", got.Claude.Window5h.Tokens)
	}
	if got.Claude.Window5h.Messages != 2 {
		t.Errorf("Claude.5h.Messages = %d, want 2", got.Claude.Window5h.Messages)
	}
	if got.Claude.Window5h.WindowSeconds != usage.Window5h {
		t.Errorf("Claude.5h.WindowSeconds = %d, want %d", got.Claude.Window5h.WindowSeconds, usage.Window5h)
	}

	// Claude 7d: recent-sonnet + recent-opus + old-sonnet = 1700; ancient excluded.
	if got.Claude.Window7d.Tokens != 1700 {
		t.Errorf("Claude.7d.Tokens = %d, want 1700", got.Claude.Window7d.Tokens)
	}

	// Claude 7d Sonnet: recent-sonnet (1000) + old-sonnet (200) = 1200; opus excluded.
	if got.Claude.Window7dSonnet.Tokens != 1200 {
		t.Errorf("Claude.7dSonnet.Tokens = %d, want 1200", got.Claude.Window7dSonnet.Tokens)
	}

	// FirstMsgTs of the 5h Claude window must be the older of recent-opus.
	wantFirst := nowMs - 2*60*60*1000
	if got.Claude.Window5h.FirstMsgTs != wantFirst {
		t.Errorf("Claude.5h.FirstMsgTs = %d, want %d", got.Claude.Window5h.FirstMsgTs, wantFirst)
	}

	// Codex 5h: 700.
	if got.Codex.Window5h.Tokens != 700 {
		t.Errorf("Codex.5h.Tokens = %d, want 700", got.Codex.Window5h.Tokens)
	}
	// Codex 7d Sonnet must be empty (Anthropic-specific concept).
	if got.Codex.Window7dSonnet.Tokens != 0 || got.Codex.Window7dSonnet.Messages != 0 {
		t.Errorf("Codex.7dSonnet should be empty, got %+v", got.Codex.Window7dSonnet)
	}
}

func TestCompute_EmptyDB(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	got, err := usage.Compute(context.Background(), db, time.Now(), nil, nil)
	if err != nil {
		t.Fatalf("Compute on empty db: %v", err)
	}
	if got.Claude.Window5h.Tokens != 0 || got.Claude.Window5h.Messages != 0 {
		t.Errorf("expected empty Claude 5h window, got %+v", got.Claude.Window5h)
	}
	if got.Claude.Window5h.FirstMsgTs != 0 {
		t.Errorf("FirstMsgTs should be 0 for empty window, got %d", got.Claude.Window5h.FirstMsgTs)
	}
	if got.Codex.Window5h.Tokens != 0 {
		t.Errorf("expected empty Codex 5h, got %+v", got.Codex.Window5h)
	}
}

func TestCompute_NilDB(t *testing.T) {
	t.Parallel()
	if _, err := usage.Compute(context.Background(), nil, time.Now(), nil, nil); err == nil {
		t.Errorf("expected error for nil db")
	}
}

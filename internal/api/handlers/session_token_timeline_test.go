package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/api/handlers"
	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/store"
)

// newTokenTimelineRouter wires only the token-timeline route, with the
// clock pinned so window math is deterministic. Returning a frozen
// `now` lets the tests place assistant turns at known offsets from it
// without depending on time.Now drift between insert and request.
func newTokenTimelineRouter(t *testing.T, db *store.DB, now time.Time) http.Handler {
	t.Helper()
	h := handlers.NewSessionTokenTimelineHandler(handlers.SessionTokenTimelineDeps{
		DB:    db,
		NowFn: func() time.Time { return now },
	})
	r := chi.NewRouter()
	r.Get(api.RouteSessionTokenTimeline, h.Get)
	return r
}

func seedTokenTimelineSession(t *testing.T, db *store.DB, id string) {
	t.Helper()
	s := &connectors.Session{
		ID:          id,
		CLI:         connectors.CLIClaude,
		ProjectPath: "/tmp/proj",
		StartedAt:   1000,
		LastMsgAt:   2000,
		Status:      connectors.SessionStatusIdle,
		Model:       "claude-sonnet-4-6",
	}
	if err := store.UpsertSession(context.Background(), db, s); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
}

// seedTimelineAssistantTurn inserts one assistant message with the
// full cache-aware token breakdown the timeline computation reads.
// CachedRead is split from the raw TokensIn input — this mirrors what
// connectors produce when Claude returns a turn served partly from
// the prompt cache.
func seedTimelineAssistantTurn(
	t *testing.T,
	db *store.DB,
	sessionID, msgID, model string,
	tokensIn, cachedRead, tokensOut int64,
	ts int64,
) {
	t.Helper()
	m := &connectors.Message{
		ID:               msgID,
		SessionID:        sessionID,
		CLI:              connectors.CLIClaude,
		Role:             connectors.RoleAssistant,
		Content:          "(assistant)",
		TokensIn:         tokensIn,
		CachedReadTokens: cachedRead,
		TokensOut:        tokensOut,
		Model:            model,
		Ts:               ts,
	}
	if err := store.InsertMessage(context.Background(), db, m); err != nil {
		t.Fatalf("insert message: %v", err)
	}
}

// --- 404 ---

func TestSessionTokenTimeline_NotFound(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	router := newTokenTimelineRouter(t, db, time.Unix(0, 5_000_000_000_000*int64(time.Millisecond)))
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/sessions/no-such/token-timeline")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

// --- Happy path: three assistant turns, full-session view ---

func TestSessionTokenTimeline_HappyPath(t *testing.T) {
	t.Parallel()

	db := newTestStore(t)
	const id = "tl-happy"
	seedTokenTimelineSession(t, db, id)

	// Pin `now` so windowing is deterministic. Pick a recent epoch
	// large enough that subtracting a few minutes stays positive.
	nowMs := int64(5_000_000_000_000) // arbitrary epoch-ms in 2128
	now := time.UnixMilli(nowMs)

	// Three assistant turns at -3m, -2m, -1m. TokensIn grows so the
	// chart shows a rising prefix; cachedRead grows alongside so the
	// effective_input column reflects the partial cache benefit.
	seedTimelineAssistantTurn(t, db, id, "a1", "claude-sonnet-4-6", 10_000, 0, 500, nowMs-3*60_000)
	seedTimelineAssistantTurn(t, db, id, "a2", "claude-sonnet-4-6", 25_000, 5_000, 600, nowMs-2*60_000)
	seedTimelineAssistantTurn(t, db, id, "a3", "claude-sonnet-4-6", 40_000, 20_000, 700, nowMs-1*60_000)

	router := newTokenTimelineRouter(t, db, now)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/sessions/" + id + "/token-timeline")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body api.TokenTimelineResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if body.SessionID != id {
		t.Errorf("session_id = %q, want %q", body.SessionID, id)
	}
	if body.Model != "claude-sonnet-4-6" {
		t.Errorf("model = %q, want claude-sonnet-4-6", body.Model)
	}
	if body.ContextWindow != 1_000_000 {
		t.Errorf("context_window = %d, want 1000000 (sonnet-4 default 1M)", body.ContextWindow)
	}
	if len(body.Points) != 3 {
		t.Fatalf("len(points) = %d, want 3", len(body.Points))
	}
	// Chronological order check + per-row values.
	wantPts := []struct {
		tsMs   int64
		total  int64
		eff    int64
		cached int64
		out    int64
	}{
		{nowMs - 3*60_000, 10_000, 10_000, 0, 500},
		{nowMs - 2*60_000, 25_000, 20_000, 5_000, 600},
		{nowMs - 1*60_000, 40_000, 20_000, 20_000, 700},
	}
	for i, p := range body.Points {
		if p.TsMs != wantPts[i].tsMs {
			t.Errorf("points[%d].ts_ms = %d, want %d", i, p.TsMs, wantPts[i].tsMs)
		}
		if p.TotalInput != wantPts[i].total {
			t.Errorf("points[%d].total_input = %d, want %d", i, p.TotalInput, wantPts[i].total)
		}
		if p.EffectiveInput != wantPts[i].eff {
			t.Errorf("points[%d].effective_input = %d, want %d", i, p.EffectiveInput, wantPts[i].eff)
		}
		if p.CachedReadTokens != wantPts[i].cached {
			t.Errorf("points[%d].cached_read_tokens = %d, want %d", i, p.CachedReadTokens, wantPts[i].cached)
		}
		if p.OutputTokens != wantPts[i].out {
			t.Errorf("points[%d].output_tokens = %d, want %d", i, p.OutputTokens, wantPts[i].out)
		}
	}
	if body.FirstInput != 10_000 {
		t.Errorf("first_input = %d, want 10000", body.FirstInput)
	}
	if body.LatestInput != 40_000 {
		t.Errorf("latest_input = %d, want 40000", body.LatestInput)
	}
	if body.PeakInput != 40_000 {
		t.Errorf("peak_input = %d, want 40000", body.PeakInput)
	}
	// PctOfContext = LatestInput / ContextWindow × 100 = 40000/1_000_000 × 100 = 4
	wantPct := float64(40_000) / float64(1_000_000) * 100.0
	if body.PctOfContext != wantPct {
		t.Errorf("pct_of_context = %f, want %f", body.PctOfContext, wantPct)
	}
	// WindowEndMs is pinned to the clock; WindowStartMs is the first
	// point's ts in the entire-session view.
	if body.WindowEndMs != nowMs {
		t.Errorf("window_end_ms = %d, want %d", body.WindowEndMs, nowMs)
	}
	if body.WindowStartMs != nowMs-3*60_000 {
		t.Errorf("window_start_ms = %d, want %d (first point ts)", body.WindowStartMs, nowMs-3*60_000)
	}
}

// --- Empty session: zero assistant turns ---

func TestSessionTokenTimeline_EmptySession(t *testing.T) {
	t.Parallel()

	db := newTestStore(t)
	const id = "tl-empty"
	seedTokenTimelineSession(t, db, id)

	nowMs := int64(5_000_000_000_000)
	now := time.UnixMilli(nowMs)

	router := newTokenTimelineRouter(t, db, now)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/sessions/" + id + "/token-timeline")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body api.TokenTimelineResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.SessionID != id {
		t.Errorf("session_id = %q, want %q", body.SessionID, id)
	}
	if len(body.Points) != 0 {
		t.Errorf("len(points) = %d, want 0 on empty session", len(body.Points))
	}
	if body.FirstInput != 0 || body.LatestInput != 0 || body.PeakInput != 0 {
		t.Errorf("expected zero token counts on empty session, got first=%d latest=%d peak=%d",
			body.FirstInput, body.LatestInput, body.PeakInput)
	}
	if body.PctOfContext != 0 {
		t.Errorf("pct_of_context = %f, want 0", body.PctOfContext)
	}
	// Model falls back to session.Model so the UI can still resolve
	// the context window for axis labelling even before the first turn.
	if body.Model != "claude-sonnet-4-6" {
		t.Errorf("model = %q, want claude-sonnet-4-6 (session-level fallback)", body.Model)
	}
	if body.ContextWindow != 1_000_000 {
		t.Errorf("context_window = %d, want 1000000", body.ContextWindow)
	}
	if body.WindowEndMs != nowMs {
		t.Errorf("window_end_ms = %d, want %d", body.WindowEndMs, nowMs)
	}
}

// --- Window param clips older turns ---

func TestSessionTokenTimeline_WindowFiltersOldTurns(t *testing.T) {
	t.Parallel()

	db := newTestStore(t)
	const id = "tl-window"
	seedTokenTimelineSession(t, db, id)

	nowMs := int64(5_000_000_000_000)
	now := time.UnixMilli(nowMs)

	// One ancient turn (5h ago) and two recent turns (-2m, -1m).
	// A window=10m request should drop the ancient one.
	seedTimelineAssistantTurn(t, db, id, "old", "claude-sonnet-4-6", 9_999, 0, 100, nowMs-int64(5*time.Hour/time.Millisecond))
	seedTimelineAssistantTurn(t, db, id, "r1", "claude-sonnet-4-6", 20_000, 0, 200, nowMs-2*60_000)
	seedTimelineAssistantTurn(t, db, id, "r2", "claude-sonnet-4-6", 30_000, 0, 300, nowMs-1*60_000)

	router := newTokenTimelineRouter(t, db, now)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/sessions/" + id + "/token-timeline?window=10m")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	var body api.TokenTimelineResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Points) != 2 {
		t.Fatalf("len(points) = %d, want 2 (ancient turn dropped by 10m window)", len(body.Points))
	}
	if body.FirstInput != 20_000 {
		t.Errorf("first_input = %d, want 20000 (post-clip)", body.FirstInput)
	}
	if body.LatestInput != 30_000 {
		t.Errorf("latest_input = %d, want 30000", body.LatestInput)
	}
	// Fixed window: WindowStartMs = now - 10m.
	wantStart := nowMs - int64(10*time.Minute/time.Millisecond)
	if body.WindowStartMs != wantStart {
		t.Errorf("window_start_ms = %d, want %d (fixed 10m window)", body.WindowStartMs, wantStart)
	}
}

// --- Invalid window returns 400 ---

func TestSessionTokenTimeline_InvalidWindow(t *testing.T) {
	t.Parallel()

	db := newTestStore(t)
	const id = "tl-bad-window"
	seedTokenTimelineSession(t, db, id)

	router := newTokenTimelineRouter(t, db, time.UnixMilli(5_000_000_000_000))
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/sessions/" + id + "/token-timeline?window=not-a-duration")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for malformed window, got %d", resp.StatusCode)
	}
}

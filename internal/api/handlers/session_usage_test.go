package handlers_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/mohitpatell/agentdeck/internal/api"
	"github.com/mohitpatell/agentdeck/internal/api/handlers"
	"github.com/mohitpatell/agentdeck/internal/claudeauth"
	"github.com/mohitpatell/agentdeck/internal/connectors"
	"github.com/mohitpatell/agentdeck/internal/store"
	"github.com/mohitpatell/agentdeck/internal/usage"
)

// stubRoundTripper synthesises an HTTP response without a real network.
type stubRoundTripper struct {
	body   string
	status int
}

func (s stubRoundTripper) RoundTrip(_ *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: s.status,
		Body:       io.NopCloser(strings.NewReader(s.body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}, nil
}

// newSessionUsageRouter mounts only the session-usage route, optionally
// pre-wiring an OAuth fetcher with the given utilization% and the codex
// snapshot reader. Pass oauthBody="" to skip the OAuth path entirely
// (drives the uncalibrated branch).
func newSessionUsageRouter(t *testing.T, db *store.DB, oauthBody string) http.Handler {
	t.Helper()

	var fetcher *usage.OAuthFetcher
	if oauthBody != "" {
		fetcher = usage.NewOAuthFetcher()
		fetcher.SetCredentialLoader(func() (*claudeauth.Credentials, error) {
			return &claudeauth.Credentials{AccessToken: "tok", SubscriptionType: "max"}, nil
		})
		fetcher.SetHTTPClient(&http.Client{Transport: stubRoundTripper{body: oauthBody, status: 200}})
	}

	h := handlers.NewSessionUsageHandler(handlers.SessionUsageDeps{
		DB:    db,
		OAuth: fetcher,
	})
	r := chi.NewRouter()
	r.Get(api.RouteSessionUsage, h.Get)
	return r
}

func seedClaudeSession(t *testing.T, db *store.DB, id string) {
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

func seedCodexSession(t *testing.T, db *store.DB, id string, rawPath string) {
	t.Helper()
	s := &connectors.Session{
		ID:          id,
		CLI:         connectors.CLICodex,
		ProjectPath: "/tmp/proj",
		StartedAt:   1000,
		LastMsgAt:   2000,
		Status:      connectors.SessionStatusIdle,
		Model:       "gpt-5.5",
		RawPath:     rawPath,
	}
	if err := store.UpsertSession(context.Background(), db, s); err != nil {
		t.Fatalf("upsert codex session: %v", err)
	}
}

func seedAssistantTurn(t *testing.T, db *store.DB, sessionID, msgID, model string, tokensIn int64, ts int64) {
	t.Helper()
	m := &connectors.Message{
		ID:        msgID,
		SessionID: sessionID,
		CLI:       connectors.CLIClaude,
		Role:      connectors.RoleAssistant,
		Content:   "(assistant)",
		TokensIn:  tokensIn,
		TokensOut: 200,
		Model:     model,
		Ts:        ts,
	}
	if err := store.InsertMessage(context.Background(), db, m); err != nil {
		t.Fatalf("insert message: %v", err)
	}
}

// --- 404 ---

func TestSessionUsage_NotFound(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	router := newSessionUsageRouter(t, db, "")
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/sessions/no-such/usage")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

// --- Calibrated happy path ---

func TestSessionUsage_HappyPath_CalibratedFromOAuth(t *testing.T) {
	t.Parallel()

	db := newTestStore(t)
	const id = "calib-sess"
	seedClaudeSession(t, db, id)
	// Seed a single assistant turn within the 5h window so usage.Compute
	// returns Claude.Window5h.Tokens > 0.
	seedAssistantTurn(t, db, id, "asst-1", "claude-sonnet-4-6", 4_000, 4_000_000_000_000)

	// OAuth says we've used 2% of the 5h limit. Local sum is 4_200 tokens
	// (4000 in + 200 out). So tokensPerPct = 4200 / 2 = 2100.
	const oauth = `{"five_hour": {"utilization": 2.0, "resets_at": ""}}`

	router := newSessionUsageRouter(t, db, oauth)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/sessions/" + id + "/usage")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body api.SessionUsageResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if body.SessionID != id {
		t.Errorf("session id = %q, want %q", body.SessionID, id)
	}
	if !body.CalibratedFromOAuth {
		t.Errorf("expected CalibratedFromOAuth=true, got false")
	}
	if body.Model != "claude-sonnet-4-6" {
		t.Errorf("model = %q, want claude-sonnet-4-6", body.Model)
	}
	// Sonnet 4.x family ships with 1M context as the marketed default
	// (see internal/usage/context_window.go).
	if body.ContextWindow != 1_000_000 {
		t.Errorf("context_window = %d, want 1000000 (sonnet-4 default 1M)", body.ContextWindow)
	}
	if body.ContextUsed != 4_000 {
		t.Errorf("context_used = %d, want 4000", body.ContextUsed)
	}
	wantFill := float64(4_000) / float64(1_000_000) * 100.0
	if body.ContextFillPct != wantFill {
		t.Errorf("context_fill_pct = %f, want %f", body.ContextFillPct, wantFill)
	}
	// tokensPerPct = (4000 + 200) / 2.0 = 2100.
	// nextTurnPct5h = 4000 / 2100 ≈ 1.9047619
	wantNext := 4000.0 / 2100.0
	if !floatNear(body.NextTurnPct5h, wantNext, 1e-6) {
		t.Errorf("next_turn_pct_5h = %f, want %f", body.NextTurnPct5h, wantNext)
	}
	// CompactRatio is 0.15 → compacted = nextTurn * 0.15.
	wantCompact := 4000.0 * 0.15 / 2100.0
	if !floatNear(body.CompactedNextTurnPct5h, wantCompact, 1e-6) {
		t.Errorf("compacted_next_turn_pct_5h = %f, want %f", body.CompactedNextTurnPct5h, wantCompact)
	}
	// SystemPromptFloor = 5000.
	wantRestart := 5000.0 / 2100.0
	if !floatNear(body.RestartedNextTurnPct5h, wantRestart, 1e-6) {
		t.Errorf("restarted_next_turn_pct_5h = %f, want %f", body.RestartedNextTurnPct5h, wantRestart)
	}
	if !floatNear(body.CompactSavingsPct5h, wantNext-wantCompact, 1e-6) {
		t.Errorf("compact_savings_pct_5h = %f, want %f", body.CompactSavingsPct5h, wantNext-wantCompact)
	}
	if !floatNear(body.RestartSavingsPct5h, wantNext-wantRestart, 1e-6) {
		t.Errorf("restart_savings_pct_5h = %f, want %f", body.RestartSavingsPct5h, wantNext-wantRestart)
	}
	if body.CompactRatio != 0.15 {
		t.Errorf("compact_ratio = %f, want 0.15", body.CompactRatio)
	}
}

// --- Hard fallback when OAuth is absent ---

func TestSessionUsage_HardFallback_NoOAuth(t *testing.T) {
	t.Parallel()

	db := newTestStore(t)
	const id = "fallback-sess"
	seedClaudeSession(t, db, id)
	seedAssistantTurn(t, db, id, "asst-1", "claude-sonnet-4-6", 4_000, 4_000_000_000_000)

	// No OAuth body → fetcher is nil → calibration returns false.
	router := newSessionUsageRouter(t, db, "")
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/sessions/" + id + "/usage")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body api.SessionUsageResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.CalibratedFromOAuth {
		t.Errorf("expected CalibratedFromOAuth=false when OAuth absent")
	}
	for name, v := range map[string]float64{
		"next_turn_pct_5h":           body.NextTurnPct5h,
		"compacted_next_turn_pct_5h": body.CompactedNextTurnPct5h,
		"restarted_next_turn_pct_5h": body.RestartedNextTurnPct5h,
		"compact_savings_pct_5h":     body.CompactSavingsPct5h,
		"restart_savings_pct_5h":     body.RestartSavingsPct5h,
	} {
		if v != -1 {
			t.Errorf("%s = %f, want -1 when uncalibrated", name, v)
		}
	}
	// Context fields are still populated from the local data.
	if body.ContextUsed != 4_000 {
		t.Errorf("context_used = %d, want 4000", body.ContextUsed)
	}
	if body.ContextWindow != 1_000_000 {
		t.Errorf("context_window = %d, want 1000000 (sonnet-4 default 1M)", body.ContextWindow)
	}
}

// --- Hard fallback when OAuth utilization is 0 ---

func TestSessionUsage_HardFallback_ZeroUtilization(t *testing.T) {
	t.Parallel()

	db := newTestStore(t)
	const id = "zero-util-sess"
	seedClaudeSession(t, db, id)
	seedAssistantTurn(t, db, id, "asst-1", "claude-sonnet-4-6", 4_000, 4_000_000_000_000)

	const oauth = `{"five_hour": {"utilization": 0.0, "resets_at": ""}}`

	router := newSessionUsageRouter(t, db, oauth)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/sessions/" + id + "/usage")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	var body api.SessionUsageResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.CalibratedFromOAuth {
		t.Errorf("expected CalibratedFromOAuth=false on zero utilization")
	}
	if body.NextTurnPct5h != -1 {
		t.Errorf("next_turn_pct_5h = %f, want -1", body.NextTurnPct5h)
	}
}

// --- Empty session: no assistant messages ---

func TestSessionUsage_EmptySession(t *testing.T) {
	t.Parallel()

	db := newTestStore(t)
	const id = "empty-sess"
	seedClaudeSession(t, db, id)

	router := newSessionUsageRouter(t, db, "")
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/sessions/" + id + "/usage")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	var body api.SessionUsageResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.ContextUsed != 0 {
		t.Errorf("context_used = %d, want 0 for empty session", body.ContextUsed)
	}
	if body.ContextFillPct != 0 {
		t.Errorf("context_fill_pct = %f, want 0", body.ContextFillPct)
	}
	// Falls back to session.Model (claude-sonnet-4-6) → 1M default.
	if body.ContextWindow != 1_000_000 {
		t.Errorf("context_window = %d, want 1000000 (sonnet-4 default 1M)", body.ContextWindow)
	}
}

// --- Empty session WITH calibration: NextTurnPct5h reflects floor ---

func TestSessionUsage_EmptySession_CalibratedReflectsFloor(t *testing.T) {
	t.Parallel()

	db := newTestStore(t)
	const id = "empty-calib-sess"
	seedClaudeSession(t, db, id)
	// Seed one OTHER session's message so usage.Compute has a non-zero
	// 5h Window for Claude. The session-under-test stays empty.
	const otherID = "other-sess"
	seedClaudeSession(t, db, otherID)
	seedAssistantTurn(t, db, otherID, "other-asst", "claude-sonnet-4-6", 4_000, 4_000_000_000_000)

	const oauth = `{"five_hour": {"utilization": 2.0, "resets_at": ""}}`

	router := newSessionUsageRouter(t, db, oauth)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/sessions/" + id + "/usage")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	var body api.SessionUsageResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.CalibratedFromOAuth {
		t.Fatalf("expected calibration with both empty-session & data in 5h window")
	}
	if body.ContextUsed != 0 {
		t.Errorf("context_used = %d, want 0", body.ContextUsed)
	}
	// nextTurn falls back to SystemPromptFloor (5000) tokens.
	// tokensPerPct = (4000 + 200) / 2 = 2100. NextTurnPct = 5000/2100.
	wantNext := 5000.0 / 2100.0
	if !floatNear(body.NextTurnPct5h, wantNext, 1e-6) {
		t.Errorf("next_turn_pct_5h = %f, want %f", body.NextTurnPct5h, wantNext)
	}
}

func TestSessionUsage_CodexUsesLatestTokenCountContextSnapshot(t *testing.T) {
	t.Parallel()

	db := newTestStore(t)
	const id = "codex-context-sess"
	rawPath := filepath.Join(t.TempDir(), "rollout.jsonl")
	lines := strings.Join([]string{
		`{"timestamp":"2026-05-06T10:00:01.000Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":12000},"model_context_window":258400}}}`,
		`{"timestamp":"2026-05-06T10:00:02.000Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":216326},"model_context_window":258400}}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(rawPath, []byte(lines), 0o600); err != nil {
		t.Fatalf("write codex raw jsonl: %v", err)
	}
	seedCodexSession(t, db, id, rawPath)

	router := newSessionUsageRouter(t, db, "")
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/sessions/" + id + "/usage")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body api.SessionUsageResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.ContextUsed != 216_326 {
		t.Errorf("context_used = %d, want latest last_token_usage input_tokens", body.ContextUsed)
	}
	if body.ContextWindow != 258_400 {
		t.Errorf("context_window = %d, want model_context_window", body.ContextWindow)
	}
	wantFill := float64(216_326) / float64(258_400) * 100.0
	if !floatNear(body.ContextFillPct, wantFill, 1e-9) {
		t.Errorf("context_fill_pct = %f, want %f", body.ContextFillPct, wantFill)
	}
}

// floatNear returns true when |a-b| <= eps.
func floatNear(a, b, eps float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= eps
}

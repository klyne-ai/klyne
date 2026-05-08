package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/klyne-ai/klyne/internal/ai"
	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/api/handlers"
	"github.com/klyne-ai/klyne/internal/store"
)

// fakeProvider is a deterministic ai.Provider stub. It records call count
// and returns a configurable Chat response.
type fakeProvider struct {
	name      string
	chatText  string
	chatErr   error
	callCount int64
}

func (f *fakeProvider) Name() string { return f.name }
func (f *fakeProvider) Models() []string {
	return []string{"fake-model"}
}
func (f *fakeProvider) Chat(_ context.Context, _ ai.ChatRequest) (*ai.ChatResponse, error) {
	atomic.AddInt64(&f.callCount, 1)
	if f.chatErr != nil {
		return nil, f.chatErr
	}
	return &ai.ChatResponse{Text: f.chatText, Model: "fake-model"}, nil
}
func (f *fakeProvider) Embed(_ context.Context, _ ai.EmbedRequest) (*ai.EmbedResponse, error) {
	return nil, ai.ErrUnsupported
}

// newBreakAdviceRouter mounts only the break-advice route, wired to the
// caller-supplied factory.
func newBreakAdviceRouter(t *testing.T, db *store.DB, factory handlers.AIProviderFactory) http.Handler {
	t.Helper()
	h := handlers.NewBreakAdviceHandler(handlers.BreakAdviceDeps{
		DB:        db,
		AIFactory: factory,
	})
	r := chi.NewRouter()
	r.Get(api.RouteSessionBreakAdvice, h.Get)
	return r
}

// --- 404 ---

func TestBreakAdvice_NotFound(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	router := newBreakAdviceRouter(t, db, nil)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/sessions/no-such/break-advice")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

// --- No provider available ---

func TestBreakAdvice_NoProvider_ReturnsUnavailable(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	const id = "noprov-sess"
	seedSession(t, db, id)

	// nil factory → unavailable.
	router := newBreakAdviceRouter(t, db, nil)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/sessions/" + id + "/break-advice")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body api.BreakAdviceResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Verdict != api.BreakAdviceUnavailable {
		t.Errorf("verdict = %q, want %q", body.Verdict, api.BreakAdviceUnavailable)
	}
	if body.Reason == "" {
		t.Error("expected non-empty reason on unavailable")
	}
}

func TestBreakAdvice_FactoryReturnsFalse_ReturnsUnavailable(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	const id = "factoryno-sess"
	seedSession(t, db, id)

	factory := func() (ai.Provider, string, bool) { return nil, "", false }

	router := newBreakAdviceRouter(t, db, factory)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/sessions/" + id + "/break-advice")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	var body api.BreakAdviceResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Verdict != api.BreakAdviceUnavailable {
		t.Errorf("verdict = %q, want %q", body.Verdict, api.BreakAdviceUnavailable)
	}
}

// --- Happy path: AI returns valid JSON ---

func TestBreakAdvice_HappyPath_ParsesVerdict(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	const id = "happy-sess"
	seedSession(t, db, id)
	seedMessage(t, db, id, "m1", 1000)
	seedMessage(t, db, id, "m2", 2000)

	prov := &fakeProvider{
		name:     "fake",
		chatText: `{"verdict":"start_fresh","reason":"the user finished their refactor","topic":"refactor cost engine"}`,
	}
	factory := func() (ai.Provider, string, bool) { return prov, "fake-model", true }

	router := newBreakAdviceRouter(t, db, factory)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/sessions/" + id + "/break-advice")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body api.BreakAdviceResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Verdict != api.BreakAdviceStartFresh {
		t.Errorf("verdict = %q, want %q", body.Verdict, api.BreakAdviceStartFresh)
	}
	if body.Reason != "the user finished their refactor" {
		t.Errorf("reason = %q", body.Reason)
	}
	if body.SuggestedTopic != "refactor cost engine" {
		t.Errorf("suggested_topic = %q", body.SuggestedTopic)
	}
	if body.Provider != "fake" {
		t.Errorf("provider = %q, want fake", body.Provider)
	}
	if body.Model != "fake-model" {
		t.Errorf("model = %q, want fake-model", body.Model)
	}
	if body.CachedAt == 0 {
		t.Error("expected non-zero cached_at")
	}
}

// --- Cache: two consecutive calls hit AI exactly once ---

func TestBreakAdvice_CachesAcrossCalls(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	const id = "cache-sess"
	seedSession(t, db, id)

	prov := &fakeProvider{
		name:     "fake",
		chatText: `{"verdict":"continue","reason":"keep going","topic":""}`,
	}
	factory := func() (ai.Provider, string, bool) { return prov, "fake-model", true }

	router := newBreakAdviceRouter(t, db, factory)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	for i := 0; i < 3; i++ {
		resp, err := http.Get(srv.URL + "/sessions/" + id + "/break-advice")
		if err != nil {
			t.Fatalf("GET %d: %v", i, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("iteration %d expected 200, got %d", i, resp.StatusCode)
		}
	}

	if got := atomic.LoadInt64(&prov.callCount); got != 1 {
		t.Errorf("expected exactly 1 AI call across 3 requests, got %d", got)
	}
}

// --- Malformed AI JSON degrades to "continue" ---

func TestBreakAdvice_MalformedJSON_FallsBackToContinue(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	const id = "malformed-sess"
	seedSession(t, db, id)

	prov := &fakeProvider{
		name:     "fake",
		chatText: "I cannot say. (no JSON here)",
	}
	factory := func() (ai.Provider, string, bool) { return prov, "fake-model", true }

	router := newBreakAdviceRouter(t, db, factory)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/sessions/" + id + "/break-advice")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 (graceful degrade), got %d", resp.StatusCode)
	}

	var body api.BreakAdviceResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Verdict != api.BreakAdviceContinue {
		t.Errorf("verdict = %q, want %q on malformed JSON", body.Verdict, api.BreakAdviceContinue)
	}
}

// --- Markdown-fenced JSON still parses (real-world LLM behaviour) ---

func TestBreakAdvice_MarkdownFencedJSON_Parses(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	const id = "fenced-sess"
	seedSession(t, db, id)

	prov := &fakeProvider{
		name:     "fake",
		chatText: "```json\n{\"verdict\":\"compact\",\"reason\":\"mid-task, big context\",\"topic\":\"\"}\n```",
	}
	factory := func() (ai.Provider, string, bool) { return prov, "fake-model", true }

	router := newBreakAdviceRouter(t, db, factory)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/sessions/" + id + "/break-advice")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	var body api.BreakAdviceResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Verdict != api.BreakAdviceCompact {
		t.Errorf("verdict = %q, want %q", body.Verdict, api.BreakAdviceCompact)
	}
}

// --- Provider error → graceful continue (NOT 500) ---

func TestBreakAdvice_ProviderErrors_GracefulContinue(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	const id = "err-sess"
	seedSession(t, db, id)

	prov := &fakeProvider{name: "fake", chatErr: ai.ErrProviderUnavailable}
	factory := func() (ai.Provider, string, bool) { return prov, "fake-model", true }

	router := newBreakAdviceRouter(t, db, factory)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/sessions/" + id + "/break-advice")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 even on provider error, got %d", resp.StatusCode)
	}

	var body api.BreakAdviceResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Verdict != api.BreakAdviceContinue {
		t.Errorf("verdict = %q, want %q", body.Verdict, api.BreakAdviceContinue)
	}
}

// --- Cache TTL: explicit clock injection ---

func TestBreakAdvice_CacheTTLExpires(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	const id = "ttl-sess"
	seedSession(t, db, id)

	prov := &fakeProvider{
		name:     "fake",
		chatText: `{"verdict":"continue","reason":"keep going","topic":""}`,
	}
	factory := func() (ai.Provider, string, bool) { return prov, "fake-model", true }

	t0 := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	clock := t0
	now := func() time.Time { return clock }

	h := handlers.NewBreakAdviceHandler(handlers.BreakAdviceDeps{
		DB:        db,
		AIFactory: factory,
		NowFn:     now,
	})
	r := chi.NewRouter()
	r.Get(api.RouteSessionBreakAdvice, h.Get)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	// First call.
	doGet := func() {
		resp, err := http.Get(srv.URL + "/sessions/" + id + "/break-advice")
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		resp.Body.Close()
	}

	doGet()
	if c := atomic.LoadInt64(&prov.callCount); c != 1 {
		t.Fatalf("call count after first request = %d, want 1", c)
	}

	// Within TTL → cache hit.
	clock = t0.Add(5 * time.Minute)
	doGet()
	if c := atomic.LoadInt64(&prov.callCount); c != 1 {
		t.Errorf("call count within TTL = %d, want 1 (cached)", c)
	}

	// Past TTL → cache miss → new AI call.
	clock = t0.Add(15 * time.Minute)
	doGet()
	if c := atomic.LoadInt64(&prov.callCount); c != 2 {
		t.Errorf("call count past TTL = %d, want 2", c)
	}
}

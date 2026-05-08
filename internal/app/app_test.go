package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/klyne-ai/klyne/internal/ai"
	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/connectors"
)

func aiChatReqStub() ai.ChatRequest  { return ai.ChatRequest{Model: "x"} }
func aiEmbedReqStub() ai.EmbedRequest { return ai.EmbedRequest{Model: "x", Input: "y"} }

// newTestConfig returns a *config.Config rooted at t.TempDir(): the DB
// lives in <tempdir>/agentdeck.db and connectors point at empty
// subdirectories so Watch returns no events. Browser is suppressed.
func newTestConfig(t *testing.T) *config.Config {
	t.Helper()
	tmp := t.TempDir()

	// Stub HOME so config.* functions resolve under tmp.
	orig := config.HomeDir
	config.HomeDir = func() (string, error) { return tmp, nil }
	t.Cleanup(func() { config.HomeDir = orig })

	cfg := config.Defaults()
	cfg.Server.Addr = "127.0.0.1:0" // random port
	cfg.Paths.DB = filepath.Join(tmp, "agentdeck.db")
	cfg.Paths.PricingOverride = filepath.Join(tmp, "pricing.json")
	cfg.Connectors.Claude.Root = filepath.Join(tmp, "claude")
	cfg.Connectors.Codex.Root = filepath.Join(tmp, "codex")
	return cfg
}

func TestBuildOnly_AllDepsWired(t *testing.T) {
	cfg := newTestConfig(t)
	a, err := BuildOnly(cfg)
	if err != nil {
		t.Fatalf("BuildOnly: %v", err)
	}
	defer func() { _ = a.Stop(context.Background()) }()

	if a.cfg == nil {
		t.Fatal("cfg nil")
	}
	if a.db == nil {
		t.Fatal("db nil")
	}
	if a.cost == nil {
		t.Fatal("cost nil")
	}
	if a.hub == nil {
		t.Fatal("hub nil")
	}
	if a.runner == nil {
		t.Fatal("runner nil")
	}
	if len(a.connectors) != 2 {
		t.Fatalf("expected 2 connectors (claude+codex), got %d", len(a.connectors))
	}
	if len(a.mounters) < 2 {
		t.Fatalf("expected at least 2 default mounters, got %d", len(a.mounters))
	}
	// Schema must be migrated to a non-zero version.
	v, err := a.db.SchemaVersion(context.Background())
	if err != nil {
		t.Fatalf("schema version: %v", err)
	}
	if v <= 0 {
		t.Fatalf("schema version = %d, want > 0", v)
	}
}

func TestBuildOnly_NilCfg(t *testing.T) {
	if _, err := BuildOnly(nil); err == nil {
		t.Fatal("BuildOnly(nil) should error")
	}
}

func TestApp_StartStop_Roundtrip(t *testing.T) {
	cfg := newTestConfig(t)
	a, err := BuildOnly(cfg)
	if err != nil {
		t.Fatalf("BuildOnly: %v", err)
	}
	a.SuppressBrowser = true

	ctx, cancel := context.WithCancel(context.Background())
	startErr := make(chan error, 1)
	go func() {
		startErr <- a.Start(ctx)
	}()

	// Wait for the listener to bind.
	if !waitForAddr(a, 2*time.Second) {
		cancel()
		t.Fatal("server did not bind within 2s")
	}

	// Hit /healthz on the bound address.
	resp, err := http.Get("http://" + a.Addr() + "/healthz")
	if err != nil {
		cancel()
		t.Fatalf("GET /healthz: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf("/healthz status = %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// Cancel ctx and assert Start returns within 2s.
	cancel()
	select {
	case err := <-startErr:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Start returned %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Start did not return within 3s after cancel")
	}
}

func TestApp_Stop_Idempotent(t *testing.T) {
	cfg := newTestConfig(t)
	a, err := BuildOnly(cfg)
	if err != nil {
		t.Fatalf("BuildOnly: %v", err)
	}
	if err := a.Stop(context.Background()); err != nil {
		t.Fatalf("Stop #1: %v", err)
	}
	if err := a.Stop(context.Background()); err != nil {
		t.Fatalf("Stop #2 (idempotent): %v", err)
	}
}

func TestApp_OpenBrowserCalled(t *testing.T) {
	cfg := newTestConfig(t)
	a, err := BuildOnly(cfg)
	if err != nil {
		t.Fatalf("BuildOnly: %v", err)
	}

	var called atomic.Bool
	var capturedURL atomic.Value
	a.OpenBrowserFunc = func(url string) error {
		called.Store(true)
		capturedURL.Store(url)
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	startErr := make(chan error, 1)
	go func() {
		startErr <- a.Start(ctx)
	}()

	if !waitForAddr(a, 2*time.Second) {
		cancel()
		t.Fatal("server did not bind within 2s")
	}

	// Allow Start to schedule the browser open call.
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) && !called.Load() {
		time.Sleep(20 * time.Millisecond)
	}

	cancel()
	<-startErr

	if !called.Load() {
		t.Fatal("OpenBrowserFunc was not invoked")
	}
	url, _ := capturedURL.Load().(string)
	if !strings.HasPrefix(url, "http://127.0.0.1:") {
		t.Fatalf("OpenBrowser url = %q, want http://127.0.0.1:<port>", url)
	}
}

func TestApp_SuppressBrowser(t *testing.T) {
	cfg := newTestConfig(t)
	a, err := BuildOnly(cfg)
	if err != nil {
		t.Fatalf("BuildOnly: %v", err)
	}
	a.SuppressBrowser = true

	var called atomic.Bool
	a.OpenBrowserFunc = func(_ string) error {
		called.Store(true)
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	startErr := make(chan error, 1)
	go func() { startErr <- a.Start(ctx) }()

	if !waitForAddr(a, 2*time.Second) {
		cancel()
		t.Fatal("server did not bind")
	}
	time.Sleep(200 * time.Millisecond)
	cancel()
	<-startErr

	if called.Load() {
		t.Fatal("OpenBrowserFunc invoked despite SuppressBrowser=true")
	}
}

func TestApp_AppendMounter(t *testing.T) {
	cfg := newTestConfig(t)
	a, err := BuildOnly(cfg)
	if err != nil {
		t.Fatalf("BuildOnly: %v", err)
	}
	a.SuppressBrowser = true

	a.AppendMounter(&pingMounter{})
	a.AppendMounter(nil) // nil tolerated

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = a.Start(ctx) }()

	if !waitForAddr(a, 2*time.Second) {
		cancel()
		t.Fatal("server did not bind")
	}
	defer cancel()

	resp, err := http.Get("http://" + a.Addr() + "/__ping")
	if err != nil {
		t.Fatalf("GET /__ping: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "pong" {
		t.Fatalf("body = %q, want pong", body)
	}
}

func TestApp_EmbeddedUI_Served(t *testing.T) {
	cfg := newTestConfig(t)
	a, err := BuildOnly(cfg)
	if err != nil {
		t.Fatalf("BuildOnly: %v", err)
	}
	a.SuppressBrowser = true

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = a.Start(ctx) }()

	if !waitForAddr(a, 2*time.Second) {
		cancel()
		t.Fatal("server did not bind")
	}
	defer cancel()

	resp, err := http.Get("http://" + a.Addr() + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	// The UI may be a stub directory in test, but the handler must respond
	// either 200 (index served) or 404 (no index built). Anything else is
	// a wiring bug.
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /: status = %d, want 200 or 404", resp.StatusCode)
	}
}

func TestApp_LoopbackOnly(t *testing.T) {
	cfg := newTestConfig(t)
	a, err := BuildOnly(cfg)
	if err != nil {
		t.Fatalf("BuildOnly: %v", err)
	}
	a.SuppressBrowser = true

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = a.Start(ctx) }()
	if !waitForAddr(a, 2*time.Second) {
		cancel()
		t.Fatal("server did not bind")
	}
	defer cancel()

	// 127.0.0.1 must be allowed.
	resp, err := http.Get("http://" + a.Addr() + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/healthz status = %d, want 200", resp.StatusCode)
	}

	// The bound address itself is on 127.0.0.1, so we cannot easily
	// originate a non-loopback request inside this test. Asserting the
	// loopback middleware at all is W7 territory; here we only confirm
	// the listener bound to a loopback address.
	if !strings.HasPrefix(a.Addr(), "127.") {
		t.Fatalf("listener addr %q, want 127.* (loopback)", a.Addr())
	}
}

func TestDashboardURL(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"127.0.0.1:7878", "http://127.0.0.1:7878"},
		{"0.0.0.0:1234", "http://127.0.0.1:1234"},
		{":7878", "http://127.0.0.1:7878"},
	}
	for _, tc := range cases {
		if got := dashboardURL(tc.in); got != tc.want {
			t.Errorf("dashboardURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestApp_ProcessRawEvent_Claude smoke-tests the writer goroutine by
// feeding it a minimal Claude JSONL line. Since the writer derives the
// connector from the path, we only verify the happy-path side effects:
// session row created, message row inserted, MsgNew event published.
func TestApp_ProcessRawEvent_Claude(t *testing.T) {
	cfg := newTestConfig(t)
	a, err := BuildOnly(cfg)
	if err != nil {
		t.Fatalf("BuildOnly: %v", err)
	}
	defer func() { _ = a.Stop(context.Background()) }()

	// Subscribe to hub before the write so MsgNew is captured.
	ch, unsub := a.hub.Subscribe()
	defer unsub()

	byName := make(map[string]connectors.Connector, len(a.connectors))
	for _, c := range a.connectors {
		byName[c.Name()] = c
	}

	// Minimal JSONL line — Claude format. Use the simplest user-text shape.
	line := []byte(`{"type":"user","uuid":"m1","sessionId":"s1","timestamp":"2026-05-06T10:00:00.000Z","cwd":"/proj","message":{"role":"user","content":"hi"}}`)
	ev := connectors.RawEvent{
		Path: "/tmp/.claude/projects/-proj/abc.jsonl",
		Line: line,
		Ts:   time.Now().UnixMilli(),
	}
	a.processRawEvent(context.Background(), ev, byName)

	// Assert we got a MsgNew within a short window.
	select {
	case got, ok := <-ch:
		if !ok {
			t.Fatal("hub closed unexpectedly")
		}
		if got.EventName() != api.EventMsgNew {
			t.Fatalf("event = %s, want %s", got.EventName(), api.EventMsgNew)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected MsgNew on hub")
	}

	// Assert the session row exists.
	dbReader := a.db.Read()
	var count int
	if err := dbReader.QueryRow("SELECT COUNT(*) FROM sessions WHERE id = ?", "s1").Scan(&count); err != nil {
		t.Fatalf("query session: %v", err)
	}
	if count != 1 {
		t.Fatalf("session count = %d, want 1", count)
	}
}

// TestApp_ProcessRawEvent_NoConnectorMatch covers the path where neither
// "/.claude/" nor "/.codex/" appears in the file path AND multiple
// connectors are configured — the writer logs and skips.
func TestApp_ProcessRawEvent_NoConnectorMatch(t *testing.T) {
	cfg := newTestConfig(t)
	a, err := BuildOnly(cfg)
	if err != nil {
		t.Fatalf("BuildOnly: %v", err)
	}
	defer func() { _ = a.Stop(context.Background()) }()

	byName := make(map[string]connectors.Connector, len(a.connectors))
	for _, c := range a.connectors {
		byName[c.Name()] = c
	}

	a.processRawEvent(context.Background(), connectors.RawEvent{
		Path: "/totally/random/path",
		Line: []byte(`{"type":"user"}`),
	}, byName)
	// No assertion — just covering the early return path.
}

// TestApp_ProcessRawEvent_BadJSON exercises the parse-error path.
func TestApp_ProcessRawEvent_BadJSON(t *testing.T) {
	cfg := newTestConfig(t)
	a, err := BuildOnly(cfg)
	if err != nil {
		t.Fatalf("BuildOnly: %v", err)
	}
	defer func() { _ = a.Stop(context.Background()) }()

	byName := make(map[string]connectors.Connector, len(a.connectors))
	for _, c := range a.connectors {
		byName[c.Name()] = c
	}

	a.processRawEvent(context.Background(), connectors.RawEvent{
		Path: "/tmp/.claude/projects/x/foo.jsonl",
		Line: []byte(`{not json`),
	}, byName)
}

func TestApp_AccessorsExposeFields(t *testing.T) {
	cfg := newTestConfig(t)
	a, err := New(cfg) // exercises New (alias for BuildOnly)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = a.Stop(context.Background()) }()

	if a.DB() != a.db {
		t.Fatal("DB() does not match internal db")
	}
	if a.Hub() != a.hub {
		t.Fatal("Hub() does not match internal hub")
	}
	if a.Cost() != a.cost {
		t.Fatal("Cost() does not match internal cost")
	}
	if a.Cfg() != a.cfg {
		t.Fatal("Cfg() does not match internal cfg")
	}
}

func TestExpandHomeOrDefault(t *testing.T) {
	tmp := t.TempDir()
	orig := config.HomeDir
	config.HomeDir = func() (string, error) { return tmp, nil }
	defer func() { config.HomeDir = orig }()

	cases := []struct {
		in, def, want string
	}{
		{"", "/fallback", "/fallback"},
		{"/abs/path", "", "/abs/path"},
		{"~", "", tmp},
		{"~/foo", "", filepath.Join(tmp, "foo")},
		{"~bob/foo", "", "~bob/foo"}, // unchanged: not "~/" prefix
	}
	for _, tc := range cases {
		if got := expandHomeOrDefault(tc.in, tc.def); got != tc.want {
			t.Errorf("expandHomeOrDefault(%q, %q) = %q, want %q",
				tc.in, tc.def, got, tc.want)
		}
	}
}

func TestBuildProvider_Switch(t *testing.T) {
	cases := []struct {
		name   string
		expect string
	}{
		{"anthropic", "anthropic"},
		{"openai", "openai"},
		{"gemini", "gemini"},
		{"ollama", "ollama"},
		{"unknown", ""},
	}
	for _, tc := range cases {
		got := buildProvider(tc.name)
		if tc.expect == "" {
			if got != nil {
				t.Errorf("buildProvider(%q) = %v, want nil", tc.name, got)
			}
			continue
		}
		if got == nil {
			t.Errorf("buildProvider(%q) returned nil", tc.name)
			continue
		}
		if got.Name() != tc.expect {
			t.Errorf("buildProvider(%q).Name() = %q, want %q", tc.name, got.Name(), tc.expect)
		}
	}
}

func TestNoopProvider(t *testing.T) {
	p := noopProvider{}
	if p.Name() != "noop" {
		t.Fatal("noopProvider.Name != noop")
	}
	if _, err := p.Chat(context.Background(), aiChatReqStub()); err == nil {
		t.Fatal("noopProvider.Chat must return error")
	}
	if _, err := p.Embed(context.Background(), aiEmbedReqStub()); err == nil {
		t.Fatal("noopProvider.Embed must return error")
	}
	if got := p.Models(); got != nil {
		t.Fatalf("Models = %v, want nil", got)
	}
}

func TestPickConnectorForPath(t *testing.T) {
	cfg := newTestConfig(t)
	a, err := BuildOnly(cfg)
	if err != nil {
		t.Fatalf("BuildOnly: %v", err)
	}
	defer func() { _ = a.Stop(context.Background()) }()

	byName := make(map[string]connectors.Connector, len(a.connectors))
	for _, c := range a.connectors {
		byName[c.Name()] = c
	}

	got := pickConnectorForPath(byName, "/Users/x/.claude/projects/foo.jsonl")
	if got == nil || got.Name() != "claude" {
		t.Fatalf("claude path -> %v", got)
	}
	got = pickConnectorForPath(byName, "/Users/x/.codex/sessions/2026/01/foo.jsonl")
	if got == nil || got.Name() != "codex" {
		t.Fatalf("codex path -> %v", got)
	}
	got = pickConnectorForPath(byName, "/totally/random/path")
	if got != nil {
		t.Fatalf("ambiguous path with two connectors -> %v, want nil", got)
	}

	// Single-connector fallback.
	one := map[string]connectors.Connector{"claude": byName["claude"]}
	got = pickConnectorForPath(one, "/random/path")
	if got == nil || got.Name() != "claude" {
		t.Fatalf("single fallback -> %v", got)
	}

	// Empty map → nil.
	got = pickConnectorForPath(map[string]connectors.Connector{}, "/nothing")
	if got != nil {
		t.Fatalf("empty map -> %v, want nil", got)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// waitForAddr polls a.Addr() until non-empty or timeout, returning true if
// the server bound in time.
func waitForAddr(a *App, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if a.Addr() != "" {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return a.Addr() != ""
}

// pingMounter is a test-only RouterMounter that registers GET /__ping.
type pingMounter struct{}

func (pingMounter) Mount(r chi.Router) {
	r.Get("/__ping", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("pong"))
	})
}

// Compile-time check the test mounter still satisfies api.RouterMounter.
var _ api.RouterMounter = pingMounter{}

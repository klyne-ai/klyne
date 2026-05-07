// Package app is the composition root for the agentdeck daemon.
//
// It wires every Wave 1+2 module — store, connectors, cost, SSE hub,
// HTTP router, AI runner — into a single App value with a Start/Stop
// lifecycle. The cobra commands in cmd/agentdeck/ are thin shells that
// delegate to App.
//
// # Wiring topology (spec §11)
//
//	fsnotify watchers (W4 + W5)
//	        │
//	        ▼
//	  RawEvent channel
//	        │
//	        ▼
//	  Writer goroutine ── store.UpsertSession + store.InsertMessage (W2)
//	        │
//	        ▼
//	  Hub.Publish(MsgNew) (W8)
//	        │
//	        ├──► EventSource clients
//	        │
//	        └──► AI tasks.Runner (W11)
//	                   │
//	                   ▼
//	             Selector → Provider (W10) → InsertSummary (W3)
//	                   │
//	                   ▼
//	             Hub.Publish(SummaryReady)
//
//	HTTP server on cfg.Server.Addr (default 127.0.0.1:7878)
//	   ├──► chi routes (W7 handlers + W8 SSE + W15 wizard mounters)
//	   └──► /             → embedded ui/build/ (SPA fallback)
//
// # Concurrency invariants
//
//  1. The writer goroutine is the ONLY caller of store.InsertMessage.
//     RawEvent → Parse → InsertMessage runs serially per RawEvent so the
//     SQLite write handle (MaxOpenConns=1) never observes concurrent writes.
//  2. **Summaries run in their own goroutine pool — never inline with the
//     writer.** Risk R7 mitigation: a summarizer that hangs (e.g. on a
//     slow Anthropic 5xx) MUST NOT back-pressure ingestion. tasks.Runner
//     enforces this with its own buffered work channel.
//  3. Hub.Publish is non-blocking; slow SSE subscribers are dropped, never
//     block the writer.
//
// # Mounter injection (W15 wizard / restore handlers)
//
// W15's wizard and restore endpoints are owned by a parallel agent. Their
// package may not exist when this code compiles. The composition root
// exposes App.AppendMounter so the parent (cmd/agentdeck or a test) can
// register additional api.RouterMounter implementations BEFORE Start is
// called. After Start, mounters are frozen.
//
// W12 wires the W7 handlers mounter and the W8 EventsMounter automatically;
// W15 mounters are appended externally.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	rootembed "github.com/mohitpatell/agentdeck"
	"github.com/mohitpatell/agentdeck/internal/ai"
	"github.com/mohitpatell/agentdeck/internal/ai/providers"
	"github.com/mohitpatell/agentdeck/internal/ai/tasks"
	"github.com/mohitpatell/agentdeck/internal/api"
	"github.com/mohitpatell/agentdeck/internal/api/handlers"
	"github.com/mohitpatell/agentdeck/internal/config"
	"github.com/mohitpatell/agentdeck/internal/connectors"
	"github.com/mohitpatell/agentdeck/internal/connectors/claude"
	"github.com/mohitpatell/agentdeck/internal/connectors/codex"
	"github.com/mohitpatell/agentdeck/internal/cost"
	"github.com/mohitpatell/agentdeck/internal/store"
)

// shutdownGrace is the maximum time Stop will wait for the HTTP server to
// drain before forcing a close. Spec §6 acceptance: ≤ 2s total exit.
const shutdownGrace = 2 * time.Second

// rawEventBuffer is the size of the RawEvent channel between connectors and
// the writer goroutine. 256 events absorbs typical fsnotify bursts without
// blocking the watcher.
const rawEventBuffer = 256

// App is the assembled daemon. Construct with BuildOnly (tests) or New
// (production), then call Start.
type App struct {
	// Configuration & runtime deps
	cfg    *config.Config
	logger *slog.Logger

	// Storage
	db *store.DB

	// Cost engine (pricing.json + optional override)
	cost *cost.Engine

	// SSE broadcast hub
	hub *api.Hub

	// AI runner (summaries / titles)
	runner *tasks.Runner

	// Connectors selected by config (Claude, Codex, …)
	connectors []connectors.Connector

	// Mounters for the chi router. Built-in W7 handlers and W8 events are
	// appended at construction; W15 mounters are appended externally via
	// AppendMounter.
	mounters []api.RouterMounter

	// HTTP server (nil before Start)
	server *http.Server

	// OpenBrowserFunc launches the dashboard URL on Start. Tests inject
	// NoopOpenBrowser; production uses DefaultOpenBrowser.
	OpenBrowserFunc OpenBrowserFunc

	// SuppressBrowser, when true, skips OpenBrowserFunc entirely. Useful
	// for headless CI, doctor-only invocations, and integration tests.
	SuppressBrowser bool

	// startedMu guards startedAddr. The HTTP listener may bind to a
	// random port (Addr ":0") in tests; the actual address is recorded
	// here so callers can target the live server.
	startedMu   sync.Mutex
	startedAddr string

	// stopOnce ensures Stop is idempotent.
	stopOnce sync.Once

	// ingestWG tracks watcher + writer goroutines.
	ingestWG sync.WaitGroup
	cancel   context.CancelFunc

	// runnerStarted is true once Start has booted the AI runner. Stop
	// should NOT call runner.Stop unless Start ran — runner.Stop blocks
	// on its internal done channel which is only closed by Start.
	runnerStarted bool
}

// BuildOnly assembles the App without starting servers, watchers, or the AI
// runner. Returned App fields are populated and ready for inspection in
// tests; call Start on it for production behavior.
//
// All filesystem-side initialisation (DB open, migrations, connector
// construction) happens here so any fatal misconfig fails before Start.
func BuildOnly(cfg *config.Config) (*App, error) {
	if cfg == nil {
		return nil, errors.New("app: BuildOnly: nil cfg")
	}

	logger := slog.Default()

	// 1. Open the SQLite store — this also runs migrations.
	dbPath := expandHomeOrDefault(cfg.Paths.DB, config.DBPath())
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return nil, fmt.Errorf("app: mkdir for DB %s: %w", dbPath, err)
	}
	db, err := store.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("app: open store: %w", err)
	}

	// 2. Cost engine (embedded pricing + optional override).
	costCfg := cloneCfgWithExpandedPaths(cfg)
	costEngine, err := cost.New(costCfg)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("app: cost engine: %w", err)
	}

	// 3. SSE hub.
	hub := api.NewHub(api.WithLogger(logger))

	// 4. Connectors selected by config.
	conns := buildConnectors(cfg)

	// 5. AI runner — provider/model selected by selector against detected
	// providers. If no provider is available the runner is still constructed
	// (with a no-op provider) so Start cannot panic; summaries simply error
	// out and log. The detection happens once at build time.
	runner, err := buildRunner(cfg, db, hub, logger)
	if err != nil {
		_ = db.Close()
		hub.Close()
		return nil, fmt.Errorf("app: build ai runner: %w", err)
	}

	// 6. Default mounters — W7 handlers + W8 events.
	mounters := []api.RouterMounter{
		handlers.NewMounter(handlers.Deps{
			DB:     db,
			Cfg:    cfg,
			Cost:   costEngine,
			Logger: logger,
		}),
		&handlers.EventsMounter{Hub: hub},
	}

	app := &App{
		cfg:             cfg,
		logger:          logger,
		db:              db,
		cost:            costEngine,
		hub:             hub,
		runner:          runner,
		connectors:      conns,
		mounters:        mounters,
		OpenBrowserFunc: DefaultOpenBrowser,
	}
	return app, nil
}

// New is BuildOnly with the production browser opener. Provided so the
// public surface mirrors the brief; production callers should prefer New
// over BuildOnly.
func New(cfg *config.Config) (*App, error) {
	a, err := BuildOnly(cfg)
	if err != nil {
		return nil, err
	}
	return a, nil
}

// AppendMounter registers an additional api.RouterMounter to be installed
// when Start builds the chi router. Must be called before Start. After
// Start, mounters are frozen.
//
// W15 (wizard/restore) is the canonical caller from cmd/agentdeck:
//
//	app.AppendMounter(wizard.NewMounter(...))
//	app.AppendMounter(restore.NewMounter(...))
func (a *App) AppendMounter(m api.RouterMounter) {
	if m == nil {
		return
	}
	a.mounters = append(a.mounters, m)
}

// DB returns the underlying *store.DB. Exposed for cmd-level diagnostics
// (doctor) and for W15 mounters that need direct DB access.
func (a *App) DB() *store.DB { return a.db }

// Hub returns the SSE broadcast hub. Exposed for tests and for W15
// mounters that publish events directly.
func (a *App) Hub() *api.Hub { return a.hub }

// Cost returns the cost engine. Exposed for diagnostics and W15.
func (a *App) Cost() *cost.Engine { return a.cost }

// Cfg returns the runtime config. Exposed for diagnostics and W15.
func (a *App) Cfg() *config.Config { return a.cfg }

// Addr returns the bound listening address (empty before Start succeeds).
// Useful for tests that bind to ":0" and need the assigned port.
func (a *App) Addr() string {
	a.startedMu.Lock()
	defer a.startedMu.Unlock()
	return a.startedAddr
}

// Start runs the daemon: starts watchers, the AI runner, the HTTP server
// bound to cfg.Server.Addr, and (unless SuppressBrowser) opens the browser
// on the dashboard URL.
//
// Start blocks until ctx is cancelled (e.g. SIGINT/SIGTERM). On return it
// calls Stop to flush the writer queue and close the DB; total shutdown
// completes within shutdownGrace.
func (a *App) Start(ctx context.Context) error {
	if a == nil {
		return errors.New("app: Start: nil receiver")
	}

	// Spawn an internal context so Stop can cancel watchers and runner.
	ctx, a.cancel = context.WithCancel(ctx)
	defer a.cancel()

	// 1. Listen on the configured address (or :0 in tests).
	addr := a.cfg.Server.Addr
	if addr == "" {
		addr = "127.0.0.1:7878"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("app: listen %s: %w", addr, err)
	}
	a.startedMu.Lock()
	a.startedAddr = ln.Addr().String()
	a.startedMu.Unlock()

	// 2. Build router with all mounters + UI fallback.
	router, err := a.buildRouter()
	if err != nil {
		_ = ln.Close()
		return fmt.Errorf("app: build router: %w", err)
	}
	a.server = &http.Server{
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// 3. Boot ingestion (watchers + writer) and the AI runner.
	rawEvents := make(chan connectors.RawEvent, rawEventBuffer)
	for _, c := range a.connectors {
		c := c
		a.ingestWG.Add(1)
		go func() {
			defer a.ingestWG.Done()
			if werr := c.Watch(ctx, rawEvents); werr != nil && !errors.Is(werr, context.Canceled) {
				a.logger.Error("connector watch ended",
					slog.String("connector", c.Name()),
					slog.Any("error", werr))
			}
		}()
	}

	// Writer goroutine fan-in: parse RawEvent → upsert session → insert
	// message → publish MsgNew. Single goroutine — see invariant #1.
	a.ingestWG.Add(1)
	go func() {
		defer a.ingestWG.Done()
		a.runWriter(ctx, rawEvents)
	}()

	// AI runner goroutine. Documented invariant #2: summaries NEVER run
	// inline with the writer — tasks.Runner has its own work pool.
	if a.runner != nil {
		a.runnerStarted = true
		a.ingestWG.Add(1)
		go func() {
			defer a.ingestWG.Done()
			if rerr := a.runner.Start(ctx); rerr != nil && !errors.Is(rerr, context.Canceled) {
				a.logger.Error("ai runner ended", slog.Any("error", rerr))
			}
		}()
	}

	// 4. Serve HTTP in a goroutine and wait for ctx.
	serveErr := make(chan error, 1)
	go func() {
		a.logger.Info("agentdeck listening", slog.String("addr", a.startedAddr))
		err := a.server.Serve(ln)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		} else {
			serveErr <- nil
		}
	}()

	// 5. Open the browser unless suppressed.
	if !a.SuppressBrowser && a.OpenBrowserFunc != nil {
		// Only open when the configured addr is a loopback address. If
		// someone overrode the bind to an external interface (not
		// supported in v1) we still print the URL but skip the launch.
		url := dashboardURL(a.startedAddr)
		if oerr := a.OpenBrowserFunc(url); oerr != nil {
			a.logger.Warn("open browser failed",
				slog.String("url", url),
				slog.Any("error", oerr))
		}
	}

	// 6. Wait for shutdown signal or HTTP error.
	select {
	case <-ctx.Done():
	case err := <-serveErr:
		if err != nil {
			a.logger.Error("http server error", slog.Any("error", err))
		}
	}

	// Trigger Stop with the original (potentially already-cancelled) ctx
	// so we get a deterministic graceful path. We do NOT propagate ctx
	// cancellation into Stop's shutdown ctx — Stop has its own grace timer.
	stopCtx, stopCancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer stopCancel()
	return a.Stop(stopCtx)
}

// Stop flushes the writer queue, closes the DB, and exits within
// shutdownGrace. Idempotent.
//
// Order of operations:
//  1. Cancel watchers + runner + writer (via context).
//  2. http.Server.Shutdown (graceful, max shutdownGrace).
//  3. Wait for ingestWG (writer drains in-flight RawEvents).
//  4. Stop AI runner (waits for in-flight summaries to finish).
//  5. Close hub + DB.
func (a *App) Stop(ctx context.Context) error {
	var firstErr error
	a.stopOnce.Do(func() {
		// Cancel internal ctx so watchers and runner unwind.
		if a.cancel != nil {
			a.cancel()
		}

		// HTTP shutdown (best effort within ctx grace window).
		if a.server != nil {
			if err := a.server.Shutdown(ctx); err != nil &&
				!errors.Is(err, context.Canceled) &&
				!errors.Is(err, http.ErrServerClosed) {
				firstErr = fmt.Errorf("app: server shutdown: %w", err)
			}
		}

		// Wait for ingestion goroutines (watchers + writer + runner).
		// The writer drains rawEvents fully before exiting, ensuring no
		// in-flight messages are lost (R7 mitigation).
		done := make(chan struct{})
		go func() {
			a.ingestWG.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-ctx.Done():
			a.logger.Warn("ingest goroutines did not exit within grace window")
		}

		// Stop the AI runner only if Start booted it. Calling runner.Stop
		// when Start never ran would block forever on its internal done
		// channel.
		if a.runner != nil && a.runnerStarted {
			_ = a.runner.Stop()
		}

		if a.hub != nil {
			a.hub.Close()
		}
		if a.db != nil {
			if err := a.db.Close(); err != nil && firstErr == nil {
				firstErr = fmt.Errorf("app: db close: %w", err)
			}
		}
	})
	return firstErr
}

// runWriter is the single goroutine that fans in RawEvents from all
// connectors, parses them via the originating connector, upserts the
// session row, inserts the message, and publishes the resulting
// MsgNew event. It returns when rawEvents is closed OR ctx is cancelled.
func (a *App) runWriter(ctx context.Context, rawEvents <-chan connectors.RawEvent) {
	// Map connector name → connector. Used to dispatch Parse for each
	// RawEvent based on which file path it came from.
	byName := make(map[string]connectors.Connector, len(a.connectors))
	for _, c := range a.connectors {
		byName[c.Name()] = c
	}

	for {
		select {
		case <-ctx.Done():
			// Drain channel best-effort then return.
			a.drainRawEvents(rawEvents, byName)
			return
		case ev, ok := <-rawEvents:
			if !ok {
				return
			}
			a.processRawEvent(ctx, ev, byName)
		}
	}
}

// drainRawEvents reads any pending events from the channel without
// blocking and processes them with a fresh background context. Called by
// runWriter on shutdown so we don't lose buffered messages.
func (a *App) drainRawEvents(ch <-chan connectors.RawEvent, byName map[string]connectors.Connector) {
	drainCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace/2)
	defer cancel()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return
			}
			a.processRawEvent(drainCtx, ev, byName)
		default:
			return
		}
	}
}

// processRawEvent parses ev using the connector that produced it, ensures
// the session row exists, inserts the message, and publishes MsgNew.
//
// Errors are logged and swallowed: a single malformed line should never
// take the daemon down.
func (a *App) processRawEvent(ctx context.Context, ev connectors.RawEvent, byName map[string]connectors.Connector) {
	conn := pickConnectorForPath(byName, ev.Path)
	if conn == nil {
		a.logger.Warn("no connector matched RawEvent path",
			slog.String("path", ev.Path))
		return
	}

	msg, err := conn.Parse(ev.Line, ev.Path)
	if err != nil {
		a.logger.Debug("parse failed; skipping line",
			slog.String("connector", conn.Name()),
			slog.String("path", ev.Path),
			slog.Any("error", err))
		return
	}
	if msg == nil {
		return
	}

	// Compute cost via the engine if the parser did not populate it.
	if msg.CostUSD == 0 && (msg.TokensIn > 0 || msg.TokensOut > 0) && msg.Model != "" {
		msg.CostUSD = a.cost.Cost(msg.TokensIn, msg.TokensOut, msg.Model)
	}

	// Ensure the session exists. UpsertSession is idempotent and cheap.
	sess := &connectors.Session{
		ID:          msg.SessionID,
		CLI:         msg.CLI,
		ProjectPath: msg.ProjectPath,
		StartedAt:   msg.Ts,
		LastMsgAt:   msg.Ts,
		Model:       msg.Model,
		Status:      connectors.SessionStatusActive,
		RawPath:     ev.Path,
	}
	if err := store.UpsertSession(ctx, a.db, sess); err != nil {
		a.logger.Error("upsert session failed",
			slog.String("session_id", msg.SessionID),
			slog.Any("error", err))
		return
	}

	if err := store.InsertMessage(ctx, a.db, msg); err != nil {
		a.logger.Debug("insert message failed (likely duplicate)",
			slog.String("message_id", msg.ID),
			slog.Any("error", err))
		return
	}

	a.hub.Publish(api.MsgNewEvent(api.MsgNew{
		SessionID: msg.SessionID,
		MessageID: msg.ID,
		Ts:        msg.Ts,
		Role:      string(msg.Role),
		Model:     msg.Model,
		TokensIn:  msg.TokensIn,
		TokensOut: msg.TokensOut,
		CostUSD:   msg.CostUSD,
	}))
}

// buildRouter constructs the chi router with every mounter attached and
// the embedded UI mounted as a default handler.
//
// Default handler routing: chi dispatches matched routes first; the
// NotFound handler catches anything that did not match a registered
// route, which we delegate to the SPA fallback static handler.
func (a *App) buildRouter() (http.Handler, error) {
	router := api.NewRouter(api.Deps{
		Logger:   a.logger,
		Mounters: a.mounters,
	})

	// Wrap the chi router so unmatched paths fall through to the static UI.
	uiHandler, err := NewStaticFSHandler(rootembed.UI)
	if err != nil {
		return nil, fmt.Errorf("app: build static handler: %w", err)
	}

	// Use a small composite handler: try the chi router first; if it
	// would 404, serve the UI instead. We rely on go-chi/chi/v5's
	// (chi.Router).NotFound hook, set on the embedded mux below.
	if cr, ok := router.(interface {
		NotFound(http.HandlerFunc)
	}); ok {
		cr.NotFound(uiHandler.ServeHTTP)
	}

	return router, nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// buildConnectors instantiates the v1 connectors selected by config.
// Disabled connectors are skipped. Returns at least zero connectors —
// the daemon is still useful in "doctor" mode without any.
func buildConnectors(cfg *config.Config) []connectors.Connector {
	var out []connectors.Connector

	if cfg.Connectors.Claude.Enabled {
		root := expandHomeOrDefault(cfg.Connectors.Claude.Root, "")
		out = append(out, claude.New(root))
	}
	if cfg.Connectors.Codex.Enabled {
		root := expandHomeOrDefault(cfg.Connectors.Codex.Root, "")
		out = append(out, codex.New(root))
	}
	return out
}

// buildRunner builds the AI runner, picking provider+model via the
// selector against detected providers. Returns a runner whose Start can
// be called even when no provider is available; in that case summaries
// will fail with ai.ErrNoCredential and the runner logs the error.
func buildRunner(cfg *config.Config, db *store.DB, hub *api.Hub, logger *slog.Logger) (*tasks.Runner, error) {
	detectCtx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	available := providers.DetectAvailable(detectCtx)

	override := ""
	if cfg.AI.SummaryModel != "" && cfg.AI.SummaryModel != config.AIModelAuto && cfg.AI.SummaryModel != config.AIModelOff {
		override = cfg.AI.SummaryModel
	}

	choice, err := ai.Pick(ai.TaskSummarize, available, override)
	if err != nil {
		// No provider available — return a runner with the noop provider
		// so Start is non-fatal. Summaries will log and skip.
		logger.Warn("ai: no provider available; summaries will be skipped",
			slog.Any("error", err))
		return tasks.NewRunner(tasks.RunnerDeps{
			DB:       db,
			Hub:      hub,
			Provider: noopProvider{},
			Model:    "",
			Logger:   logger,
		}), nil
	}

	prov := buildProvider(choice.Provider)
	if prov == nil {
		logger.Warn("ai: provider constructor missing; using noop",
			slog.String("provider", choice.Provider))
		prov = noopProvider{}
	}

	return tasks.NewRunner(tasks.RunnerDeps{
		DB:       db,
		Hub:      hub,
		Provider: prov,
		Model:    choice.Model,
		Logger:   logger,
	}), nil
}

// buildProvider wires concrete provider constructors. Returns nil for
// unknown names — the caller falls back to noopProvider.
func buildProvider(name string) ai.Provider {
	switch name {
	case "anthropic":
		return providers.NewAnthropic(providers.AnthropicOpts{})
	case "openai":
		return providers.NewOpenAI(providers.OpenAIOpts{})
	case "gemini":
		return providers.NewGemini(providers.GeminiOpts{})
	case "ollama":
		return providers.NewOllama(providers.OllamaOpts{})
	default:
		return nil
	}
}

// noopProvider is a safe-fail placeholder used when no real provider can
// be detected. Both Chat and Embed return ai.ErrNoCredential — callers
// log and skip rather than crash.
type noopProvider struct{}

func (noopProvider) Name() string { return "noop" }
func (noopProvider) Chat(_ context.Context, _ ai.ChatRequest) (*ai.ChatResponse, error) {
	return nil, ai.ErrNoCredential
}
func (noopProvider) Embed(_ context.Context, _ ai.EmbedRequest) (*ai.EmbedResponse, error) {
	return nil, ai.ErrUnsupported
}
func (noopProvider) Models() []string { return nil }

// pickConnectorForPath returns the connector whose Name matches a heuristic
// segment in path. Both v1 connectors carry their own root, so any path
// inside that root identifies the producer:
//
//	".../.claude/projects/..." → claude
//	".../.codex/sessions/..."  → codex
//
// Falls back to the only configured connector if exactly one is enabled.
func pickConnectorForPath(byName map[string]connectors.Connector, path string) connectors.Connector {
	switch {
	case strings.Contains(path, "/.claude/"):
		return byName["claude"]
	case strings.Contains(path, "/.codex/"):
		return byName["codex"]
	}
	if len(byName) == 1 {
		for _, c := range byName {
			return c
		}
	}
	return nil
}

// expandHomeOrDefault expands a leading "~" in p to the user's home
// directory. If p is empty, returns def. If home cannot be resolved,
// returns p verbatim (best effort).
func expandHomeOrDefault(p, def string) string {
	if p == "" {
		return def
	}
	if !strings.HasPrefix(p, "~") {
		return p
	}
	home, err := config.HomeDir()
	if err != nil {
		return p
	}
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

// cloneCfgWithExpandedPaths returns a copy of cfg with all "~" paths
// expanded. The cost engine reads PricingOverride and is the only caller
// that needs absolute paths; callers that build their own subsystems
// (e.g. store.Open) should call expandHomeOrDefault directly.
func cloneCfgWithExpandedPaths(cfg *config.Config) *config.Config {
	if cfg == nil {
		return nil
	}
	out := *cfg
	out.Paths.DB = expandHomeOrDefault(cfg.Paths.DB, config.DBPath())
	out.Paths.PricingOverride = expandHomeOrDefault(cfg.Paths.PricingOverride, config.PricingOverridePath())
	out.Connectors.Claude.Root = expandHomeOrDefault(cfg.Connectors.Claude.Root, "")
	out.Connectors.Codex.Root = expandHomeOrDefault(cfg.Connectors.Codex.Root, "")
	return &out
}

// dashboardURL builds the http://addr URL the browser is launched against.
// addr is the bound listener address (host:port); host "" / "::" / "0.0.0.0"
// is rewritten to 127.0.0.1.
func dashboardURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://" + addr
	}
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s:%s", host, port)
}

package handlers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/mohitpatell/agentdeck/internal/api"
	"github.com/mohitpatell/agentdeck/internal/api/handlers"
	"github.com/mohitpatell/agentdeck/internal/config"
	"github.com/mohitpatell/agentdeck/internal/connectors"
	"github.com/mohitpatell/agentdeck/internal/cost"
	"github.com/mohitpatell/agentdeck/internal/store"
)


// setHomeDir replaces config.HomeDir for the duration of the test.
// NOTE: must NOT be combined with t.Parallel() because it mutates a global.
func setHomeDir(t *testing.T, dir string) {
	t.Helper()
	orig := config.HomeDir
	config.HomeDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { config.HomeDir = orig })
}

// newTestStore opens a fresh SQLite DB in a temp dir with all migrations applied.
func newTestStore(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// newCostEngine builds a cost.Engine with no pricing override (uses embedded table).
func newCostEngine(t *testing.T) *cost.Engine {
	t.Helper()
	eng, err := cost.New(config.Defaults())
	if err != nil {
		t.Fatalf("cost.New: %v", err)
	}
	return eng
}

// newTestRouter builds a bare chi router with the W7 handlers mounter wired
// (no loopback middleware so parallel tests work cleanly).
func newTestRouter(t *testing.T, db *store.DB) http.Handler {
	t.Helper()
	mounter := handlers.NewMounter(handlers.Deps{
		DB:   db,
		Cfg:  config.Defaults(),
		Cost: newCostEngine(t),
	})
	r := chi.NewRouter()
	mounter.Mount(r)
	return r
}

// seedSession inserts a session into the store.
func seedSession(t *testing.T, db *store.DB, id string) *connectors.Session {
	t.Helper()
	s := &connectors.Session{
		ID:          id,
		CLI:         connectors.CLIClaude,
		ProjectPath: "/tmp/proj",
		StartedAt:   1000,
		LastMsgAt:   2000,
		Status:      connectors.SessionStatusIdle,
	}
	if err := store.UpsertSession(context.Background(), db, s); err != nil {
		t.Fatalf("upsert session %q: %v", id, err)
	}
	return s
}

func seedMessage(t *testing.T, db *store.DB, sessionID, msgID string, ts int64) *connectors.Message {
	t.Helper()
	m := &connectors.Message{
		ID:        msgID,
		SessionID: sessionID,
		CLI:       connectors.CLIClaude,
		Role:      connectors.RoleUser,
		Content:   "hello from " + msgID,
		Ts:        ts,
	}
	if err := store.InsertMessage(context.Background(), db, m); err != nil {
		t.Fatalf("insert message %q: %v", msgID, err)
	}
	return m
}

// --- /sessions ---

func TestSessions_List_HappyPath(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	seedSession(t, db, "sess-1")
	seedSession(t, db, "sess-2")

	router := newTestRouter(t, db)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/sessions")
	if err != nil {
		t.Fatalf("GET /sessions: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body api.SessionListResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Sessions) != 2 {
		t.Errorf("expected 2 sessions, got %d", len(body.Sessions))
	}
}

func TestSessions_List_InvalidLimit(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	router := newTestRouter(t, db)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	for _, query := range []string{"?limit=abc", "?limit=0", "?limit=501", "?limit=-1"} {
		q := query
		t.Run(q, func(t *testing.T) {
			resp, err := http.Get(srv.URL + "/sessions" + q)
			if err != nil {
				t.Fatalf("GET: %v", err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("expected 400, got %d", resp.StatusCode)
			}
		})
	}
}

func TestSessions_List_InvalidBefore(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	router := newTestRouter(t, db)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/sessions?before=notanumber")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestSessions_List_Pagination(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)

	for i := 1; i <= 5; i++ {
		s := &connectors.Session{
			ID:          fmt.Sprintf("sess-%d", i),
			CLI:         connectors.CLIClaude,
			ProjectPath: "/tmp/proj",
			StartedAt:   int64(i * 100),
			LastMsgAt:   int64(i * 1000),
			Status:      connectors.SessionStatusIdle,
		}
		if err := store.UpsertSession(context.Background(), db, s); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	router := newTestRouter(t, db)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/sessions?limit=2")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	var body api.SessionListResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(body.Sessions))
	}
	if body.NextBefore == 0 {
		t.Error("expected non-zero NextBefore cursor")
	}
}

// --- /sessions/{id} ---

func TestSessions_Get_HappyPath(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	seedSession(t, db, "get-sess-1")

	router := newTestRouter(t, db)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/sessions/get-sess-1")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body api.SessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Session.ID != "get-sess-1" {
		t.Errorf("expected session id 'get-sess-1', got %q", body.Session.ID)
	}
}

func TestSessions_Get_NotFound(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	router := newTestRouter(t, db)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/sessions/nonexistent-id")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

// --- /sessions/{id}/messages ---

func TestSessions_Messages_HappyPath(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	seedSession(t, db, "msg-sess-1")
	seedMessage(t, db, "msg-sess-1", "msg-1", 1000)
	seedMessage(t, db, "msg-sess-1", "msg-2", 2000)

	router := newTestRouter(t, db)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/sessions/msg-sess-1/messages")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body api.MessageListResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(body.Messages))
	}
}

func TestSessions_Messages_SessionNotFound(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	router := newTestRouter(t, db)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/sessions/does-not-exist/messages")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestSessions_Messages_InvalidLimit(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	seedSession(t, db, "limit-sess")

	router := newTestRouter(t, db)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/sessions/limit-sess/messages?limit=abc")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestSessions_Messages_Pagination(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	seedSession(t, db, "page-sess")
	for i := 1; i <= 5; i++ {
		seedMessage(t, db, "page-sess", fmt.Sprintf("pmsg-%d", i), int64(i*100))
	}

	router := newTestRouter(t, db)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/sessions/page-sess/messages?limit=3")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	var body api.MessageListResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(body.Messages))
	}
	if body.NextBefore == 0 {
		t.Error("expected non-zero NextBefore cursor on full page")
	}
}

// --- /sessions/{id}/summary ---

func TestSessions_Summary_HappyPath(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	seedSession(t, db, "sum-sess-1")

	if err := store.InsertSummary(context.Background(), db, &store.Summary{
		SessionID: "sum-sess-1",
		Text:      "A great session summary.",
		Model:     "claude-sonnet-4-6",
		TS:        3000,
	}); err != nil {
		t.Fatalf("insert summary: %v", err)
	}

	router := newTestRouter(t, db)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/sessions/sum-sess-1/summary")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body api.SummaryResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Text != "A great session summary." {
		t.Errorf("unexpected summary text: %q", body.Text)
	}
}

func TestSessions_Summary_SessionNotFound(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	router := newTestRouter(t, db)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/sessions/no-such-session/summary")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestSessions_Summary_NoSummary(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	seedSession(t, db, "no-summary-sess")

	router := newTestRouter(t, db)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/sessions/no-summary-sess/summary")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 when no summary, got %d", resp.StatusCode)
	}
}

// --- loopback middleware (via full router) ---

// buildFullRouter builds the full API router with loopback middleware
// for tests that check the middleware layer.
// NOTE: Not run in parallel because it mutates config.HomeDir.
func buildFullRouter(t *testing.T) http.Handler {
	t.Helper()
	db := newTestStore(t)
	setHomeDir(t, t.TempDir())

	cfg := config.Defaults()
	cfg.Paths.PricingOverride = ""

	eng, err := cost.New(cfg)
	if err != nil {
		t.Fatalf("cost.New: %v", err)
	}

	mounter := handlers.NewMounter(handlers.Deps{
		DB:   db,
		Cfg:  cfg,
		Cost: eng,
	})

	return api.NewRouter(api.Deps{
		Mounters: []api.RouterMounter{mounter},
	})
}

// TestLoopback_Rejects_NonLoopback verifies non-loopback IPs get HTTP 403.
// Sequential because buildFullRouter mutates config.HomeDir.
func TestLoopback_Rejects_NonLoopback(t *testing.T) {
	router := buildFullRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.RemoteAddr = "10.0.0.1:1234"

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("non-loopback should get 403, got %d", w.Code)
	}
}

// TestLoopback_Allows_Loopback verifies 127.0.0.1 passes the loopback check.
// Sequential because buildFullRouter mutates config.HomeDir.
func TestLoopback_Allows_Loopback(t *testing.T) {
	router := buildFullRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.RemoteAddr = "127.0.0.1:1234"

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code == http.StatusForbidden {
		t.Errorf("loopback address should not get 403, got %d", w.Code)
	}
}

// TestNewRouter_AllRoutesPresent verifies all W7-owned routes are registered.
// For resource-bearing routes (sessions/:id etc.) we use a real seeded ID.
// Sequential because buildFullRouter mutates config.HomeDir.
func TestNewRouter_AllRoutesPresent(t *testing.T) {
	db := newTestStore(t)
	setHomeDir(t, t.TempDir())

	cfg := config.Defaults()
	cfg.Paths.PricingOverride = ""
	eng, _ := cost.New(cfg)

	// Seed a session so ID-based routes return 200/404-resource, not 404-route.
	const sessID = "route-test-sess"
	seedSession(t, db, sessID)

	mounter := handlers.NewMounter(handlers.Deps{DB: db, Cfg: cfg, Cost: eng})
	router := api.NewRouter(api.Deps{Mounters: []api.RouterMounter{mounter}})
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	// For routes with URL params, we use the seeded session ID.
	// For /sessions/:id/summary we expect 404-resource (no summary), NOT 404-route.
	routes := []struct {
		method        string
		path          string
		allowedStatus []int // any of these is OK (means route IS registered)
	}{
		{http.MethodGet, "/sessions", []int{200}},
		{http.MethodGet, "/sessions/" + sessID, []int{200}},
		{http.MethodGet, "/sessions/" + sessID + "/messages", []int{200}},
		{http.MethodGet, "/sessions/" + sessID + "/summary", []int{404}}, // no summary — but route IS registered
		{http.MethodGet, "/search?q=test", []int{200}},
		{http.MethodGet, "/cost/summary", []int{200}},
		{http.MethodGet, "/settings", []int{200}},
		{http.MethodGet, "/healthz", []int{200}},
	}

	for _, tc := range routes {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req, err := http.NewRequest(tc.method, srv.URL+tc.path, nil)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			resp, err := (&http.Client{}).Do(req)
			if err != nil {
				t.Fatalf("do request: %v", err)
			}
			resp.Body.Close()

			for _, allowed := range tc.allowedStatus {
				if resp.StatusCode == allowed {
					return
				}
			}
			// A chi 404 for unmatched pattern (Method Not Allowed = 405, no pattern = 404).
			// If we get a status NOT in the allowed list, route might be missing.
			// But we only FAIL if we get a truly unexpected 404.
			// The summary route returning 404 is expected (allowed).
			// If we get 404 for a route not in the "allowed 404" list, that's a bug.
			t.Errorf("%s %s returned %d (allowed: %v): route may not be registered",
				tc.method, tc.path, resp.StatusCode, tc.allowedStatus)
		})
	}
}

// TestRegisterMounter_RoutesAttached verifies that a mounter registered via
// Deps.Mounters actually causes its route to be reachable on the router.
// Sequential because it uses the full api.NewRouter (which uses setHomeDir indirectly).
func TestRegisterMounter_RoutesAttached(t *testing.T) {
	db := newTestStore(t)
	setHomeDir(t, t.TempDir())

	cfg := config.Defaults()
	cfg.Paths.PricingOverride = ""

	eng, _ := cost.New(cfg)

	mainMounter := handlers.NewMounter(handlers.Deps{
		DB:   db,
		Cfg:  cfg,
		Cost: eng,
	})

	// Use a simple inline mounter.
	fakeMount := &fakeMounterImpl{path: "/test", status: http.StatusTeapot}

	router := api.NewRouter(api.Deps{
		Mounters: []api.RouterMounter{mainMounter, fakeMount},
	})

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/test")
	if err != nil {
		t.Fatalf("GET /test: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTeapot {
		t.Errorf("expected 418 Teapot from fake mounter, got %d", resp.StatusCode)
	}
}

// fakeMounterImpl is a simple RouterMounter for testing W8/W15-style registration.
type fakeMounterImpl struct {
	path   string
	status int
}

func (f *fakeMounterImpl) Mount(r chi.Router) {
	r.Get(f.path, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(f.status)
	})
}

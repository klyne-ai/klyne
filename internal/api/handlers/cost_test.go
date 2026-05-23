package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/api/handlers"
	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/store"
)

func newCostRouter(t *testing.T, db *store.DB) http.Handler {
	t.Helper()
	r := chi.NewRouter()
	h := handlers.NewCostHandler(db, newCostEngine(t))
	r.Get(api.RouteCostSummary, h.Summary)
	return r
}

func seedCostData(t *testing.T, db *store.DB) {
	t.Helper()
	// Two sessions in different projects.
	for _, id := range []string{"csess-1", "csess-2"} {
		s := &connectors.Session{
			ID:          id,
			CLI:         connectors.CLIClaude,
			ProjectPath: "/proj/" + id,
			StartedAt:   1000,
			LastMsgAt:   2000,
			Status:      connectors.SessionStatusIdle,
		}
		if err := store.UpsertSession(context.Background(), db, s); err != nil {
			t.Fatalf("upsert session: %v", err)
		}
		msg := &connectors.Message{
			ID:        "cmsg-" + id,
			SessionID: id,
			CLI:       connectors.CLIClaude,
			Role:      connectors.RoleAssistant,
			Content:   "response",
			TokensIn:  100,
			TokensOut: 200,
			Model:     "claude-3-5-haiku-20241022",
			Ts:        2000,
		}
		if err := store.InsertMessage(context.Background(), db, msg); err != nil {
			t.Fatalf("insert message: %v", err)
		}
	}
}

func TestCost_Summary_HappyPath_Model(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	seedCostData(t, db)

	srv := httptest.NewServer(newCostRouter(t, db))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/cost/summary?group=model")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body api.CostSummaryResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Group != api.CostGroupModel {
		t.Errorf("expected group model, got %q", body.Group)
	}
	if body.Buckets == nil {
		t.Error("expected non-nil buckets")
	}
}

func TestCost_Summary_HappyPath_Session(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	seedCostData(t, db)

	srv := httptest.NewServer(newCostRouter(t, db))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/cost/summary?group=session")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body api.CostSummaryResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Buckets) != 2 {
		t.Errorf("expected 2 session buckets, got %d", len(body.Buckets))
	}
}

func TestCost_Summary_HappyPath_Project(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	seedCostData(t, db)

	srv := httptest.NewServer(newCostRouter(t, db))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/cost/summary?group=project")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body api.CostSummaryResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Buckets) != 2 {
		t.Errorf("expected 2 project buckets, got %d", len(body.Buckets))
	}
}

func TestCost_Summary_HappyPath_Day(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	seedCostData(t, db)

	srv := httptest.NewServer(newCostRouter(t, db))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/cost/summary?group=day")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body api.CostSummaryResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Group != api.CostGroupDay {
		t.Errorf("expected group day, got %q", body.Group)
	}
}

func TestCost_Summary_DefaultGroup(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)

	srv := httptest.NewServer(newCostRouter(t, db))
	t.Cleanup(srv.Close)

	// No group param — must match the handler's documented default
	// (see cost.go: "default \"day\""). The UI explicitly passes
	// group=day, so the default only matters for ad-hoc API consumers,
	// but locking it down here means future renames break loudly
	// instead of silently shipping a new default.
	resp, err := http.Get(srv.URL + "/cost/summary")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body api.CostSummaryResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Group != api.CostGroupDay {
		t.Errorf("expected default group 'day' (handler default per cost.go docstring), got %q", body.Group)
	}
}

func TestCost_Summary_InvalidGroup(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)

	srv := httptest.NewServer(newCostRouter(t, db))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/cost/summary?group=invalid")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestCost_Summary_InvalidSince(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)

	srv := httptest.NewServer(newCostRouter(t, db))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/cost/summary?since=abc")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

// TestCost_Summary_LoopbackMiddleware verifies loopback enforcement on /cost/summary.
// Sequential: buildFullRouter mutates config.HomeDir.
func TestCost_Summary_LoopbackMiddleware(t *testing.T) {
	router := buildFullRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/cost/summary", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for non-loopback, got %d", w.Code)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/cost/summary", nil)
	req2.RemoteAddr = "127.0.0.1:1234"
	req2.Host = "127.0.0.1:7878" // sameOriginOnly also checks the Host header
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	if w2.Code == http.StatusForbidden {
		t.Errorf("loopback should not be 403, got %d", w2.Code)
	}
}

package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/api/handlers"
	"github.com/klyne-ai/klyne/internal/store"
)

func TestHealth_HappyPath(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)

	r := chi.NewRouter()
	h := handlers.NewHealthHandler(db)
	r.Get(api.RouteHealthz, h.Healthz)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body api.HealthzResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.OK {
		t.Error("expected OK=true")
	}
	if body.SchemaVersion <= 0 {
		t.Errorf("expected SchemaVersion > 0, got %d", body.SchemaVersion)
	}
	if body.Version == "" {
		t.Error("expected non-empty Version")
	}
}

// TestHealth_ClosedDB verifies /healthz returns 503 when the store is unavailable.
func TestHealth_ClosedDB(t *testing.T) {
	t.Parallel()
	// Open a DB, then close it so SchemaVersion fails.
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// Close the DB now — subsequent calls will fail.
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	r := chi.NewRouter()
	h := handlers.NewHealthHandler(db)
	r.Get(api.RouteHealthz, h.Healthz)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 for closed DB, got %d", w.Code)
	}

	var body api.HealthzResponse
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.OK {
		t.Error("expected OK=false for closed DB")
	}
}

// TestHealth_LoopbackMiddleware verifies loopback enforcement on /healthz.
// Sequential: buildFullRouter mutates config.HomeDir.
func TestHealth_LoopbackMiddleware(t *testing.T) {
	router := buildFullRouter(t)

	// Non-loopback → 403.
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for non-loopback, got %d", w.Code)
	}

	// Loopback → not 403.
	req2 := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req2.RemoteAddr = "127.0.0.1:1234"
	req2.Host = "127.0.0.1:7878" // sameOriginOnly also checks the Host header
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	if w2.Code == http.StatusForbidden {
		t.Errorf("loopback should pass, got %d", w2.Code)
	}
}

func TestHealth_ContentType_JSON(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)

	r := chi.NewRouter()
	h := handlers.NewHealthHandler(db)
	r.Get(api.RouteHealthz, h.Healthz)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		t.Error("expected Content-Type header")
	}
}

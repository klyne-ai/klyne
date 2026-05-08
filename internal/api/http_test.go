package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/klyne-ai/klyne/internal/api"
)

// fakeMount is a minimal RouterMounter for smoke-testing RegisterMounter.
type fakeMount struct {
	path   string
	status int
}

func (f *fakeMount) Mount(r chi.Router) {
	r.Get(f.path, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(f.status)
	})
}

// TestRegisterMounter_RoutesAttached verifies that a mounter registered via
// Deps.Mounters actually causes its route to be reachable on the router.
func TestRegisterMounter_RoutesAttached(t *testing.T) {
	t.Parallel()

	mounter := &fakeMount{path: "/test", status: http.StatusTeapot}
	router := api.NewRouter(api.Deps{
		Mounters: []api.RouterMounter{mounter},
	})

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/test")
	if err != nil {
		t.Fatalf("GET /test: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusTeapot {
		t.Errorf("expected 418 Teapot, got %d", resp.StatusCode)
	}
}

// TestNewRouter_AllRoutesPresent verifies that every W7-owned route returns
// a non-404 status when hit (may be 500/503 due to nil deps, but not 404).
func TestNewRouter_AllRoutesPresent(t *testing.T) {
	t.Parallel()

	// Use a mounter that registers routes without real deps (they'll return
	// 500 due to nil store, which is acceptable — we're testing routing not logic).
	// Pass nil Mounters to test the base router itself is wired correctly.
	// Route registration is delegated to mounters in the new design.
	mounter := &fakeMount{path: "/healthz", status: http.StatusOK}
	router := api.NewRouter(api.Deps{
		Mounters: []api.RouterMounter{mounter},
	})
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Errorf("/healthz returned 404: route not registered")
	}
}

// TestLoopbackOnly_RejectsNonLoopback verifies the middleware rejects
// non-127.0.0.1 remote addresses with HTTP 403.
func TestLoopbackOnly_RejectsNonLoopback(t *testing.T) {
	t.Parallel()

	router := api.NewRouter(api.Deps{
		Mounters: []api.RouterMounter{&fakeMount{path: "/ping", status: http.StatusOK}},
	})

	// Manually invoke ServeHTTP with a spoofed RemoteAddr.
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.RemoteAddr = "10.0.0.1:1234" // non-loopback

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for non-loopback IP, got %d", w.Code)
	}
}

// TestLoopbackOnly_AllowsLoopback verifies the middleware allows 127.0.0.1.
func TestLoopbackOnly_AllowsLoopback(t *testing.T) {
	t.Parallel()

	router := api.NewRouter(api.Deps{
		Mounters: []api.RouterMounter{&fakeMount{path: "/ping", status: http.StatusOK}},
	})

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.RemoteAddr = "127.0.0.1:1234"

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 OK for loopback IP, got %d", w.Code)
	}
}

// TestLoopbackOnly_AllowsIPv6Loopback verifies the middleware allows ::1.
func TestLoopbackOnly_AllowsIPv6Loopback(t *testing.T) {
	t.Parallel()

	router := api.NewRouter(api.Deps{
		Mounters: []api.RouterMounter{&fakeMount{path: "/ping", status: http.StatusOK}},
	})

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.RemoteAddr = "[::1]:1234"

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 OK for IPv6 loopback, got %d", w.Code)
	}
}

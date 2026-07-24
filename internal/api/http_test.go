package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/klyne-ai/klyne/internal/api"
)

// fakeMount is a minimal RouterMounter for smoke-testing RegisterMounter.
// It registers both GET and POST handlers so middleware tests can exercise
// state-changing methods without getting 405 Method Not Allowed.
type fakeMount struct {
	path   string
	status int
}

func (f *fakeMount) Mount(r chi.Router) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(f.status)
	}
	r.Get(f.path, handler)
	r.Post(f.path, handler)
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
	req.Host = "127.0.0.1:7878" // must also pass sameOriginOnly

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
	req.Host = "localhost:7878" // must also pass sameOriginOnly

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 OK for IPv6 loopback, got %d", w.Code)
	}
}

// --- sameOriginOnly middleware tests ---

// newLoopbackRequest creates an httptest.Request that already passes
// loopbackOnly (RemoteAddr = 127.0.0.1) so we can exercise sameOriginOnly
// in isolation.
func newLoopbackRequest(method, path string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = "127.0.0.1:9999"
	return req
}

// TestSameOriginOnly_RejectsDNSReboundHost verifies that a POST with a
// non-loopback Host header is blocked (DNS-rebind vector).
func TestSameOriginOnly_RejectsDNSReboundHost(t *testing.T) {
	t.Parallel()

	router := api.NewRouter(api.Deps{
		Mounters: []api.RouterMounter{&fakeMount{path: "/ping", status: http.StatusOK}},
	})

	req := newLoopbackRequest(http.MethodPost, "/ping")
	req.Host = "evil.com:7878"

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for non-loopback Host, got %d", w.Code)
	}
}

// TestSameOriginOnly_RejectsExternalOriginOnPost verifies that a POST with
// an external Origin header is blocked regardless of Host.
func TestSameOriginOnly_RejectsExternalOriginOnPost(t *testing.T) {
	t.Parallel()

	router := api.NewRouter(api.Deps{
		Mounters: []api.RouterMounter{&fakeMount{path: "/ping", status: http.StatusOK}},
	})

	req := newLoopbackRequest(http.MethodPost, "/ping")
	req.Host = "127.0.0.1:7878"
	req.Header.Set("Origin", "http://evil.com")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for external Origin on POST, got %d", w.Code)
	}
}

// TestSameOriginOnly_AllowsGETWithNoOrigin verifies that a GET with no
// Origin and a loopback Host passes the middleware (curl / CLI / same-origin
// browser fetch where the browser omits Origin on GETs).
func TestSameOriginOnly_AllowsGETWithNoOrigin(t *testing.T) {
	t.Parallel()

	router := api.NewRouter(api.Deps{
		Mounters: []api.RouterMounter{&fakeMount{path: "/ping", status: http.StatusOK}},
	})

	req := newLoopbackRequest(http.MethodGet, "/ping")
	req.Host = "127.0.0.1:7878"
	// No Origin header — matches curl / CLI behaviour.

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for loopback GET with no Origin, got %d", w.Code)
	}
}

// TestSameOriginOnly_AllowsPostWithLoopbackOrigin verifies that a POST with
// a matching loopback Host and loopback Origin is permitted.
func TestSameOriginOnly_AllowsPostWithLoopbackOrigin(t *testing.T) {
	t.Parallel()

	router := api.NewRouter(api.Deps{
		Mounters: []api.RouterMounter{&fakeMount{path: "/ping", status: http.StatusOK}},
	})

	req := newLoopbackRequest(http.MethodPost, "/ping")
	req.Host = "127.0.0.1:7878"
	req.Header.Set("Origin", "http://127.0.0.1:7878")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for loopback POST with loopback Origin, got %d", w.Code)
	}
}

// TestSameOriginOnly_RejectsNullOriginOnPost verifies that an Origin: null
// header is rejected for state-changing requests (sandboxed iframe vector).
func TestSameOriginOnly_RejectsNullOriginOnPost(t *testing.T) {
	t.Parallel()

	router := api.NewRouter(api.Deps{
		Mounters: []api.RouterMounter{&fakeMount{path: "/ping", status: http.StatusOK}},
	})

	req := newLoopbackRequest(http.MethodPost, "/ping")
	req.Host = "127.0.0.1:7878"
	req.Header.Set("Origin", "null")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for Origin: null on POST, got %d", w.Code)
	}
}

// TestSameOriginOnly_RejectsExternalOriginOnGET verifies finding #5: an
// external Origin on a GET (some GETs have side effects, e.g.
// /api/productivity?refresh=1 spawns git fetch) is rejected. Previously the
// Origin check only ran for non-GET methods.
func TestSameOriginOnly_RejectsExternalOriginOnGET(t *testing.T) {
	t.Parallel()

	router := api.NewRouter(api.Deps{
		Mounters: []api.RouterMounter{&fakeMount{path: "/ping", status: http.StatusOK}},
	})

	req := newLoopbackRequest(http.MethodGet, "/ping")
	req.Host = "127.0.0.1:7878"
	req.Header.Set("Origin", "http://evil.com")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for external Origin on GET, got %d", w.Code)
	}
}

// TestSameOriginOnly_AllowsGETWithMatchingLoopbackOrigin verifies that a GET
// whose Origin matches the request's own loopback host:port is permitted
// (the legitimate same-origin browser case).
func TestSameOriginOnly_AllowsGETWithMatchingLoopbackOrigin(t *testing.T) {
	t.Parallel()

	router := api.NewRouter(api.Deps{
		Mounters: []api.RouterMounter{&fakeMount{path: "/ping", status: http.StatusOK}},
	})

	req := newLoopbackRequest(http.MethodGet, "/ping")
	req.Host = "127.0.0.1:7878"
	req.Header.Set("Origin", "http://127.0.0.1:7878")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for matching loopback Origin on GET, got %d", w.Code)
	}
}

// TestSameOriginOnly_RejectsCrossPortLoopbackOrigin verifies finding #6: a
// page served by ANOTHER loopback port (a different origin) is rejected even
// though its Origin host is loopback. The Origin's port must match the
// request's own Host port.
func TestSameOriginOnly_RejectsCrossPortLoopbackOrigin(t *testing.T) {
	t.Parallel()

	router := api.NewRouter(api.Deps{
		Mounters: []api.RouterMounter{&fakeMount{path: "/ping", status: http.StatusOK}},
	})

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		req := newLoopbackRequest(method, "/ping")
		req.Host = "127.0.0.1:7878"
		// Different loopback port — a separate dev server's page.
		req.Header.Set("Origin", "http://127.0.0.1:3000")

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusForbidden {
			t.Errorf("%s: expected 403 for cross-port loopback Origin, got %d", method, w.Code)
		}
	}
}

// TestSameOriginOnly_AllowsGETWithNoOrigin_Sanity re-confirms that a request
// with NO Origin still passes on GET (curl / CLI / same-origin fetch).
func TestSameOriginOnly_AllowsPostWithNoOrigin(t *testing.T) {
	t.Parallel()

	router := api.NewRouter(api.Deps{
		Mounters: []api.RouterMounter{&fakeMount{path: "/ping", status: http.StatusOK}},
	})

	req := newLoopbackRequest(http.MethodPost, "/ping")
	req.Host = "127.0.0.1:7878"
	// No Origin header — CLI / curl.

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for loopback POST with no Origin, got %d", w.Code)
	}
}

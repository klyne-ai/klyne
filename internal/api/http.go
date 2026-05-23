// Package api wires the chi HTTP router, middleware stack, and all
// read-only handlers for the klyne daemon.
//
// # RouterMounter — integration contract for W8 (SSE hub) and W15 (wizard/restore)
//
// Sub-systems that need to register routes on the main router must implement
// the RouterMounter interface and pass their implementation to Deps.Mounters.
// W12 wires W8, W7 handlers, and W15 mounters into Deps at startup; neither
// workstream needs to touch this file:
//
//	type RouterMounter interface {
//	    Mount(r chi.Router)
//	}
//
// NewRouter calls m.Mount(r) for each mounter after attaching middleware.
// The ordering of Mounters is preserved, so W8 is attached before W15 when
// Deps.Mounters = []RouterMounter{handlersMounter, sseMounter, wizardMounter}.
//
// # Route registration
//
// Built-in read-only routes (W7) are registered by passing a
// *handlers.Mounter (from internal/api/handlers) as the first entry of
// Deps.Mounters. This avoids an import cycle: http.go does not import
// handlers; the caller (W12 / main) wires both packages together.
package api

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"log/slog"
)

// RouterMounter is implemented by sub-systems that want to register routes
// on the main chi router (W7 handlers, W8 SSE hub, W15 wizard/restore).
//
// W7: use handlers.NewMounter(handlers.Deps{...}) to create the built-in
// mounter and pass it as the first entry in Deps.Mounters.
//
// W8: implement this interface on your SSE hub and append it to Deps.Mounters.
// Your Mount method is called with the fully-configured chi.Router after
// middleware is attached.
//
// W15: same pattern — implement RouterMounter on your restore/wizard
// controller and append it to Deps.Mounters.
type RouterMounter interface {
	Mount(r chi.Router)
}

// Deps is the dependency bundle passed to NewRouter.
// W12 populates all fields and wires Mounters before calling NewRouter.
type Deps struct {
	// Logger is the structured logger. When nil, slog.Default() is used.
	Logger *slog.Logger
	// Mounters holds all sub-systems that register routes on the main router.
	// W12 wires W7 handlers mounter (first), W8 SSE mounter, and W15 here.
	// Each mounter's Mount method is called in order.
	Mounters []RouterMounter
}

// NewRouter builds a chi.Router, attaches middleware, and then calls
// m.Mount(r) for each Deps.Mounters entry in order. The returned
// http.Handler is ready to pass to http.Server.Handler.
//
// Middleware stack (outermost first):
//
//  1. loopbackOnly — rejects non-127.0.0.1 source IPs with HTTP 403.
//  2. sameOriginOnly — rejects requests whose Host is not a loopback
//     hostname, and for state-changing methods also rejects a present
//     non-loopback Origin header. Guards against CSRF + DNS-rebind.
//  3. chi middleware.RequestID — stamps X-Request-Id on every request.
//  4. chi middleware.Logger — structured request logging.
//  5. chi middleware.Recoverer — converts panics to HTTP 500.
func NewRouter(deps Deps) http.Handler {
	r := chi.NewRouter()

	// --- middleware ---
	r.Use(loopbackOnly)
	r.Use(sameOriginOnly)
	r.Use(middleware.RequestID)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// --- all route mounters (W7 handlers, W8 SSE, W15 wizard/restore) ---
	for _, m := range deps.Mounters {
		m.Mount(r)
	}

	return r
}

// loopbackOnly is a chi middleware that refuses requests whose remote IP is
// not the IPv4 or IPv6 loopback address. All other source addresses receive
// HTTP 403 Forbidden.
//
// This enforces the spec §17 constraint that the daemon binds only to
// 127.0.0.1:7878 and rejects non-local clients even if the OS somehow
// routes external traffic to the port.
func loopbackOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			// Malformed RemoteAddr — reject for safety.
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			http.Error(w, "forbidden: loopback-only", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// sameOriginOnly is a chi middleware that guards against CSRF and DNS-rebind
// attacks by validating the Host and Origin headers on every request.
//
// Threat model: a rebound domain pointing at 127.0.0.1 bypasses loopbackOnly
// (which checks RemoteAddr) but can still trigger state-changing endpoints
// from a browser tab on an external page. A combined Host + Origin check
// closes this gap.
//
// Rules:
//  1. The Host header's hostname must be a loopback address (127.0.0.1,
//     localhost, ::1, or any net.IP.IsLoopback() address). Requests with a
//     non-loopback Host — the DNS-rebind vector — are rejected with 403.
//  2. For state-changing methods (anything other than GET / HEAD / OPTIONS),
//     the Origin header, when present, must also resolve to a loopback host.
//     An Origin of "null" is always rejected for state-changing requests.
//     Requests with no Origin are allowed (curl, CLI tools, same-origin
//     fetches where the browser omits Origin).
//
// This middleware is applied after loopbackOnly so RemoteAddr is already
// known to be loopback — these are layered defenses.
func sameOriginOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// --- Rule 1: validate Host header hostname ---
		hostHeader := r.Host // chi preserves r.Host from the request
		if hostHeader == "" {
			// HTTP/1.0 clients may omit Host; allow (loopbackOnly already
			// guards the source IP, and real browsers always send Host).
			next.ServeHTTP(w, r)
			return
		}
		hostName := hostHeader
		if h, _, err := net.SplitHostPort(hostHeader); err == nil {
			hostName = h
		}
		if !isLoopbackHost(hostName) {
			forbiddenOrigin(w)
			return
		}

		// --- Rule 2: validate Origin for state-changing methods ---
		stateChanging := r.Method != http.MethodGet &&
			r.Method != http.MethodHead &&
			r.Method != http.MethodOptions

		if stateChanging {
			origin := strings.TrimSpace(r.Header.Get("Origin"))
			if origin != "" {
				// "null" Origin is sent by sandboxed iframes / redirects and
				// must never be trusted for state-changing requests.
				if origin == "null" {
					forbiddenOrigin(w)
					return
				}
				u, err := url.Parse(origin)
				if err != nil || !isLoopbackHost(u.Hostname()) {
					forbiddenOrigin(w)
					return
				}
			}
		}

		next.ServeHTTP(w, r)
	})
}

// isLoopbackHost returns true when hostname is 127.0.0.1, localhost, ::1, or
// any address that net.IP.IsLoopback() considers loopback.
func isLoopbackHost(hostname string) bool {
	switch hostname {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	ip := net.ParseIP(hostname)
	return ip != nil && ip.IsLoopback()
}

// forbiddenOrigin writes a 403 JSON response for same-origin enforcement
// failures (DNS-rebind / CSRF).
func forbiddenOrigin(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "forbidden_origin"})
}


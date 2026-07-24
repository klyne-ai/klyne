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
//     hostname, and on ALL methods rejects a present Origin header that is
//     non-loopback or targets a different host:port than the request itself.
//     Guards against CSRF (incl. side-effecting GETs + cross-port) + DNS-rebind.
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
// (which checks RemoteAddr) but can still trigger endpoints from a browser
// tab on an external page. Some GET endpoints have side effects
// (GET /api/productivity?refresh=1 spawns `git fetch`), and another loopback
// port's page (a different local dev server) is a distinct origin. A combined
// Host + Origin check closes both gaps.
//
// Rules:
//  1. The Host header's hostname must be a loopback address (127.0.0.1,
//     localhost, ::1, or any net.IP.IsLoopback() address). Requests with a
//     non-loopback Host — the DNS-rebind vector — are rejected with 403.
//  2. On ALL methods (not just state-changing ones — some GETs have side
//     effects), when an Origin header is present it must:
//     a. not be "null" (sandboxed iframes / redirects — never trusted), and
//     b. resolve to a loopback host, and
//     c. match the request's own Host header host:port, so a page served by
//        another loopback PORT is rejected (cross-port CSRF).
//     Requests with NO Origin are allowed (curl, CLI tools, and same-origin
//     fetches where the browser omits Origin) — they cannot be cross-site.
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

		// --- Rule 2: validate Origin (when present) on ALL methods ---
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin != "" {
			// "null" Origin is sent by sandboxed iframes / redirects and
			// must never be trusted.
			if origin == "null" {
				forbiddenOrigin(w)
				return
			}
			u, err := url.Parse(origin)
			if err != nil || !isLoopbackHost(u.Hostname()) {
				forbiddenOrigin(w)
				return
			}
			// Cross-port defense: the Origin must target the same host:port
			// the request was made to (its own Host header). Another loopback
			// port's page is a different origin and must be rejected even
			// though both are "loopback".
			if !originMatchesHost(u, hostHeader, hostName) {
				forbiddenOrigin(w)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// originMatchesHost reports whether the parsed Origin URL targets the same
// host:port as the request's Host header. Loopback hostnames are treated as
// equivalent (127.0.0.1 / localhost / ::1 all name the same loopback server),
// so only the PORT must match — that is the cross-port distinction we care
// about. The default port for the Origin's scheme is used when the Origin
// omits one; the Host header's port defaults to 80 when absent.
func originMatchesHost(o *url.URL, hostHeader, hostName string) bool {
	// Resolve the request's own port from the Host header.
	hostPort := ""
	if _, p, err := net.SplitHostPort(hostHeader); err == nil {
		hostPort = p
	}
	if hostPort == "" {
		hostPort = "80"
	}

	// Resolve the Origin's port, defaulting from its scheme.
	originPort := o.Port()
	if originPort == "" {
		switch o.Scheme {
		case "https":
			originPort = "443"
		default:
			originPort = "80"
		}
	}

	// Both hosts are already known loopback (caller checked isLoopbackHost);
	// only the port distinguishes a same-origin page from a cross-port one.
	_ = hostName
	return originPort == hostPort
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


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
	"net"
	"net/http"

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
//  2. chi middleware.RequestID — stamps X-Request-Id on every request.
//  3. chi middleware.Logger — structured request logging.
//  4. chi middleware.Recoverer — converts panics to HTTP 500.
func NewRouter(deps Deps) http.Handler {
	r := chi.NewRouter()

	// --- middleware ---
	r.Use(loopbackOnly)
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


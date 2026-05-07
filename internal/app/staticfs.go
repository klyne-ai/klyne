// Package app — static file system helper for the embedded UI.
//
// The SvelteKit static build output lives at ui/build/ and is embedded by
// the module-root agentdeck.UI variable (see /embed.go). This file exposes
// a constructor that returns an http.Handler suitable for mounting under
// "/" by App.Start.
//
// Behavior:
//   - GET /                         → serves ui/build/index.html.
//   - GET /<asset path>             → serves ui/build/<asset path> (or 404).
//   - GET /<unknown deep route>     → falls back to ui/build/index.html so
//     SvelteKit's client-side router can handle the path (SPA fallback).
//
// API routes (e.g. /sessions, /events, /healthz, /search, /cost/summary,
// /settings, /wizard/*) are mounted on the chi router BEFORE this handler
// is attached as a default — chi's mux dispatches matched API routes first
// and only delegates to the static handler when no API route matches.
package app

import (
	"errors"
	"io/fs"
	"net/http"
	"strings"
)

// uiSubdir is the path inside the embedded FS where the SvelteKit build
// lives. The embed directive `//go:embed all:ui/build` produces an FS rooted
// one level above the actual files — fs.Sub strips that prefix.
const uiSubdir = "ui/build"

// indexFile is the SvelteKit-built entry point.
const indexFile = "index.html"

// NewStaticFSHandler returns an http.Handler that serves the embedded UI
// rooted at the given embed.FS. SPA fallback is implemented by serving
// indexFile for any GET that does not resolve to a real file.
//
// The provided FS must contain the build/ subtree (i.e. the parent of
// index.html). For the production embed at /embed.go, pass agentdeck.UI.
func NewStaticFSHandler(rootFS fs.FS) (http.Handler, error) {
	sub, err := fs.Sub(rootFS, uiSubdir)
	if err != nil {
		return nil, err
	}
	return &staticHandler{root: sub, fileSrv: http.FileServer(http.FS(sub))}, nil
}

// staticHandler serves files from a fs.FS with SPA fallback.
type staticHandler struct {
	root    fs.FS
	fileSrv http.Handler
}

// ServeHTTP implements http.Handler.
//
// Routing logic:
//  1. The literal "/" maps directly to index.html.
//  2. Any path that resolves to an existing file is served verbatim.
//  3. Otherwise the request is rewritten to "/" so the SPA shell loads.
//     This ensures deep links like /sessions/abc123 work after a refresh.
func (h *staticHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Only GET / HEAD are served from the static FS — anything else falls
	// through to the chi mux's default behavior (typically 405).
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Fast path: explicit root.
	clean := strings.TrimPrefix(r.URL.Path, "/")
	if clean == "" {
		serveIndex(w, r, h.root)
		return
	}

	// Does the path resolve to a real file?
	if fileExists(h.root, clean) {
		h.fileSrv.ServeHTTP(w, r)
		return
	}

	// SPA fallback: anything we don't recognise becomes index.html.
	serveIndex(w, r, h.root)
}

// fileExists reports whether path resolves to a regular file in fsys.
// Directories are reported as "not a file" so directory listings never
// leak into responses.
func fileExists(fsys fs.FS, path string) bool {
	info, err := fs.Stat(fsys, path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// serveIndex writes index.html with the correct Content-Type and 200 status.
// Returns 404 if the index file is somehow missing (e.g. the embed has not
// been built yet).
func serveIndex(w http.ResponseWriter, _ *http.Request, fsys fs.FS) {
	data, err := fs.ReadFile(fsys, indexFile)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			http.Error(w, "ui not built", http.StatusNotFound)
			return
		}
		http.Error(w, "ui read error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

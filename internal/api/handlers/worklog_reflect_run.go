package handlers

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/store"
)

// WorklogReflectRunHandler spawns `claude -p '/klyne:reflect'` as a child
// process for the requested project and returns its combined output.
//
// This is the ONE place in the daemon that intentionally invokes the
// `claude` CLI. It is gated on three checks:
//
//  1. Same-origin: the Origin/Referer header (when present) must point at
//     a 127.0.0.1 / localhost host. This blocks browser drive-by spawns
//     from arbitrary external pages.
//  2. Project-path allowlist: the requested path MUST already exist in
//     the worklog rollup. We never spawn a subprocess in a path the user
//     hasn't already authorised via prior klyne usage.
//  3. Bounded execution: a hard 5-minute context timeout caps any
//     runaway subprocess.
//
// The spawn itself runs the same one-shot the UI previously asked the
// user to copy-paste:
//
//	cd <project_path> && claude -p --permission-mode bypassPermissions '/klyne:reflect'
//
// stdout+stderr are captured and returned in the JSON response so the
// browser can render them inline.
type WorklogReflectRunHandler struct {
	db *store.DB
	// runCmd is the subprocess factory. Tests inject a stub here so
	// the handler can be exercised without a real `claude` binary on
	// the test runner.
	runCmd func(ctx context.Context, projectPath string) ([]byte, error)
}

// NewWorklogReflectRunHandler constructs a handler that shells out via
// the real `claude` CLI. Tests should construct the struct literal
// directly to supply a stub runCmd.
func NewWorklogReflectRunHandler(db *store.DB) *WorklogReflectRunHandler {
	return &WorklogReflectRunHandler{db: db, runCmd: spawnClaudeReflect}
}

// reflectRunRequest is the POST body.
type reflectRunRequest struct {
	ProjectPath string `json:"project_path"`
}

// reflectRunResponse is what the UI renders inline.
type reflectRunResponse struct {
	ProjectPath string `json:"project_path"`
	Status      string `json:"status"` // "ok" | "error" | "timeout"
	Output      string `json:"output"`
	DurationMs  int64  `json:"duration_ms"`
	Error       string `json:"error,omitempty"`
}

// Run handles POST /worklog/reflect/run.
func (h *WorklogReflectRunHandler) Run(w http.ResponseWriter, r *http.Request) {
	// Same-origin / DNS-rebind enforcement is handled globally by the
	// sameOriginOnly middleware in internal/api/http.go. The per-handler
	// sameOriginOK helper below is retained for reference but is no longer
	// called here — the global middleware runs before any route handler.

	var req reflectRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.ProjectPath == "" {
		http.Error(w, "missing project_path", http.StatusBadRequest)
		return
	}

	// Allowlist: the requested path must already appear in the worklog
	// rollup. This prevents the endpoint from being used as a generic
	// shell-spawn vector — only paths klyne has previously indexed
	// (i.e. the user already ran a Claude/Codex session there) are
	// eligible.
	rollup, err := store.ListWorklogRollup(r.Context(), h.db)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	allowed := false
	for _, p := range rollup {
		if p.ProjectPath == req.ProjectPath {
			allowed = true
			break
		}
	}
	if !allowed {
		http.Error(w, "project_path not in worklog rollup", http.StatusForbidden)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	start := time.Now()
	out, runErr := h.runCmd(ctx, req.ProjectPath)
	dur := time.Since(start).Milliseconds()

	resp := reflectRunResponse{
		ProjectPath: req.ProjectPath,
		Output:      string(out),
		DurationMs:  dur,
	}
	switch {
	case runErr == nil:
		resp.Status = "ok"
	case ctx.Err() == context.DeadlineExceeded:
		resp.Status = "timeout"
		resp.Error = "subprocess exceeded 5m budget"
	default:
		resp.Status = "error"
		resp.Error = runErr.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

// spawnClaudeReflect is the production runCmd: shells out to the local
// `claude` CLI in one-shot mode with the reflect slash command.
//
// We invoke `claude -p` directly (not via a shell) so the project path
// cannot be reinterpreted as shell syntax. exec.Cmd.Dir sets the working
// directory for the child process, replacing the previous `cd "$path" &&
// claude …` pattern.
func spawnClaudeReflect(ctx context.Context, projectPath string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "claude",
		"-p",
		"--permission-mode", "bypassPermissions",
		"/klyne:reflect",
	)
	cmd.Dir = projectPath
	return cmd.CombinedOutput()
}

// sameOriginOK returns true when the request either has no Origin/
// Referer header (e.g. curl, fetch from same origin without Origin) or
// when those headers point at a loopback host. The daemon already binds
// to 127.0.0.1; this is belt-and-braces for browser drive-by attempts.
func sameOriginOK(r *http.Request) bool {
	for _, h := range []string{"Origin", "Referer"} {
		v := strings.TrimSpace(r.Header.Get(h))
		if v == "" {
			continue
		}
		u, err := url.Parse(v)
		if err != nil {
			return false
		}
		host := u.Hostname()
		if host == "localhost" || host == "127.0.0.1" || host == "::1" {
			continue
		}
		ip := net.ParseIP(host)
		if ip != nil && ip.IsLoopback() {
			continue
		}
		return false
	}
	return true
}

// Compile-time guard against silent contract drift.
var _ = api.RouteWorklog

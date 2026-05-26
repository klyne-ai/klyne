package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
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

// reflectRunRequest is the POST body. Day is optional — defaults to
// today (server-local) — and is forwarded to the chained productivity
// recompose call that runs after a successful reflect so the typed
// What-was-done cards are re-derived for the correct day before the
// UI reloads the dashboard.
type reflectRunRequest struct {
	ProjectPath string `json:"project_path"`
	Day         string `json:"day,omitempty"`
}

// reflectRunResponse is what the UI renders inline.
//
// RecomposedCardCount is the number of typed What-was-done cards
// re-derived after a successful reflect — zero when the reflect didn't
// land any typed payload (legacy prose path) or when the chained
// recompose call itself failed (which is logged-but-not-fatal: the
// reflect already succeeded; the dashboard's next read will recompose
// lazily).
type reflectRunResponse struct {
	ProjectPath         string `json:"project_path"`
	Status              string `json:"status"` // "ok" | "error" | "timeout"
	Output              string `json:"output"`
	DurationMs          int64  `json:"duration_ms"`
	Error               string `json:"error,omitempty"`
	RecomposedCardCount int    `json:"recomposed_card_count,omitempty"`
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

	// On success, chain a typed What-was-done recompose for the day
	// the reflect covered so the dashboard shows the new cards on the
	// same UI round-trip. Day defaults to today (server-local) when
	// the request omits it; an invalid day is treated as today so the
	// chain never breaks the reflect's success status.
	//
	// Failure here is logged-but-not-fatal — the reflect already
	// committed; the dashboard's lazy-recompose path picks the cards
	// up on next read.
	if resp.Status == "ok" {
		dayStr := strings.TrimSpace(req.Day)
		if dayStr == "" {
			dayStr = time.Now().Local().Format("2006-01-02")
		} else if _, err := time.ParseInLocation("2006-01-02", dayStr, time.Local); err != nil {
			dayStr = time.Now().Local().Format("2006-01-02")
		}
		// Use a fresh background context for the chained recompose so
		// the client closing the connection (e.g. a successful POST
		// where the browser tears down the fetch) doesn't abort the
		// persistence write.
		recCtx, recCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer recCancel()
		cards, recErr := RecomposeWhatWasDone(recCtx, h.db, req.ProjectPath, dayStr)
		if recErr != nil {
			// Surface as a server-log breadcrumb; do NOT mutate
			// resp.Status — the reflect call itself succeeded.
			fmt.Printf("worklog reflect: recompose chain failed for %s/%s: %v\n",
				req.ProjectPath, dayStr, recErr)
		}
		resp.RecomposedCardCount = len(cards)
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
//
// `claude -p` (print/headless mode) does NOT auto-load user-level
// MCP servers from ~/.claude.json — so even though the user has the
// klyne MCP entry there for interactive sessions, the spawned child
// would otherwise be unable to see klyne's propose_reflection /
// record_reflection tools. We pass --mcp-config inline pointing at
// THIS klyne binary (os.Executable) so the child always uses the same
// build the daemon is running.
func spawnClaudeReflect(ctx context.Context, projectPath string) ([]byte, error) {
	klyneBin, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("reflect-run: locate klyne binary: %w", err)
	}
	mcpConfig, err := json.Marshal(map[string]any{
		"mcpServers": map[string]any{
			"klyne": map[string]any{
				"type":    "stdio",
				"command": klyneBin,
				"args":    []string{"mcp"},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("reflect-run: build mcp-config: %w", err)
	}

	// claude's --mcp-config flag is VARIADIC (<configs...>) and greedily
	// consumes every following token until the next flag or end of argv,
	// including the trailing /klyne:reflect prompt. Write the config to a
	// temp file so the flag receives exactly one token. The file is cleaned
	// up at the end of this function — claude has already finished reading
	// it by then.
	f, err := os.CreateTemp("", "klyne-reflect-mcp-*.json")
	if err != nil {
		return nil, fmt.Errorf("reflect-run: create mcp-config temp: %w", err)
	}
	defer os.Remove(f.Name()) //nolint:errcheck
	if _, err := f.Write(mcpConfig); err != nil {
		f.Close() //nolint:errcheck
		return nil, fmt.Errorf("reflect-run: write mcp-config temp: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("reflect-run: close mcp-config temp: %w", err)
	}

	// Layout: every other flag first, --mcp-config dead last with `--`
	// before the prompt. claude treats --mcp-config as variadic
	// (<configs...>) and will greedily swallow every following token
	// — including /klyne:reflect — unless `--` forces an end of flags.
	cmd := exec.CommandContext(ctx, "claude",
		"-p",
		"--permission-mode", "bypassPermissions",
		"--mcp-config", f.Name(),
		"--",
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

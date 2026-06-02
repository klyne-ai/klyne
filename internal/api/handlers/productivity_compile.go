package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/store"
)

// ProductivityCompileHandler serves the detached background second LLM
// pass over two routes:
//
//   - POST /api/productivity/compile/start — compute the day's
//     floor-based pending services and launch one background job
//     (idempotent per day; survives client reload). See Start.
//   - GET  /api/productivity/compile/status — report the day's live
//     per-service progress. See Status.
//
// The job runs `claude -p /klyne:productivity-sync project_path=<...>
// day=<...>` per service sequentially on context.Background() with a
// 10-minute per-service timeout (see CompileJobRegistry.execute). Each
// subprocess persists one card per service with llm_compiled=true via
// the record_productivity_card MCP tool.
//
// This is one of the two places in the daemon that intentionally invokes
// the `claude` CLI (the other is /worklog/reflect/run for the first
// pass). Security gating: same-origin (global sameOriginOnly middleware)
// and a project-path allowlist preserved structurally — discovery only
// ever targets paths drawn from the worklog rollup.
type ProductivityCompileHandler struct {
	db       *store.DB
	registry *CompileJobRegistry
}

// compileModelAllowlist constrains which models the productivity page's
// picker may select. The map value is the exact model id passed to the
// `claude` CLI's --model flag. Adding a new option requires extending
// this map AND updating the frontend picker — keep them in lockstep.
var compileModelAllowlist = map[string]string{
	"sonnet": "claude-sonnet-4-6",
	"opus":   "claude-opus-4-7",
}

// defaultCompileModel is what the handler picks when the request omits
// `model`. Sonnet (2026-05-27): A/B-tested against Opus on the same
// stop_summaries fixture; structurally identical cards at ~1/7 the
// cost. Users can opt into Opus from the productivity page header.
const defaultCompileModel = "sonnet"

// NewProductivityCompileHandler constructs a handler whose registry
// shells out via the real `claude` CLI. Tests construct the struct
// literal directly with a registry built around a stub runCmd.
func NewProductivityCompileHandler(db *store.DB) *ProductivityCompileHandler {
	return &ProductivityCompileHandler{
		db:       db,
		registry: NewCompileJobRegistry(db, spawnClaudeProductivitySync),
	}
}

// compileStartRequest is the POST /compile/start body. Day is required
// (the action back-fills a specific day); model is optional and defaults
// to defaultCompileModel.
type compileStartRequest struct {
	Day   string `json:"day"`
	Model string `json:"model,omitempty"`
}

// Start handles POST /api/productivity/compile/start. Validates day +
// model, computes the floor-based pending set, and launches a detached
// background job (idempotent per day). Returns the initial CompileJob,
// or {"status":"none"} when nothing is pending.
func (h *ProductivityCompileHandler) Start(w http.ResponseWriter, r *http.Request) {
	var req compileStartRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	dayStr := strings.TrimSpace(req.Day)
	if dayStr == "" {
		http.Error(w, "missing day", http.StatusBadRequest)
		return
	}
	if _, err := time.ParseInLocation("2006-01-02", dayStr, time.Local); err != nil {
		http.Error(w, "invalid day: must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	modelKey := strings.TrimSpace(req.Model)
	if modelKey == "" {
		modelKey = defaultCompileModel
	}
	if _, ok := compileModelAllowlist[modelKey]; !ok {
		http.Error(w, fmt.Sprintf("unknown model: %s", modelKey), http.StatusBadRequest)
		return
	}

	// If a job is already running for this day, return it unchanged — no
	// re-discovery, no second spawn (idempotent).
	if existing, ok := h.registry.Status(dayStr); ok && existing.Status == "running" {
		writeJSON(w, http.StatusOK, existing)
		return
	}

	pending, err := discoverPendingCompileServices(r.Context(), h.db, dayStr)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if len(pending) == 0 {
		writeJSON(w, http.StatusOK, map[string]string{"status": "none"})
		return
	}

	job := h.registry.Start(dayStr, modelKey, pending)
	writeJSON(w, http.StatusOK, job)
}

// Status handles GET /api/productivity/compile/status?day=YYYY-MM-DD.
func (h *ProductivityCompileHandler) Status(w http.ResponseWriter, r *http.Request) {
	dayStr := strings.TrimSpace(r.URL.Query().Get("day"))
	if dayStr == "" {
		http.Error(w, "missing day", http.StatusBadRequest)
		return
	}
	if _, err := time.ParseInLocation("2006-01-02", dayStr, time.Local); err != nil {
		http.Error(w, "invalid day: must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	if job, ok := h.registry.Status(dayStr); ok {
		writeJSON(w, http.StatusOK, job)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "none"})
}

// spawnClaudeProductivitySync is the production runCmd: shells out to
// the local `claude` CLI in one-shot mode with the productivity-sync
// slash command. Mirrors spawnClaudeReflect's security and layout
// considerations — only the model pin and slash command differ.
//
// The (project_path, day) tuple is forwarded as named arguments after
// the slash command name. Claude Code resolves the slash command file
// and the model sees the args inline; we also include them in the
// prompt explicitly so they survive any front-matter trimming.
//
// Output format is `--output-format=json` so the CLI emits a single
// JSON envelope to stdout (assistant text + usage block). The handler
// parses it via parseClaudeRunResult so the dashboard's Klyne-usage
// tile can record the per-run token spend.
func spawnClaudeProductivitySync(ctx context.Context, projectPath, day, modelKey string) (claudeRunResult, error) {
	klyneBin, err := os.Executable()
	if err != nil {
		return claudeRunResult{}, fmt.Errorf("productivity-compile: locate klyne binary: %w", err)
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
		return claudeRunResult{}, fmt.Errorf("productivity-compile: build mcp-config: %w", err)
	}

	f, err := os.CreateTemp("", "klyne-productivity-sync-mcp-*.json")
	if err != nil {
		return claudeRunResult{}, fmt.Errorf("productivity-compile: create mcp-config temp: %w", err)
	}
	defer os.Remove(f.Name()) //nolint:errcheck
	if _, err := f.Write(mcpConfig); err != nil {
		f.Close() //nolint:errcheck
		return claudeRunResult{}, fmt.Errorf("productivity-compile: write mcp-config temp: %w", err)
	}
	if err := f.Close(); err != nil {
		return claudeRunResult{}, fmt.Errorf("productivity-compile: close mcp-config temp: %w", err)
	}

	// Model is caller-supplied, validated against compileModelAllowlist
	// in Run before reaching here. Default is "sonnet" (2026-05-27 —
	// flipped from Opus after an A/B on the same fixture showed Sonnet
	// is structurally equivalent at ~1/7 the cost; users can opt into
	// Opus from the productivity page header picker).
	syncModel, ok := compileModelAllowlist[modelKey]
	if !ok {
		// Defensive — Run should have rejected unknown keys already.
		return claudeRunResult{}, fmt.Errorf("productivity-compile: unknown model key %q", modelKey)
	}

	// projectPath is transcript-derived and is interpolated into the
	// free-text prompt below while running --permission-mode
	// bypassPermissions, so validate it before use: it must be an absolute
	// path to an existing directory and must not contain newlines or other
	// control characters that could smuggle extra prompt instructions.
	// (argv passing of the prompt is preserved; this guards the interpolated
	// content itself.)
	if err := validateProjectPath(projectPath); err != nil {
		return claudeRunResult{}, fmt.Errorf("productivity-compile: %w", err)
	}

	// The slash command body reads `project_path=<...> day=<...>` from
	// the user prompt to drive its tool calls. Pass them inline.
	prompt := fmt.Sprintf("/klyne:productivity-sync project_path=%s day=%s", projectPath, day)

	cmd := exec.CommandContext(ctx, "claude",
		"-p",
		"--model", syncModel,
		"--output-format", "json",
		"--permission-mode", "bypassPermissions",
		"--mcp-config", f.Name(),
		"--",
		prompt,
	)
	cmd.Dir = projectPath
	// WaitDelay bounds how long Wait() blocks after ctx is cancelled/killed.
	// Without it, an orphaned `klyne mcp` grandchild (no Setpgid) keeps the
	// captured stdout/stderr pipe write-end open after the direct `claude`
	// child is SIGKILLed on the deadline, so Wait() would block forever and
	// wedge the compile job. WaitDelay converts that hang into a clean error.
	cmd.WaitDelay = 5 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	res := parseClaudeRunResult(stdout.Bytes(), stderr.Bytes(), syncModel)
	return res, runErr
}

// validateProjectPath rejects a project path that is unsafe to interpolate
// into a bypassPermissions prompt. It requires an absolute path to an
// existing directory with no newline or other control characters. Returns
// a descriptive error suitable for failing/skipping the service.
func validateProjectPath(p string) error {
	if p == "" {
		return fmt.Errorf("project_path is empty")
	}
	if !filepath.IsAbs(p) {
		return fmt.Errorf("project_path is not absolute: %q", p)
	}
	for _, r := range p {
		// Reject newlines and any C0/C1 control characters; these are the
		// vectors that could inject extra prompt lines or terminal escapes.
		if r == '\n' || r == '\r' || r < 0x20 || r == 0x7f {
			return fmt.Errorf("project_path contains control characters")
		}
	}
	info, err := os.Stat(p)
	if err != nil {
		return fmt.Errorf("project_path does not exist: %q", p)
	}
	if !info.IsDir() {
		return fmt.Errorf("project_path is not a directory: %q", p)
	}
	return nil
}

// Compile-time guard against silent contract drift on the route const.
var _ = api.RouteProductivityCompileStart

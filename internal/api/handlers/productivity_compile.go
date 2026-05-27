package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/store"
)

// ProductivityCompileHandler serves POST /api/productivity/compile —
// the second LLM pass invocation. Spawns `claude -p
// /klyne:productivity-sync project_path=<...> day=<...>` for a project
// already in the worklog rollup.
//
// This is the second of the two places in the daemon that intentionally
// invokes the `claude` CLI (the other is /worklog/reflect/run for the
// first pass). It is gated on the same three checks:
//
//  1. Same-origin: enforced globally by the sameOriginOnly middleware.
//  2. Project-path allowlist: requested path MUST already appear in the
//     worklog rollup.
//  3. Bounded execution: hard 5-minute context timeout.
//
// The spawn invokes the second-pass slash command directly with the
// resolved (project_path, day) so the model never has to guess. The
// command itself calls list_typed_reflections + record_productivity_card
// MCP tools to read the day's typed reflections and persist one card
// per service with llm_compiled=true.
//
// Distinct from /worklog/reflect/run because callers want to RE-SYNC
// without re-running reflection (legacy days, manual UI button, etc.).
// /worklog/reflect/run chains this same spawn after a successful
// reflect so the dashboard's "Run /klyne:reflect now" button does
// reflect → sync invisibly.
type ProductivityCompileHandler struct {
	db *store.DB
	// runCmd is the subprocess factory. Tests inject a stub here so
	// the handler can be exercised without a real `claude` binary on
	// the test runner. The second return is a parsed result envelope
	// (text + token usage) extracted from the CLI's
	// `--output-format=json` output.
	//
	// modelKey is the caller-supplied model selection from the request
	// (validated against compileModelAllowlist before reaching this fn).
	runCmd func(ctx context.Context, projectPath, day, modelKey string) (claudeRunResult, error)
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

// NewProductivityCompileHandler constructs a handler that shells out
// via the real `claude` CLI. Tests should construct the struct literal
// directly to supply a stub runCmd.
func NewProductivityCompileHandler(db *store.DB) *ProductivityCompileHandler {
	return &ProductivityCompileHandler{db: db, runCmd: spawnClaudeProductivitySync}
}

// productivityCompileRequest is the POST body. Both fields are
// required — unlike /worklog/reflect/run which defaults Day to today,
// /api/productivity/compile demands an explicit day because its whole
// purpose is to back-fill compiled cards for specific (often past)
// days.
type productivityCompileRequest struct {
	ProjectPath string `json:"project_path"`
	Day         string `json:"day"`
	// Model is one of compileModelAllowlist's keys ("sonnet" | "opus").
	// Optional: omitted/empty → defaultCompileModel. Unknown values are
	// rejected with 400 — never silently coerced.
	Model string `json:"model,omitempty"`
}

// productivityCompileResponse mirrors reflectRunResponse so the UI's
// "Compile" button can render the same inline output panel as the
// "Run /klyne:reflect now" button.
type productivityCompileResponse struct {
	ProjectPath string `json:"project_path"`
	Day         string `json:"day"`
	Status      string `json:"status"` // "ok" | "error" | "timeout"
	Output      string `json:"output"`
	DurationMs  int64  `json:"duration_ms"`
	Error       string `json:"error,omitempty"`
}

// Run handles POST /api/productivity/compile.
func (h *ProductivityCompileHandler) Run(w http.ResponseWriter, r *http.Request) {
	var req productivityCompileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	req.ProjectPath = strings.TrimSpace(req.ProjectPath)
	if req.ProjectPath == "" {
		http.Error(w, "missing project_path", http.StatusBadRequest)
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

	// Allowlist: same gate as /worklog/reflect/run.
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

	// Opus 4.7 on a 24-card service can run 5-7 minutes. Bumped from 5m
	// → 10m so large services (klyne itself most notably) finish without
	// a context-deadline kill.
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()

	start := time.Now()
	result, runErr := h.runCmd(ctx, req.ProjectPath, dayStr, modelKey)
	dur := time.Since(start).Milliseconds()

	resp := productivityCompileResponse{
		ProjectPath: req.ProjectPath,
		Day:         dayStr,
		Output:      result.Output,
		DurationMs:  dur,
	}
	switch {
	case runErr == nil:
		resp.Status = "ok"
	case ctx.Err() == context.DeadlineExceeded:
		resp.Status = "timeout"
		resp.Error = "subprocess exceeded 10m budget"
	default:
		resp.Status = "error"
		resp.Error = runErr.Error()
	}

	// Record token usage for the dashboard's "Klyne is X% of today's
	// Claude usage" tile. Persist regardless of subprocess status —
	// failed/timeout runs still consumed tokens up to the cut-off and
	// the user should see them.
	persistKlyneUsage(r.Context(), h.db, req.ProjectPath, dayStr,
		"productivity_sync", result, resp.Status, dur)

	writeJSON(w, http.StatusOK, resp)
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
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	res := parseClaudeRunResult(stdout.Bytes(), stderr.Bytes(), syncModel)
	return res, runErr
}

// Compile-time guard against silent contract drift on the route const.
var _ = api.RouteProductivityCompile

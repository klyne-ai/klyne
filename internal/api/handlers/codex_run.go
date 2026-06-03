package handlers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/klyne-ai/klyne/internal/mcpserver"
)

// codexAppFallbackPath is where the Codex desktop app installs its CLI on
// macOS. The `codex` binary is frequently NOT on PATH (it ships inside the
// app bundle), so engine detection and invocation fall back to this path
// when exec.LookPath fails. See the 2026-06-03 multi-engine design.
const codexAppFallbackPath = "/Applications/Codex.app/Contents/Resources/codex"

// codexBinary resolves the codex CLI: PATH first, then the Codex.app
// bundle. Returns "" when neither is present (codex not installed).
func codexBinary() string {
	if p, err := exec.LookPath("codex"); err == nil {
		return p
	}
	if fi, err := os.Stat(codexAppFallbackPath); err == nil && !fi.IsDir() {
		return codexAppFallbackPath
	}
	return ""
}

// codexAvailable reports whether the codex CLI can be located.
func codexAvailable() bool { return codexBinary() != "" }

// runCodexSlashCommand runs one klyne slash-command's instructions through
// `codex exec` (headless GPT) instead of `claude`. Codex has no
// `/klyne:<name>` slash-command surface, so the embedded command body is
// inlined as the prompt with project_path/day appended; the klyne MCP
// server wired into ~/.codex/config.toml provides the tools the body calls
// (list_stop_summaries_for_day, record_productivity_card, …).
//
// model is the codex --model value; "" means "use the codex config default"
// (the locked decision — a ChatGPT-account login rejects gpt-5-codex, and
// the configured default model is always valid).
//
// Returns a claudeRunResult so usage attribution and the registry are
// engine-agnostic. The result's Model is tagged "codex" (or "codex:<model>")
// for the klyne_llm_usage row.
func runCodexSlashCommand(
	ctx context.Context, projectPath, day, slashName, model string,
) (claudeRunResult, error) {
	bin := codexBinary()
	if bin == "" {
		return claudeRunResult{}, fmt.Errorf("codex-run: codex CLI not found (PATH or %s)", codexAppFallbackPath)
	}

	// projectPath is transcript-derived and is interpolated into the
	// free-text prompt below; validate it the same way the claude path
	// does (absolute, existing dir, no control chars) before use.
	if err := validateProjectPath(projectPath); err != nil {
		return claudeRunResult{}, fmt.Errorf("codex-run: %w", err)
	}

	body, err := mcpserver.SlashCommandBody(slashName)
	if err != nil {
		return claudeRunResult{}, fmt.Errorf("codex-run: %w", err)
	}
	prompt := fmt.Sprintf("%s\n\nproject_path=%s day=%s", body, projectPath, day)

	// Capture the agent's final message via -o; the JSONL on stdout
	// carries turn.completed.usage for token attribution.
	lastFile, err := os.CreateTemp("", "klyne-codex-last-*.txt")
	if err != nil {
		return claudeRunResult{}, fmt.Errorf("codex-run: create output temp: %w", err)
	}
	lastPath := lastFile.Name()
	_ = lastFile.Close()
	defer os.Remove(lastPath) //nolint:errcheck

	args := []string{
		"exec",
		"--skip-git-repo-check", // the codex project is often a non-git scratch dir
		// Headless exec must not prompt for approval, or the klyne MCP tool
		// calls (list_stop_summaries_for_day, record_productivity_card) are
		// auto-cancelled and no card is produced. This is the codex analog
		// of the claude path's --permission-mode bypassPermissions and
		// carries the same local-trust assumption (klyne's own MCP server,
		// loopback-only daemon). Verified: without it codex reports "the
		// stop-summary read was cancelled".
		"--dangerously-bypass-approvals-and-sandbox",
		"--json",
		"-o", lastPath,
	}
	modelTag := "codex"
	if strings.TrimSpace(model) != "" {
		args = append(args, "-m", model)
		modelTag = "codex:" + model
	}
	args = append(args, "--", prompt)

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = projectPath
	// Bound Wait() after a deadline kill so a wedged grandchild holding the
	// captured pipe can't hang the job (mirrors the claude spawn path).
	cmd.WaitDelay = 5 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	last, _ := os.ReadFile(lastPath) //nolint:errcheck
	res := parseCodexRunResult(stdout.Bytes(), last, modelTag)
	if !res.Parsed && runErr == nil {
		runErr = fmt.Errorf("codex-run: no parseable turn.completed event")
	}
	return res, runErr
}

// codexEvent is one line of `codex exec --json` JSONL output. Only the
// fields klyne needs are modelled; unknown fields are ignored.
type codexEvent struct {
	Type  string `json:"type"`
	Usage *struct {
		InputTokens          int64 `json:"input_tokens"`
		CachedInputTokens    int64 `json:"cached_input_tokens"`
		OutputTokens         int64 `json:"output_tokens"`
		ReasoningOutputToken int64 `json:"reasoning_output_tokens"`
	} `json:"usage,omitempty"`
	// agent_message carries the assistant text; shape varies across codex
	// versions so we accept either a flat "text"/"message" string.
	Text    string `json:"text,omitempty"`
	Message string `json:"message,omitempty"`
}

// parseCodexRunResult maps `codex exec --json` JSONL + the -o final message
// onto the shared claudeRunResult. Token usage comes from the
// turn.completed event's usage block; cached_input_tokens maps to
// CacheReadTokens (codex reports no separate cache-write). Parsed is true
// only when a turn.completed event was seen, so a failed turn is recorded
// as status=error (no double-counting a no-op as zero usage).
func parseCodexRunResult(stdout, lastMessage []byte, modelTag string) claudeRunResult {
	out := claudeRunResult{Model: modelTag}
	out.Output = strings.TrimSpace(string(lastMessage))

	var failureMsg string
	sc := bufio.NewScanner(bytes.NewReader(stdout))
	sc.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var ev codexEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "turn.completed":
			out.Parsed = true
			if ev.Usage != nil {
				out.InputTokens = ev.Usage.InputTokens
				out.OutputTokens = ev.Usage.OutputTokens
				out.CacheReadTokens = ev.Usage.CachedInputTokens
			}
			out.NumTurns = 1
		case "agent_message":
			if out.Output == "" {
				if ev.Text != "" {
					out.Output = ev.Text
				} else if ev.Message != "" {
					out.Output = ev.Message
				}
			}
		case "error", "turn.failed":
			if ev.Message != "" {
				failureMsg = ev.Message
			}
		}
	}

	if !out.Parsed && out.Output == "" {
		if failureMsg != "" {
			out.Output = failureMsg
		} else if len(stdout) > 0 {
			out.Output = string(stdout)
		} else {
			out.Output = "(no output)"
		}
	}
	return out
}

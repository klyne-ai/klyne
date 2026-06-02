package handlers

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"
)

// askSpawnModel is the model used for Ask Klyne. Sonnet 4.6 is the
// repo's default for synthesis tasks (cheaper than Opus, fast enough
// for an interactive drawer). Pinned here — change it in one place
// rather than threading a flag through the handler.
const askSpawnModel = "claude-sonnet-4-6"

// spawnAskClaude runs `claude -p` with the given prompt and returns
// the parsed envelope. Args go via argv (no shell) — same security
// posture as spawnClaudeProductivitySync. The caller controls the
// timeout via ctx.
func spawnAskClaude(ctx context.Context, prompt string) (claudeRunResult, error) {
	cmd := exec.CommandContext(ctx, "claude",
		"-p",
		"--model", askSpawnModel,
		"--output-format", "json",
		"--permission-mode", "bypassPermissions",
		"--",
		prompt,
	)
	// WaitDelay bounds Wait() after a ctx cancel/kill so an orphaned
	// `klyne mcp` grandchild holding the captured pipe open cannot block
	// Wait() forever (turns the hang into a clean timeout error).
	cmd.WaitDelay = 5 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	res := parseClaudeRunResult(stdout.Bytes(), stderr.Bytes(), askSpawnModel)

	// If the binary itself is missing, surface a specific error so the
	// HTTP handler can return a friendly message.
	if runErr != nil {
		if _, lookErr := exec.LookPath("claude"); lookErr != nil {
			return res, fmt.Errorf("claude CLI not installed: %w", lookErr)
		}
	}
	return res, runErr
}

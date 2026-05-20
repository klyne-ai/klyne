// Package providers — codex-cli provider.
//
// Same shape as ClaudeCLI: shells out to OpenAI's `codex` CLI in its
// non-interactive `exec` mode so the user's existing Codex subscription
// auth is reused. We never read ~/.codex/auth.json (spec §17).
//
// Note: `codex` is NOT installed on every developer machine. The
// provider is gated by detect.go's `which codex` probe; when absent,
// klyne falls back to the next preference-table entry.
package providers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/klyne-ai/klyne/internal/ai"
)

const codexCLIBinary = "codex"

// defaultCodexCLIModels mirrors the model identifiers the Codex CLI
// accepts via `--model`. Curated for klyne's task profiles.
var defaultCodexCLIModels = []string{
	"gpt-5",
	"gpt-5-mini",
	"gpt-5-nano",
}

// CodexCLIOpts configures a CodexCLI provider instance.
type CodexCLIOpts struct {
	// Binary overrides the executable path. Default: "codex".
	Binary string
}

// CodexCLI shells out to `codex exec` for each Chat call. Auth comes
// from the user's existing CLI session — this provider never touches
// credentials directly.
type CodexCLI struct {
	binary string
}

// NewCodexCLI constructs a CodexCLI provider.
func NewCodexCLI(opts CodexCLIOpts) *CodexCLI {
	bin := opts.Binary
	if bin == "" {
		bin = codexCLIBinary
	}
	return &CodexCLI{binary: bin}
}

// Name returns "codex-cli".
func (c *CodexCLI) Name() string { return "codex-cli" }

// Models returns the default Codex model list.
func (c *CodexCLI) Models() []string { return defaultCodexCLIModels }

// Embed is not supported by the Codex CLI.
func (c *CodexCLI) Embed(_ context.Context, _ ai.EmbedRequest) (*ai.EmbedResponse, error) {
	return nil, ai.ErrUnsupported
}

// Chat runs `codex exec` with the prompt and returns stdout.
//
// SystemPrompt is prepended to the user prompt (separated by a blank
// line) because `codex exec` doesn't expose a dedicated system-prompt
// flag in non-interactive mode. Multi-message history is flattened —
// see flattenMessages in claudecli.go for the shape.
func (c *CodexCLI) Chat(ctx context.Context, req ai.ChatRequest) (*ai.ChatResponse, error) {
	prompt := flattenMessages(req.Messages)
	if strings.TrimSpace(prompt) == "" {
		return nil, errors.New("codex-cli: empty prompt")
	}
	if req.SystemPrompt != "" {
		prompt = req.SystemPrompt + "\n\n" + prompt
	}

	args := []string{"exec", "--skip-git-repo-check"}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	args = append(args, prompt)

	cmd := exec.CommandContext(ctx, c.binary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, fmt.Errorf("%w: codex-cli: binary %q not on PATH", ai.ErrProviderUnavailable, c.binary)
		}
		return nil, fmt.Errorf("%w: codex-cli: %v: %s",
			ai.ErrProviderUnavailable, err, strings.TrimSpace(stderr.String()))
	}

	text := strings.TrimRight(stdout.String(), "\n")
	return &ai.ChatResponse{
		Text:       text,
		Model:      req.Model,
		StopReason: "stop",
	}, nil
}

// probeCodexCLI returns true when the `codex` binary is on PATH.
func probeCodexCLI(binary string) bool {
	if binary == "" {
		binary = codexCLIBinary
	}
	_, err := exec.LookPath(binary)
	return err == nil
}

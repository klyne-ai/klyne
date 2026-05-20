// Package providers — claude-cli provider.
//
// Klyne's product promise is that no separate API key is needed. The
// Anthropic provider used to depend on ANTHROPIC_API_KEY, breaking that
// promise. Instead, this provider shells out to the user's already-
// authenticated `claude` CLI binary (Claude Code), which carries the
// user's OAuth / Pro / Max subscription. We never read ~/.claude
// credential files directly (spec §8 — Anthropic enforcement
// 2026-04-04). The subprocess does that on our behalf.
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

const claudeCLIBinary = "claude"

// defaultClaudeCLIModels lists model identifiers the `claude` CLI
// accepts via --model. These are the canonical names (point-release
// suffix included) that the CLI's model registry actually resolves —
// shorter aliases like "haiku" also work but the explicit form is
// portable across releases.
var defaultClaudeCLIModels = []string{
	"claude-opus-4-5",
	"claude-sonnet-4-6",
	"claude-haiku-4-5",
}

// defaultNeutralSystemPrompt replaces the CLI's default agentic
// system prompt for our non-interactive Chat calls. Without this,
// `claude --print` thinks it's helping a developer in their CWD and
// answers conversationally ("What would you like to work on?")
// instead of obeying the structured prompt the caller sent.
const defaultNeutralSystemPrompt = "You are a model-as-a-service backend. Follow the user's prompt exactly. Reply with only the requested output (JSON / text / code) — no preamble, no explanation, no follow-up questions."

// ClaudeCLIOpts configures a ClaudeCLI provider instance.
type ClaudeCLIOpts struct {
	// Binary overrides the executable path. Default: "claude" (resolved
	// via PATH). Tests inject a stub script.
	Binary string
}

// ClaudeCLI shells out to `claude --print` for each Chat call. Auth
// comes from the user's existing CLI session — this provider never
// touches credentials directly.
type ClaudeCLI struct {
	binary string
}

// NewClaudeCLI constructs a ClaudeCLI provider.
func NewClaudeCLI(opts ClaudeCLIOpts) *ClaudeCLI {
	bin := opts.Binary
	if bin == "" {
		bin = claudeCLIBinary
	}
	return &ClaudeCLI{binary: bin}
}

// Name returns "claude-cli".
func (c *ClaudeCLI) Name() string { return "claude-cli" }

// Models returns the default model list for the CLI.
func (c *ClaudeCLI) Models() []string { return defaultClaudeCLIModels }

// Embed is not supported by the Claude CLI.
func (c *ClaudeCLI) Embed(_ context.Context, _ ai.EmbedRequest) (*ai.EmbedResponse, error) {
	return nil, ai.ErrUnsupported
}

// Chat runs `claude --print` and returns the stdout as the assistant
// reply. Multi-message history is flattened into a single prompt
// (CLI doesn't expose a multi-turn message API in non-interactive
// mode). SystemPrompt is passed via --append-system-prompt.
//
// Returns ai.ErrProviderUnavailable when the binary is missing or
// exits with a non-zero status — callers treat both as "skip this
// turn, retry next worker tick".
func (c *ClaudeCLI) Chat(ctx context.Context, req ai.ChatRequest) (*ai.ChatResponse, error) {
	prompt := flattenMessages(req.Messages)
	if strings.TrimSpace(prompt) == "" {
		return nil, errors.New("claude-cli: empty prompt")
	}

	// --system-prompt (not --append-system-prompt) replaces the CLI's
	// default agentic system prompt so the model treats this call as a
	// raw inference request instead of an interactive coding session.
	sys := req.SystemPrompt
	if sys == "" {
		sys = defaultNeutralSystemPrompt
	}

	args := []string{"--print", "--system-prompt", sys}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	args = append(args, "--", prompt)

	cmd := exec.CommandContext(ctx, c.binary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// exec.LookPath / fork failures and non-zero exits both come back here.
		if errors.Is(err, exec.ErrNotFound) {
			return nil, fmt.Errorf("%w: claude-cli: binary %q not on PATH", ai.ErrProviderUnavailable, c.binary)
		}
		return nil, fmt.Errorf("%w: claude-cli: %v: %s",
			ai.ErrProviderUnavailable, err, strings.TrimSpace(stderr.String()))
	}

	text := strings.TrimRight(stdout.String(), "\n")
	return &ai.ChatResponse{
		Text:       text,
		Model:      req.Model,
		StopReason: "stop",
	}, nil
}

// flattenMessages joins a chat history into a single prompt string. Each
// turn is prefixed with the role label so the model can distinguish
// who said what. For the common one-user-message case, the output is
// just the content (no labels).
func flattenMessages(msgs []ai.Message) string {
	if len(msgs) == 0 {
		return ""
	}
	if len(msgs) == 1 {
		return msgs[0].Content
	}
	var b strings.Builder
	for i, m := range msgs {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(strings.ToUpper(m.Role))
		b.WriteString(": ")
		b.WriteString(m.Content)
	}
	return b.String()
}

// probeClaudeCLI returns true when the `claude` binary is resolvable
// on PATH. Used by DetectAvailable.
func probeClaudeCLI(binary string) bool {
	if binary == "" {
		binary = claudeCLIBinary
	}
	_, err := exec.LookPath(binary)
	return err == nil
}

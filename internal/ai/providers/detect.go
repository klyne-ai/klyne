package providers

import (
	"context"
	"net/http"

	"github.com/klyne-ai/klyne/internal/ai"
)

// detectOpts holds overrideable options for DetectAvailable. Tests use
// functional options to inject CLI binary paths, base URLs, and HTTP
// clients without modifying global state.
type detectOpts struct {
	ollamaBaseURL string
	ollamaClient  *http.Client
	claudeBinary  string
	codexBinary   string
}

// DetectOption is a functional option for DetectAvailable.
type DetectOption func(*detectOpts)

// WithOllamaBaseURL overrides the Ollama base URL used during detection.
func WithOllamaBaseURL(baseURL string) DetectOption {
	return func(o *detectOpts) { o.ollamaBaseURL = baseURL }
}

// WithOllamaHTTPClient overrides the HTTP client used for the Ollama probe.
func WithOllamaHTTPClient(c *http.Client) DetectOption {
	return func(o *detectOpts) { o.ollamaClient = c }
}

// WithClaudeBinary overrides the claude CLI binary used during detection.
// Tests point this at a stub script or "/no/such/path" to force
// availability one way or the other.
func WithClaudeBinary(path string) DetectOption {
	return func(o *detectOpts) { o.claudeBinary = path }
}

// WithCodexBinary overrides the codex CLI binary used during detection.
func WithCodexBinary(path string) DetectOption {
	return func(o *detectOpts) { o.codexBinary = path }
}

// DetectAvailable probes each supported AI provider and returns a slice
// of ProviderInfo values describing their availability.
//
// Detection rules (post 2026-05-20 rewrite — no API keys anywhere):
//   - claude-cli: available iff the `claude` binary is on PATH. Auth
//     comes from the user's existing Claude Code subscription —
//     klyne never reads ~/.claude credentials directly.
//   - codex-cli:  available iff the `codex` binary is on PATH. Auth
//     comes from the user's existing Codex CLI session — klyne never
//     reads ~/.codex/auth.json directly.
//   - ollama:     available iff GET <baseURL>/api/tags returns HTTP 200
//                 within 1s. No credential needed.
//
// Reason strings are stable and consumed by the wizard UI (W15).
func DetectAvailable(ctx context.Context, opts ...DetectOption) []ai.ProviderInfo {
	cfg := &detectOpts{
		ollamaBaseURL: ollamaDefaultBaseURL,
		ollamaClient:  http.DefaultClient,
	}
	for _, o := range opts {
		o(cfg)
	}

	results := make([]ai.ProviderInfo, 0, 3)

	// --- claude-cli ---
	if probeClaudeCLI(cfg.claudeBinary) {
		results = append(results, ai.ProviderInfo{
			Name:          "claude-cli",
			Available:     true,
			Reason:        "`claude` binary on PATH — using your Claude Code subscription",
			Models:        defaultClaudeCLIModels,
			SupportsEmbed: false,
		})
	} else {
		results = append(results, ai.ProviderInfo{
			Name:          "claude-cli",
			Available:     false,
			Reason:        "`claude` binary not on PATH — install Claude Code to enable",
			Models:        defaultClaudeCLIModels,
			SupportsEmbed: false,
		})
	}

	// --- codex-cli ---
	if probeCodexCLI(cfg.codexBinary) {
		results = append(results, ai.ProviderInfo{
			Name:          "codex-cli",
			Available:     true,
			Reason:        "`codex` binary on PATH — using your Codex CLI session",
			Models:        defaultCodexCLIModels,
			SupportsEmbed: false,
		})
	} else {
		results = append(results, ai.ProviderInfo{
			Name:          "codex-cli",
			Available:     false,
			Reason:        "`codex` binary not on PATH — install OpenAI Codex CLI to enable",
			Models:        defaultCodexCLIModels,
			SupportsEmbed: false,
		})
	}

	// --- ollama ---
	if probeOllama(ctx, cfg.ollamaBaseURL, cfg.ollamaClient) {
		results = append(results, ai.ProviderInfo{
			Name:          "ollama",
			Available:     true,
			Reason:        "Ollama server reachable at " + cfg.ollamaBaseURL,
			Models:        defaultOllamaModels,
			SupportsEmbed: false,
		})
	} else {
		results = append(results, ai.ProviderInfo{
			Name:          "ollama",
			Available:     false,
			Reason:        "Ollama server unreachable at " + cfg.ollamaBaseURL,
			Models:        defaultOllamaModels,
			SupportsEmbed: false,
		})
	}

	return results
}

package providers

import (
	"context"
	"net/http"
	"os"

	"github.com/mohitpatell/agentdeck/internal/ai"
)

// detectOpts holds overrideable options for DetectAvailable. Tests use
// functional options to inject custom base URLs and HTTP clients without
// modifying global state.
type detectOpts struct {
	ollamaBaseURL  string
	ollamaClient   *http.Client
}

// DetectOption is a functional option for DetectAvailable.
type DetectOption func(*detectOpts)

// WithOllamaBaseURL overrides the Ollama base URL used during detection.
// Used by tests to point at a controlled httptest.Server or a closed port.
func WithOllamaBaseURL(baseURL string) DetectOption {
	return func(o *detectOpts) {
		o.ollamaBaseURL = baseURL
	}
}

// WithOllamaHTTPClient overrides the HTTP client used for the Ollama probe.
func WithOllamaHTTPClient(c *http.Client) DetectOption {
	return func(o *detectOpts) {
		o.ollamaClient = c
	}
}

// DetectAvailable probes each supported AI provider and returns a slice of
// ProviderInfo values describing their availability.
//
// Detection rules (spec §8):
//   - anthropic: available iff ANTHROPIC_API_KEY env var is non-empty.
//   - openai:    available iff OPENAI_API_KEY env var is non-empty.
//   - gemini:    available iff GEMINI_API_KEY env var is non-empty.
//   - ollama:    available iff GET <baseURL>/api/tags returns HTTP 200 within 1s.
//
// NEVER reads ~/.claude, ~/.codex, or any credential files (spec §8 / §17).
//
// The reason strings are stable and suitable for display in the wizard UI
// (consumed by W15).
func DetectAvailable(ctx context.Context, opts ...DetectOption) []ai.ProviderInfo {
	cfg := &detectOpts{
		ollamaBaseURL: ollamaDefaultBaseURL,
		ollamaClient:  http.DefaultClient,
	}
	for _, o := range opts {
		o(cfg)
	}

	results := make([]ai.ProviderInfo, 0, 4)

	// --- Anthropic ---
	// CRITICAL: check env var ONLY. Never read ~/.claude (spec §8).
	anthropicKey := os.Getenv("ANTHROPIC_API_KEY")
	if anthropicKey != "" {
		results = append(results, ai.ProviderInfo{
			Name:          "anthropic",
			Available:     true,
			Reason:        "ANTHROPIC_API_KEY set",
			Models:        defaultAnthropicModels,
			SupportsEmbed: false,
		})
	} else {
		results = append(results, ai.ProviderInfo{
			Name:          "anthropic",
			Available:     false,
			Reason:        "ANTHROPIC_API_KEY not set",
			Models:        defaultAnthropicModels,
			SupportsEmbed: false,
		})
	}

	// --- OpenAI ---
	// CRITICAL: check env var ONLY. Never read ~/.codex/auth.json (spec §17).
	openAIKey := os.Getenv("OPENAI_API_KEY")
	if openAIKey != "" {
		results = append(results, ai.ProviderInfo{
			Name:          "openai",
			Available:     true,
			Reason:        "OPENAI_API_KEY set",
			Models:        defaultOpenAIModels,
			SupportsEmbed: true,
		})
	} else {
		results = append(results, ai.ProviderInfo{
			Name:          "openai",
			Available:     false,
			Reason:        "OPENAI_API_KEY not set",
			Models:        defaultOpenAIModels,
			SupportsEmbed: true,
		})
	}

	// --- Gemini ---
	geminiKey := os.Getenv("GEMINI_API_KEY")
	if geminiKey != "" {
		results = append(results, ai.ProviderInfo{
			Name:          "gemini",
			Available:     true,
			Reason:        "GEMINI_API_KEY set",
			Models:        defaultGeminiModels,
			SupportsEmbed: false,
		})
	} else {
		results = append(results, ai.ProviderInfo{
			Name:          "gemini",
			Available:     false,
			Reason:        "GEMINI_API_KEY not set",
			Models:        defaultGeminiModels,
			SupportsEmbed: false,
		})
	}

	// --- Ollama ---
	// Availability requires a successful HTTP probe (not an env var).
	ollamaReachable := probeOllama(ctx, cfg.ollamaBaseURL, cfg.ollamaClient)
	if ollamaReachable {
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

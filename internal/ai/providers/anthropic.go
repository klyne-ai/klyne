// Package providers contains concrete implementations of the ai.Provider
// interface for Anthropic, OpenAI, Gemini, and Ollama.
//
// CRITICAL LEGAL CONSTRAINT (spec §8, Anthropic enforcement 2026-04-04):
// No file under ~/.claude may be read by this package for any purpose,
// including credential discovery. ANTHROPIC_API_KEY (environment variable)
// is the only accepted credential source. If the variable is unset, every
// method returns ai.ErrNoCredential immediately.
package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/mohitpatell/agentdeck/internal/ai"
)

const (
	anthropicDefaultBaseURL = "https://api.anthropic.com"
	anthropicAPIVersion     = "2023-06-01"
	anthropicMessagesPath   = "/v1/messages"
)

// defaultAnthropicModels is the curated list shown in the wizard UI.
var defaultAnthropicModels = []string{
	"claude-opus-4-5",
	"claude-sonnet-4-6",
	"claude-haiku-4",
}

// AnthropicOpts configures an Anthropic provider instance.
type AnthropicOpts struct {
	// APIKey is the Anthropic API key. If empty, the provider reads
	// ANTHROPIC_API_KEY from the environment at construction time.
	// Providing it here allows tests to pass keys without setting env vars.
	APIKey string
	// BaseURL overrides the default API base URL. Used by tests to point at
	// an httptest.Server.
	BaseURL string
	// HTTPClient overrides the HTTP client used for all requests. When nil,
	// http.DefaultClient is used.
	HTTPClient *http.Client
}

// Anthropic is an ai.Provider implementation backed by the Anthropic
// Messages API (https://docs.anthropic.com/en/api/messages).
//
// It NEVER reads any file — credentials come only from AnthropicOpts.APIKey
// or the ANTHROPIC_API_KEY environment variable (spec §8).
type Anthropic struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

// NewAnthropic constructs an Anthropic provider.
// If opts.APIKey is empty, ANTHROPIC_API_KEY is read from the environment.
// If opts.BaseURL is empty, the production endpoint is used.
func NewAnthropic(opts AnthropicOpts) *Anthropic {
	key := opts.APIKey
	if key == "" {
		key = os.Getenv("ANTHROPIC_API_KEY")
	}
	base := opts.BaseURL
	if base == "" {
		base = anthropicDefaultBaseURL
	}
	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	return &Anthropic{
		apiKey:  key,
		baseURL: base,
		client:  client,
	}
}

// Name returns "anthropic".
func (a *Anthropic) Name() string { return "anthropic" }

// Models returns the default Anthropic model list.
func (a *Anthropic) Models() []string { return defaultAnthropicModels }

// Embed is not supported by Anthropic's API; it always returns
// ai.ErrUnsupported.
func (a *Anthropic) Embed(_ context.Context, _ ai.EmbedRequest) (*ai.EmbedResponse, error) {
	return nil, ai.ErrUnsupported
}

// Chat sends a request to the Anthropic Messages API and returns the
// assistant's reply.
//
// Returns ai.ErrNoCredential if ANTHROPIC_API_KEY is unset.
// Returns ai.ErrProviderUnavailable on transport-level failure.
// Returns a descriptive error for API-level 4xx/5xx responses.
func (a *Anthropic) Chat(ctx context.Context, req ai.ChatRequest) (*ai.ChatResponse, error) {
	if a.apiKey == "" {
		return nil, ai.ErrNoCredential
	}

	model := req.Model
	if model == "" {
		model = defaultAnthropicModels[1] // claude-sonnet-4-6
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 1024
	}

	// Build request body.
	type anthropicMessage struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type anthropicRequest struct {
		Model     string             `json:"model"`
		MaxTokens int                `json:"max_tokens"`
		Messages  []anthropicMessage `json:"messages"`
		System    string             `json:"system,omitempty"`
	}

	msgs := make([]anthropicMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		msgs = append(msgs, anthropicMessage{Role: m.Role, Content: m.Content})
	}

	body := anthropicRequest{
		Model:     model,
		MaxTokens: maxTokens,
		Messages:  msgs,
		System:    req.SystemPrompt,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		a.baseURL+anthropicMessagesPath,
		bytes.NewReader(payload),
	)
	if err != nil {
		return nil, fmt.Errorf("anthropic: build request: %w", err)
	}
	httpReq.Header.Set("x-api-key", a.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicAPIVersion)
	httpReq.Header.Set("content-type", "application/json")

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w: anthropic: %v", ai.ErrProviderUnavailable, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("anthropic: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("anthropic: API error %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse successful response.
	type contentBlock struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type usageBlock struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	}
	type anthropicResponse struct {
		Content    []contentBlock `json:"content"`
		Usage      usageBlock     `json:"usage"`
		Model      string         `json:"model"`
		StopReason string         `json:"stop_reason"`
	}

	var apiResp anthropicResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("anthropic: unmarshal response: %w", err)
	}

	text := ""
	for _, block := range apiResp.Content {
		if block.Type == "text" {
			text += block.Text
		}
	}

	return &ai.ChatResponse{
		Text:       text,
		TokensIn:   apiResp.Usage.InputTokens,
		TokensOut:  apiResp.Usage.OutputTokens,
		Model:      apiResp.Model,
		StopReason: apiResp.StopReason,
	}, nil
}

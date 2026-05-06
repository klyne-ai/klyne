package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/mohitpatell/agentdeck/internal/ai"
)

const (
	ollamaDefaultBaseURL = "http://localhost:11434"
	ollamaChatPath       = "/api/chat"
	ollamaTagsPath       = "/api/tags"
	ollamaProbeTimeout   = 1 * time.Second
)

// defaultOllamaModels is returned when no models have been fetched yet.
var defaultOllamaModels = []string{
	"llama3.1:8b",
	"llama3.2",
	"mistral",
	"qwen2.5",
}

// OllamaOpts configures an Ollama provider instance.
type OllamaOpts struct {
	// BaseURL is the Ollama server base URL. Defaults to
	// http://localhost:11434.
	BaseURL string
	// HTTPClient overrides the HTTP client. When nil, http.DefaultClient is used.
	HTTPClient *http.Client
}

// Ollama is an ai.Provider implementation backed by a local Ollama instance.
// No API key is required — availability is determined by whether the Ollama
// server is reachable at the configured base URL.
type Ollama struct {
	baseURL string
	client  *http.Client
}

// NewOllama constructs an Ollama provider.
func NewOllama(opts OllamaOpts) *Ollama {
	base := opts.BaseURL
	if base == "" {
		base = ollamaDefaultBaseURL
	}
	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	return &Ollama{baseURL: base, client: client}
}

// Name returns "ollama".
func (o *Ollama) Name() string { return "ollama" }

// Models returns the default Ollama model list.
func (o *Ollama) Models() []string { return defaultOllamaModels }

// Embed is not supported by this Ollama provider implementation;
// returns ai.ErrUnsupported.
func (o *Ollama) Embed(_ context.Context, _ ai.EmbedRequest) (*ai.EmbedResponse, error) {
	return nil, ai.ErrUnsupported
}

// IsReachable probes the Ollama server using GET /api/tags with the given
// context. Returns true if the server responds with HTTP 200 within the
// deadline set on ctx.
func (o *Ollama) IsReachable(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.baseURL+ollamaTagsPath, nil)
	if err != nil {
		return false
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close() //nolint:errcheck
	return resp.StatusCode == http.StatusOK
}

// Chat sends a chat request to the Ollama API.
//
// Returns ai.ErrProviderUnavailable if the Ollama server is unreachable.
//
// Ollama API shape (as of 2026, /api/chat):
//
//	POST /api/chat
//	{
//	  "model": "llama3.1:8b",
//	  "messages": [{"role":"user","content":"..."}],
//	  "stream": false,
//	  "options": {"num_predict": 1024}
//	}
//
//	Response (non-streaming):
//	{
//	  "model": "llama3.1:8b",
//	  "message": {"role": "assistant", "content": "..."},
//	  "done_reason": "stop",
//	  "prompt_eval_count": N,
//	  "eval_count": M
//	}
func (o *Ollama) Chat(ctx context.Context, req ai.ChatRequest) (*ai.ChatResponse, error) {
	model := req.Model
	if model == "" {
		model = defaultOllamaModels[0]
	}

	type ollamaMessage struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type ollamaOptions struct {
		NumPredict int `json:"num_predict,omitempty"`
	}
	type ollamaRequest struct {
		Model    string          `json:"model"`
		Messages []ollamaMessage `json:"messages"`
		Stream   bool            `json:"stream"`
		Options  ollamaOptions   `json:"options,omitempty"`
	}

	msgs := make([]ollamaMessage, 0, len(req.Messages)+1)
	if req.SystemPrompt != "" {
		msgs = append(msgs, ollamaMessage{Role: "system", Content: req.SystemPrompt})
	}
	for _, m := range req.Messages {
		msgs = append(msgs, ollamaMessage{Role: m.Role, Content: m.Content})
	}

	body := ollamaRequest{
		Model:    model,
		Messages: msgs,
		Stream:   false,
		Options:  ollamaOptions{NumPredict: req.MaxTokens},
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("ollama: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+ollamaChatPath, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("ollama: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w: ollama: %v", ai.ErrProviderUnavailable, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("ollama: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama: API error %d: %s", resp.StatusCode, string(respBody))
	}

	type ollamaResponseMessage struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type ollamaResponse struct {
		Model             string                `json:"model"`
		Message           ollamaResponseMessage `json:"message"`
		DoneReason        string                `json:"done_reason"`
		PromptEvalCount   int64                 `json:"prompt_eval_count"`
		EvalCount         int64                 `json:"eval_count"`
	}

	var apiResp ollamaResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("ollama: unmarshal response: %w", err)
	}

	return &ai.ChatResponse{
		Text:       apiResp.Message.Content,
		TokensIn:   apiResp.PromptEvalCount,
		TokensOut:  apiResp.EvalCount,
		Model:      apiResp.Model,
		StopReason: apiResp.DoneReason,
	}, nil
}

// probeOllama checks if an Ollama server is reachable at baseURL within
// a 1s timeout. Used by DetectAvailable.
func probeOllama(ctx context.Context, baseURL string, client *http.Client) bool {
	probeCtx, cancel := context.WithTimeout(ctx, ollamaProbeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, baseURL+ollamaTagsPath, nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close() //nolint:errcheck
	return resp.StatusCode == http.StatusOK
}

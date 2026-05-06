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
	openAIDefaultBaseURL  = "https://api.openai.com"
	openAIChatPath        = "/v1/chat/completions"
	openAIEmbeddingsPath  = "/v1/embeddings"
)

// defaultOpenAIModels is the curated list shown in the wizard UI.
var defaultOpenAIModels = []string{
	"gpt-4o",
	"gpt-4o-mini",
	"gpt-4-turbo",
	"gpt-3.5-turbo",
}

// defaultOpenAIEmbedModels is the curated list for embedding.
var defaultOpenAIEmbedModels = []string{
	"text-embedding-3-small",
	"text-embedding-3-large",
	"text-embedding-ada-002",
}

// OpenAIOpts configures an OpenAI provider instance.
type OpenAIOpts struct {
	// APIKey is the OpenAI API key. If empty, OPENAI_API_KEY is read from
	// the environment at construction time.
	//
	// NEVER read ~/.codex/auth.json — spec §17 non-goal #3.
	APIKey string
	// BaseURL overrides the default API base URL. Used by tests.
	BaseURL string
	// HTTPClient overrides the HTTP client. When nil, http.DefaultClient is used.
	HTTPClient *http.Client
}

// OpenAI is an ai.Provider implementation backed by the OpenAI Chat
// Completions API.
//
// Credentials come only from OpenAIOpts.APIKey or OPENAI_API_KEY env var.
// This provider NEVER reads ~/.codex/auth.json (spec §17).
type OpenAI struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

// NewOpenAI constructs an OpenAI provider.
func NewOpenAI(opts OpenAIOpts) *OpenAI {
	key := opts.APIKey
	if key == "" {
		key = os.Getenv("OPENAI_API_KEY")
	}
	base := opts.BaseURL
	if base == "" {
		base = openAIDefaultBaseURL
	}
	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	return &OpenAI{apiKey: key, baseURL: base, client: client}
}

// Name returns "openai".
func (o *OpenAI) Name() string { return "openai" }

// Models returns the default OpenAI model list.
func (o *OpenAI) Models() []string { return defaultOpenAIModels }

// Embed computes a dense vector embedding using the OpenAI Embeddings API.
func (o *OpenAI) Embed(ctx context.Context, req ai.EmbedRequest) (*ai.EmbedResponse, error) {
	if o.apiKey == "" {
		return nil, ai.ErrNoCredential
	}

	model := req.Model
	if model == "" {
		model = defaultOpenAIEmbedModels[0]
	}

	type embedRequest struct {
		Model string `json:"model"`
		Input string `json:"input"`
	}
	body := embedRequest{Model: model, Input: req.Input}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("openai: marshal embed request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+openAIEmbeddingsPath, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("openai: build embed request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+o.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w: openai: %v", ai.ErrProviderUnavailable, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("openai: read embed response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai: embed API error %d: %s", resp.StatusCode, string(respBody))
	}

	type embeddingData struct {
		Embedding []float32 `json:"embedding"`
	}
	type embedUsage struct {
		PromptTokens int64 `json:"prompt_tokens"`
	}
	type embedResponse struct {
		Data  []embeddingData `json:"data"`
		Model string          `json:"model"`
		Usage embedUsage      `json:"usage"`
	}

	var apiResp embedResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("openai: unmarshal embed response: %w", err)
	}
	if len(apiResp.Data) == 0 {
		return nil, fmt.Errorf("openai: no embedding data in response")
	}

	return &ai.EmbedResponse{
		Vector:   apiResp.Data[0].Embedding,
		Model:    apiResp.Model,
		TokensIn: apiResp.Usage.PromptTokens,
	}, nil
}

// Chat sends a request to the OpenAI Chat Completions API.
func (o *OpenAI) Chat(ctx context.Context, req ai.ChatRequest) (*ai.ChatResponse, error) {
	if o.apiKey == "" {
		return nil, ai.ErrNoCredential
	}

	model := req.Model
	if model == "" {
		model = defaultOpenAIModels[0]
	}

	type openAIMessage struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type openAIRequest struct {
		Model       string          `json:"model"`
		Messages    []openAIMessage `json:"messages"`
		MaxTokens   int             `json:"max_tokens,omitempty"`
		Temperature float64         `json:"temperature,omitempty"`
	}

	msgs := make([]openAIMessage, 0, len(req.Messages)+1)
	if req.SystemPrompt != "" {
		msgs = append(msgs, openAIMessage{Role: "system", Content: req.SystemPrompt})
	}
	for _, m := range req.Messages {
		msgs = append(msgs, openAIMessage{Role: m.Role, Content: m.Content})
	}

	body := openAIRequest{
		Model:       model,
		Messages:    msgs,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("openai: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+openAIChatPath, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("openai: build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+o.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w: openai: %v", ai.ErrProviderUnavailable, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("openai: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai: API error %d: %s", resp.StatusCode, string(respBody))
	}

	type choiceMessage struct {
		Content string `json:"content"`
	}
	type choice struct {
		Message      choiceMessage `json:"message"`
		FinishReason string        `json:"finish_reason"`
	}
	type usage struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
	}
	type openAIResponse struct {
		Choices []choice `json:"choices"`
		Usage   usage    `json:"usage"`
		Model   string   `json:"model"`
	}

	var apiResp openAIResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("openai: unmarshal response: %w", err)
	}
	if len(apiResp.Choices) == 0 {
		return nil, fmt.Errorf("openai: no choices in response")
	}

	return &ai.ChatResponse{
		Text:       apiResp.Choices[0].Message.Content,
		TokensIn:   apiResp.Usage.PromptTokens,
		TokensOut:  apiResp.Usage.CompletionTokens,
		Model:      apiResp.Model,
		StopReason: apiResp.Choices[0].FinishReason,
	}, nil
}

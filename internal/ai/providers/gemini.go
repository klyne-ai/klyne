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
	geminiDefaultBaseURL = "https://generativelanguage.googleapis.com"
)

// defaultGeminiModels is the curated list shown in the wizard UI.
var defaultGeminiModels = []string{
	"gemini-2.5-flash",
	"gemini-2.5-flash-lite",
	"gemini-2.0-flash",
	"gemini-1.5-pro",
}

// GeminiOpts configures a Gemini provider instance.
type GeminiOpts struct {
	// APIKey is the Gemini API key. If empty, GEMINI_API_KEY is read from
	// the environment at construction time.
	APIKey string
	// BaseURL overrides the default API base URL. Used by tests.
	BaseURL string
	// HTTPClient overrides the HTTP client. When nil, http.DefaultClient is used.
	HTTPClient *http.Client
}

// Gemini is an ai.Provider implementation backed by the Google Gemini
// generateContent API.
type Gemini struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

// NewGemini constructs a Gemini provider.
func NewGemini(opts GeminiOpts) *Gemini {
	key := opts.APIKey
	if key == "" {
		key = os.Getenv("GEMINI_API_KEY")
	}
	base := opts.BaseURL
	if base == "" {
		base = geminiDefaultBaseURL
	}
	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	return &Gemini{apiKey: key, baseURL: base, client: client}
}

// Name returns "gemini".
func (g *Gemini) Name() string { return "gemini" }

// Models returns the default Gemini model list.
func (g *Gemini) Models() []string { return defaultGeminiModels }

// Embed is not supported for the Gemini provider via this interface;
// returns ai.ErrUnsupported.
// Note: Google does have embedding models (e.g. text-embedding-004) but they
// use a separate embedContent endpoint. This can be added in a future workstream.
func (g *Gemini) Embed(_ context.Context, _ ai.EmbedRequest) (*ai.EmbedResponse, error) {
	return nil, ai.ErrUnsupported
}

// geminiChatPath returns the generateContent URL for a given model.
func geminiChatPath(model string) string {
	return fmt.Sprintf("/v1beta/models/%s:generateContent", model)
}

// Chat sends a request to the Gemini generateContent API.
//
// API shape (as of 2026):
//
//	POST /v1beta/models/{model}:generateContent?key={apiKey}
//
//	Body:
//	{
//	  "system_instruction": {"parts": [{"text": "..."}]},
//	  "contents": [
//	    {"role": "user", "parts": [{"text": "..."}]},
//	    {"role": "model", "parts": [{"text": "..."}]}
//	  ],
//	  "generationConfig": {"maxOutputTokens": 1024}
//	}
//
//	Response:
//	{
//	  "candidates": [{"content": {"parts": [{"text": "..."}]}, "finishReason": "STOP"}],
//	  "usageMetadata": {"promptTokenCount": N, "candidatesTokenCount": M, "totalTokenCount": T},
//	  "modelVersion": "..."
//	}
func (g *Gemini) Chat(ctx context.Context, req ai.ChatRequest) (*ai.ChatResponse, error) {
	if g.apiKey == "" {
		return nil, ai.ErrNoCredential
	}

	model := req.Model
	if model == "" {
		model = defaultGeminiModels[0]
	}

	type gPart struct {
		Text string `json:"text"`
	}
	type gContent struct {
		Role  string  `json:"role,omitempty"`
		Parts []gPart `json:"parts"`
	}
	type gGenerationConfig struct {
		MaxOutputTokens int     `json:"maxOutputTokens,omitempty"`
		Temperature     float64 `json:"temperature,omitempty"`
	}
	type gRequest struct {
		SystemInstruction *gContent         `json:"system_instruction,omitempty"`
		Contents          []gContent        `json:"contents"`
		GenerationConfig  gGenerationConfig `json:"generationConfig,omitempty"`
	}

	contents := make([]gContent, 0, len(req.Messages))
	for _, m := range req.Messages {
		// Gemini uses "user" and "model" roles (not "assistant").
		role := m.Role
		if role == "assistant" {
			role = "model"
		}
		contents = append(contents, gContent{
			Role:  role,
			Parts: []gPart{{Text: m.Content}},
		})
	}

	body := gRequest{
		Contents: contents,
		GenerationConfig: gGenerationConfig{
			MaxOutputTokens: req.MaxTokens,
			Temperature:     req.Temperature,
		},
	}
	if req.SystemPrompt != "" {
		body.SystemInstruction = &gContent{
			Parts: []gPart{{Text: req.SystemPrompt}},
		}
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("gemini: marshal request: %w", err)
	}

	url := g.baseURL + geminiChatPath(model) + "?key=" + g.apiKey
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("gemini: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w: gemini: %v", ai.ErrProviderUnavailable, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("gemini: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gemini: API error %d: %s", resp.StatusCode, string(respBody))
	}

	type gCandidatePart struct {
		Text string `json:"text"`
	}
	type gCandidateContent struct {
		Parts []gCandidatePart `json:"parts"`
	}
	type gCandidate struct {
		Content      gCandidateContent `json:"content"`
		FinishReason string            `json:"finishReason"`
	}
	type gUsageMetadata struct {
		PromptTokenCount     int64 `json:"promptTokenCount"`
		CandidatesTokenCount int64 `json:"candidatesTokenCount"`
	}
	type gResponse struct {
		Candidates   []gCandidate   `json:"candidates"`
		UsageMetadata gUsageMetadata `json:"usageMetadata"`
		ModelVersion  string         `json:"modelVersion"`
	}

	var apiResp gResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("gemini: unmarshal response: %w", err)
	}
	if len(apiResp.Candidates) == 0 {
		return nil, fmt.Errorf("gemini: no candidates in response")
	}

	text := ""
	for _, part := range apiResp.Candidates[0].Content.Parts {
		text += part.Text
	}

	// Normalize finish reason to lower-case "stop" convention.
	stopReason := apiResp.Candidates[0].FinishReason

	return &ai.ChatResponse{
		Text:       text,
		TokensIn:   apiResp.UsageMetadata.PromptTokenCount,
		TokensOut:  apiResp.UsageMetadata.CandidatesTokenCount,
		Model:      model,
		StopReason: stopReason,
	}, nil
}

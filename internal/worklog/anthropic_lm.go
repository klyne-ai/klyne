package worklog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// AnthropicLM is a minimal LM implementation backed by Anthropic's
// Messages API. Constructed via NewAnthropicLM(); returns nil when
// ANTHROPIC_API_KEY is not set so the caller can gracefully skip
// synthesis without crashing.
type AnthropicLM struct {
	apiKey  string
	model   string
	http    *http.Client
	baseURL string
}

// NewAnthropicLM reads ANTHROPIC_API_KEY from env. Returns nil + a
// descriptive error when the key is absent. Defaults to claude-haiku
// (cheapest tier — the Reflection prompts are small).
func NewAnthropicLM() (*AnthropicLM, error) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		return nil, errors.New("worklog: ANTHROPIC_API_KEY not set")
	}
	return &AnthropicLM{
		apiKey:  key,
		model:   "claude-haiku-4-5-20251001",
		http:    &http.Client{Timeout: 60 * time.Second},
		baseURL: "https://api.anthropic.com",
	}, nil
}

type anthropicReq struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResp struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// Complete sends prompt as a single user message and returns the
// concatenated text content of the response.
func (a *AnthropicLM) Complete(ctx context.Context, prompt string) (string, error) {
	body, err := json.Marshal(anthropicReq{
		Model:     a.model,
		MaxTokens: 1024,
		Messages:  []anthropicMessage{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return "", fmt.Errorf("worklog: marshal anthropic request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", a.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("worklog: build anthropic request: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", a.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := a.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("worklog: anthropic call: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("worklog: anthropic status %d: %s", resp.StatusCode, string(raw))
	}
	var out anthropicResp
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("worklog: parse anthropic response: %w", err)
	}
	var sb bytes.Buffer
	for _, c := range out.Content {
		if c.Type == "text" {
			sb.WriteString(c.Text)
		}
	}
	return sb.String(), nil
}

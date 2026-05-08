package providers_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/ai"
	"github.com/klyne-ai/klyne/internal/ai/providers"
)

func openAISuccessResponse() map[string]interface{} {
	return map[string]interface{}{
		"id":      "chatcmpl-123",
		"object":  "chat.completion",
		"model":   "gpt-4o-2024-08-06",
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"message": map[string]interface{}{
					"role":    "assistant",
					"content": "Hello from OpenAI!",
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]interface{}{
			"prompt_tokens":     15,
			"completion_tokens": 5,
			"total_tokens":      20,
		},
	}
}

func openAIEmbedSuccessResponse() map[string]interface{} {
	return map[string]interface{}{
		"object": "list",
		"data": []map[string]interface{}{
			{
				"object":    "embedding",
				"index":     0,
				"embedding": []float64{0.1, 0.2, 0.3},
			},
		},
		"model": "text-embedding-3-small",
		"usage": map[string]interface{}{
			"prompt_tokens": 8,
			"total_tokens":  8,
		},
	}
}

func TestOpenAI_Chat_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-openai-key" {
			t.Errorf("missing or wrong Authorization header")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(openAISuccessResponse()) //nolint:errcheck
	}))
	defer srv.Close()

	p := providers.NewOpenAI(providers.OpenAIOpts{
		APIKey:     "test-openai-key",
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
	})

	resp, err := p.Chat(context.Background(), ai.ChatRequest{
		Model:     "gpt-4o",
		Messages:  []ai.Message{{Role: "user", Content: "Hello"}},
		MaxTokens: 100,
	})
	if err != nil {
		t.Fatalf("Chat: unexpected error: %v", err)
	}
	if resp.Text != "Hello from OpenAI!" {
		t.Errorf("Text = %q, want %q", resp.Text, "Hello from OpenAI!")
	}
	if resp.TokensIn != 15 {
		t.Errorf("TokensIn = %d, want 15", resp.TokensIn)
	}
	if resp.TokensOut != 5 {
		t.Errorf("TokensOut = %d, want 5", resp.TokensOut)
	}
	if resp.StopReason != "stop" {
		t.Errorf("StopReason = %q, want %q", resp.StopReason, "stop")
	}
}

func TestOpenAI_Chat_APIError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
	}{
		{"400 bad request", http.StatusBadRequest},
		{"401 unauthorized", http.StatusUnauthorized},
		{"500 server error", http.StatusInternalServerError},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
				w.Write([]byte(`{"error":{"message":"test error","type":"api_error"}}`)) //nolint:errcheck
			}))
			defer srv.Close()

			p := providers.NewOpenAI(providers.OpenAIOpts{
				APIKey:     "test-key",
				BaseURL:    srv.URL,
				HTTPClient: srv.Client(),
			})

			_, err := p.Chat(context.Background(), ai.ChatRequest{
				Model:    "gpt-4o",
				Messages: []ai.Message{{Role: "user", Content: "Hello"}},
			})
			if err == nil {
				t.Errorf("expected error for status %d, got nil", tc.statusCode)
			}
		})
	}
}

func TestOpenAI_Chat_ContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := providers.NewOpenAI(providers.OpenAIOpts{
		APIKey:     "test-key",
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := p.Chat(ctx, ai.ChatRequest{
		Model:    "gpt-4o",
		Messages: []ai.Message{{Role: "user", Content: "Hello"}},
	})
	if err == nil {
		t.Fatal("expected error when context canceled, got nil")
	}
	if !errors.Is(err, context.Canceled) && !errors.Is(err, ai.ErrProviderUnavailable) {
		t.Errorf("expected context.Canceled or ErrProviderUnavailable, got: %v", err)
	}
}

func TestOpenAI_Chat_NoCredential(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")

	p := providers.NewOpenAI(providers.OpenAIOpts{
		BaseURL: "http://localhost:1",
	})

	_, err := p.Chat(context.Background(), ai.ChatRequest{
		Model:    "gpt-4o",
		Messages: []ai.Message{{Role: "user", Content: "Hello"}},
	})
	if !errors.Is(err, ai.ErrNoCredential) {
		t.Errorf("expected ErrNoCredential, got: %v", err)
	}
}

func TestOpenAI_Embed_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(openAIEmbedSuccessResponse()) //nolint:errcheck
	}))
	defer srv.Close()

	p := providers.NewOpenAI(providers.OpenAIOpts{
		APIKey:     "test-key",
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
	})

	resp, err := p.Embed(context.Background(), ai.EmbedRequest{
		Model: "text-embedding-3-small",
		Input: "Hello world",
	})
	if err != nil {
		t.Fatalf("Embed: unexpected error: %v", err)
	}
	if len(resp.Vector) != 3 {
		t.Errorf("Vector length = %d, want 3", len(resp.Vector))
	}
	if resp.TokensIn != 8 {
		t.Errorf("TokensIn = %d, want 8", resp.TokensIn)
	}
}

func TestOpenAI_Embed_NoCredential(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")

	p := providers.NewOpenAI(providers.OpenAIOpts{
		BaseURL: "http://localhost:1",
	})

	_, err := p.Embed(context.Background(), ai.EmbedRequest{
		Model: "text-embedding-3-small",
		Input: "hello",
	})
	if !errors.Is(err, ai.ErrNoCredential) {
		t.Errorf("expected ErrNoCredential, got: %v", err)
	}
}

func TestOpenAI_Name(t *testing.T) {
	p := providers.NewOpenAI(providers.OpenAIOpts{APIKey: "k"})
	if p.Name() != "openai" {
		t.Errorf("Name() = %q, want %q", p.Name(), "openai")
	}
}

func TestOpenAI_Models(t *testing.T) {
	p := providers.NewOpenAI(providers.OpenAIOpts{APIKey: "k"})
	if len(p.Models()) == 0 {
		t.Error("Models() returned empty slice")
	}
}

func TestOpenAI_Embed_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":{"message":"server error"}}`)) //nolint:errcheck
	}))
	defer srv.Close()

	p := providers.NewOpenAI(providers.OpenAIOpts{
		APIKey:     "test-key",
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
	})

	_, err := p.Embed(context.Background(), ai.EmbedRequest{
		Model: "text-embedding-3-small",
		Input: "hello",
	})
	if err == nil {
		t.Error("expected error for 500 response, got nil")
	}
}

func TestOpenAI_Embed_ContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := providers.NewOpenAI(providers.OpenAIOpts{
		APIKey:     "test-key",
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := p.Embed(ctx, ai.EmbedRequest{
		Model: "text-embedding-3-small",
		Input: "hello",
	})
	if err == nil {
		t.Fatal("expected error when context canceled, got nil")
	}
}

func TestOpenAI_Chat_SystemPromptPrepended(t *testing.T) {
	var capturedBody map[string]interface{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedBody) //nolint:errcheck
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(openAISuccessResponse()) //nolint:errcheck
	}))
	defer srv.Close()

	p := providers.NewOpenAI(providers.OpenAIOpts{
		APIKey:     "test-key",
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
	})

	_, err := p.Chat(context.Background(), ai.ChatRequest{
		Model:        "gpt-4o",
		Messages:     []ai.Message{{Role: "user", Content: "Hi"}},
		SystemPrompt: "Be concise.",
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	msgs, ok := capturedBody["messages"].([]interface{})
	if !ok || len(msgs) == 0 {
		t.Fatal("no messages in captured body")
	}
	first := msgs[0].(map[string]interface{})
	if first["role"] != "system" {
		t.Errorf("first message role = %q, want %q", first["role"], "system")
	}
	if first["content"] != "Be concise." {
		t.Errorf("system content = %q, want %q", first["content"], "Be concise.")
	}
}

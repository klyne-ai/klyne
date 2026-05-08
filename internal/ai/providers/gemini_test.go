package providers_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/ai"
	"github.com/klyne-ai/klyne/internal/ai/providers"
)

func geminiSuccessResponse() map[string]interface{} {
	return map[string]interface{}{
		"candidates": []map[string]interface{}{
			{
				"content": map[string]interface{}{
					"parts": []map[string]interface{}{
						{"text": "Hello from Gemini!"},
					},
					"role": "model",
				},
				"finishReason": "STOP",
				"index":        0,
			},
		},
		"usageMetadata": map[string]interface{}{
			"promptTokenCount":     10,
			"candidatesTokenCount": 4,
			"totalTokenCount":      14,
		},
		"modelVersion": "gemini-2.5-flash-001",
	}
}

func TestGemini_Chat_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1beta/models/") {
			t.Errorf("unexpected path prefix: %s", r.URL.Path)
		}
		if !strings.Contains(r.URL.Path, ":generateContent") {
			t.Errorf("path does not contain :generateContent: %s", r.URL.Path)
		}
		apiKey := r.URL.Query().Get("key")
		if apiKey != "test-gemini-key" {
			t.Errorf("unexpected api key: %s", apiKey)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(geminiSuccessResponse()) //nolint:errcheck
	}))
	defer srv.Close()

	p := providers.NewGemini(providers.GeminiOpts{
		APIKey:     "test-gemini-key",
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
	})

	resp, err := p.Chat(context.Background(), ai.ChatRequest{
		Model:     "gemini-2.5-flash",
		Messages:  []ai.Message{{Role: "user", Content: "Hello"}},
		MaxTokens: 100,
	})
	if err != nil {
		t.Fatalf("Chat: unexpected error: %v", err)
	}
	if resp.Text != "Hello from Gemini!" {
		t.Errorf("Text = %q, want %q", resp.Text, "Hello from Gemini!")
	}
	if resp.TokensIn != 10 {
		t.Errorf("TokensIn = %d, want 10", resp.TokensIn)
	}
	if resp.TokensOut != 4 {
		t.Errorf("TokensOut = %d, want 4", resp.TokensOut)
	}
	if resp.StopReason != "STOP" {
		t.Errorf("StopReason = %q, want %q", resp.StopReason, "STOP")
	}
}

func TestGemini_Chat_APIError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
	}{
		{"400 bad request", http.StatusBadRequest},
		{"403 forbidden", http.StatusForbidden},
		{"500 server error", http.StatusInternalServerError},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
				w.Write([]byte(`{"error":{"code":500,"message":"test error","status":"INTERNAL"}}`)) //nolint:errcheck
			}))
			defer srv.Close()

			p := providers.NewGemini(providers.GeminiOpts{
				APIKey:     "test-key",
				BaseURL:    srv.URL,
				HTTPClient: srv.Client(),
			})

			_, err := p.Chat(context.Background(), ai.ChatRequest{
				Model:    "gemini-2.5-flash",
				Messages: []ai.Message{{Role: "user", Content: "Hello"}},
			})
			if err == nil {
				t.Errorf("expected error for status %d, got nil", tc.statusCode)
			}
		})
	}
}

func TestGemini_Chat_ContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := providers.NewGemini(providers.GeminiOpts{
		APIKey:     "test-key",
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := p.Chat(ctx, ai.ChatRequest{
		Model:    "gemini-2.5-flash",
		Messages: []ai.Message{{Role: "user", Content: "Hello"}},
	})
	if err == nil {
		t.Fatal("expected error when context canceled, got nil")
	}
	if !errors.Is(err, context.Canceled) && !errors.Is(err, ai.ErrProviderUnavailable) {
		t.Errorf("expected context.Canceled or ErrProviderUnavailable, got: %v", err)
	}
}

func TestGemini_Chat_NoCredential(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")

	p := providers.NewGemini(providers.GeminiOpts{
		BaseURL: "http://localhost:1",
	})

	_, err := p.Chat(context.Background(), ai.ChatRequest{
		Model:    "gemini-2.5-flash",
		Messages: []ai.Message{{Role: "user", Content: "Hello"}},
	})
	if !errors.Is(err, ai.ErrNoCredential) {
		t.Errorf("expected ErrNoCredential, got: %v", err)
	}
}

func TestGemini_Embed_Unsupported(t *testing.T) {
	p := providers.NewGemini(providers.GeminiOpts{APIKey: "k"})
	_, err := p.Embed(context.Background(), ai.EmbedRequest{Model: "any", Input: "test"})
	if !errors.Is(err, ai.ErrUnsupported) {
		t.Errorf("expected ErrUnsupported, got: %v", err)
	}
}

func TestGemini_Name(t *testing.T) {
	p := providers.NewGemini(providers.GeminiOpts{APIKey: "k"})
	if p.Name() != "gemini" {
		t.Errorf("Name() = %q, want %q", p.Name(), "gemini")
	}
}

func TestGemini_Models(t *testing.T) {
	p := providers.NewGemini(providers.GeminiOpts{APIKey: "k"})
	if len(p.Models()) == 0 {
		t.Error("Models() returned empty slice")
	}
}

func TestGemini_Chat_AssistantRoleNormalized(t *testing.T) {
	var capturedBody map[string]interface{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedBody) //nolint:errcheck
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(geminiSuccessResponse()) //nolint:errcheck
	}))
	defer srv.Close()

	p := providers.NewGemini(providers.GeminiOpts{
		APIKey:     "test-key",
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
	})

	_, err := p.Chat(context.Background(), ai.ChatRequest{
		Model: "gemini-2.5-flash",
		Messages: []ai.Message{
			{Role: "user", Content: "Hi"},
			{Role: "assistant", Content: "Hello"},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	contents, ok := capturedBody["contents"].([]interface{})
	if !ok || len(contents) < 2 {
		t.Fatalf("expected 2 contents, got: %v", capturedBody["contents"])
	}
	second := contents[1].(map[string]interface{})
	if second["role"] != "model" {
		t.Errorf("assistant role not normalized to 'model': got %q", second["role"])
	}
}

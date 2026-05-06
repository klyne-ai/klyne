package providers_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mohitpatell/agentdeck/internal/ai"
	"github.com/mohitpatell/agentdeck/internal/ai/providers"
)

func ollamaSuccessResponse() map[string]interface{} {
	return map[string]interface{}{
		"model": "llama3.1:8b",
		"message": map[string]interface{}{
			"role":    "assistant",
			"content": "Hello from Ollama!",
		},
		"done_reason":       "stop",
		"done":              true,
		"prompt_eval_count": 12,
		"eval_count":        4,
	}
}

func TestOllama_Chat_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		// Verify stream: false is set.
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if stream, _ := body["stream"].(bool); stream {
			t.Error("stream should be false")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ollamaSuccessResponse()) //nolint:errcheck
	}))
	defer srv.Close()

	p := providers.NewOllama(providers.OllamaOpts{
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
	})

	resp, err := p.Chat(context.Background(), ai.ChatRequest{
		Model:     "llama3.1:8b",
		Messages:  []ai.Message{{Role: "user", Content: "Hello"}},
		MaxTokens: 100,
	})
	if err != nil {
		t.Fatalf("Chat: unexpected error: %v", err)
	}
	if resp.Text != "Hello from Ollama!" {
		t.Errorf("Text = %q, want %q", resp.Text, "Hello from Ollama!")
	}
	if resp.TokensIn != 12 {
		t.Errorf("TokensIn = %d, want 12", resp.TokensIn)
	}
	if resp.TokensOut != 4 {
		t.Errorf("TokensOut = %d, want 4", resp.TokensOut)
	}
	if resp.StopReason != "stop" {
		t.Errorf("StopReason = %q, want %q", resp.StopReason, "stop")
	}
}

func TestOllama_Chat_APIError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
	}{
		{"400 bad request", http.StatusBadRequest},
		{"404 model not found", http.StatusNotFound},
		{"500 server error", http.StatusInternalServerError},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
				w.Write([]byte(`{"error":"test error"}`)) //nolint:errcheck
			}))
			defer srv.Close()

			p := providers.NewOllama(providers.OllamaOpts{
				BaseURL:    srv.URL,
				HTTPClient: srv.Client(),
			})

			_, err := p.Chat(context.Background(), ai.ChatRequest{
				Model:    "llama3.1:8b",
				Messages: []ai.Message{{Role: "user", Content: "Hello"}},
			})
			if err == nil {
				t.Errorf("expected error for status %d, got nil", tc.statusCode)
			}
		})
	}
}

func TestOllama_Chat_ContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := providers.NewOllama(providers.OllamaOpts{
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := p.Chat(ctx, ai.ChatRequest{
		Model:    "llama3.1:8b",
		Messages: []ai.Message{{Role: "user", Content: "Hello"}},
	})
	if err == nil {
		t.Fatal("expected error when context canceled, got nil")
	}
	if !errors.Is(err, context.Canceled) && !errors.Is(err, ai.ErrProviderUnavailable) {
		t.Errorf("expected context.Canceled or ErrProviderUnavailable, got: %v", err)
	}
}

// TestOllama_LocalhostUnreachable verifies that when the Ollama server is not
// running, DetectAvailable reports it as unavailable, and Chat returns
// ErrProviderUnavailable.
func TestOllama_LocalhostUnreachable(t *testing.T) {
	// Find a free port then immediately close the listener so the port is
	// guaranteed to refuse connections.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not find free port: %v", err)
	}
	closedAddr := "http://" + ln.Addr().String()
	ln.Close()

	p := providers.NewOllama(providers.OllamaOpts{
		BaseURL:    closedAddr,
		HTTPClient: &http.Client{Timeout: 2 * time.Second},
	})

	// Chat should return ErrProviderUnavailable.
	_, chatErr := p.Chat(context.Background(), ai.ChatRequest{
		Model:    "llama3.1:8b",
		Messages: []ai.Message{{Role: "user", Content: "Hello"}},
	})
	if !errors.Is(chatErr, ai.ErrProviderUnavailable) {
		t.Errorf("Chat: expected ErrProviderUnavailable, got: %v", chatErr)
	}

	// DetectAvailable should report ollama as unavailable when pointing at
	// the closed port.
	infos := providers.DetectAvailable(context.Background(),
		providers.WithOllamaBaseURL(closedAddr),
	)
	var ollamaInfo *ai.ProviderInfo
	for i := range infos {
		if infos[i].Name == "ollama" {
			ollamaInfo = &infos[i]
			break
		}
	}
	if ollamaInfo == nil {
		t.Fatal("ollama not found in DetectAvailable results")
	}
	if ollamaInfo.Available {
		t.Error("ollama should be unavailable when localhost is closed")
	}
}

func TestOllama_Embed_Unsupported(t *testing.T) {
	p := providers.NewOllama(providers.OllamaOpts{})
	_, err := p.Embed(context.Background(), ai.EmbedRequest{Model: "any", Input: "test"})
	if !errors.Is(err, ai.ErrUnsupported) {
		t.Errorf("expected ErrUnsupported, got: %v", err)
	}
}

func TestOllama_Name(t *testing.T) {
	p := providers.NewOllama(providers.OllamaOpts{})
	if p.Name() != "ollama" {
		t.Errorf("Name() = %q, want %q", p.Name(), "ollama")
	}
}

func TestOllama_Models(t *testing.T) {
	p := providers.NewOllama(providers.OllamaOpts{})
	if len(p.Models()) == 0 {
		t.Error("Models() returned empty slice")
	}
}

func TestOllama_IsReachable_True(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"models":[]}`)) //nolint:errcheck
	}))
	defer srv.Close()

	p := providers.NewOllama(providers.OllamaOpts{
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
	})

	if !p.IsReachable(context.Background()) {
		t.Error("IsReachable should return true when server responds 200")
	}
}

func TestOllama_IsReachable_False(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not find free port: %v", err)
	}
	closedAddr := "http://" + ln.Addr().String()
	ln.Close()

	p := providers.NewOllama(providers.OllamaOpts{
		BaseURL:    closedAddr,
		HTTPClient: &http.Client{Timeout: 2 * time.Second},
	})

	if p.IsReachable(context.Background()) {
		t.Error("IsReachable should return false when port is closed")
	}
}

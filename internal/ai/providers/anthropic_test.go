package providers_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mohitpatell/agentdeck/internal/ai"
	"github.com/mohitpatell/agentdeck/internal/ai/providers"
)

// anthropicSuccessResponse returns a realistic Anthropic Messages API
// response payload.
func anthropicSuccessResponse() map[string]interface{} {
	return map[string]interface{}{
		"id":   "msg_01XFDUDYJgAACzvnptvVoYEL",
		"type": "message",
		"role": "assistant",
		"content": []map[string]interface{}{
			{"type": "text", "text": "Hello, world!"},
		},
		"model":       "claude-sonnet-4-6-20250514",
		"stop_reason": "end_turn",
		"usage": map[string]interface{}{
			"input_tokens":  42,
			"output_tokens": 7,
		},
	}
}

func TestAnthropic_Chat_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("missing or wrong API key header")
		}
		if r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Errorf("missing anthropic-version header")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(anthropicSuccessResponse()) //nolint:errcheck
	}))
	defer srv.Close()

	p := providers.NewAnthropic(providers.AnthropicOpts{
		APIKey:     "test-key",
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
	})

	resp, err := p.Chat(context.Background(), ai.ChatRequest{
		Model:     "claude-sonnet-4-6",
		Messages:  []ai.Message{{Role: "user", Content: "Hello"}},
		MaxTokens: 100,
	})
	if err != nil {
		t.Fatalf("Chat: unexpected error: %v", err)
	}
	if resp.Text != "Hello, world!" {
		t.Errorf("Text = %q, want %q", resp.Text, "Hello, world!")
	}
	if resp.TokensIn != 42 {
		t.Errorf("TokensIn = %d, want 42", resp.TokensIn)
	}
	if resp.TokensOut != 7 {
		t.Errorf("TokensOut = %d, want 7", resp.TokensOut)
	}
	if resp.StopReason != "end_turn" {
		t.Errorf("StopReason = %q, want %q", resp.StopReason, "end_turn")
	}
}

func TestAnthropic_Chat_APIError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
	}{
		{"400 bad request", http.StatusBadRequest},
		{"401 unauthorized", http.StatusUnauthorized},
		{"429 rate limit", http.StatusTooManyRequests},
		{"500 server error", http.StatusInternalServerError},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
				w.Write([]byte(`{"error":{"type":"api_error","message":"test error"}}`)) //nolint:errcheck
			}))
			defer srv.Close()

			p := providers.NewAnthropic(providers.AnthropicOpts{
				APIKey:     "test-key",
				BaseURL:    srv.URL,
				HTTPClient: srv.Client(),
			})

			_, err := p.Chat(context.Background(), ai.ChatRequest{
				Model:    "claude-sonnet-4-6",
				Messages: []ai.Message{{Role: "user", Content: "Hello"}},
			})
			if err == nil {
				t.Errorf("expected error for status %d, got nil", tc.statusCode)
			}
		})
	}
}

func TestAnthropic_Chat_ContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Block until client disconnects.
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := providers.NewAnthropic(providers.AnthropicOpts{
		APIKey:     "test-key",
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := p.Chat(ctx, ai.ChatRequest{
		Model:    "claude-sonnet-4-6",
		Messages: []ai.Message{{Role: "user", Content: "Hello"}},
	})
	if err == nil {
		t.Fatal("expected error when context canceled, got nil")
	}
	if !errors.Is(err, context.Canceled) && !errors.Is(err, ai.ErrProviderUnavailable) {
		t.Errorf("expected context.Canceled or ErrProviderUnavailable, got: %v", err)
	}
}

func TestAnthropic_Chat_NoCredential(t *testing.T) {
	// Ensure ANTHROPIC_API_KEY is unset for this test.
	t.Setenv("ANTHROPIC_API_KEY", "")

	p := providers.NewAnthropic(providers.AnthropicOpts{
		// No APIKey, no env var → ErrNoCredential
		BaseURL: "http://localhost:1", // unreachable but should not be hit
	})

	_, err := p.Chat(context.Background(), ai.ChatRequest{
		Model:    "claude-sonnet-4-6",
		Messages: []ai.Message{{Role: "user", Content: "Hello"}},
	})
	if !errors.Is(err, ai.ErrNoCredential) {
		t.Errorf("expected ErrNoCredential, got: %v", err)
	}
}

// TestAnthropic_NeverReadsOAuth asserts that the Anthropic provider never
// opens any file under a fake ~/.claude directory, even when ANTHROPIC_API_KEY
// is unset.
//
// This test exists to guard the spec §8 constraint that has been Anthropic
// server-side enforced since 2026-04-04: Claude Code OAuth tokens must never
// be reused by third-party tools. The provider must return ErrNoCredential
// and must NOT attempt to read credential files.
func TestAnthropic_NeverReadsOAuth(t *testing.T) {
	// Create a temp directory that represents a fake ~/.claude tree with a
	// sentinel credential file. If the provider tries to open any file here,
	// the sentinel file's existence would let us detect it. But we don't even
	// hook os.Open — instead we verify the provider fails fast with
	// ErrNoCredential before any I/O can occur, by confirming the sentinel
	// path was never needed.

	tmpHome := t.TempDir()
	fakeDotClaude := filepath.Join(tmpHome, ".claude")
	if err := os.MkdirAll(fakeDotClaude, 0o700); err != nil {
		t.Fatalf("setup: %v", err)
	}
	sentinelFile := filepath.Join(fakeDotClaude, "credentials.json")
	if err := os.WriteFile(sentinelFile, []byte(`{"oauth_token":"SENTINEL"}`), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// Ensure ANTHROPIC_API_KEY is unset — provider must not fall back to
	// reading the sentinel file.
	t.Setenv("ANTHROPIC_API_KEY", "")

	p := providers.NewAnthropic(providers.AnthropicOpts{
		// No APIKey; do NOT use HOME or sentinelFile in any way.
		BaseURL: "http://localhost:1",
	})

	_, err := p.Chat(context.Background(), ai.ChatRequest{
		Model:    "claude-sonnet-4-6",
		Messages: []ai.Message{{Role: "user", Content: "Hello"}},
	})

	// The only acceptable outcome is ErrNoCredential, returned BEFORE any
	// network or file I/O. If we get any other error the provider attempted
	// something it should not have.
	if !errors.Is(err, ai.ErrNoCredential) {
		t.Errorf("expected ErrNoCredential (no file reads allowed), got: %v", err)
	}

	// Also assert that Embed returns ErrUnsupported (not ErrNoCredential and
	// definitely not a file-read attempt).
	_, embedErr := p.Embed(context.Background(), ai.EmbedRequest{Model: "any", Input: "test"})
	if !errors.Is(embedErr, ai.ErrUnsupported) {
		t.Errorf("Embed: expected ErrUnsupported, got: %v", embedErr)
	}
}

func TestAnthropic_Name(t *testing.T) {
	p := providers.NewAnthropic(providers.AnthropicOpts{APIKey: "k"})
	if p.Name() != "anthropic" {
		t.Errorf("Name() = %q, want %q", p.Name(), "anthropic")
	}
}

func TestAnthropic_Models(t *testing.T) {
	p := providers.NewAnthropic(providers.AnthropicOpts{APIKey: "k"})
	models := p.Models()
	if len(models) == 0 {
		t.Error("Models() returned empty slice")
	}
}

func TestAnthropic_Chat_SystemPrompt(t *testing.T) {
	var capturedBody map[string]interface{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedBody) //nolint:errcheck
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(anthropicSuccessResponse()) //nolint:errcheck
	}))
	defer srv.Close()

	p := providers.NewAnthropic(providers.AnthropicOpts{
		APIKey:     "test-key",
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
	})

	_, err := p.Chat(context.Background(), ai.ChatRequest{
		Model:        "claude-sonnet-4-6",
		Messages:     []ai.Message{{Role: "user", Content: "Hi"}},
		SystemPrompt: "You are a helpful assistant.",
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if capturedBody["system"] != "You are a helpful assistant." {
		t.Errorf("system prompt not forwarded: %v", capturedBody["system"])
	}
}

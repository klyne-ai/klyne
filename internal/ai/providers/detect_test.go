package providers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mohitpatell/agentdeck/internal/ai"
	"github.com/mohitpatell/agentdeck/internal/ai/providers"
)

// findProvider is a helper to locate a ProviderInfo by name.
func findProvider(infos []ai.ProviderInfo, name string) *ai.ProviderInfo {
	for i := range infos {
		if infos[i].Name == name {
			return &infos[i]
		}
	}
	return nil
}

// TestDetect_AllCombinations exercises every combination of env var
// presence/absence and verifies that reason strings are stable.
func TestDetect_AllCombinations(t *testing.T) {
	// Start a mock Ollama server for "reachable" cases.
	ollamaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"models":[]}`)) //nolint:errcheck
	}))
	defer ollamaSrv.Close()

	tests := []struct {
		name              string
		anthropicKey      string
		openaiKey         string
		geminiKey         string
		ollamaReachable   bool
		wantAnthropic     bool
		wantOpenAI        bool
		wantGemini        bool
		wantOllama        bool
		wantAnthropicReason string
		wantOpenAIReason    string
		wantGeminiReason    string
	}{
		{
			name:                "all set",
			anthropicKey:        "sk-ant-test",
			openaiKey:           "sk-openai-test",
			geminiKey:           "gemini-test",
			ollamaReachable:     true,
			wantAnthropic:       true,
			wantOpenAI:          true,
			wantGemini:          true,
			wantOllama:          true,
			wantAnthropicReason: "ANTHROPIC_API_KEY set",
			wantOpenAIReason:    "OPENAI_API_KEY set",
			wantGeminiReason:    "GEMINI_API_KEY set",
		},
		{
			name:                "none set",
			anthropicKey:        "",
			openaiKey:           "",
			geminiKey:           "",
			ollamaReachable:     false,
			wantAnthropic:       false,
			wantOpenAI:          false,
			wantGemini:          false,
			wantOllama:          false,
			wantAnthropicReason: "ANTHROPIC_API_KEY not set",
			wantOpenAIReason:    "OPENAI_API_KEY not set",
			wantGeminiReason:    "GEMINI_API_KEY not set",
		},
		{
			name:                "anthropic only",
			anthropicKey:        "sk-ant-test",
			openaiKey:           "",
			geminiKey:           "",
			ollamaReachable:     false,
			wantAnthropic:       true,
			wantOpenAI:          false,
			wantGemini:          false,
			wantOllama:          false,
			wantAnthropicReason: "ANTHROPIC_API_KEY set",
			wantOpenAIReason:    "OPENAI_API_KEY not set",
			wantGeminiReason:    "GEMINI_API_KEY not set",
		},
		{
			name:                "openai only",
			anthropicKey:        "",
			openaiKey:           "sk-openai-test",
			geminiKey:           "",
			ollamaReachable:     false,
			wantAnthropic:       false,
			wantOpenAI:          true,
			wantGemini:          false,
			wantOllama:          false,
			wantAnthropicReason: "ANTHROPIC_API_KEY not set",
			wantOpenAIReason:    "OPENAI_API_KEY set",
			wantGeminiReason:    "GEMINI_API_KEY not set",
		},
		{
			name:                "gemini only",
			anthropicKey:        "",
			openaiKey:           "",
			geminiKey:           "gemini-test",
			ollamaReachable:     false,
			wantAnthropic:       false,
			wantOpenAI:          false,
			wantGemini:          true,
			wantOllama:          false,
			wantAnthropicReason: "ANTHROPIC_API_KEY not set",
			wantOpenAIReason:    "OPENAI_API_KEY not set",
			wantGeminiReason:    "GEMINI_API_KEY set",
		},
		{
			name:                "ollama only",
			anthropicKey:        "",
			openaiKey:           "",
			geminiKey:           "",
			ollamaReachable:     true,
			wantAnthropic:       false,
			wantOpenAI:          false,
			wantGemini:          false,
			wantOllama:          true,
			wantAnthropicReason: "ANTHROPIC_API_KEY not set",
			wantOpenAIReason:    "OPENAI_API_KEY not set",
			wantGeminiReason:    "GEMINI_API_KEY not set",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ANTHROPIC_API_KEY", tc.anthropicKey)
			t.Setenv("OPENAI_API_KEY", tc.openaiKey)
			t.Setenv("GEMINI_API_KEY", tc.geminiKey)

			var ollamaOpts []providers.DetectOption
			if tc.ollamaReachable {
				ollamaOpts = append(ollamaOpts,
					providers.WithOllamaBaseURL(ollamaSrv.URL),
					providers.WithOllamaHTTPClient(ollamaSrv.Client()),
				)
			} else {
				ollamaOpts = append(ollamaOpts,
					providers.WithOllamaBaseURL("http://127.0.0.1:1"),
				)
			}

			infos := providers.DetectAvailable(context.Background(), ollamaOpts...)

			if len(infos) != 4 {
				t.Fatalf("expected 4 providers, got %d", len(infos))
			}

			check := func(name string, wantAvail bool, wantReason string) {
				t.Helper()
				p := findProvider(infos, name)
				if p == nil {
					t.Errorf("provider %q not found", name)
					return
				}
				if p.Available != wantAvail {
					t.Errorf("%s: Available = %v, want %v", name, p.Available, wantAvail)
				}
				if p.Reason != wantReason {
					t.Errorf("%s: Reason = %q, want %q", name, p.Reason, wantReason)
				}
				if len(p.Models) == 0 {
					t.Errorf("%s: Models is empty", name)
				}
			}

			check("anthropic", tc.wantAnthropic, tc.wantAnthropicReason)
			check("openai", tc.wantOpenAI, tc.wantOpenAIReason)
			check("gemini", tc.wantGemini, tc.wantGeminiReason)

			ollamaInfo := findProvider(infos, "ollama")
			if ollamaInfo == nil {
				t.Error("ollama not found in results")
				return
			}
			if ollamaInfo.Available != tc.wantOllama {
				t.Errorf("ollama: Available = %v, want %v", ollamaInfo.Available, tc.wantOllama)
			}
			if len(ollamaInfo.Models) == 0 {
				t.Error("ollama: Models is empty")
			}
		})
	}
}

// TestDetect_OllamaReachable verifies that when Ollama is reachable, the
// reason string contains the base URL.
func TestDetect_OllamaReachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"models":[]}`)) //nolint:errcheck
	}))
	defer srv.Close()

	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")

	infos := providers.DetectAvailable(
		context.Background(),
		providers.WithOllamaBaseURL(srv.URL),
		providers.WithOllamaHTTPClient(srv.Client()),
	)

	ollamaInfo := findProvider(infos, "ollama")
	if ollamaInfo == nil {
		t.Fatal("ollama not found in results")
	}
	if !ollamaInfo.Available {
		t.Error("ollama should be available when server responds 200")
	}
}

// TestDetect_ProviderCount asserts exactly 4 providers are always returned.
func TestDetect_ProviderCount(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")

	infos := providers.DetectAvailable(
		context.Background(),
		providers.WithOllamaBaseURL("http://127.0.0.1:1"),
	)
	if len(infos) != 4 {
		t.Errorf("expected 4 providers, got %d", len(infos))
	}

	names := map[string]bool{}
	for _, info := range infos {
		names[info.Name] = true
	}
	for _, expected := range []string{"anthropic", "openai", "gemini", "ollama"} {
		if !names[expected] {
			t.Errorf("provider %q missing from results", expected)
		}
	}
}

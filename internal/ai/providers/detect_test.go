package providers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/klyne-ai/klyne/internal/ai"
	"github.com/klyne-ai/klyne/internal/ai/providers"
)

// findProvider locates a ProviderInfo by name.
func findProvider(infos []ai.ProviderInfo, name string) *ai.ProviderInfo {
	for i := range infos {
		if infos[i].Name == name {
			return &infos[i]
		}
	}
	return nil
}

// stubExecutable writes an empty executable file the kernel will accept
// as a binary. Used to flip CLI-availability probes on without needing
// the real binary on the test machine.
func stubExecutable(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-cli")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return path
}

func TestDetect_AllCombinations(t *testing.T) {
	ollamaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"models":[]}`))
	}))
	defer ollamaSrv.Close()

	realStub := stubExecutable(t)
	const missingPath = "/no/such/binary"

	tests := []struct {
		name             string
		claudeBinary     string
		codexBinary      string
		ollamaReachable  bool
		wantClaude       bool
		wantCodex        bool
		wantOllama       bool
		wantClaudeReason string
		wantCodexReason  string
	}{
		{
			name:             "all available",
			claudeBinary:     realStub,
			codexBinary:      realStub,
			ollamaReachable:  true,
			wantClaude:       true,
			wantCodex:        true,
			wantOllama:       true,
			wantClaudeReason: "`claude` binary on PATH — using your Claude Code subscription",
			wantCodexReason:  "`codex` binary on PATH — using your Codex CLI session",
		},
		{
			name:             "none available",
			claudeBinary:     missingPath,
			codexBinary:      missingPath,
			ollamaReachable:  false,
			wantClaude:       false,
			wantCodex:        false,
			wantOllama:       false,
			wantClaudeReason: "`claude` binary not on PATH — install Claude Code to enable",
			wantCodexReason:  "`codex` binary not on PATH — install OpenAI Codex CLI to enable",
		},
		{
			name:             "claude only",
			claudeBinary:     realStub,
			codexBinary:      missingPath,
			ollamaReachable:  false,
			wantClaude:       true,
			wantCodex:        false,
			wantOllama:       false,
			wantClaudeReason: "`claude` binary on PATH — using your Claude Code subscription",
			wantCodexReason:  "`codex` binary not on PATH — install OpenAI Codex CLI to enable",
		},
		{
			name:             "ollama only",
			claudeBinary:     missingPath,
			codexBinary:      missingPath,
			ollamaReachable:  true,
			wantClaude:       false,
			wantCodex:        false,
			wantOllama:       true,
			wantClaudeReason: "`claude` binary not on PATH — install Claude Code to enable",
			wantCodexReason:  "`codex` binary not on PATH — install OpenAI Codex CLI to enable",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opts := []providers.DetectOption{
				providers.WithClaudeBinary(tc.claudeBinary),
				providers.WithCodexBinary(tc.codexBinary),
			}
			if tc.ollamaReachable {
				opts = append(opts,
					providers.WithOllamaBaseURL(ollamaSrv.URL),
					providers.WithOllamaHTTPClient(ollamaSrv.Client()),
				)
			} else {
				opts = append(opts, providers.WithOllamaBaseURL("http://127.0.0.1:1"))
			}

			infos := providers.DetectAvailable(context.Background(), opts...)

			if len(infos) != 3 {
				t.Fatalf("expected 3 providers, got %d", len(infos))
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

			check("claude-cli", tc.wantClaude, tc.wantClaudeReason)
			check("codex-cli", tc.wantCodex, tc.wantCodexReason)

			ollamaInfo := findProvider(infos, "ollama")
			if ollamaInfo == nil {
				t.Fatal("ollama not found in results")
			}
			if ollamaInfo.Available != tc.wantOllama {
				t.Errorf("ollama: Available = %v, want %v", ollamaInfo.Available, tc.wantOllama)
			}
		})
	}
}

func TestDetect_ProviderCountAndNames(t *testing.T) {
	infos := providers.DetectAvailable(
		context.Background(),
		providers.WithClaudeBinary("/no/such/claude"),
		providers.WithCodexBinary("/no/such/codex"),
		providers.WithOllamaBaseURL("http://127.0.0.1:1"),
	)
	if len(infos) != 3 {
		t.Errorf("expected 3 providers, got %d", len(infos))
	}
	names := map[string]bool{}
	for _, info := range infos {
		names[info.Name] = true
	}
	for _, expected := range []string{"claude-cli", "codex-cli", "ollama"} {
		if !names[expected] {
			t.Errorf("provider %q missing from results", expected)
		}
	}
	for _, gone := range []string{"anthropic", "openai", "gemini"} {
		if names[gone] {
			t.Errorf("provider %q must no longer appear (replaced by CLI variant)", gone)
		}
	}
}

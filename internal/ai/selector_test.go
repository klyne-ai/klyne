package ai_test

import (
	"errors"
	"testing"

	"github.com/klyne-ai/klyne/internal/ai"
)

// helpers

func providerInfo(name string, available bool) ai.ProviderInfo {
	return ai.ProviderInfo{Name: name, Available: available}
}

// allProviders builds the full set of post-rewrite providers. The
// argument order matches the preference order for Summarize/Title:
// claude-cli first, then codex-cli, then ollama.
func allProviders(claude, codex, ollama bool) []ai.ProviderInfo {
	return []ai.ProviderInfo{
		providerInfo("claude-cli", claude),
		providerInfo("codex-cli", codex),
		providerInfo("ollama", ollama),
	}
}

// TestPick_FullMatrix exercises every available-providers combination
// for each task. There are 3 providers post-rewrite, so 2^3 = 8 subsets.
func TestPick_FullMatrix(t *testing.T) {
	t.Parallel()

	type tc struct {
		name         string
		task         ai.TaskKind
		available    []ai.ProviderInfo
		wantProvider string
		wantModel    string
		wantErr      bool
	}

	tests := []tc{
		// ── Summarize: claude-cli → codex-cli → ollama ──
		{
			name:         "Summarize/all-available → claude-cli wins",
			task:         ai.TaskSummarize,
			available:    allProviders(true, true, true),
			wantProvider: "claude-cli",
			wantModel:    "claude-haiku-4",
		},
		{
			name:         "Summarize/no-claude → codex-cli wins",
			task:         ai.TaskSummarize,
			available:    allProviders(false, true, true),
			wantProvider: "codex-cli",
			wantModel:    "gpt-5-mini",
		},
		{
			name:         "Summarize/only-ollama → ollama wins",
			task:         ai.TaskSummarize,
			available:    allProviders(false, false, true),
			wantProvider: "ollama",
			wantModel:    "llama3.1:8b",
		},
		{
			name:      "Summarize/none-available → error",
			task:      ai.TaskSummarize,
			available: allProviders(false, false, false),
			wantErr:   true,
		},

		// ── Title: claude-cli → codex-cli (gpt-5-nano) → ollama ──
		{
			name:         "Title/all-available → claude-cli wins",
			task:         ai.TaskTitle,
			available:    allProviders(true, true, true),
			wantProvider: "claude-cli",
			wantModel:    "claude-haiku-4",
		},
		{
			name:         "Title/no-claude → codex-cli (nano) wins",
			task:         ai.TaskTitle,
			available:    allProviders(false, true, true),
			wantProvider: "codex-cli",
			wantModel:    "gpt-5-nano",
		},
		{
			name:         "Title/only-ollama → ollama wins",
			task:         ai.TaskTitle,
			available:    allProviders(false, false, true),
			wantProvider: "ollama",
			wantModel:    "llama3.1:8b",
		},
		{
			name:      "Title/none-available → error",
			task:      ai.TaskTitle,
			available: allProviders(false, false, false),
			wantErr:   true,
		},

		// ── Embed: ollama only (no CLI provider supports embeddings) ──
		{
			name:         "Embed/ollama-available → ollama wins",
			task:         ai.TaskEmbed,
			available:    allProviders(true, true, true),
			wantProvider: "ollama",
			wantModel:    "nomic-embed-text",
		},
		{
			name:      "Embed/no-ollama → error",
			task:      ai.TaskEmbed,
			available: allProviders(true, true, false),
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ai.Pick(tt.task, tt.available, "")
			if tt.wantErr {
				if err == nil {
					t.Errorf("Pick() = (%+v, nil); want error", got)
				}
				if !errors.Is(err, ai.ErrNoProviderAvailable) {
					t.Errorf("Pick() error = %v; want ErrNoProviderAvailable", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Pick() unexpected error: %v", err)
			}
			if got.Provider != tt.wantProvider {
				t.Errorf("Provider = %q; want %q", got.Provider, tt.wantProvider)
			}
			if got.Model != tt.wantModel {
				t.Errorf("Model = %q; want %q", got.Model, tt.wantModel)
			}
		})
	}
}

func TestPick_OverrideHonored(t *testing.T) {
	t.Parallel()
	available := allProviders(true, true, true) // claude-cli would normally win

	got, err := ai.Pick(ai.TaskSummarize, available, "codex-cli:gpt-5-mini")
	if err != nil {
		t.Fatalf("Pick() unexpected error: %v", err)
	}
	if got.Provider != "codex-cli" {
		t.Errorf("Provider = %q; want %q", got.Provider, "codex-cli")
	}
	if got.Model != "gpt-5-mini" {
		t.Errorf("Model = %q; want %q", got.Model, "gpt-5-mini")
	}
	if got.Reason == "" {
		t.Error("Reason must be non-empty for override picks")
	}
}

func TestPick_OverrideUnavailable_FallsBack(t *testing.T) {
	t.Parallel()
	// codex-cli not available; claude-cli is.
	available := allProviders(true, false, false)

	got, err := ai.Pick(ai.TaskSummarize, available, "codex-cli:gpt-5-mini")
	if err != nil {
		t.Fatalf("Pick() unexpected error: %v", err)
	}
	if got.Provider != "claude-cli" {
		t.Errorf("Provider = %q; want %q (fallback)", got.Provider, "claude-cli")
	}
	if got.Reason == "" {
		t.Error("Reason must be non-empty")
	}
	if !containsAny(got.Reason, "codex-cli:gpt-5-mini", "unavailable", "Override") {
		t.Errorf("Reason %q should mention the override miss", got.Reason)
	}
}

func TestPick_NoneAvailable_Errors(t *testing.T) {
	t.Parallel()
	available := allProviders(false, false, false)
	_, err := ai.Pick(ai.TaskSummarize, available, "")
	if err == nil {
		t.Fatal("Pick() expected error, got nil")
	}
	if !errors.Is(err, ai.ErrNoProviderAvailable) {
		t.Errorf("error = %v; want ErrNoProviderAvailable", err)
	}
}

func TestPick_NoneAvailable_WithOverride_Errors(t *testing.T) {
	t.Parallel()
	available := allProviders(false, false, false)
	_, err := ai.Pick(ai.TaskSummarize, available, "claude-cli:claude-haiku-4")
	if err == nil {
		t.Fatal("Pick() expected error, got nil")
	}
	if !errors.Is(err, ai.ErrNoProviderAvailable) {
		t.Errorf("error = %v; want ErrNoProviderAvailable", err)
	}
}

func TestPick_ReasonStringsHumanReadable(t *testing.T) {
	t.Parallel()

	tasks := []ai.TaskKind{ai.TaskSummarize, ai.TaskTitle, ai.TaskEmbed}
	provs := []string{"claude-cli", "codex-cli", "ollama"}

	for _, task := range tasks {
		for _, prov := range provs {
			prov := prov
			task := task
			available := []ai.ProviderInfo{providerInfo(prov, true)}
			got, err := ai.Pick(task, available, "")
			if err != nil {
				// e.g. claude-cli/codex-cli not in Embed table — skip.
				continue
			}
			if got.Reason == "" {
				t.Errorf("task=%d provider=%s: Reason is empty", task, prov)
			}
			if len(got.Reason) < 10 {
				t.Errorf("task=%d provider=%s: Reason %q is too short to be human-readable", task, prov, got.Reason)
			}
		}
	}
}

func TestPick_Auto_TreatedAsNoOverride(t *testing.T) {
	t.Parallel()
	available := allProviders(false, true, false)

	gotAuto, err := ai.Pick(ai.TaskSummarize, available, "auto")
	if err != nil {
		t.Fatalf("Pick(auto): %v", err)
	}
	gotEmpty, err := ai.Pick(ai.TaskSummarize, available, "")
	if err != nil {
		t.Fatalf("Pick(empty): %v", err)
	}
	if gotAuto.Provider != gotEmpty.Provider || gotAuto.Model != gotEmpty.Model {
		t.Errorf("auto=%v vs empty=%v — should be identical", gotAuto, gotEmpty)
	}
}

// containsAny returns true if s contains any of the given substrings.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) > 0 {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}

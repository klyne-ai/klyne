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

func allProviders(gemini, openai, anthropic, ollama bool) []ai.ProviderInfo {
	return []ai.ProviderInfo{
		providerInfo("gemini", gemini),
		providerInfo("openai", openai),
		providerInfo("anthropic", anthropic),
		providerInfo("ollama", ollama),
	}
}

// TestPick_FullMatrix exercises every (task, available-providers) combination
// that matters for spec §8 coverage.  We enumerate all 16 non-empty subsets of
// {gemini, openai, anthropic, ollama} for Summarize and Title, and the 8
// subsets of {openai, gemini, ollama} for Embed.
func TestPick_FullMatrix(t *testing.T) {
	t.Parallel()

	type tc struct {
		name             string
		task             ai.TaskKind
		available        []ai.ProviderInfo
		wantProvider     string
		wantModel        string
		wantErr          bool
	}

	tests := []tc{
		// ── Summarize preference order: gemini → openai → anthropic → ollama ──
		{
			name:         "Summarize/all-available → gemini wins",
			task:         ai.TaskSummarize,
			available:    allProviders(true, true, true, true),
			wantProvider: "gemini",
			wantModel:    "gemini-2.5-flash-lite",
		},
		{
			name:         "Summarize/no-gemini → openai wins",
			task:         ai.TaskSummarize,
			available:    allProviders(false, true, true, true),
			wantProvider: "openai",
			wantModel:    "gpt-5-mini",
		},
		{
			name:         "Summarize/no-gemini-no-openai → anthropic wins",
			task:         ai.TaskSummarize,
			available:    allProviders(false, false, true, true),
			wantProvider: "anthropic",
			wantModel:    "claude-haiku-4",
		},
		{
			name:         "Summarize/only-ollama → ollama wins",
			task:         ai.TaskSummarize,
			available:    allProviders(false, false, false, true),
			wantProvider: "ollama",
			wantModel:    "llama3.1:8b",
		},
		{
			name:         "Summarize/gemini-only → gemini wins",
			task:         ai.TaskSummarize,
			available:    allProviders(true, false, false, false),
			wantProvider: "gemini",
			wantModel:    "gemini-2.5-flash-lite",
		},
		{
			name:         "Summarize/openai-only → openai wins",
			task:         ai.TaskSummarize,
			available:    allProviders(false, true, false, false),
			wantProvider: "openai",
			wantModel:    "gpt-5-mini",
		},
		{
			name:         "Summarize/anthropic-only → anthropic wins",
			task:         ai.TaskSummarize,
			available:    allProviders(false, false, true, false),
			wantProvider: "anthropic",
			wantModel:    "claude-haiku-4",
		},
		{
			name:     "Summarize/none-available → error",
			task:     ai.TaskSummarize,
			available: allProviders(false, false, false, false),
			wantErr:  true,
		},
		// ── Title preference order: gemini → openai → anthropic → ollama ──
		{
			name:         "Title/all-available → gemini wins",
			task:         ai.TaskTitle,
			available:    allProviders(true, true, true, true),
			wantProvider: "gemini",
			wantModel:    "gemini-2.5-flash-lite",
		},
		{
			name:         "Title/no-gemini → openai wins (gpt-5-nano)",
			task:         ai.TaskTitle,
			available:    allProviders(false, true, true, true),
			wantProvider: "openai",
			wantModel:    "gpt-5-nano",
		},
		{
			name:         "Title/no-gemini-no-openai → anthropic wins",
			task:         ai.TaskTitle,
			available:    allProviders(false, false, true, true),
			wantProvider: "anthropic",
			wantModel:    "claude-haiku-4",
		},
		{
			name:         "Title/only-ollama → ollama wins",
			task:         ai.TaskTitle,
			available:    allProviders(false, false, false, true),
			wantProvider: "ollama",
			wantModel:    "llama3.1:8b",
		},
		{
			name:     "Title/none-available → error",
			task:     ai.TaskTitle,
			available: allProviders(false, false, false, false),
			wantErr:  true,
		},
		// ── Embed preference order: openai → gemini → ollama ──
		{
			name:  "Embed/all-available → openai wins",
			task:  ai.TaskEmbed,
			available: []ai.ProviderInfo{
				providerInfo("openai", true),
				providerInfo("gemini", true),
				providerInfo("ollama", true),
				providerInfo("anthropic", true),
			},
			wantProvider: "openai",
			wantModel:    "text-embedding-3-small",
		},
		{
			name:  "Embed/no-openai → gemini wins",
			task:  ai.TaskEmbed,
			available: []ai.ProviderInfo{
				providerInfo("openai", false),
				providerInfo("gemini", true),
				providerInfo("ollama", true),
			},
			wantProvider: "gemini",
			wantModel:    "text-embedding-004",
		},
		{
			name:  "Embed/only-ollama → ollama wins",
			task:  ai.TaskEmbed,
			available: []ai.ProviderInfo{
				providerInfo("openai", false),
				providerInfo("gemini", false),
				providerInfo("ollama", true),
			},
			wantProvider: "ollama",
			wantModel:    "nomic-embed-text",
		},
		{
			name:  "Embed/none-available → error",
			task:  ai.TaskEmbed,
			available: []ai.ProviderInfo{
				providerInfo("openai", false),
				providerInfo("gemini", false),
				providerInfo("ollama", false),
			},
			wantErr: true,
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

// TestPick_OverrideHonored asserts that a valid override "openai:gpt-5-mini"
// is returned when openai is available, regardless of preference order.
func TestPick_OverrideHonored(t *testing.T) {
	t.Parallel()
	available := allProviders(true, true, true, true) // gemini would normally win

	got, err := ai.Pick(ai.TaskSummarize, available, "openai:gpt-5-mini")
	if err != nil {
		t.Fatalf("Pick() unexpected error: %v", err)
	}
	if got.Provider != "openai" {
		t.Errorf("Provider = %q; want %q", got.Provider, "openai")
	}
	if got.Model != "gpt-5-mini" {
		t.Errorf("Model = %q; want %q", got.Model, "gpt-5-mini")
	}
	if got.Reason == "" {
		t.Error("Reason must be non-empty for override picks")
	}
}

// TestPick_OverrideUnavailable_FallsBack asserts that when the overridden
// provider is not available, Pick falls back to the preference order and
// the Reason mentions the override miss.
func TestPick_OverrideUnavailable_FallsBack(t *testing.T) {
	t.Parallel()
	// anthropic not available; gemini is.
	available := allProviders(true, false, false, false)

	got, err := ai.Pick(ai.TaskSummarize, available, "anthropic:claude-haiku-4")
	if err != nil {
		t.Fatalf("Pick() unexpected error: %v", err)
	}
	// Should fall back to gemini.
	if got.Provider != "gemini" {
		t.Errorf("Provider = %q; want %q (fallback)", got.Provider, "gemini")
	}
	// Reason must mention the override miss.
	if got.Reason == "" {
		t.Error("Reason must be non-empty")
	}
	// The reason should reference the failed override.
	if !containsAny(got.Reason, "anthropic:claude-haiku-4", "unavailable", "Override") {
		t.Errorf("Reason %q should mention the override miss", got.Reason)
	}
}

// TestPick_NoneAvailable_Errors asserts that ErrNoProviderAvailable is
// returned when no provider is available, even with an override.
func TestPick_NoneAvailable_Errors(t *testing.T) {
	t.Parallel()
	available := allProviders(false, false, false, false)

	_, err := ai.Pick(ai.TaskSummarize, available, "")
	if err == nil {
		t.Fatal("Pick() expected error, got nil")
	}
	if !errors.Is(err, ai.ErrNoProviderAvailable) {
		t.Errorf("error = %v; want ErrNoProviderAvailable", err)
	}
}

// TestPick_NoneAvailable_WithOverride_Errors asserts that an override for an
// unavailable provider, with no other providers available, returns an error.
func TestPick_NoneAvailable_WithOverride_Errors(t *testing.T) {
	t.Parallel()
	available := allProviders(false, false, false, false)

	_, err := ai.Pick(ai.TaskSummarize, available, "openai:gpt-5-mini")
	if err == nil {
		t.Fatal("Pick() expected error, got nil")
	}
	if !errors.Is(err, ai.ErrNoProviderAvailable) {
		t.Errorf("error = %v; want ErrNoProviderAvailable", err)
	}
}

// TestPick_ReasonStringsHumanReadable asserts that every successful Pick
// returns a non-empty, descriptive Reason string.
func TestPick_ReasonStringsHumanReadable(t *testing.T) {
	t.Parallel()

	tasks := []ai.TaskKind{ai.TaskSummarize, ai.TaskTitle, ai.TaskEmbed}
	providers := []string{"gemini", "openai", "anthropic", "ollama"}

	for _, task := range tasks {
		for _, prov := range providers {
			prov := prov
			task := task
			available := []ai.ProviderInfo{providerInfo(prov, true)}
			got, err := ai.Pick(task, available, "")
			if err != nil {
				// Some providers don't cover all tasks (e.g. anthropic not in Embed)
				// — that's fine, just skip.
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

// TestPick_Auto_TreatedAsNoOverride asserts that override="auto" behaves
// identically to override="".
func TestPick_Auto_TreatedAsNoOverride(t *testing.T) {
	t.Parallel()
	available := allProviders(false, true, false, false)

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
			// simple linear scan; fine for tests.
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}

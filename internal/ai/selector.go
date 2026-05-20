// Package ai — smart BYOK provider selector.
//
// Pick implements the spec §8 preference-order matrix for each task kind.
// It is a pure function: no I/O, no global state, exhaustively unit-testable.
package ai

import (
	"errors"
	"fmt"
	"strings"
)

// TaskKind identifies which AI task a provider is being selected for.
type TaskKind int

const (
	// TaskSummarize picks a provider for rolling session summarisation.
	TaskSummarize TaskKind = iota
	// TaskTitle picks a provider for 3-7 word session title generation.
	TaskTitle
	// TaskEmbed picks a provider for text embedding (v1.1 stub — selector
	// handles it, but tasks/embed.go is NOT shipped in v1).
	TaskEmbed
)

// Choice is the result of Pick: the recommended provider+model combination.
type Choice struct {
	// Provider is the stable lower-case provider identifier:
	// "claude-cli" | "codex-cli" | "ollama".
	Provider string
	// Model is the model identifier to pass in ChatRequest.Model.
	Model string
	// Reason is a user-readable explanation surfaced in the wizard UI's
	// "why this was picked" badge. Always non-empty on a successful Pick.
	Reason string
}

// preferenceEntry is one candidate in the ordered preference table.
type preferenceEntry struct {
	provider string
	model    string
	reason   string
}

// preferenceTable maps each TaskKind to its ordered list of candidates.
// First matching available provider wins.
//
// Post 2026-05-20: API-key-backed providers (anthropic / openai /
// gemini) are gone. Klyne's product promise is "no separate API key",
// so every cloud provider shells out to the user's existing CLI
// subscription. Embed has no CLI equivalent — Ollama (local) is the
// only option for that task.
var preferenceTable = map[TaskKind][]preferenceEntry{
	TaskSummarize: {
		{
			provider: "claude-cli",
			model:    "claude-haiku-4",
			reason:   "Claude CLI (haiku-4) picked: your Claude Code subscription, no API key",
		},
		{
			provider: "codex-cli",
			model:    "gpt-5-mini",
			reason:   "Codex CLI (gpt-5-mini) picked: your Codex subscription, no API key",
		},
		{
			provider: "ollama",
			model:    "llama3.1:8b",
			reason:   "Ollama llama3.1:8b picked: local inference, no API cost",
		},
	},
	TaskTitle: {
		{
			provider: "claude-cli",
			model:    "claude-haiku-4",
			reason:   "Claude CLI (haiku-4) picked: lightweight, your Claude Code subscription",
		},
		{
			provider: "codex-cli",
			model:    "gpt-5-nano",
			reason:   "Codex CLI (gpt-5-nano) picked: smallest model, your Codex subscription",
		},
		{
			provider: "ollama",
			model:    "llama3.1:8b",
			reason:   "Ollama llama3.1:8b picked: local inference, no API cost",
		},
	},
	TaskEmbed: {
		{
			provider: "ollama",
			model:    "nomic-embed-text",
			reason:   "Ollama nomic-embed-text picked: only embedding option without an API key",
		},
	},
}

// ErrNoProviderAvailable is returned by Pick when no provider in the
// preference order is available.
var ErrNoProviderAvailable = errors.New("ai/selector: no provider available for this task")

// Pick returns the recommended provider+model for task given the
// available providers reported by providers.DetectAvailable. Preference
// order (post 2026-05-20 CLI-subprocess rewrite):
//
//	Summarize: claude-cli (haiku-4) → codex-cli (gpt-5-mini) → ollama llama3.1:8b
//	Title:     claude-cli (haiku-4) → codex-cli (gpt-5-nano) → ollama llama3.1:8b
//	Embed:     ollama nomic-embed-text  (no CLI provider supports embeddings)
//
// If override is non-empty (e.g. "claude-cli:claude-sonnet-4-6"), Pick
// forces that provider+model IF that provider is available; otherwise
// it falls back to the preference order and notes the miss in
// Choice.Reason.
//
// If no provider in the preference order is available, Pick returns
// ErrNoProviderAvailable.
func Pick(task TaskKind, available []ProviderInfo, override string) (Choice, error) {
	// Build a set of available provider names for O(1) lookup.
	avail := make(map[string]bool, len(available))
	for _, p := range available {
		if p.Available {
			avail[p.Name] = true
		}
	}

	// Handle override — format: "<provider>:<model>" or "auto".
	if override != "" && override != "auto" {
		prov, model, ok := parseOverride(override)
		if ok && avail[prov] {
			return Choice{
				Provider: prov,
				Model:    model,
				Reason:   fmt.Sprintf("Override applied: %s/%s (user-configured)", prov, model),
			}, nil
		}
		// Override provider not available — fall back with a warning in Reason.
		choice, err := pickFromTable(task, avail)
		if err != nil {
			return Choice{}, err
		}
		choice.Reason = fmt.Sprintf(
			"Override %q unavailable (provider not detected); falling back: %s",
			override, choice.Reason,
		)
		return choice, nil
	}

	return pickFromTable(task, avail)
}

// pickFromTable walks the preference table for task and returns the first
// candidate whose provider is in the avail set.
func pickFromTable(task TaskKind, avail map[string]bool) (Choice, error) {
	entries, ok := preferenceTable[task]
	if !ok {
		return Choice{}, fmt.Errorf("ai/selector: unknown task kind %d", task)
	}

	for _, e := range entries {
		if avail[e.provider] {
			return Choice{
				Provider: e.provider,
				Model:    e.model,
				Reason:   e.reason,
			}, nil
		}
	}
	return Choice{}, ErrNoProviderAvailable
}

// parseOverride splits "provider:model" into its two parts. Returns ok=false
// when the format is invalid.
func parseOverride(s string) (provider, model string, ok bool) {
	idx := strings.IndexByte(s, ':')
	if idx <= 0 || idx == len(s)-1 {
		return "", "", false
	}
	return s[:idx], s[idx+1:], true
}

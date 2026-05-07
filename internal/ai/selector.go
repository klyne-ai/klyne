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
	// "anthropic" | "openai" | "gemini" | "ollama".
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
// First matching available provider wins (spec §8).
var preferenceTable = map[TaskKind][]preferenceEntry{
	TaskSummarize: {
		{
			provider: "gemini",
			model:    "gemini-2.5-flash-lite",
			reason:   "Gemini Flash-Lite picked: free tier, 1,000 RPD covers your workload",
		},
		{
			provider: "openai",
			model:    "gpt-5-mini",
			reason:   "OpenAI gpt-5-mini picked: cost-efficient, fast turnaround for summaries",
		},
		{
			provider: "anthropic",
			model:    "claude-haiku-4",
			reason:   "Anthropic claude-haiku-4 picked: lightest Claude model, low latency",
		},
		{
			provider: "ollama",
			model:    "llama3.1:8b",
			reason:   "Ollama llama3.1:8b picked: local inference, no API cost",
		},
	},
	TaskTitle: {
		{
			provider: "gemini",
			model:    "gemini-2.5-flash-lite",
			reason:   "Gemini Flash-Lite picked: free tier, instant title generation",
		},
		{
			provider: "openai",
			model:    "gpt-5-nano",
			reason:   "OpenAI gpt-5-nano picked: smallest OpenAI model, ideal for short-output tasks",
		},
		{
			provider: "anthropic",
			model:    "claude-haiku-4",
			reason:   "Anthropic claude-haiku-4 picked: lightest Claude model, low latency",
		},
		{
			provider: "ollama",
			model:    "llama3.1:8b",
			reason:   "Ollama llama3.1:8b picked: local inference, no API cost",
		},
	},
	TaskEmbed: {
		{
			provider: "openai",
			model:    "text-embedding-3-small",
			reason:   "OpenAI text-embedding-3-small picked: high quality, low cost embeddings",
		},
		{
			provider: "gemini",
			model:    "text-embedding-004",
			reason:   "Gemini text-embedding-004 picked: free tier embedding model",
		},
		{
			provider: "ollama",
			model:    "nomic-embed-text",
			reason:   "Ollama nomic-embed-text picked: local embedding, no API cost",
		},
	},
}

// ErrNoProviderAvailable is returned by Pick when no provider in the
// preference order is available.
var ErrNoProviderAvailable = errors.New("ai/selector: no provider available for this task")

// Pick returns the recommended provider+model for task given the available
// providers reported by providers.DetectAvailable. It implements the spec §8
// preference order:
//
//	Summarize: Gemini Flash-Lite → gpt-5-mini → claude-haiku-4 → ollama llama3.1:8b
//	Title:     Gemini Flash-Lite → gpt-5-nano  → claude-haiku-4 → ollama llama3.1:8b
//	Embed:     OpenAI text-embedding-3-small → Gemini text-embedding-004 → ollama nomic-embed-text
//
// If override is non-empty (e.g. "openai:gpt-5-mini"), Pick forces that
// provider+model IF that provider is available; otherwise it falls back to the
// preference order and notes the miss in Choice.Reason.
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

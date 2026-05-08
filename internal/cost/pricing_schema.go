// Package cost defines the typed shape of pricing.json — the LiteLLM-style
// per-million-token rate card that powers the cost engine.
//
// W0-FROZEN CONTRACT
// ------------------
// W0 ships the schema only. W9 fills in the v1 default rate values for
// the model coverage list in docs/plan/04-shared-contracts.md §8 (claude-
// sonnet-4.5, claude-haiku-4, claude-opus-4.6, gpt-5, gpt-5-mini,
// gpt-5-nano, gemini-2.5-flash, gemini-2.5-flash-lite, text-embedding-3-
// small, llama3.1:8b).
//
// Spec references:
//   - docs/plan/04-shared-contracts.md §8 (JSON shape)
//   - spec §18 #6 (local LiteLLM-style table baked into the binary)
package cost

// PricingFile is the on-disk JSON document. Both the binary's embedded
// default and the user's optional ~/.klyne/pricing.json override
// share this shape.
type PricingFile struct {
	// Version is the schema version. v1 ships with Version=1.
	Version int `json:"version"`
	// Models maps the canonical model identifier -> rate card.
	Models map[string]PerTokenRates `json:"models"`
}

// PerTokenRates is the rate card for one model. All values are USD per
// one million tokens (the LiteLLM convention). Fields default to 0 when
// absent in the JSON, which the cost engine treats as "free / unknown".
type PerTokenRates struct {
	PromptPerMtok     float64 `json:"prompt_per_mtok"`
	CompletionPerMtok float64 `json:"completion_per_mtok"`
	CacheReadPerMtok  float64 `json:"cache_read_per_mtok"`
	CacheWritePerMtok float64 `json:"cache_write_per_mtok"`
}

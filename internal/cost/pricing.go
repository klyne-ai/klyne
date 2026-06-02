// Package cost implements the cost engine: per-token USD calculations and
// session/project/daily/model rollups backed by the SQLite store.
//
// Rate sources:
//   - Anthropic: https://www.anthropic.com/pricing
//   - OpenAI: https://openai.com/pricing
//   - Google AI: https://ai.google.dev/pricing
//
// Rates in pricing.json are approximate as of 2025-05 and must be refreshed
// in W17 before launch. Local models (llama3.1:8b) are zero-cost.
//
// Spec references:
//   - §7  (cost fields in Message / Session)
//   - §10 (LiteLLM-style table)
//   - §15 D7 (per-session $ matches ccusage within 0.5%) — deferred to W16/W17
package cost

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"log"
	"regexp"

	"github.com/klyne-ai/klyne/internal/config"
)

// dateSuffix matches a trailing `-YYYYMMDD` release-date suffix that real
// transcripts append to model IDs (e.g. `claude-sonnet-4-5-20250929`).
// pricing.json keys are the undated family IDs (`claude-sonnet-4-5`), so we
// strip this before retrying the lookup.
var dateSuffix = regexp.MustCompile(`-\d{8}$`)

//go:embed pricing.json
var embeddedPricing []byte

// Engine holds the merged pricing table (embedded + optional user override)
// and exposes Lookup / Cost / Rollup* methods. It is safe for concurrent use
// after construction.
type Engine struct {
	// rates is the merged model → PerTokenRates map.
	// All reads are performed after construction; no locking needed.
	rates map[string]PerTokenRates
}

// New constructs an Engine by:
//  1. Decoding the embedded pricing.json,
//  2. Loading and merging the user's override file (cfg.Paths.PricingOverride),
//     if it exists.
//
// Override entries take precedence over embedded entries for the same model
// key. Models present only in the embedded file are retained unchanged.
func New(cfg *config.Config) (*Engine, error) {
	embedded, err := decodePricingJSON(embeddedPricing)
	if err != nil {
		return nil, fmt.Errorf("cost: decode embedded pricing.json: %w", err)
	}

	// Build the initial rates map from the embedded file.
	rates := make(map[string]PerTokenRates, len(embedded.Models))
	for k, v := range embedded.Models {
		rates[k] = v
	}

	// Try to load the user override. loadOverride returns nil, nil when the
	// file does not exist so we can proceed silently.
	overridePath := ""
	if cfg != nil {
		overridePath = cfg.Paths.PricingOverride
	}
	if overridePath != "" {
		override, err := loadOverride(overridePath)
		if err != nil {
			return nil, fmt.Errorf("cost: load pricing override: %w", err)
		}
		if override != nil {
			for k, v := range override.Models {
				rates[k] = v
			}
		}
	}

	return &Engine{rates: rates}, nil
}

// Lookup returns the PerTokenRates for the given model identifier and true,
// or an empty PerTokenRates and false if the model is not in the table.
//
// The model key is normalized before lookup so dated transcript IDs resolve
// to their undated pricing.json family entry — see resolveRates.
func (e *Engine) Lookup(model string) (PerTokenRates, bool) {
	return e.resolveRates(model)
}

// resolveRates resolves a (possibly dated/over-specified) model identifier to
// a pricing entry. Resolution order:
//
//  1. Exact match on the verbatim key.
//  2. Strip a trailing `-YYYYMMDD` release-date suffix and retry exact match
//     (`claude-sonnet-4-5-20250929` → `claude-sonnet-4-5`).
//  3. Longest-prefix family fallback: pick the longest pricing.json key that
//     the (date-stripped) model is a `<key>-...` extension of. This catches
//     variant suffixes other than a date without ever matching across
//     families (the `-` boundary check stops `gpt-5` from absorbing
//     `gpt-5-mini`-style siblings into a shorter key when a longer one fits).
//
// Returns the matched rates and true, or the zero PerTokenRates and false
// when nothing resolves. It never fabricates a rate.
func (e *Engine) resolveRates(model string) (PerTokenRates, bool) {
	if r, ok := e.rates[model]; ok {
		return r, true
	}
	stripped := dateSuffix.ReplaceAllString(model, "")
	if stripped != model {
		if r, ok := e.rates[stripped]; ok {
			return r, true
		}
	}
	// Longest-prefix family fallback.
	var bestKey string
	for k := range e.rates {
		if len(k) >= len(stripped) {
			continue
		}
		// stripped must extend k at a `-` boundary: "<k>-...".
		if stripped[:len(k)] == k && stripped[len(k)] == '-' {
			if len(k) > len(bestKey) {
				bestKey = k
			}
		}
	}
	if bestKey != "" {
		return e.rates[bestKey], true
	}
	return PerTokenRates{}, false
}

// Cost returns the USD cost for the given token counts and model.
//
// tokensIn is the TOTAL prompt-side token count (fresh + cache_read +
// cache_write). cachedRead and cachedWrite are the cached subsets of
// tokensIn so the engine can apply differentiated rates. The fresh
// portion (the remainder) is billed at the full prompt rate.
//
// Formula:
//
//	fresh = max(tokensIn - cachedRead - cachedWrite, 0)
//	cost  = (fresh        / 1_000_000) * rates.PromptPerMtok
//	      + (tokensOut    / 1_000_000) * rates.CompletionPerMtok
//	      + (cachedRead   / 1_000_000) * rates.CacheReadPerMtok
//	      + (cachedWrite  / 1_000_000) * rates.CacheWritePerMtok
//
// If the model is not found in the pricing table, Cost logs a warning and
// returns 0 (not an error — matches spec §10 behaviour for unknown models).
func (e *Engine) Cost(tokensIn, tokensOut, cachedRead, cachedWrite int64, model string) float64 {
	r, ok := e.resolveRates(model)
	if !ok {
		log.Printf("cost: unknown model %q — returning $0.00 (add to pricing.json to resolve)", model)
		return 0
	}
	fresh := tokensIn - cachedRead - cachedWrite
	if fresh < 0 {
		fresh = 0
	}
	const perMillion = 1_000_000.0
	return float64(fresh)/perMillion*r.PromptPerMtok +
		float64(tokensOut)/perMillion*r.CompletionPerMtok +
		float64(cachedRead)/perMillion*r.CacheReadPerMtok +
		float64(cachedWrite)/perMillion*r.CacheWritePerMtok
}

// ---------------------------------------------------------------------------
// Rollup methods — sum costs across the messages table for various groupings.
//
// All rollups fetch (tokens_in, tokens_out, model) from the messages table
// and re-compute cost via Cost(), ensuring the store never drifts from the
// current pricing table. This matches the ccusage parity requirement (D7):
// the cost is always the live table rate applied to the recorded token counts.
// ---------------------------------------------------------------------------

// RollupSession returns the total cost in USD for all messages in sessionID.
func (e *Engine) RollupSession(ctx context.Context, db *sql.DB, sessionID string) (float64, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT COALESCE(tokens_in, 0), COALESCE(tokens_out, 0),
		        COALESCE(cached_read_tokens, 0), COALESCE(cached_write_tokens, 0),
		        COALESCE(model, '')
		   FROM messages
		  WHERE session_id = ?`,
		sessionID)
	if err != nil {
		return 0, fmt.Errorf("cost: RollupSession query: %w", err)
	}
	defer rows.Close()

	return sumMessageRows(e, rows)
}

// RollupProject returns the total cost in USD for all messages in sessions
// whose project_path matches path and whose ts >= since (epoch-ms).
// Pass since=0 to include all time.
func (e *Engine) RollupProject(ctx context.Context, db *sql.DB, path string, since int64) (float64, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT COALESCE(m.tokens_in, 0), COALESCE(m.tokens_out, 0),
		        COALESCE(m.cached_read_tokens, 0), COALESCE(m.cached_write_tokens, 0),
		        COALESCE(m.model, '')
		   FROM messages m
		   JOIN sessions s ON s.id = m.session_id
		  WHERE s.project_path = ?
		    AND m.ts >= ?`,
		path, since)
	if err != nil {
		return 0, fmt.Errorf("cost: RollupProject query: %w", err)
	}
	defer rows.Close()

	return sumMessageRows(e, rows)
}

// RollupDaily returns the total cost in USD for all messages whose ts falls
// within the calendar day starting at dayStartMs (epoch-ms, UTC midnight).
// The day window is [dayStartMs, dayStartMs+86_400_000).
func (e *Engine) RollupDaily(ctx context.Context, db *sql.DB, dayStartMs int64) (float64, error) {
	dayEndMs := dayStartMs + 86_400_000
	rows, err := db.QueryContext(ctx,
		`SELECT COALESCE(tokens_in, 0), COALESCE(tokens_out, 0),
		        COALESCE(cached_read_tokens, 0), COALESCE(cached_write_tokens, 0),
		        COALESCE(model, '')
		   FROM messages
		  WHERE ts >= ? AND ts < ?`,
		dayStartMs, dayEndMs)
	if err != nil {
		return 0, fmt.Errorf("cost: RollupDaily query: %w", err)
	}
	defer rows.Close()

	return sumMessageRows(e, rows)
}

// RollupByModel returns a map of model → total cost in USD for all messages
// whose ts >= since (epoch-ms). Pass since=0 to include all time.
// Models with zero total cost are omitted from the result map.
func (e *Engine) RollupByModel(ctx context.Context, db *sql.DB, since int64) (map[string]float64, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT COALESCE(tokens_in, 0), COALESCE(tokens_out, 0),
		        COALESCE(cached_read_tokens, 0), COALESCE(cached_write_tokens, 0),
		        COALESCE(model, '')
		   FROM messages
		  WHERE ts >= ?`,
		since)
	if err != nil {
		return nil, fmt.Errorf("cost: RollupByModel query: %w", err)
	}
	defer rows.Close()

	result := make(map[string]float64)
	for rows.Next() {
		var tokIn, tokOut, cachedRead, cachedWrite int64
		var model string
		if err := rows.Scan(&tokIn, &tokOut, &cachedRead, &cachedWrite, &model); err != nil {
			return nil, fmt.Errorf("cost: RollupByModel scan: %w", err)
		}
		c := e.Cost(tokIn, tokOut, cachedRead, cachedWrite, model)
		if c != 0 {
			result[model] += c
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cost: RollupByModel rows: %w", err)
	}
	return result, nil
}

// sumMessageRows is a helper that iterates a *sql.Rows result set of
// (tokens_in, tokens_out, cached_read_tokens, cached_write_tokens, model)
// and accumulates total cost.
func sumMessageRows(e *Engine, rows *sql.Rows) (float64, error) {
	var total float64
	for rows.Next() {
		var tokIn, tokOut, cachedRead, cachedWrite int64
		var model string
		if err := rows.Scan(&tokIn, &tokOut, &cachedRead, &cachedWrite, &model); err != nil {
			return 0, fmt.Errorf("cost: scan message row: %w", err)
		}
		total += e.Cost(tokIn, tokOut, cachedRead, cachedWrite, model)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("cost: iterate message rows: %w", err)
	}
	return total, nil
}

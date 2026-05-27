package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/klyne-ai/klyne/internal/store"
)

// claudeRunResult is the parsed envelope from `claude -p
// --output-format=json`. It carries the assistant's final text (Output)
// plus the per-run token usage we record into klyne_llm_usage. Both
// fields are populated by parseClaudeRunResult; callers do not need to
// check whether the JSON parse succeeded — the Output falls back to the
// raw stdout/stderr when the envelope is unparseable so the UI's
// inline-output panel still has something to show.
type claudeRunResult struct {
	// Output is the human-facing text shown by the UI inline panel.
	// For a parsed JSON envelope this is the .result field; for an
	// unparseable run it is the concatenated stdout+stderr.
	Output string
	// Model echoes what we asked the CLI to run (NOT what it reported
	// back) — used for the klyne_llm_usage.model column so the figure
	// is stable across CLI versions that may or may not include it.
	Model string
	// SessionID is the .session_id from the JSON envelope when
	// present, empty otherwise.
	SessionID string
	// Parsed is true when stdout was a well-formed JSON result
	// envelope. False means the token figures below are all zero and
	// the row should be persisted with status=error so the UI doesn't
	// double-count a no-op as zero usage.
	Parsed bool
	// Token counts from the envelope's .usage block. Cache-read and
	// cache-write map to the Anthropic API names
	// cache_read_input_tokens / cache_creation_input_tokens which
	// match the messages.cached_read_tokens / cached_write_tokens
	// columns used for the user's daily denominator.
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	NumTurns         int
	// DurationApiMs is the .duration_api_ms field — pure model time,
	// distinct from the handler-side wall clock that wraps subprocess
	// startup. Currently unused; persisted into klyne_llm_usage in a
	// follow-up if useful.
	DurationApiMs int64
}

// claudeJSONEnvelope mirrors the shape `claude -p --output-format=json`
// emits. Fields are tagged loosely so additions on the CLI side don't
// break parsing.
type claudeJSONEnvelope struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	IsError   bool   `json:"is_error"`
	Result    string `json:"result"`
	SessionID string `json:"session_id"`
	NumTurns  int    `json:"num_turns"`
	// duration_api_ms — model wall-clock; some CLI builds omit this.
	DurationApiMs int64 `json:"duration_api_ms"`
	Usage         struct {
		InputTokens              int64 `json:"input_tokens"`
		OutputTokens             int64 `json:"output_tokens"`
		CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	} `json:"usage"`
}

// parseClaudeRunResult attempts to interpret stdout as a JSON envelope.
// On parse failure it returns Parsed=false with Output set to a sensible
// human fallback (stdout, then stderr, then a placeholder) so the UI's
// inline-output panel is never blank.
func parseClaudeRunResult(stdout, stderr []byte, model string) claudeRunResult {
	out := claudeRunResult{Model: model}
	trimmed := bytes.TrimSpace(stdout)
	if len(trimmed) > 0 {
		var env claudeJSONEnvelope
		if err := json.Unmarshal(trimmed, &env); err == nil && env.Type != "" {
			out.Parsed = true
			out.Output = env.Result
			out.SessionID = env.SessionID
			out.InputTokens = env.Usage.InputTokens
			out.OutputTokens = env.Usage.OutputTokens
			out.CacheReadTokens = env.Usage.CacheReadInputTokens
			out.CacheWriteTokens = env.Usage.CacheCreationInputTokens
			out.NumTurns = env.NumTurns
			out.DurationApiMs = env.DurationApiMs
			return out
		}
	}
	// Unparseable — surface whatever the CLI printed so the UI panel
	// still helps diagnose the failure.
	switch {
	case len(stdout) > 0:
		out.Output = string(stdout)
	case len(stderr) > 0:
		out.Output = string(stderr)
	default:
		out.Output = "(no output)"
	}
	return out
}

// persistKlyneUsage records ONE klyne_llm_usage row for a subprocess
// invocation. Called by the two spawn handlers regardless of
// success/timeout/error so the dashboard tile counts the day's full
// LLM footprint, not just successful runs.
//
// projectPath, day, operation MUST be the request inputs (not anything
// the subprocess reported). httpStatus is the resp.Status the handler
// is about to write so we can tag the row "ok"/"error"/"timeout"
// uniformly. wallMs is the handler-side wall clock (subprocess startup
// + model time) since the API endpoint cares about end-to-end cost.
//
// Failure to persist is logged-but-not-fatal — the user-facing response
// has already been computed.
func persistKlyneUsage(
	ctx context.Context, db *store.DB,
	projectPath, day, operation string,
	res claudeRunResult, httpStatus string, wallMs int64,
) {
	row := store.KlyneLLMUsage{
		ProjectPath:      projectPath,
		Day:              day,
		Operation:        operation,
		Model:            res.Model,
		InputTokens:      res.InputTokens,
		OutputTokens:     res.OutputTokens,
		CacheReadTokens:  res.CacheReadTokens,
		CacheWriteTokens: res.CacheWriteTokens,
		DurationMs:       wallMs,
		NumTurns:         res.NumTurns,
		SessionID:        res.SessionID,
		Status:           httpStatus,
		CreatedAt:        time.Now().UnixMilli(),
	}
	// Use a fresh background context so a client that closed the
	// connection mid-response doesn't strand the usage write.
	bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := store.InsertKlyneLLMUsage(bgCtx, db, row); err != nil {
		fmt.Printf("klyne llm usage: persist failed for %s/%s/%s: %v\n",
			projectPath, day, operation, err)
	}
	_ = ctx
}

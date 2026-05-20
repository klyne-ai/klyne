package richentry

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/klyne-ai/klyne/internal/ai"
	"github.com/klyne-ai/klyne/internal/store"
)

// WriteOutcome groups what one Write call produced. The Phase 5 worker
// uses these to call store.UpsertStopSummaryWithEntry; the worker also
// owns the worklog_attempts counter (which spans multiple Write calls
// across retries scheduled via the queue).
type WriteOutcome struct {
	Entry   store.WorklogEntryJSON
	Verdict string // gateVerdict, or "skipped-validator" after 2 failed attempts
	Retries int    // 0 if first LLM call passed validation, 1 if a re-prompt was needed
}

// Write runs the rich-entry writer for one turn: build prompt → call
// LLM → parse → validate → (retry once on validation failure) →
// return outcome. Does NOT persist; the caller (Phase 5 worker) is
// responsible for the store write so it can fold the verdict +
// retries into the worklog_attempts counter and the queue state.
//
// gateVerdict is the verdict the Phase 3 gate produced for this turn
// (admitted-heuristic / admitted-llm). It's carried through unchanged
// on success; on a second validation failure the outcome verdict is
// overridden to "skipped-validator" and an empty (but well-formed)
// entry is returned so the column always holds valid JSON.
//
// LLM transient errors are returned to the caller without retry —
// the worker decides whether to re-enqueue (verdict='pending') based
// on attempts < MAX. The writer never retries on transport errors
// because re-prompting an exhausted endpoint just wastes a call.
func Write(
	ctx context.Context, llm AIClient, in BundleInputs, gateVerdict string,
) (WriteOutcome, error) {
	bundle := BuildBundle(in)

	// First attempt.
	entry, err := callOnce(ctx, llm, bundle.Prompt)
	if err != nil {
		return WriteOutcome{}, err
	}
	if errs := Validate(entry, bundle.Allowlist); len(errs) == 0 {
		return WriteOutcome{Entry: entry, Verdict: gateVerdict, Retries: 0}, nil
	} else {
		// Second attempt — re-prompt with the rejection list inlined so
		// the LLM can correct just the bad refs.
		retryPrompt := bundle.Prompt + "\n\n" + buildRejectionMessage(errs) +
			"\n\nEmit the corrected JSON now. Nothing else.\n"
		entry2, err := callOnce(ctx, llm, retryPrompt)
		if err != nil {
			return WriteOutcome{}, err
		}
		if errs2 := Validate(entry2, bundle.Allowlist); len(errs2) == 0 {
			return WriteOutcome{Entry: entry2, Verdict: gateVerdict, Retries: 1}, nil
		}
	}

	// Both attempts failed validation — record skipped-validator with
	// a well-formed empty entry. Phase 5's worker will set
	// worklog_attempts; later runs do NOT re-try this row (verdict !=
	// '' / 'pending' is the queue-drain filter).
	return WriteOutcome{
		Entry:   emptyEntry(),
		Verdict: "skipped-validator",
		Retries: 1,
	}, nil
}

// callOnce sends prompt to the LLM and parses the reply as a
// WorklogEntryJSON. Strips leading/trailing JSON-fence wrappers
// because some providers add them despite the prompt forbidding it.
func callOnce(ctx context.Context, llm AIClient, prompt string) (store.WorklogEntryJSON, error) {
	resp, err := llm.Chat(ctx, ai.ChatRequest{
		Messages:    []ai.Message{{Role: "user", Content: prompt}},
		MaxTokens:   2048,
		Temperature: 0,
	})
	if err != nil {
		return store.WorklogEntryJSON{}, fmt.Errorf("richentry: writer LLM call: %w", err)
	}
	if resp == nil {
		return store.WorklogEntryJSON{}, fmt.Errorf("richentry: writer LLM returned nil response")
	}
	return parseWriterReply(resp.Text)
}

// parseWriterReply unmarshals the LLM's text reply into a
// WorklogEntryJSON, defensively stripping ```json / ``` fences and
// surrounding whitespace if the LLM ignored the contract.
func parseWriterReply(raw string) (store.WorklogEntryJSON, error) {
	s := strings.TrimSpace(raw)
	// Strip common code-fence wrappers.
	if strings.HasPrefix(s, "```json") {
		s = strings.TrimPrefix(s, "```json")
	} else if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
	}
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)

	var entry store.WorklogEntryJSON
	if err := json.Unmarshal([]byte(s), &entry); err != nil {
		return store.WorklogEntryJSON{}, fmt.Errorf("richentry: parse writer reply: %w", err)
	}
	if entry.Categories == nil {
		entry.Categories = map[string][]store.WorklogItem{}
	}
	return entry, nil
}

// buildRejectionMessage formats the validator's findings into a
// concrete instruction the LLM can act on. Keeps the message
// short — the prompt below it already carries the full input bundle.
func buildRejectionMessage(errs []ValidationError) string {
	var sb strings.Builder
	sb.WriteString("Your previous reply had INVALID citations. The following refs[] entries were rejected because they don't appear in the input bundle:\n")
	for _, e := range errs {
		fmt.Fprintf(&sb, "  - category=%s item=%d ref=%q: %s\n", e.Category, e.ItemIdx, e.Ref, e.Reason)
	}
	sb.WriteString("\nReplace each rejected ref with a token that DOES appear in the input bundle, OR drop that bullet entirely. Keep all other bullets unchanged.")
	return sb.String()
}

// emptyEntry returns a well-formed WorklogEntryJSON with every
// category present as []. Used as the persisted entry when both
// LLM attempts fail validation — the column always holds valid JSON
// the reflection consumer can iterate over.
func emptyEntry() store.WorklogEntryJSON {
	return store.WorklogEntryJSON{
		SchemaVersion: 1,
		Categories: map[string][]store.WorklogItem{
			"features_worked_on":    {},
			"features_picked":       {},
			"shipped":               {},
			"bugs_found":            {},
			"bugs_fixed":            {},
			"investigations":        {},
			"decisions":             {},
			"config_changes":        {},
			"blockers":              {},
			"blocked_on":            {},
			"pending":               {},
			"followups_for_others":  {},
			"must_remember":         {},
			"mistakes_or_dead_ends": {},
			"reviews_given":         {},
		},
	}
}

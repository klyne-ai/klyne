package attribution

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/klyne-ai/klyne/internal/connectors"
)

// WasteClass is the string tag stored in work_spans.waste_classes_json.
type WasteClass string

const (
	// WasteLoop fires when the same tool-call fingerprint repeats >5 times
	// within a 10-message sliding window. Caught the 3.75M-token OAuth retry
	// storm from issue #10784.
	WasteLoop WasteClass = "WASTE_LOOP"

	// windowSize is the sliding-window width for WASTE_LOOP detection.
	windowSize = 10
	// repeatThreshold is the minimum repeat count within the window to fire.
	repeatThreshold = 5
)

// WasteResult is one detected waste event within a span.
type WasteResult struct {
	Class         WasteClass
	OffendingHash string // SHA-256 hex of the normalized tool_calls fingerprint
	Count         int    // occurrences within the window that triggered the flag
}

// DetectWasteLoop scans msgs with a sliding window of windowSize and flags any
// message whose normalized tool-call fingerprint appears >repeatThreshold times
// within the window.
//
// "Normalized" means: sort tool call names+inputs lexicographically and hash
// the result. This strips ordering noise while preserving identity.
//
// Returns one WasteResult per unique offending hash. An empty slice means no
// waste detected.
func DetectWasteLoop(msgs []*connectors.Message) []WasteResult {
	if len(msgs) < repeatThreshold {
		return nil
	}

	// Compute a fingerprint for every message.
	fingerprints := make([]string, len(msgs))
	for i, msg := range msgs {
		if msg == nil {
			fingerprints[i] = ""
			continue
		}
		fingerprints[i] = fingerprintMessage(msg)
	}

	// Slide window across fingerprints, counting occurrences per hash.
	offenders := map[string]int{} // hash → max count seen in any window
	for start := 0; start <= len(fingerprints)-windowSize; start++ {
		window := fingerprints[start : start+windowSize]
		counts := map[string]int{}
		for _, fp := range window {
			if fp == "" {
				continue
			}
			counts[fp]++
		}
		for hash, cnt := range counts {
			if cnt > repeatThreshold && cnt > offenders[hash] {
				offenders[hash] = cnt
			}
		}
	}

	// Also check smaller trailing windows when len(msgs) < windowSize*2.
	// Slide over the whole slice with windows smaller than windowSize when
	// there are fewer messages.
	if len(msgs) < windowSize {
		counts := map[string]int{}
		for _, fp := range fingerprints {
			if fp != "" {
				counts[fp]++
			}
		}
		for hash, cnt := range counts {
			if cnt > repeatThreshold && cnt > offenders[hash] {
				offenders[hash] = cnt
			}
		}
	}

	if len(offenders) == 0 {
		return nil
	}

	results := make([]WasteResult, 0, len(offenders))
	for hash, count := range offenders {
		results = append(results, WasteResult{
			Class:         WasteLoop,
			OffendingHash: hash,
			Count:         count,
		})
	}
	// Deterministic output order: highest count first.
	sort.Slice(results, func(i, j int) bool {
		return results[i].Count > results[j].Count
	})
	return results
}

// fingerprintMessage produces a stable, noise-stripped fingerprint of a
// message's tool calls. Two messages are considered "the same" for WASTE_LOOP
// purposes when they have identical tool names and inputs after normalization.
//
// Normalization strips: timestamps, message IDs, request IDs, and sorts the
// tool call list so insertion-order differences don't confuse the detector.
func fingerprintMessage(msg *connectors.Message) string {
	if len(msg.ToolCalls) == 0 {
		// Non-tool messages (pure text) don't contribute to loop detection.
		return ""
	}

	type tcKey struct {
		Name  string `json:"n"`
		Input string `json:"i"`
	}
	keys := make([]tcKey, 0, len(msg.ToolCalls))
	for _, tc := range msg.ToolCalls {
		keys = append(keys, tcKey{
			Name:  tc.Name,
			Input: normalizeInput(tc.Input),
		})
	}
	// Sort so {"Read","a"},{"Bash","b"} == {"Bash","b"},{"Read","a"}.
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Name != keys[j].Name {
			return keys[i].Name < keys[j].Name
		}
		return keys[i].Input < keys[j].Input
	})

	b, err := json.Marshal(keys)
	if err != nil {
		return fmt.Sprintf("%v", keys)
	}
	h := sha256.Sum256(b)
	return fmt.Sprintf("%x", h)
}

// normalizeInput strips noise fields from a tool call's JSON input so that
// semantically identical calls hash identically even if they carry different
// IDs or timestamps.
func normalizeInput(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	// Only normalize JSON objects; pass plain strings through.
	if raw[0] != '{' {
		return raw
	}

	var obj map[string]any
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		// Not valid JSON — use raw as-is (still useful for comparison).
		return raw
	}

	// Drop well-known noise fields.
	noiseFields := []string{
		"id", "request_id", "timestamp", "ts", "created_at", "updated_at",
	}
	for _, f := range noiseFields {
		delete(obj, f)
	}

	b, err := json.Marshal(obj)
	if err != nil {
		return raw
	}
	return string(b)
}

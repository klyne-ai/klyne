package audit

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

// codexRawLine mirrors the subset of Codex's rollout JSONL schema this
// package needs. Codex uses an envelope (`type`, `payload`) very
// different from Claude's flat layout — the audit keeps a local
// schema stub rather than importing the connector's full struct so
// the audit can second-guess that connector independently.
type codexRawLine struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

// codexEventMsgPayload is the payload carried by `type: "event_msg"`
// lines. Only the `token_count` subtype matters for the audit; every
// other subtype is ignored by the extractor.
type codexEventMsgPayload struct {
	Type string                  `json:"type"`
	Info *codexTokenCountInfo    `json:"info"`
}

// codexTokenCountInfo carries the actual usage numbers. Verified
// against real ~/.codex/sessions data on 2026-05-08 — see commit
// notes for the sample shape.
//
// Per OpenAI's API contract (mirrored in
// internal/connectors/codex/parse.go:90-96), `input_tokens` already
// INCLUDES `cached_input_tokens`. The audit takes this number as-is
// without summing.
type codexTokenCountInfo struct {
	LastTokenUsage  *codexTokenUsage `json:"last_token_usage"`
	TotalTokenUsage *codexTokenUsage `json:"total_token_usage"`
	// ModelContextWindow is the per-session window that Codex itself
	// reports — typically smaller than the marketed model max because
	// Codex reserves part for the system prompt and tools manifest.
	ModelContextWindow int64 `json:"model_context_window"`
}

type codexTokenUsage struct {
	InputTokens       int64 `json:"input_tokens"`
	CachedInputTokens int64 `json:"cached_input_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
}

// codexSessionMeta is the payload of the `session_meta` line that
// always appears at the top of a rollout JSONL. We use it to recover
// the canonical session id and model.
type codexSessionMeta struct {
	ID    string `json:"id"`
	Model string `json:"model"`
}

// LatestCodexTokens scans a Codex rollout JSONL transcript and returns
// the input_tokens from the most-recent (by RFC 3339 timestamp)
// `event_msg` whose payload type is `token_count` and whose `info` is
// populated. Mirrors LatestAssistantTokens for Claude.
//
// Returns the zero SourceTokens (with SessionID populated when the file
// has a session_meta line) when no qualifying event exists. Malformed
// lines are silently skipped.
func LatestCodexTokens(path string) (SourceTokens, error) {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return SourceTokens{}, fmt.Errorf("open codex transcript: %w", err)
	}
	defer f.Close()

	var (
		out          SourceTokens
		bestTs       int64
		bestSet      bool
		scanBuf      = make([]byte, 0, 64*1024)
		maxScanToken = 16 * 1024 * 1024
	)
	sc := bufio.NewScanner(f)
	sc.Buffer(scanBuf, maxScanToken)

	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var raw codexRawLine
		if err := json.Unmarshal(line, &raw); err != nil {
			continue
		}
		// Recover session id + model from session_meta.
		if raw.Type == "session_meta" && out.SessionID == "" {
			var meta codexSessionMeta
			if err := json.Unmarshal(raw.Payload, &meta); err == nil {
				out.SessionID = meta.ID
				if out.Model == "" {
					out.Model = meta.Model
				}
			}
			continue
		}
		if raw.Type != "event_msg" {
			continue
		}
		var ep codexEventMsgPayload
		if err := json.Unmarshal(raw.Payload, &ep); err != nil {
			continue
		}
		if ep.Type != "token_count" || ep.Info == nil || ep.Info.LastTokenUsage == nil {
			continue
		}
		if ep.Info.LastTokenUsage.InputTokens <= 0 {
			continue
		}
		ts := parseClaudeTS(raw.Timestamp) // RFC 3339, same parser works
		if !bestSet || ts > bestTs {
			bestSet = true
			bestTs = ts
			// Codex's input_tokens is already cache-inclusive — do not
			// sum cached_input_tokens on top.
			out.Tokens = ep.Info.LastTokenUsage.InputTokens
		}
	}
	if err := sc.Err(); err != nil {
		return out, fmt.Errorf("scan codex transcript: %w", err)
	}
	return out, nil
}

// codexCompactedPayload is the payload for `type: "compacted"` lines.
// Codex stores compaction differently from Claude — there is no
// `compactMetadata` block and no preTokens count. We can detect that
// a compaction happened, but cannot quantify "how much was lost" the
// way we can for Claude transcripts. The audit reports both numbers
// honestly so the user knows the per-CLI limitation.
type codexCompactedPayload struct {
	// Reason / message text Codex attaches to the event, when present.
	Message string `json:"message"`
}

// CompactBoundaryStatsCodex returns the count of `type: "compacted"`
// lines in a Codex rollout JSONL. Unlike the Claude variant this
// CANNOT populate Manual/Auto or PreTokens — Codex's marker is an
// envelope without that metadata.
func CompactBoundaryStatsCodex(path string) (CompactStats, error) {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return CompactStats{}, fmt.Errorf("open codex transcript: %w", err)
	}
	defer f.Close()

	var (
		out          CompactStats
		scanBuf      = make([]byte, 0, 64*1024)
		maxScanToken = 16 * 1024 * 1024
	)
	sc := bufio.NewScanner(f)
	sc.Buffer(scanBuf, maxScanToken)

	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		// Cheap pre-filter — same trick as the Claude variant.
		if !bytesContains(line, "\"compacted\"") {
			continue
		}
		var raw codexRawLine
		if err := json.Unmarshal(line, &raw); err != nil {
			continue
		}
		if raw.Type != "compacted" {
			continue
		}
		out.Count++
		// Codex does not distinguish manual vs auto in the JSONL —
		// leave both subcounters at 0 so the renderer can flag the
		// per-CLI limitation honestly.
	}
	if err := sc.Err(); err != nil {
		return out, fmt.Errorf("scan codex transcript: %w", err)
	}
	return out, nil
}

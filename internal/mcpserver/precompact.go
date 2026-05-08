package mcpserver

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/klyne-ai/klyne/internal/connectors"
	claudeparse "github.com/klyne-ai/klyne/internal/connectors/claude"
)

// preCompactDefaultLimit caps the messages returned by
// LoadPreCompactMessages by default. The handoff prompt + agent
// context that the AI is going to bake on top of these messages has
// to fit in a normal context window, so we lean conservative.
const preCompactDefaultLimit = 30

// preCompactMaxLimit is the hard ceiling on the limit input. Above
// this the response payload is too big for most consumers.
const preCompactMaxLimit = 100

// PreCompactBundle is the result of LoadPreCompactMessages.
type PreCompactBundle struct {
	// Trigger is "manual" or "auto" if the source compactMetadata
	// carries it; "" when the compact line lacks the metadata block
	// (older Claude Code versions).
	Trigger string
	// PreTokens is the cache-aware token count Claude Code reported
	// immediately before the compact event. Zero when the line had no
	// compactMetadata.
	PreTokens int64
	// CompactTimestamp is the RFC3339 timestamp of the compact event.
	CompactTimestamp string
	// Messages are the canonical messages that immediately preceded
	// the last compact event, in chronological order (oldest first).
	// At most `limit` rows.
	Messages []*connectors.Message
	// FoundCompact is true when at least one compact_boundary line
	// existed in the file. False = no /compact has ever happened in
	// this session.
	FoundCompact bool
}

// LoadPreCompactMessages dispatches to the right pre-compact recovery
// path based on the file's CLI:
//
//   - Claude: scan JSONL for the last `subtype:"compact_boundary"`
//     line and return prior messages from earlier in the file.
//   - Codex:  decode the last `type:"compacted"` line's
//     `payload.replacement_history` array directly. Codex embeds the
//     pre-compaction conversation in the event itself, so we don't
//     have to scan the file backward.
//
// Returns FoundCompact=false (with no error) when no compact event
// exists. Malformed lines are silently skipped throughout.
//
// Per-CLI metadata gaps:
//   - Claude returns Trigger and PreTokens populated from
//     compactMetadata.
//   - Codex returns Trigger="" and PreTokens=0 because Codex's
//     `compacted` envelope does not carry either field.
func LoadPreCompactMessages(path string, limit int) (*PreCompactBundle, error) {
	switch CLIForPath(path) {
	case connectors.CLICodex:
		return loadCodexPreCompactMessages(path, limit)
	default:
		return loadClaudePreCompactMessages(path, limit)
	}
}

// loadClaudePreCompactMessages is the original Claude-only logic.
func loadClaudePreCompactMessages(path string, limit int) (*PreCompactBundle, error) {
	if limit <= 0 {
		limit = preCompactDefaultLimit
	}
	if limit > preCompactMaxLimit {
		limit = preCompactMaxLimit
	}

	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("open transcript: %w", err)
	}
	defer f.Close()

	scanBuf := make([]byte, 0, 64*1024)
	const maxScanToken = 16 * 1024 * 1024
	sc := bufio.NewScanner(f)
	sc.Buffer(scanBuf, maxScanToken)

	// Two-pass design: first pass walks the whole file recording the
	// byte-line index of every compact_boundary. Second pass parses
	// only the messages between (lastCompactIdx - limit) and
	// lastCompactIdx into Messages. Cheaper than parsing every line
	// of large transcripts twice.
	type boundaryHit struct {
		index            int
		trigger          string
		preTokens        int64
		compactTimestamp string
	}
	var (
		lineIdx    int
		compacts   []boundaryHit
	)
	for sc.Scan() {
		lineIdx++
		line := sc.Bytes()
		if len(line) == 0 || !bytesContains(line, "compact_boundary") {
			continue
		}
		var raw struct {
			Type      string `json:"type"`
			Subtype   string `json:"subtype"`
			Timestamp string `json:"timestamp"`
			Compact   *struct {
				Trigger   string `json:"trigger"`
				PreTokens int64  `json:"preTokens"`
			} `json:"compactMetadata"`
		}
		if err := json.Unmarshal(line, &raw); err != nil {
			continue
		}
		if raw.Type != "system" || raw.Subtype != "compact_boundary" {
			continue
		}
		hit := boundaryHit{index: lineIdx, compactTimestamp: raw.Timestamp}
		if raw.Compact != nil {
			hit.trigger = raw.Compact.Trigger
			hit.preTokens = raw.Compact.PreTokens
		}
		compacts = append(compacts, hit)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan transcript: %w", err)
	}

	bundle := &PreCompactBundle{}
	if len(compacts) == 0 {
		return bundle, nil
	}
	last := compacts[len(compacts)-1]
	bundle.FoundCompact = true
	bundle.Trigger = last.trigger
	bundle.PreTokens = last.preTokens
	bundle.CompactTimestamp = last.compactTimestamp

	// Second pass: re-open and parse only the lines we need.
	if _, err := f.Seek(0, 0); err != nil {
		return nil, fmt.Errorf("rewind transcript: %w", err)
	}
	sc = bufio.NewScanner(f)
	sc.Buffer(scanBuf[:0:cap(scanBuf)], maxScanToken)

	startLine := last.index - limit
	if startLine < 1 {
		startLine = 1
	}
	currentLine := 0
	for sc.Scan() {
		currentLine++
		if currentLine >= last.index {
			break
		}
		if currentLine < startLine {
			continue
		}
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		msg, err := claudeparse.Parse(line, path)
		if err != nil || msg == nil {
			continue
		}
		bundle.Messages = append(bundle.Messages, msg)
	}
	if err := sc.Err(); err != nil {
		return bundle, fmt.Errorf("re-scan transcript: %w", err)
	}
	return bundle, nil
}

// loadCodexPreCompactMessages opens a Codex rollout file, finds the
// LAST `type:"compacted"` line, and decodes its
// payload.replacement_history into canonical Messages. Unlike the
// Claude variant this is single-pass: the event embeds the pre-
// compaction conversation, so we never scan the file backward.
//
// Limit semantics match the Claude path: when replacement_history
// has more than `limit` items, keep the LAST N (most recent).
func loadCodexPreCompactMessages(path string, limit int) (*PreCompactBundle, error) {
	if limit <= 0 {
		limit = preCompactDefaultLimit
	}
	if limit > preCompactMaxLimit {
		limit = preCompactMaxLimit
	}

	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("open transcript: %w", err)
	}
	defer f.Close()

	scanBuf := make([]byte, 0, 64*1024)
	const maxScanToken = 16 * 1024 * 1024
	sc := bufio.NewScanner(f)
	sc.Buffer(scanBuf, maxScanToken)

	type codexCompactPayload struct {
		ReplacementHistory []struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"replacement_history"`
	}
	type codexEnvelope struct {
		Timestamp string          `json:"timestamp"`
		Type      string          `json:"type"`
		Payload   json.RawMessage `json:"payload"`
	}

	var (
		sessionID  string
		lastEvent  *codexCompactPayload
		lastTS     string
		foundEvent bool
	)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		// Cheap pre-filter for the two line types we care about.
		if !bytesContains(line, "session_meta") && !bytesContains(line, `"compacted"`) {
			continue
		}
		var env codexEnvelope
		if err := json.Unmarshal(line, &env); err != nil {
			continue
		}
		switch env.Type {
		case "session_meta":
			if sessionID != "" {
				continue
			}
			var sm struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(env.Payload, &sm) == nil {
				sessionID = sm.ID
			}
		case "compacted":
			var cp codexCompactPayload
			if err := json.Unmarshal(env.Payload, &cp); err != nil {
				continue
			}
			lastEvent = &cp
			lastTS = env.Timestamp
			foundEvent = true
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan transcript: %w", err)
	}

	bundle := &PreCompactBundle{}
	if !foundEvent {
		return bundle, nil
	}
	bundle.FoundCompact = true
	bundle.CompactTimestamp = lastTS
	// Codex does not expose Trigger or PreTokens — leave zero.

	// Decode every replacement_history item into a canonical Message.
	// Items have shape {type:"message", role:user|assistant, content:[{type,text}]}.
	// Codex content types: "input_text" for user, "output_text" for
	// assistant. Other shapes (tool calls, reasoning) appear in
	// real transcripts but carry no human-readable text — skip them.
	ts := codexTimestampToMillis(lastTS)
	for _, item := range lastEvent.ReplacementHistory {
		if item.Type != "message" {
			continue
		}
		var role connectors.Role
		var contentTypeWanted string
		switch item.Role {
		case "user":
			role = connectors.RoleUser
			contentTypeWanted = "input_text"
		case "assistant":
			role = connectors.RoleAssistant
			contentTypeWanted = "output_text"
		default:
			continue
		}
		var text string
		for _, c := range item.Content {
			if c.Type == contentTypeWanted && c.Text != "" {
				if text != "" {
					text += "\n"
				}
				text += c.Text
			}
		}
		if text == "" {
			continue
		}
		bundle.Messages = append(bundle.Messages, &connectors.Message{
			SessionID: sessionID,
			CLI:       connectors.CLICodex,
			Role:      role,
			Content:   text,
			Ts:        ts,
		})
	}

	// Cap to most recent `limit`. The replacement_history is in
	// chronological order (oldest first) so we trim the FRONT.
	if len(bundle.Messages) > limit {
		bundle.Messages = bundle.Messages[len(bundle.Messages)-limit:]
	}
	return bundle, nil
}

// codexTimestampToMillis parses Codex's RFC 3339 ms-precision
// timestamp into epoch-ms. Returns 0 on failure; the caller treats
// 0 as "unknown" rather than a real value.
func codexTimestampToMillis(ts string) int64 {
	if ts == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return 0
	}
	return t.UnixMilli()
}

// bytesContains is a tiny inline substring search; same trick as
// the audit package's pre-filter for compact lines.
func bytesContains(haystack []byte, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	if len(haystack) < len(needle) {
		return false
	}
	n := len(needle)
	for i := 0; i+n <= len(haystack); i++ {
		match := true
		for j := 0; j < n; j++ {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

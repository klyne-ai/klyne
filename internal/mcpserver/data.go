package mcpserver

import (
	"bufio"
	"fmt"
	"os"

	"github.com/klyne-ai/klyne/internal/audit"
	"github.com/klyne-ai/klyne/internal/connectors"
	claudeparse "github.com/klyne-ai/klyne/internal/connectors/claude"
	codexparse "github.com/klyne-ai/klyne/internal/connectors/codex"
	"github.com/klyne-ai/klyne/internal/usage"
)

// SessionSnapshot is everything a tool needs to call contexthealth.Classify
// for a session, computed by reading the JSONL directly.
type SessionSnapshot struct {
	// Path is the source JSONL absolute path.
	Path string
	// SessionID is the session id reported by the JSONL itself.
	SessionID string
	// Model is the model id from the latest qualifying assistant turn.
	Model string
	// ContextFillPct is the cache-aware ratio of latest-assistant
	// input tokens to the model's context window, expressed 0..100.
	ContextFillPct float64
	// MsgCount is the total parsed message count in the transcript.
	MsgCount int
	// Messages is every parsed message in chronological file order.
	// Lines that the parser skips (permission-mode, file-history-snapshot,
	// summary lines without leaf-uuid context, etc.) are NOT included.
	Messages []*connectors.Message
}

// LoadSnapshot opens path, parses every line using the right parser
// for the file's CLI (detected from the storage root), and computes
// the cache-aware context-fill percentage.
//
// Returns an error only when the file itself is unreadable; malformed
// lines are silently skipped (same tolerance as the audit package, for
// the same reason — real transcripts contain stray bytes after CLI
// crashes). When the path lives outside both ~/.claude/projects and
// ~/.codex/sessions, falls back to the Claude parser; this preserves
// pre-Codex test fixtures that drop transcripts in arbitrary tempdirs.
func LoadSnapshot(path string) (*SessionSnapshot, error) {
	switch CLIForPath(path) {
	case connectors.CLICodex:
		return loadCodexSnapshot(path)
	default:
		return loadClaudeSnapshot(path)
	}
}

// loadClaudeSnapshot parses path as a Claude Code transcript and
// computes ContextFillPct via the cache-aware audit extractor.
func loadClaudeSnapshot(path string) (*SessionSnapshot, error) {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("open transcript: %w", err)
	}
	defer f.Close()

	snap := &SessionSnapshot{Path: path}
	// Match the audit's scan budget — multi-MB tool_result payloads
	// would overflow the default bufio token limit otherwise.
	scanBuf := make([]byte, 0, 64*1024)
	maxScanToken := 16 * 1024 * 1024
	sc := bufio.NewScanner(f)
	sc.Buffer(scanBuf, maxScanToken)

	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		msg, err := claudeparse.Parse(line, path)
		if err != nil || msg == nil {
			continue
		}
		if snap.SessionID == "" && msg.SessionID != "" {
			snap.SessionID = msg.SessionID
		}
		snap.Messages = append(snap.Messages, msg)
	}
	snap.MsgCount = len(snap.Messages)

	// Fill % comes from the cache-aware audit extractor — same code
	// path the user just verified at 100% accuracy across 188 sessions.
	tokens, err := audit.LatestAssistantTokens(path)
	if err == nil && tokens.Tokens > 0 {
		snap.Model = tokens.Model
		window := usage.ContextWindowForModel(tokens.Model)
		if window > 0 {
			snap.ContextFillPct = float64(tokens.Tokens) / float64(window) * 100.0
		}
	}
	return snap, nil
}

// loadCodexSnapshot parses path as a Codex rollout file using a fresh
// Codex connector instance (per-file state stays isolated from the
// global daemon). ContextFillPct uses audit.LatestCodexTokens, which
// returns Codex's already-cache-inclusive input_tokens.
//
// Codex's JSONL stores per-turn token usage in standalone event_msg
// `token_count` lines, separate from the assistant `response_item.message`
// lines that report what the model said. The streaming parser emits the
// token data as a synthetic system-role message (so the daemon's
// session-counter accumulator stays correct) — but the timeline aggregator
// only looks at assistant turns with TokensIn > 0. This snapshot loader
// runs a post-pass that projects each system-role token_count delta onto
// the chronologically-nearest preceding assistant message, so the
// timeline sees per-turn token usage on the same axis it does for Claude.
//
// The post-pass does NOT remove the system-role messages — leaving them
// in place keeps the classifier's hidden-message-ratio signal honest and
// preserves the audit's view of the file.
func loadCodexSnapshot(path string) (*SessionSnapshot, error) {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("open transcript: %w", err)
	}
	defer f.Close()

	// Root parameter only matters for Discover/Watch — Parse uses
	// per-file state keyed by path. Empty string is fine here.
	parser := codexparse.New("")

	snap := &SessionSnapshot{Path: path}
	scanBuf := make([]byte, 0, 64*1024)
	maxScanToken := 16 * 1024 * 1024
	sc := bufio.NewScanner(f)
	sc.Buffer(scanBuf, maxScanToken)

	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		msg, err := parser.Parse(line, path)
		if err != nil || msg == nil {
			continue
		}
		if snap.SessionID == "" && msg.SessionID != "" {
			snap.SessionID = msg.SessionID
		}
		snap.Messages = append(snap.Messages, msg)
	}
	snap.MsgCount = len(snap.Messages)
	projectCodexTokensOntoAssistants(snap.Messages)

	tokens, err := audit.LatestCodexTokens(path)
	if err == nil && tokens.Tokens > 0 {
		snap.Model = tokens.Model
		window := usage.ContextWindowForModel(tokens.Model)
		if window > 0 {
			snap.ContextFillPct = float64(tokens.Tokens) / float64(window) * 100.0
		}
	}
	return snap, nil
}

// projectCodexTokensOntoAssistants walks msgs (in chronological order)
// and copies per-turn token data from each token_count system-role
// message onto the immediately-preceding assistant message — making
// Codex assistant turns look the same shape (TokensIn > 0, etc.) as
// Claude assistant turns to the timeline aggregator.
//
// Mutates msgs in place. Each system-role message is consumed at most
// once (it never re-decorates a more-recent assistant message). When
// there is no preceding assistant message yet, the system-role token
// row is left as-is — the timeline already filters non-assistant rows
// out, and a leading token_count without a paired assistant turn
// carries no useful information for the per-turn view anyway.
//
// Why DELTA == per-turn: Codex's parser emits the system-role message
// with TokensIn = total_token_usage[n].input_tokens -
// total_token_usage[n-1].input_tokens, which is mathematically equal to
// last_token_usage[n].input_tokens — i.e. the prefix the model received
// for that single call. That is exactly the value the per-turn timeline
// renderer expects (matches Claude's per-turn usage.input_tokens).
func projectCodexTokensOntoAssistants(msgs []*connectors.Message) {
	// Index of the most recent assistant message that has not yet been
	// decorated with token data — once we attach a snapshot we advance
	// this so the same assistant message can't accumulate multiple
	// snapshots.
	asstIdx := -1
	for i, m := range msgs {
		if m == nil {
			continue
		}
		switch m.Role {
		case connectors.RoleAssistant:
			// New assistant turn — becomes the next projection target.
			asstIdx = i
		case connectors.RoleSystem:
			// System-role messages with TokensIn > 0 are exactly the
			// codex parser's token_count emissions (Codex never produces
			// "real" system messages otherwise).
			if asstIdx < 0 {
				continue
			}
			if m.TokensIn <= 0 && m.TokensOut <= 0 && m.CachedReadTokens <= 0 {
				continue
			}
			target := msgs[asstIdx]
			// Avoid double-attribution: only project when the assistant
			// message is still token-less.
			if target.TokensIn > 0 || target.TokensOut > 0 || target.CachedReadTokens > 0 {
				continue
			}
			target.TokensIn = m.TokensIn
			target.TokensOut = m.TokensOut
			target.CachedReadTokens = m.CachedReadTokens
			target.CachedWriteTokens = m.CachedWriteTokens
			if target.Model == "" && m.Model != "" {
				target.Model = m.Model
			}
			// Mark consumed so a later token_count doesn't re-decorate
			// the same assistant message.
			asstIdx = -1
		}
	}
}

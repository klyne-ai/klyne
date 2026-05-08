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

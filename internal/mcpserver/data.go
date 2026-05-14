package mcpserver

import (
	"bufio"
	"context"
	"fmt"
	"os"

	"github.com/klyne-ai/klyne/internal/audit"
	"github.com/klyne-ai/klyne/internal/connectors"
	claudeparse "github.com/klyne-ai/klyne/internal/connectors/claude"
	codexparse "github.com/klyne-ai/klyne/internal/connectors/codex"
	"github.com/klyne-ai/klyne/internal/usage"
)

// scanCtxCheckInterval is how often (in scanned lines) we test ctx.Done()
// during a JSONL streaming pass. Cheap per-iteration ctx polls would
// dominate the inner loop on small lines; checking every 64 lines keeps
// cancellation latency well under a second even for fast scanners while
// adding negligible overhead.
const scanCtxCheckInterval = 64

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
func LoadSnapshot(ctx context.Context, path string) (*SessionSnapshot, error) {
	switch CLIForPath(path) {
	case connectors.CLICodex:
		return loadCodexSnapshot(ctx, path)
	default:
		return loadClaudeSnapshot(ctx, path)
	}
}

// loadClaudeSnapshot parses path as a Claude Code transcript and
// computes ContextFillPct via the cache-aware audit extractor.
//
// Honours ctx cancellation by polling ctx.Done() periodically during
// the file scan — large transcripts (hundreds of MB after long
// compacted sessions) used to leave the slash command spinning with
// no recourse; the host's deadline now reliably aborts the work and
// returns a clean error.
//
// Previously this function read the file twice: once to build the
// Messages slice, then a second time via audit.LatestAssistantTokens
// to compute the cache-aware token total. The audit call is now
// folded into the same scan: every parsed assistant message already
// carries the raw token fields the audit extractor would read, and
// taking the latest-by-ts non-zero usage row produces the same
// SourceTokens semantics. This roughly halves the I/O cost on a
// cold cache.
func loadClaudeSnapshot(ctx context.Context, path string) (*SessionSnapshot, error) {
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

	// Track the latest-by-timestamp assistant message that carries a
	// non-zero usage block. Mirrors audit.LatestAssistantTokens
	// semantics so we don't need a second file pass to compute fill %.
	var (
		bestTs      int64
		bestSet     bool
		bestTokens  int64
		bestModel   string
		linesScanned int
	)
	for sc.Scan() {
		linesScanned++
		if linesScanned%scanCtxCheckInterval == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
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

		// Latest-assistant token tracking, in-pass. Equivalent to
		// audit.LatestAssistantTokens. The Claude parser already
		// populates msg.TokensIn as the cache-aware total —
		// input_tokens + cache_read_input_tokens + cache_creation_input_tokens
		// — so we use it directly rather than re-summing the
		// CachedReadTokens / CachedWriteTokens fields (which are
		// independent copies of the same values exposed for the cost
		// engine).
		if msg.Role == connectors.RoleAssistant {
			if msg.TokensIn > 0 && (!bestSet || msg.Ts > bestTs) {
				bestSet = true
				bestTs = msg.Ts
				bestTokens = msg.TokensIn
				bestModel = msg.Model
			}
		}
	}
	if err := sc.Err(); err != nil {
		// A scanner error after a partial scan is best surfaced —
		// callers can still classify what was read, but they should
		// know the picture may be truncated.
		return nil, fmt.Errorf("scan transcript: %w", err)
	}
	snap.MsgCount = len(snap.Messages)

	if bestSet && bestTokens > 0 {
		snap.Model = bestModel
		window := usage.ContextWindowForModel(bestModel)
		if window > 0 {
			snap.ContextFillPct = float64(bestTokens) / float64(window) * 100.0
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
func loadCodexSnapshot(ctx context.Context, path string) (*SessionSnapshot, error) {
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

	linesScanned := 0
	for sc.Scan() {
		linesScanned++
		if linesScanned%scanCtxCheckInterval == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
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
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan transcript: %w", err)
	}
	snap.MsgCount = len(snap.Messages)
	projectCodexTokensOntoAssistants(snap.Messages)

	// Codex's token computation lives in audit.LatestCodexTokens because
	// it depends on the streaming parser's per-path state in a way the
	// snapshot's per-line view does not. This is the one remaining
	// double-read on the Codex path; the cost is acceptable because
	// Codex transcripts are typically much smaller than Claude's.
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

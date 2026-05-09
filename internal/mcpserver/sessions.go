// Package mcpserver exposes klyne's session intelligence as MCP tools.
//
// The server runs as a stdio subprocess that Claude Code (or any MCP host)
// spawns. It does NOT depend on the klyne daemon being running — every
// tool reads JSONL transcripts directly so answers reflect the live state
// of the user's active session, not whatever the daemon last ingested.
//
// The transport rule that everything else hangs on: stdout is the JSON-RPC
// channel. Anything written there corrupts the protocol. All logs go to
// stderr. The cobra subcommand explicitly wires log output to stderr; new
// tools must follow suit.
package mcpserver

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/klyne-ai/klyne/internal/connectors"
)

// activeWindow is how recent a JSONL's mtime must be for the session
// to count as "currently being used." 30 seconds is generous enough
// that a brief AI thinking pause doesn't disqualify a real session,
// tight enough that a session paused for more than half a minute is
// not silently picked up by another session's MCP call.
const activeWindow = 30 * time.Second

// sessionPreviewBytes caps how much of the first user message we
// include in a SessionCandidate.Preview. Long enough for the AI to
// recognise the topic ("fix the auth middleware"), short enough that
// listing 10 candidates does not blow a token budget.
const sessionPreviewBytes = 200

// SessionCandidate describes one of the JSONL transcripts that could
// match the current MCP call's cwd. Returned by ListSessionsForCWD
// so the AI (or a tool that needs to disambiguate) can pick the right
// one based on the conversation context it already has.
type SessionCandidate struct {
	// SessionID is parsed from the first session-bearing JSONL line.
	SessionID string
	// Path is the absolute path of the JSONL transcript.
	Path string
	// ModTime is the file's last-modified time. The mtime updates on
	// every appended line, so this is effectively "when did the session
	// last receive a turn."
	ModTime time.Time
	// MsgCount is the line count of the JSONL — a coarse proxy for
	// session length (each line is one message-bearing event).
	MsgCount int
	// Preview is the first ~200 characters of the first user message
	// in the session. Helps the AI match candidates against the
	// conversation it remembers having.
	Preview string
	// IsActive is true when ModTime is within activeWindow of "now."
	// The "currently being used" disambiguation rule prefers active
	// sessions over idle ones when no explicit session_id is given.
	IsActive bool
	// CLI identifies which connector wrote this transcript ("claude" or
	// "codex"). Tools dispatch to the matching parser via this field;
	// the disambiguation surface uses it to display per-CLI badges.
	CLI connectors.CLI
}

// claudeProjectsDir returns the absolute path to ~/.claude/projects.
// Returns ("", error) when HOME cannot be resolved — every tool that
// touches the file system must surface that error rather than guess.
func claudeProjectsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home: %w", err)
	}
	return filepath.Join(home, ".claude", "projects"), nil
}

// EncodeCWD turns an absolute path into the directory-name encoding
// Claude Code uses under ~/.claude/projects. Verified against the user's
// real layout on 2026-05-08: every "/" becomes "-", with a leading "-"
// preserved from the leading slash.
//
// Examples:
//
//	"/Users/x/proj"           -> "-Users-x-proj"
//	"/Users/x/proj/subdir"    -> "-Users-x-proj-subdir"
//	"" or non-absolute input  -> ""
func EncodeCWD(cwd string) string {
	if cwd == "" || !strings.HasPrefix(cwd, "/") {
		return ""
	}
	return strings.ReplaceAll(cwd, "/", "-")
}

// LatestSessionForCWD returns the absolute path of the most-recently
// modified JSONL transcript whose project directory corresponds to cwd
// or any of cwd's ancestor directories.
//
// DEPRECATED for new callers: use ListSessionsForCWD when more than
// one session may live in the project. This helper is retained for
// the single-candidate fast path (and for tests) but the MCP tool
// surface has been moved to the disambiguation-aware resolver.
func LatestSessionForCWD(cwd string) (string, error) {
	cands, err := ListSessionsForCWD(cwd)
	if err != nil {
		return "", err
	}
	if len(cands) == 0 {
		return "", nil
	}
	return cands[0].Path, nil
}

// ListSessionsForCWD returns every transcript — Claude OR Codex —
// whose launch cwd matches cwd or any ancestor of cwd, sorted by
// ModTime desc. Each candidate is tagged with its CLI so tools can
// dispatch to the right parser.
//
// Per-CLI semantics:
//   - Claude: walk up directory tree looking for ~/.claude/projects/
//     <encoded-cwd>; first ancestor with sessions wins (preserves
//     existing single-CLI behaviour).
//   - Codex: scan all rollout files; pick the LONGEST sessionCwd
//     that is an ancestor of cwd (== "nearest ancestor" semantic),
//     and return all sessions launched from exactly that cwd.
//
// Cross-CLI: results from each CLI are produced independently and
// merged by mtime. A user running Claude in /tmp/proj/sub and Codex
// in /tmp/proj will see both surfaces from /tmp/proj/sub/anywhere —
// the AI uses single-active disambiguation to pick.
func ListSessionsForCWD(cwd string) ([]SessionCandidate, error) {
	if cwd == "" {
		return nil, nil
	}
	cwd = filepath.Clean(cwd)
	claudeCands, err := claudeListSessionsForCWD(cwd)
	if err != nil {
		return nil, err
	}
	codexCands, err := codexListSessionsForCWD(cwd)
	if err != nil {
		return nil, err
	}
	merged := make([]SessionCandidate, 0, len(claudeCands)+len(codexCands))
	merged = append(merged, claudeCands...)
	merged = append(merged, codexCands...)
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].ModTime.After(merged[j].ModTime)
	})
	return merged, nil
}

// claudeListSessionsForCWD is the original Claude-only walk-up logic,
// unchanged in semantics. Extracted from the public ListSessionsForCWD
// so the multi-CLI merge stays readable.
func claudeListSessionsForCWD(cwd string) ([]SessionCandidate, error) {
	projectsDir, err := claudeProjectsDir()
	if err != nil {
		return nil, err
	}
	current := cwd
	for {
		encoded := EncodeCWD(current)
		if encoded != "" {
			candidate := filepath.Join(projectsDir, encoded)
			if info, statErr := os.Stat(candidate); statErr == nil && info.IsDir() {
				cands, err := candidatesInDir(candidate)
				if err != nil {
					return nil, err
				}
				if len(cands) > 0 {
					return cands, nil
				}
			}
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return nil, nil
}

// CLIForPath returns the connector identifier responsible for a given
// transcript path, derived from the storage root the path lives under.
// Returns "" for paths outside any known CLI's storage tree (e.g. test
// fixtures the user manually placed in /tmp). Used by LoadSnapshot
// and FindSessionByID to dispatch to the right parser.
func CLIForPath(path string) connectors.CLI {
	if path == "" {
		return ""
	}
	switch {
	case strings.Contains(path, "/.codex/sessions/"):
		return connectors.CLICodex
	case strings.Contains(path, "/.claude/projects/"):
		return connectors.CLIClaude
	}
	return ""
}

// candidatesInDir builds the SessionCandidate slice for one project
// directory. Each .jsonl file becomes one row; preview + msg_count
// require a single bounded scan of the file.
func candidatesInDir(dir string) ([]SessionCandidate, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read project dir %s: %w", dir, err)
	}
	now := time.Now()
	var cands []SessionCandidate
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		path := filepath.Join(dir, e.Name())
		sessionID, preview, msgCount := readCandidateMetadata(path)
		// Fall back to the filename's UUID when the JSONL itself does
		// not yet carry a session id (very fresh sessions).
		if sessionID == "" {
			sessionID = strings.TrimSuffix(e.Name(), ".jsonl")
		}
		cands = append(cands, SessionCandidate{
			SessionID: sessionID,
			Path:      path,
			ModTime:   info.ModTime(),
			MsgCount:  msgCount,
			Preview:   preview,
			IsActive:  now.Sub(info.ModTime()) <= activeWindow,
			CLI:       connectors.CLIClaude,
		})
	}
	sort.Slice(cands, func(i, j int) bool {
		return cands[i].ModTime.After(cands[j].ModTime)
	})
	return cands, nil
}

// readCandidateMetadata performs a single bounded scan of a JSONL
// transcript to extract: the canonical session id (first non-empty
// sessionId field), the first user message preview, and the total
// line count. Bounds the scan token at 16 MiB to match the audit
// extractor; tolerates malformed lines silently.
func readCandidateMetadata(path string) (sessionID, preview string, msgCount int) {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return "", "", 0
	}
	defer f.Close()

	scanBuf := make([]byte, 0, 64*1024)
	const maxScanToken = 16 * 1024 * 1024
	sc := bufio.NewScanner(f)
	sc.Buffer(scanBuf, maxScanToken)

	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		msgCount++
		if sessionID != "" && preview != "" {
			continue // keep counting lines, but stop parsing
		}
		var raw struct {
			Type      string          `json:"type"`
			SessionID string          `json:"sessionId"`
			Message   json.RawMessage `json:"message"`
		}
		if err := json.Unmarshal(line, &raw); err != nil {
			continue
		}
		if sessionID == "" && raw.SessionID != "" {
			sessionID = raw.SessionID
		}
		if preview == "" && raw.Type == "user" {
			preview = previewFromUserMessage(raw.Message)
		}
	}
	return sessionID, preview, msgCount
}

// previewFromUserMessage extracts a short text preview from a Claude
// Code user-message payload. Handles both shapes seen in the wild:
// `message: "string content"` and `message: {role,content}` objects.
func previewFromUserMessage(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// Shape A: message is an object with content.
	var obj struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil && len(obj.Content) > 0 {
		return truncatePreview(extractContentText(obj.Content))
	}
	// Shape B: message is a JSON string.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil && s != "" {
		return truncatePreview(s)
	}
	return ""
}

// extractContentText flattens a Claude Code content array into plain
// text for previewing. Returns the first text-bearing element's text;
// images / tool blocks are ignored on purpose because they would
// produce uninterpretable previews.
func extractContentText(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var items []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &items); err != nil {
		return ""
	}
	for _, it := range items {
		if it.Type == "text" && it.Text != "" {
			return it.Text
		}
	}
	return ""
}

// truncatePreview caps a preview at sessionPreviewBytes and trims
// trailing whitespace introduced by user messages with newlines.
func truncatePreview(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= sessionPreviewBytes {
		return s
	}
	return strings.TrimSpace(s[:sessionPreviewBytes]) + "…"
}

// PickActiveSession applies the disambiguation rule used by tools that
// take an optional session_id. Returns:
//
//	(candidate, true, nil) when exactly one session is unambiguously
//	  the right pick (single candidate, OR multiple candidates with
//	  exactly one IsActive within activeWindow);
//	(zero, false, nil) when the choice is ambiguous (zero candidates
//	  to pick from, OR multiple active sessions tied for "most
//	  recent within activeWindow"). The caller surfaces the candidate
//	  list to the AI and asks it to specify session_id.
func PickActiveSession(cands []SessionCandidate) (SessionCandidate, bool) {
	switch len(cands) {
	case 0:
		return SessionCandidate{}, false
	case 1:
		return cands[0], true
	}
	// Multiple candidates: prefer the unique active one.
	activeCount := 0
	var active SessionCandidate
	for _, c := range cands {
		if c.IsActive {
			activeCount++
			active = c
		}
	}
	if activeCount == 1 {
		return active, true
	}
	return SessionCandidate{}, false
}

// Package attribution implements the batch span-attribution runner for the
// Cost-Per-Outcome feature (v0). It streams messages from the store, groups
// them into work_spans keyed by (git_branch, cwd), and closes spans when
// it detects a "git commit" tool call in the message stream.
//
// v0 ships commit-close only; pr_create-close is deferred to v1.
package attribution

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/store"
)

// Runner streams messages from the DB, builds work spans using a state
// machine keyed by (git_branch, cwd), and writes the results back.
type Runner struct {
	db *store.DB
}

// New constructs a Runner backed by the given store.
func New(db *store.DB) *Runner {
	return &Runner{db: db}
}

// spanKey identifies an open span uniquely within a batch run.
type spanKey struct {
	gitBranch string
	cwd       string
}

// openSpan holds the in-progress accumulation state before it is flushed to
// the work_spans table.
type openSpan struct {
	key          spanKey
	projectPath  string
	sessionIDs   map[string]struct{}
	tokensFresh  int64
	tokensCacheR int64
	tokensCacheW int64
	tokensOut    int64
	msgCount     int
	openedAt     int64
	msgs         []*connectors.Message // retained for waste detection
}

func newOpenSpan(msg *connectors.Message) *openSpan {
	return &openSpan{
		key:         spanKey{gitBranch: msg.GitBranch, cwd: msg.Cwd},
		projectPath: msg.ProjectPath,
		sessionIDs:  map[string]struct{}{},
		openedAt:    msg.Ts,
	}
}

// accumulate adds one message's token counts to the open span.
func (os *openSpan) accumulate(msg *connectors.Message) {
	os.sessionIDs[msg.SessionID] = struct{}{}
	fresh := msg.TokensIn - msg.CachedReadTokens - msg.CachedWriteTokens
	if fresh < 0 {
		fresh = 0
	}
	os.tokensFresh += fresh
	os.tokensCacheR += msg.CachedReadTokens
	os.tokensCacheW += msg.CachedWriteTokens
	os.tokensOut += msg.TokensOut
	os.msgCount++
	os.msgs = append(os.msgs, msg)
}

// toWorkSpan converts the open span to a store.WorkSpan using the provided
// close coordinates. wasteClasses and wasteMeta come from the waste detector.
func (os *openSpan) toWorkSpan(commitSHA, exploreID, bucket string, closedAt int64, wasteClasses []string, wasteMeta any) store.WorkSpan {
	sessions := make([]string, 0, len(os.sessionIDs))
	for sid := range os.sessionIDs {
		sessions = append(sessions, sid)
	}
	return store.WorkSpan{
		CommitSHA:        commitSHA,
		ExplorationID:    exploreID,
		Bucket:           bucket,
		ProjectPath:      os.projectPath,
		GitBranch:        os.key.gitBranch,
		SessionIDs:       sessions,
		WasteClasses:     wasteClasses,
		WasteMeta:        wasteMeta,
		TokensFresh:      os.tokensFresh,
		TokensCacheRead:  os.tokensCacheR,
		TokensCacheWrite: os.tokensCacheW,
		TokensOut:        os.tokensOut,
		MsgCount:         os.msgCount,
		OpenedAt:         os.openedAt,
		ClosedAt:         closedAt,
	}
}

// Attribution is the result of a single batch run.
type Attribution struct {
	SpansWritten int
	SpansSkipped int // spans with zero messages (degenerate state)
}

// Run streams all messages from the DB since sinceMs, runs the attribution
// state machine, and writes new work_span rows.
//
// It does NOT deduplicate against existing spans — callers should truncate
// or use a rebuild strategy. For v0, "klyne cost week --since=7d" is the
// primary entry point and always rebuilds from the message window.
func (r *Runner) Run(ctx context.Context, sinceMs int64, dryRun bool) (*Attribution, error) {
	msgs, err := loadMessages(ctx, r.db, sinceMs)
	if err != nil {
		return nil, fmt.Errorf("attribution: load messages: %w", err)
	}

	spans, err := buildSpans(msgs)
	if err != nil {
		return nil, fmt.Errorf("attribution: build spans: %w", err)
	}

	attr := &Attribution{}
	for i := range spans {
		if spans[i].MsgCount == 0 {
			attr.SpansSkipped++
			continue
		}
		if dryRun {
			attr.SpansWritten++
			continue
		}
		if err := store.InsertWorkSpan(ctx, r.db, &spans[i]); err != nil {
			return nil, fmt.Errorf("attribution: insert span: %w", err)
		}
		attr.SpansWritten++
	}
	return attr, nil
}

// buildSpans runs the state machine over a pre-loaded, chronologically
// ordered message slice and returns the resulting work spans. It is
// separated from Run so tests can exercise the logic without a DB.
func buildSpans(msgs []*connectors.Message) ([]store.WorkSpan, error) {
	state := map[spanKey]*openSpan{}
	var results []store.WorkSpan

	for _, msg := range msgs {
		if msg == nil {
			continue
		}
		key := spanKey{gitBranch: msg.GitBranch, cwd: msg.Cwd}

		os := state[key]
		if os == nil {
			os = newOpenSpan(msg)
			state[key] = os
		}
		os.accumulate(msg)

		// Scan tool calls for git commit events.
		for _, tc := range msg.ToolCalls {
			if !isBashCall(tc) {
				continue
			}
			cmd := bashCommand(tc)
			if isGitCommit(cmd) {
				// Parse the commit SHA from the matching tool result.
				sha := parseCommitSHA(msg, tc.ID)
				if sha == "" {
					sha = "unknown-" + tc.ID
				}
				closedAt := msg.Ts

				wasteClasses, wasteMeta := detectWaste(os.msgs)
				sp := os.toWorkSpan(sha, "", "commit", closedAt, wasteClasses, wasteMeta)
				results = append(results, sp)

				// Open a fresh span immediately after the commit (same key).
				fresh := newOpenSpan(msg)
				fresh.openedAt = msg.Ts
				state[key] = fresh
				break // one commit per message is the common case
			}
		}
	}

	// Flush all still-open spans as exploration buckets.
	for _, os := range state {
		if os.msgCount == 0 {
			continue
		}
		explorationID, err := newExplorationID()
		if err != nil {
			explorationID = "exp-err"
		}
		wasteClasses, wasteMeta := detectWaste(os.msgs)
		closedAt := os.msgs[len(os.msgs)-1].Ts
		sp := os.toWorkSpan("", explorationID, "exploration", closedAt, wasteClasses, wasteMeta)
		results = append(results, sp)
	}

	return results, nil
}

// ---- message loading -------------------------------------------------------

// loadMessages fetches all messages since sinceMs ordered by (session_id, ts).
func loadMessages(ctx context.Context, db *store.DB, sinceMs int64) ([]*connectors.Message, error) {
	const q = `
SELECT m.id, m.session_id,
       COALESCE(s.project_path, ''), m.role,
       COALESCE(m.tokens_in, 0), COALESCE(m.tokens_out, 0),
       COALESCE(m.cached_read_tokens, 0), COALESCE(m.cached_write_tokens, 0),
       COALESCE(m.model, ''), m.ts,
       COALESCE(m.git_branch, ''), COALESCE(m.cwd, ''),
       COALESCE(m.tool_calls_json, '')
  FROM messages m
  LEFT JOIN sessions s ON s.id = m.session_id
 WHERE m.ts >= ?
 ORDER BY m.session_id, m.ts`

	rows, err := db.Read().QueryContext(ctx, q, sinceMs)
	if err != nil {
		return nil, fmt.Errorf("load messages: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var msgs []*connectors.Message
	for rows.Next() {
		var (
			id, sessionID, projectPath, role, model string
			tokIn, tokOut, cachedR, cachedW, ts     int64
			gitBranch, cwd, toolCallsJSON           string
		)
		if err := rows.Scan(
			&id, &sessionID, &projectPath, &role,
			&tokIn, &tokOut, &cachedR, &cachedW,
			&model, &ts, &gitBranch, &cwd, &toolCallsJSON,
		); err != nil {
			return nil, fmt.Errorf("scan message row: %w", err)
		}

		msg := &connectors.Message{
			ID:                id,
			SessionID:         sessionID,
			ProjectPath:       projectPath,
			Role:              connectors.Role(role),
			TokensIn:          tokIn,
			TokensOut:         tokOut,
			CachedReadTokens:  cachedR,
			CachedWriteTokens: cachedW,
			Model:             model,
			Ts:                ts,
			GitBranch:         gitBranch,
			Cwd:               cwd,
		}

		if toolCallsJSON != "" && toolCallsJSON != "[]" && toolCallsJSON != "null" {
			var tcs []connectors.ToolCall
			if err := json.Unmarshal([]byte(toolCallsJSON), &tcs); err == nil {
				msg.ToolCalls = tcs
			}
		}

		msgs = append(msgs, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("messages rows: %w", err)
	}
	return msgs, nil
}

// ---- tool-call parsing helpers ---------------------------------------------

func isBashCall(tc connectors.ToolCall) bool {
	return strings.EqualFold(tc.Name, "bash")
}

// bashCommand extracts the shell command string from a Bash tool call's Input.
// Claude Code emits Input as a JSON object {"command":"..."}.
func bashCommand(tc connectors.ToolCall) string {
	raw := strings.TrimSpace(tc.Input)
	if raw == "" {
		return ""
	}
	if raw[0] == '{' {
		var obj struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal([]byte(raw), &obj); err == nil && obj.Command != "" {
			return obj.Command
		}
	}
	// Plain string fallback.
	return strings.Trim(raw, `"`)
}

// isGitCommit returns true when the shell command starts with "git commit".
func isGitCommit(cmd string) bool {
	trimmed := strings.TrimSpace(cmd)
	return strings.HasPrefix(trimmed, "git commit") ||
		strings.Contains(trimmed, " && git commit") ||
		strings.Contains(trimmed, "; git commit")
}

// parseCommitSHA scans the tool results on msg for the result matching toolID
// and extracts the first git-SHA-looking token from the output.
func parseCommitSHA(msg *connectors.Message, toolID string) string {
	for _, tr := range msg.ToolResults {
		if tr.ID != toolID {
			continue
		}
		return extractGitSHA(tr.Output)
	}
	return ""
}

// extractGitSHA finds a git SHA in s. Accepts:
//   - 40-char full SHA
//   - 7–12 char short SHA (standalone word, all hex)
//
// Strips common surrounding punctuation before checking length/hex.
func extractGitSHA(s string) string {
	for _, token := range strings.Fields(s) {
		// Strip surrounding punctuation including brackets (git output: "[main abc]").
		token = strings.Trim(token, ".,;:'\"[](){}\n\r")
		if len(token) >= 40 && isHex(token[:40]) {
			return token[:40]
		}
		if len(token) == 40 && isHex(token) {
			return token
		}
		if len(token) >= 7 && len(token) <= 12 && isHex(token) {
			return token
		}
	}
	return ""
}

func isHex(s string) bool {
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// newExplorationID generates a random hex string for open/exploration spans.
func newExplorationID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "exp-" + hex.EncodeToString(b), nil
}

// detectWaste runs the v0 waste detectors and returns (classes, meta).
func detectWaste(msgs []*connectors.Message) ([]string, any) {
	loops := DetectWasteLoop(msgs)
	if len(loops) == 0 {
		return nil, nil
	}
	classes := make([]string, len(loops))
	for i, l := range loops {
		classes[i] = string(l.Class)
	}
	meta := map[string]any{
		"waste_loop": map[string]any{
			"hash":  loops[0].OffendingHash,
			"count": loops[0].Count,
		},
	}
	return classes, meta
}

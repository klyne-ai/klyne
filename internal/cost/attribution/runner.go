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
//
// sessionID is part of the key on purpose: the prior version (branch, cwd) only
// caused state to bleed across sessions. With messages ordered by
// (session_id, ts) and the same branch+cwd reused across sessions, an open span
// from session A would absorb session B's messages, then close at B's commit,
// producing identical-token "duplicate" spans whose closed_at preceded their
// opened_at (negative span duration). Per-session state machine fixes both.
type spanKey struct {
	sessionID string
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
		key:         spanKey{sessionID: msg.SessionID, gitBranch: msg.GitBranch, cwd: msg.Cwd},
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
	SpansSkipped int   // spans with zero messages (degenerate state)
	SpansDeleted int64 // rows removed by the pre-run truncate (debug visibility)
}

// Run streams all messages from the DB since sinceMs, runs the attribution
// state machine, and writes new work_span rows.
//
// Truncate-then-insert: every Run call first deletes existing work_spans rows
// in the same time window so the table holds exactly one batch per window.
// Without this, repeat `klyne cost week` invocations append duplicate spans,
// the renderer sums across all of them, and you see N× inflated totals plus
// "triplicate" spans in the Top digest.
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
	if !dryRun {
		deleted, err := store.DeleteWorkSpansSince(ctx, r.db, sinceMs)
		if err != nil {
			return nil, fmt.Errorf("attribution: truncate stale spans: %w", err)
		}
		attr.SpansDeleted = deleted
	}
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
		key := spanKey{sessionID: msg.SessionID, gitBranch: msg.GitBranch, cwd: msg.Cwd}

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
				// SHA recovery via tool_result is structurally unreliable
				// (Claude stores stdout in the next user message, not the
				// assistant message that issued the call). Fall back to
				// extracting the commit subject from the -m flag so the
				// digest shows something meaningful instead of "unknown".
				sha := parseCommitSHA(msg, tc.ID)
				if sha == "" {
					if subj := extractCommitSubject(cmd); subj != "" {
						sha = "msg:" + subj
					} else {
						sha = "unknown-" + tc.ID
					}
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

// extractCommitSubject pulls the first-line subject from a `git commit -m "..."`
// (or `-am`, `--message=`) flag. Returns "" when no -m flag is present or the
// command shape isn't recognized.
//
// Heredoc-wrapped messages (`-m "$(cat <<'EOF' ... EOF)"`) are handled by
// scanning the cmd's lines for the first non-empty line between the heredoc
// markers — that line is the commit subject by convention.
func extractCommitSubject(cmd string) string {
	subject := extractDirectMessage(cmd)
	if subject == "" {
		return ""
	}
	if strings.Contains(subject, "$(cat") || strings.Contains(cmd, "<<'") ||
		strings.Contains(cmd, `<<"`) || strings.Contains(cmd, "<<EOF") {
		if h := extractHeredocFirstLine(cmd); h != "" {
			return h
		}
	}
	return subject
}

// extractDirectMessage returns the contents of the first quoted argument that
// follows -m / -am / --message=. Naive: doesn't unescape; takes everything up
// to the matching closing quote.
func extractDirectMessage(cmd string) string {
	flags := []string{" -m ", " -am ", " --message=", " --message "}
	idx := -1
	flagLen := 0
	for _, f := range flags {
		if i := strings.Index(cmd, f); i >= 0 && (idx < 0 || i < idx) {
			idx = i
			flagLen = len(f)
		}
	}
	if idx < 0 {
		return ""
	}
	after := cmd[idx+flagLen:]
	// Find the opening quote.
	var quote byte
	startQ := -1
	for i := 0; i < len(after); i++ {
		if after[i] == '"' || after[i] == '\'' {
			quote = after[i]
			startQ = i + 1
			break
		}
		if after[i] != ' ' && after[i] != '\t' {
			break // first non-space is not a quote — bail
		}
	}
	if startQ < 0 {
		return ""
	}
	endQ := strings.IndexByte(after[startQ:], quote)
	if endQ < 0 {
		return ""
	}
	subject := after[startQ : startQ+endQ]
	if i := strings.IndexByte(subject, '\n'); i >= 0 {
		subject = subject[:i]
	}
	subject = strings.TrimSpace(subject)
	if len(subject) > 60 {
		subject = subject[:57] + "..."
	}
	return subject
}

// extractHeredocFirstLine scans cmd for a heredoc body and returns its first
// non-empty line (the commit subject by convention).
func extractHeredocFirstLine(cmd string) string {
	lines := strings.Split(cmd, "\n")
	seenStart := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !seenStart {
			if strings.Contains(line, "<<'") || strings.Contains(line, `<<"`) ||
				strings.Contains(line, "<<EOF") {
				seenStart = true
			}
			continue
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "EOF") {
			if strings.HasPrefix(trimmed, "EOF") {
				return ""
			}
			continue
		}
		if len(trimmed) > 60 {
			trimmed = trimmed[:57] + "..."
		}
		return trimmed
	}
	return ""
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

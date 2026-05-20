// Production wiring for the migration-019 rich worklog-entry pipeline.
// This file contains:
//
//   * productionInputBuilder — the daemon-side InputBuilder that
//     turns a stop_summaries row into the richentry writer's bundle
//     inputs (commits via gitscan, messages via SQL, gh PRs via
//     migration-018 cache, active intervals from message timestamps).
//
//   * (a *App) startRichEntryWorker — starts richentry.Worker.Run on
//     a goroutine alongside the other daemon services, gated on
//     cfg.Worklog.RichEntryWorkerEnabled. The worker drains pending
//     rows on a ticker; per-tick failures don't tear it down.
//
// The whole surface is OFF by default (config flag) so a fresh
// install does not start producing rich entries until the user opts
// in — same precedent as CodexDetectorEnabled (migration 015 layer).
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"

	"github.com/klyne-ai/klyne/internal/productivity"
	"github.com/klyne-ai/klyne/internal/store"
	"github.com/klyne-ai/klyne/internal/worklog/richentry"
)

// richEntryWorkerTick controls how often the worker checks the queue.
// Short enough that a stop-hook write surfaces fast; long enough that
// an empty queue isn't constantly hammered.
const richEntryWorkerTick = 30 * time.Second

// startRichEntryWorker spins up richentry.Worker.Run on a goroutine
// if cfg.Worklog.RichEntryWorkerEnabled is true. The worker uses the
// daemon's shared AI provider and runs against the daemon's DB.
//
// No-op + logs a single info line when the flag is off so users who
// haven't opted in see a clear signal "this is intentionally not
// running" in the daemon logs.
func (a *App) startRichEntryWorker(ctx context.Context) {
	if !a.cfg.Worklog.RichEntryWorkerEnabled {
		a.logger.Info("rich-entry worker disabled (cfg.worklog.rich_entry_worker_enabled = false)")
		return
	}
	if a.aiProvider == nil {
		// Should never happen — BuildOnly always sets either a real
		// provider or noopProvider{}. Defensive log + no-op.
		a.logger.Warn("rich-entry worker: aiProvider unexpectedly nil; not starting")
		return
	}

	// Surface the pending queue depth at startup so the operator can
	// see "the worker has N rows to process" without poking at the DB.
	// Cheap one-row aggregate; per-row processing logs are tick-scoped.
	if pending, err := pendingQueueCount(ctx, a.db); err == nil {
		a.logger.Info("rich-entry queue at startup",
			slog.Int("pending_rows", pending),
			slog.Duration("tick", richEntryWorkerTick))
	}

	worker := richentry.NewWorker(richentry.WorkerDeps{
		DB:     a.db,
		LLM:    a.aiProvider,
		Inputs: &productionInputBuilder{db: a.db, logger: a.logger},
	})
	a.ingestWG.Add(1)
	a.logger.Info("rich-entry worker started",
		slog.Duration("tick", richEntryWorkerTick))
	go func() {
		defer a.ingestWG.Done()
		if err := worker.Run(ctx, richEntryWorkerTick); err != nil && !errors.Is(err, context.Canceled) {
			a.logger.Warn("rich-entry worker exited", slog.Any("error", err))
		}
	}()
}

// pendingQueueCount returns how many stop_summaries rows are still
// eligible for the worker queue (verdict empty or 'pending',
// attempts under the cap). Used at startup so operators can see what
// volume the worker will chew through.
func pendingQueueCount(ctx context.Context, db *store.DB) (int, error) {
	const q = `
SELECT COUNT(*)
  FROM stop_summaries
 WHERE worklog_gate_verdict IN ('', 'pending')
   AND worklog_attempts < 3`
	var n int
	if err := db.Read().QueryRowContext(ctx, q).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// productionInputBuilder is the daemon-side InputBuilder. It reads
// messages from the DB, runs a git scan over the turn's commit window,
// and (TODO Phase 5.3 follow-up) reads the gh PR cache. v1 leaves
// MergedPRs nil so PR-number validation is SKIPPED rather than
// failing — see Allowlist.PRCacheLive in richentry/validator.go.
type productionInputBuilder struct {
	db     *store.DB
	logger *slog.Logger
}

// commitWindowLookback bounds how far back we scan git commits for a
// turn's bundle. 12h is plenty to catch the day's earlier work the
// writer might cite without pulling weeks of unrelated history.
const commitWindowLookback = 12 * time.Hour

// Build assembles the writer's BundleInputs + the gate's
// SessionMetrics for one pending stop_summaries row. Errors propagate
// so the worker's transient-retry path (verdict='pending',
// attempts++) handles them uniformly.
func (b *productionInputBuilder) Build(
	ctx context.Context, row store.PendingWorklogEntry,
) (richentry.BundleInputs, richentry.SessionMetrics, error) {
	turnTs := time.UnixMilli(row.Ts)
	since := turnTs.Add(-commitWindowLookback)
	until := turnTs.Add(1 * time.Minute) // tiny pad so this turn's own commit is included

	// 1. Repo + branch + ticket
	repoName := productivity.RepoName(row.ProjectPath)
	branch, ticket := branchAndTicket(row.ProjectPath)

	// 2. User commits in window (productivity gitscan handles the
	//    identity filter via the seeded UserEmails set).
	userEmails := productivity.UserEmails()
	var commits []richentry.CommitRef
	var totalLinesChanged int
	var totalNonDocFiles int
	if scanRes, scanErr := productivity.ScanRepo(row.ProjectPath, since, until, userEmails); scanErr == nil {
		for _, c := range scanRes.Commits {
			if !c.IsUser {
				continue
			}
			// Files in productivity.Commit is an int (count), not paths.
			// To populate the writer's allowlist with real file paths and
			// to compute NonDocFilesTouched honestly we run a cheap
			// `git show --name-only` per user commit.
			paths := filePathsForCommit(row.ProjectPath, c.SHA)
			commits = append(commits, richentry.CommitRef{
				SHA:         c.SHA,
				Files:       paths,
				CommittedAt: c.CommittedAt,
			})
			totalLinesChanged += c.Insertions + c.Deletions
			totalNonDocFiles += countNonDocFiles(paths)
		}
	} else {
		// Git scan failure is not fatal — leave commits empty and let
		// the gate decide based on duration + messages alone. Most
		// turns without commits then end up heuristic-denied, which
		// is the right outcome for "no real git work happened".
		b.logger.Warn("rich-entry: git scan failed for row",
			slog.String("session_id", row.SessionID),
			slog.String("project_path", row.ProjectPath),
			slog.Any("error", scanErr))
	}

	// 3. User messages + active intervals for THIS session in the window.
	userMsgs, msgTimes, msgErr := b.loadUserMessages(ctx, row.SessionID, since, until)
	if msgErr != nil {
		// Same transient-retry posture as a git scan failure.
		b.logger.Warn("rich-entry: load messages failed for row",
			slog.String("session_id", row.SessionID),
			slog.Any("error", msgErr))
	}
	var intervals []richentry.Interval
	if len(msgTimes) >= 2 {
		intervals = append(intervals, richentry.Interval{
			Start: msgTimes[0],
			End:   msgTimes[len(msgTimes)-1],
		})
	}

	// 4. Active duration for the gate's heuristic.
	var activeDur time.Duration
	if len(intervals) > 0 {
		activeDur = intervals[0].End.Sub(intervals[0].Start)
	}

	// gh PR cache lookup (migration-018 table). Returns nil when this
	// repo isn't on github / has no remote / the cache has no row for
	// this slug+window → PRCacheLive=false → validator skips PR-number
	// checks honestly. Non-nil (even []) means "we checked, here's the
	// answer" → unknown PRs are rejected.
	mergedPRs := b.loadMergedPRsForDay(ctx, row.ProjectPath, turnTs)

	bundle := richentry.BundleInputs{
		SessionID:       row.SessionID,
		Ts:              turnTs,
		ProjectPath:     row.ProjectPath,
		RepoName:        repoName,
		BranchName:      branch,
		BranchTicket:    ticket,
		StopSummaryBody: row.Summary,
		UserMessages:    userMsgs,
		Commits:         commits,
		MergedPRs:       mergedPRs,
		ActiveIntervals: intervals,
		SessionFiles:    row.Files,
		PriorSessionIDs: nil,
	}

	metrics := richentry.SessionMetrics{
		UserCommits:        len(commits),
		ActiveDuration:     activeDur,
		UserMessages:       len(userMsgs),
		LinesChanged:       totalLinesChanged,
		NonDocFilesTouched: totalNonDocFiles,
		HasDecisionKeyword: containsAnyKeyword(userMsgs, decisionKeywords),
		HasBugKeyword:      containsAnyKeyword(userMsgs, bugKeywords),
		HasBlockerKeyword:  containsAnyKeyword(userMsgs, blockerKeywords),
	}
	return bundle, metrics, nil
}

// loadUserMessages returns the user-role message texts for sessionID
// whose ts falls in [since, until], plus the ordered timestamps used
// for the active-interval span. Ordered ASC by ts.
func (b *productionInputBuilder) loadUserMessages(
	ctx context.Context, sessionID string, since, until time.Time,
) (texts []string, times []time.Time, err error) {
	const q = `
SELECT ts, content
  FROM messages
 WHERE session_id = ?
   AND role = 'user'
   AND ts BETWEEN ? AND ?
 ORDER BY ts ASC`
	rows, err := b.db.Read().QueryContext(ctx, q, sessionID, since.UnixMilli(), until.UnixMilli())
	if err != nil {
		return nil, nil, fmt.Errorf("rich-entry: messages query: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var ts int64
		var content string
		if err := rows.Scan(&ts, &content); err != nil {
			return nil, nil, fmt.Errorf("rich-entry: scan message: %w", err)
		}
		// content may be a raw string OR a JSON blob (e.g.
		// `[{"type":"text","text":"..."}]`). Best-effort flatten —
		// the gate's keyword check works on either; the writer prompt
		// surfaces the verbatim form.
		texts = append(texts, flattenMessageContent(content))
		times = append(times, time.UnixMilli(ts))
	}
	return texts, times, rows.Err()
}

// branchAndTicket resolves the current branch + its derived ticket id
// (if the branch name embeds CLI-NNNN / SCOPE-NNNN). Empty strings
// when git can't be resolved.
func branchAndTicket(dir string) (branch, ticket string) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return "", ""
	}
	branch = strings.TrimSpace(string(out))
	if branch == "" || branch == "HEAD" {
		return branch, ""
	}
	// Reuse the productivity package's ticket regex semantics: alpha
	// prefix of 2+ chars + dash + digits. Inline since the regex isn't
	// exported.
	if span := indexTicket(branch); span.start >= 0 {
		ticket = strings.ToUpper(branch[span.start:span.end])
	}
	return branch, ticket
}

// ticketSpan is the [start,end) byte range of a CLI-NNNN-style ticket
// inside a branch name.
type ticketSpan struct{ start, end int }

// indexTicket walks the branch string and returns the byte span of
// the first ticket-shaped token, or {-1,-1} when none.
func indexTicket(branch string) ticketSpan {
	// Tiny hand-rolled scanner — cheaper than regex for this hot path
	// and matches productivity/gitscan.go's ticketRe semantics:
	// `[A-Za-z]{2,}-\d+`.
	n := len(branch)
	for i := 0; i < n; i++ {
		j := i
		for j < n && isAlpha(branch[j]) {
			j++
		}
		if j-i < 2 || j == n || branch[j] != '-' {
			i = j
			continue
		}
		k := j + 1
		for k < n && isDigit(branch[k]) {
			k++
		}
		if k > j+1 {
			return ticketSpan{start: i, end: k}
		}
		i = k
	}
	return ticketSpan{start: -1, end: -1}
}

func isAlpha(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }
func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// filePathsForCommit returns the file paths touched by one commit via
// `git show --name-only`. Returns an empty slice on any error so the
// caller can still emit a CommitRef (with only the SHA citable).
func filePathsForCommit(dir, sha string) []string {
	out, err := exec.Command("git", "-C", dir, "show", "--name-only", "--pretty=format:", sha).Output()
	if err != nil {
		return nil
	}
	var paths []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if p := strings.TrimSpace(line); p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

// countNonDocFiles returns the count of paths that aren't "documentation"
// — used by the gate's heuristic to distinguish typo-fix-on-README from
// real code work.
func countNonDocFiles(paths []string) int {
	n := 0
	for _, p := range paths {
		if !isDocFile(p) {
			n++
		}
	}
	return n
}

func isDocFile(path string) bool {
	p := strings.ToLower(path)
	switch {
	case strings.HasPrefix(p, "docs/"):
		return true
	case strings.HasSuffix(p, ".md"), strings.HasSuffix(p, ".mdx"):
		return true
	case strings.HasSuffix(p, ".txt"), strings.HasSuffix(p, ".rst"):
		return true
	case p == "readme", p == "license", p == "changelog":
		return true
	}
	return false
}

// flattenMessageContent best-effort extracts the human-readable text
// from a messages.content cell. Many connectors store the content as
// a JSON array of `{type, text}` blocks; some store raw strings.
// Returns the original string when it isn't JSON.
func flattenMessageContent(content string) string {
	s := strings.TrimSpace(content)
	if s == "" {
		return ""
	}
	if s[0] != '[' && s[0] != '{' {
		return content
	}
	// Try array-of-blocks shape first.
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(s), &blocks); err == nil {
		var sb strings.Builder
		for _, b := range blocks {
			if b.Type == "" || b.Type == "text" {
				if sb.Len() > 0 {
					sb.WriteString("\n")
				}
				sb.WriteString(b.Text)
			}
		}
		if sb.Len() > 0 {
			return sb.String()
		}
	}
	// Fall through to a generic decode that extracts any string field.
	return content
}

// containsAnyKeyword does a case-insensitive substring check across all
// messages — looking for the decision/bug/blocker signal words that
// rescue otherwise-borderline turns from heuristic-deny.
func containsAnyKeyword(messages, keywords []string) bool {
	for _, m := range messages {
		lower := strings.ToLower(m)
		for _, k := range keywords {
			if strings.Contains(lower, k) {
				return true
			}
		}
	}
	return false
}

// loadMergedPRsForDay reads the migration-018 github_pr_cache table
// for the repo at projectPath, scoped to the day containing turnTs.
// Returns nil when:
//   - dir has no resolvable github "owner/name" slug (no remote,
//     non-github remote, or unparseable URL)
//   - the cache has no row for (slug, window) — no lookup happened
//     for this day yet
//   - the cached payload is unparseable
//
// All three cases signal "we don't know what PRs exist for this day"
// → richentry's validator switches PR-number checks OFF (the honest
// behavior — better than rejecting an unknown #N that might be real).
// Returns a non-nil []MergedPRRef{} (possibly empty) when the cache
// row exists and parses — that signals "we DO know, and here's the
// list" → unknown PR refs from the writer get rejected.
//
// Window-key format matches productivity_github.go's cache writer
// (day padded ±1) so the same rows the dashboard wrote get reused.
func (b *productionInputBuilder) loadMergedPRsForDay(
	ctx context.Context, projectPath string, turnTs time.Time,
) []richentry.MergedPRRef {
	slug := githubSlugForDir(projectPath)
	if slug == "" {
		return nil
	}
	day := turnTs.Local()
	lo := day.AddDate(0, 0, -1).Format("2006-01-02")
	hi := day.AddDate(0, 0, 1).Format("2006-01-02")
	windowKey := lo + ".." + hi

	const q = `SELECT payload_json FROM github_pr_cache WHERE slug = ? AND window_key = ?`
	var payload string
	if err := b.db.Read().QueryRowContext(ctx, q, slug, windowKey).Scan(&payload); err != nil {
		return nil
	}
	// Cached payload mirrors productivity.MergedPR's JSON shape —
	// number / title / head_ref / merged_at / opened_at / time_to_ship_minutes.
	// MergeSHA is NOT in the cache (productivity_github.go drops it to
	// avoid GraphQL node-cap blowup); MergeSHA stays empty in the
	// MergedPRRef, so the writer can cite #N but not the merge sha.
	// The gold pattern of "#400 + e9a1b2a" then degrades to just "#400" —
	// honest cost of avoiding the GraphQL hit.
	var raw []struct {
		Number   int       `json:"number"`
		Title    string    `json:"title"`
		HeadRef  string    `json:"head_ref"`
		MergedAt time.Time `json:"merged_at"`
	}
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		return nil
	}
	out := make([]richentry.MergedPRRef, 0, len(raw))
	for _, p := range raw {
		out = append(out, richentry.MergedPRRef{
			Number:   p.Number,
			Title:    p.Title,
			MergedAt: p.MergedAt,
			// MergeSHA intentionally empty — not in the cache schema.
		})
	}
	return out
}

// githubSlugForDir returns "owner/name" for the github repo at dir,
// or "" when dir has no resolvable origin remote / the remote isn't
// shaped like a github URL we can parse. Mirrors
// internal/api/handlers/productivity_github.go parseRepoSlug — kept
// inline here so this package doesn't import handlers.
func githubSlugForDir(dir string) string {
	out, err := exec.Command("git", "-C", dir, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return parseGitHubSlug(strings.TrimSpace(string(out)))
}

func parseGitHubSlug(raw string) string {
	raw = strings.TrimSuffix(strings.TrimSpace(raw), ".git")
	if raw == "" {
		return ""
	}
	if i := strings.Index(raw, "://"); i >= 0 {
		raw = raw[i+3:] // strip scheme
	} else if i := strings.Index(raw, ":"); i >= 0 {
		raw = raw[:i] + "/" + raw[i+1:] // scp-like → path
	}
	parts := strings.Split(strings.Trim(raw, "/"), "/")
	if len(parts) < 2 {
		return ""
	}
	owner, name := parts[len(parts)-2], parts[len(parts)-1]
	if owner == "" || name == "" || strings.ContainsAny(owner, "@ ") {
		return ""
	}
	return owner + "/" + name
}

// Keyword sets — lower-case, substring-matched. Kept small and
// targeted so a borderline turn that mentions "found a bug" rescues
// itself without the LLM tiebreak.
var (
	decisionKeywords = []string{"decided", "decision", "we'll go with", "we will use", "picked", "chose", "rejected"}
	bugKeywords      = []string{"bug", "broken", "regression", "failing", "fails to", "doesn't work", "won't work"}
	blockerKeywords  = []string{"blocked", "blocker", "stuck", "can't proceed", "waiting on"}
)

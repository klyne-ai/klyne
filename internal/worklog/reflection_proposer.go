// Package-level helper for the propose_reflection MCP tool.
//
// LoadPendingEntries is a pure SQLite read that returns the visible
// stop_summaries rows written for a project since the last reflection
// for that project, plus a short reason string explaining WHY a
// synthesis is being proposed now. No AI call is made here — the
// host (Claude / Codex) is expected to do the synthesis itself using
// its own subscription auth.

package worklog

import (
	"context"
	"fmt"
	"time"

	"github.com/klyne-ai/klyne/internal/store"
)

// PendingEntry is one worklog entry the synthesizer should consider.
// Kept narrow on purpose — just enough for an LLM (or a human) to write
// a useful insight with a citation back to the entry's session_id.
type PendingEntry struct {
	SessionID        string    `json:"session_id"`
	CLI              string    `json:"cli"`
	RecapTopic       string    `json:"recap_topic,omitempty"`
	AIDraftedSummary string    `json:"ai_drafted_summary,omitempty"`
	LastUser         string    `json:"last_user,omitempty"`
	TS               time.Time `json:"ts"`
	Importance       int       `json:"importance"`
}

// LoadPendingEntries returns the visible entries written for projectPath
// since the last reflection. Used by the propose_reflection MCP tool and
// (in the future) by any non-Claude AI host that wants to synthesize.
//
// The `reason` return is a short, human-readable string explaining WHY
// the synthesis is being proposed now: either "importance-sum N ≥ T",
// "weekly cron (Sunday evening)", or "user-invoked" when neither
// trigger fired.
func LoadPendingEntries(ctx context.Context, db *store.DB, projectPath string, threshold int, now time.Time) ([]PendingEntry, string, error) {
	if threshold <= 0 {
		threshold = 150
	}
	var lastRefMs int64
	if err := db.Read().QueryRowContext(ctx,
		`SELECT COALESCE(MAX(ts), 0) FROM worklog_reflections WHERE project_path = ?`,
		projectPath).Scan(&lastRefMs); err != nil {
		return nil, "", fmt.Errorf("worklog: load last reflection ts: %w", err)
	}
	rows, err := db.Read().QueryContext(ctx,
		`SELECT session_id, cli, COALESCE(recap_topic,''), COALESCE(ai_drafted_summary,''),
                COALESCE(last_user,''), ts, importance
         FROM stop_summaries
         WHERE project_path = ? AND recap_visible = 1 AND ts > ?
         ORDER BY ts ASC LIMIT 100`,
		projectPath, lastRefMs)
	if err != nil {
		return nil, "", fmt.Errorf("worklog: load pending entries: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	var entries []PendingEntry
	sum := 0
	for rows.Next() {
		var e PendingEntry
		var tsMs int64
		if err := rows.Scan(&e.SessionID, &e.CLI, &e.RecapTopic, &e.AIDraftedSummary, &e.LastUser, &tsMs, &e.Importance); err != nil {
			return nil, "", fmt.Errorf("worklog: scan entry: %w", err)
		}
		e.TS = time.UnixMilli(tsMs)
		entries = append(entries, e)
		sum += e.Importance
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	// Determine reason. Order matters: importance-sum is the stronger
	// signal so it takes precedence over the weekly cron when both fire.
	reason := "user-invoked"
	if sum >= threshold {
		reason = fmt.Sprintf("importance-sum %d ≥ %d", sum, threshold)
	} else if now.Weekday() == time.Sunday && now.Hour() >= 20 && len(entries) > 0 {
		reason = "weekly cron (Sunday evening)"
	}
	return entries, reason, nil
}

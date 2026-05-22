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

// LoadPendingEntries returns the visible stop_summary entries written
// for projectPath on `day` (local time) since the last reflection's
// cursor for that day. Used by the propose_reflection MCP tool and
// (in the future) by any non-Claude AI host that wants to synthesize.
//
// `day` zero-value defaults to today (local). The window is a
// [start-of-local-day, start-of-next-local-day) range in epoch-ms; the
// cursor (docs/features/iterative-reflection.md) further restricts to
// stop_summaries with ts > MaxReflectionCursorForDay(project, day).
//
// The `reason` return is a short, human-readable string explaining WHY
// the synthesis is being proposed now: either "importance-sum N ≥ T",
// "weekly cron (Sunday evening)", "nothing new since <ts>" (C1 no-op),
// or "user-invoked".
func LoadPendingEntries(ctx context.Context, db *store.DB, projectPath string, threshold int, now time.Time, day time.Time) ([]PendingEntry, string, error) {
	if threshold <= 0 {
		threshold = 150
	}
	if day.IsZero() {
		day = now
	}
	// Resolve the [day-start, day-end) window in the caller's local zone
	// — matches how the dashboard buckets reflections (productivity API
	// uses local-time date strings for its day key).
	loc := day.Location()
	dayStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
	dayEnd := dayStart.Add(24 * time.Hour)
	dayStr := dayStart.Format("2006-01-02")
	dayStartMs := dayStart.UnixMilli()
	dayEndMs := dayEnd.UnixMilli()

	cursor, err := store.MaxReflectionCursorForDay(ctx, db, projectPath, dayStr)
	if err != nil {
		return nil, "", err
	}
	rows, err := db.Read().QueryContext(ctx,
		`SELECT session_id, cli, COALESCE(recap_topic,''), COALESCE(ai_drafted_summary,''),
                COALESCE(last_user,''), ts, importance
         FROM stop_summaries
         WHERE project_path = ?
           AND recap_visible = 1
           AND ts >= ? AND ts < ?
           AND ts > ?
         ORDER BY ts ASC LIMIT 100`,
		projectPath, dayStartMs, dayEndMs, cursor)
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
	// Empty entries when a cursor exists is the C1 no-op shape; with no
	// cursor, an empty window is still just "user-invoked" so the host
	// can render its own "nothing yet today" UX.
	reason := "user-invoked"
	if sum >= threshold {
		reason = fmt.Sprintf("importance-sum %d ≥ %d", sum, threshold)
	} else if now.Weekday() == time.Sunday && now.Hour() >= 20 && len(entries) > 0 {
		reason = "weekly cron (Sunday evening)"
	} else if len(entries) == 0 && cursor > 0 {
		reason = fmt.Sprintf("nothing new since %s", time.UnixMilli(cursor).In(loc).Format("15:04"))
	}
	return entries, reason, nil
}

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
	"database/sql"
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
// for projectPath since the last reflection's cursor. Used by the
// propose_reflection MCP tool and (in the future) by any non-Claude AI
// host that wants to synthesize.
//
// Two modes, keyed off the `day` argument:
//
//  1. day == zero (the common case for /klyne:reflect with no `day` arg):
//     return ALL visible pending entries since the global cursor across
//     every day — the slash-command host then buckets them by UTC date
//     and writes one reflection per bucket. The Worklog UI's
//     pending_entries count uses the same "since-last-reflection-anywhere"
//     definition, so this mode keeps the proposer aligned with the UI.
//
//  2. day != zero: backfill mode — restrict to the [day-start, day-end)
//     window in `day`'s local zone, gated by that day's per-day cursor.
//     Lets a host reflect a specific past day in isolation.
//
// The `reason` return is a short, human-readable string explaining WHY
// the synthesis is being proposed now: either "importance-sum N ≥ T",
// "weekly cron (Sunday evening)", "nothing new since <ts>" (C1 no-op),
// or "user-invoked".
func LoadPendingEntries(ctx context.Context, db *store.DB, projectPath string, threshold int, now time.Time, day time.Time) ([]PendingEntry, string, error) {
	if threshold <= 0 {
		threshold = 150
	}

	var (
		cursor   int64
		err      error
		rows     *sql.Rows
		dayBound bool
	)
	loc := now.Location()
	if day.IsZero() {
		// Cross-day mode: every visible pending entry since the global
		// cursor, with no day window. Matches the UI's pending_entries
		// definition so a card that says "N entries · no reflection yet"
		// actually surfaces those N entries to /klyne:reflect.
		cursor, err = store.MaxReflectionCursor(ctx, db, projectPath)
		if err != nil {
			return nil, "", err
		}
		rows, err = db.Read().QueryContext(ctx,
			`SELECT session_id, cli, COALESCE(recap_topic,''), COALESCE(ai_drafted_summary,''),
                    COALESCE(last_user,''), ts, importance
             FROM stop_summaries
             WHERE project_path = ?
               AND recap_visible = 1
               AND ts > ?
             ORDER BY ts ASC LIMIT 100`,
			projectPath, cursor)
	} else {
		// Per-day backfill mode. The day window is the bound — we
		// intentionally IGNORE the prior cursor here so a re-reflect of
		// the same day always sees every visible entry. The reflection
		// recorder upserts on (project_path, day) so each run rewrites
		// the day's typed payload from scratch; threading the cursor in
		// would create a permanent "covered but not synthesized" gap
		// whenever an earlier reflect under-cited (the ops-app
		// 2026-05-26 1-detail card bug).
		loc = day.Location()
		dayBound = true
		dayStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
		dayEnd := dayStart.Add(24 * time.Hour)
		// cursor stays at its zero default so the reason classifier still
		// reports "nothing new since …" correctly when entries == 0.
		_ = cursor
		rows, err = db.Read().QueryContext(ctx,
			`SELECT session_id, cli, COALESCE(recap_topic,''), COALESCE(ai_drafted_summary,''),
                    COALESCE(last_user,''), ts, importance
             FROM stop_summaries
             WHERE project_path = ?
               AND recap_visible = 1
               AND ts >= ? AND ts < ?
             ORDER BY ts ASC LIMIT 200`,
			projectPath, dayStart.UnixMilli(), dayEnd.UnixMilli())
	}
	_ = dayBound // reserved for future debug logging
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

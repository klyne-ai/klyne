package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// DailyProductivitySnapshot is one row of daily_productivity_snapshot —
// the deterministic per-day backing for the productivity dashboard
// (migration 021). It carries the full single-day
// internal/productivity.Report payload as JSON so a range query is a
// pure read-loop over per-day rows (no live git scan, no race between
// reloads).
//
// Source semantics:
//   - "reflection" — written by worklog.recordReflection when a daily
//     reflection lands. Authoritative; preferred for past-day rendering.
//   - "live"       — written by the API handler when it had to recompute
//     a past day on the fly because no reflection existed yet. Lazy
//     backfill; will be overwritten by a later "reflection" upsert.
type DailyProductivitySnapshot struct {
	ProjectPath        string `json:"project_path"`
	Day                string `json:"day"` // local YYYY-MM-DD
	PayloadJSON        string `json:"payload_json"`
	TotalActiveMinutes int    `json:"total_active_minutes"`
	Source             string `json:"source"` // "reflection" | "live"
	CreatedAt          int64  `json:"created_at"`
	UpdatedAt          int64  `json:"updated_at"`
}

// UpsertDailyProductivitySnapshot writes or replaces the snapshot for
// (project_path, day). Idempotent under the PK; the row's created_at is
// preserved on update so the "first time we saw this day" timestamp
// stays stable.
//
// Source precedence: a "reflection" row is authoritative and will NOT be
// overwritten by a subsequent "live" upsert (the WHERE clause on the
// ON CONFLICT enforces this at the SQL layer). A "reflection" upsert
// always wins — re-running /klyne:reflect for a day produces a fresh
// authoritative snapshot. Two "live" upserts: last-writer-wins, fine.
//
// The clobber-prevention matters because the handler's lazy backfill
// races a /klyne:reflect that lands during the same request — without
// the guard, the live recompute would silently replace the user's
// reflection-time payload (which carries ReflectionMarkdown the
// recompute doesn't reproduce).
func UpsertDailyProductivitySnapshot(ctx context.Context, db *DB, s DailyProductivitySnapshot) error {
	if s.ProjectPath == "" {
		return errors.New("store: daily productivity snapshot: project_path required")
	}
	if s.Day == "" {
		return errors.New("store: daily productivity snapshot: day required")
	}
	if s.PayloadJSON == "" {
		return errors.New("store: daily productivity snapshot: payload_json required")
	}
	if s.Source == "" {
		s.Source = "live"
	}
	if s.CreatedAt == 0 {
		s.CreatedAt = s.UpdatedAt
	}
	// Upsert precedence rules:
	//   1. Reflection-source upserts ALWAYS win (re-running /klyne:reflect
	//      or productivity-sync produces a fresh authoritative payload).
	//   2. Live-source upserts only overwrite when the existing row is
	//      NOT a substantive reflection. A "substantive reflection" is one
	//      that has measured activity — TotalActiveMinutes > 0. A
	//      barebones reflection (TotalActiveMinutes = 0, which happens when
	//      PersistLLMCompiledCard writes the FIRST snapshot for a day
	//      without a prior live snapshot to merge into) CAN be replaced by
	//      a live row that has real time/branches/sessions data. The
	//      WhatWasDone narrative card it carried lives separately in
	//      productivity_snapshots payload_json under services[].what_was_done,
	//      and hydrateWhatWasDone re-overlays it from a fresh snapshot
	//      read on every dashboard request — so the live overwrite is
	//      lossless for the card.
	_, err := db.Write().ExecContext(ctx, `
INSERT INTO daily_productivity_snapshot (
    project_path, day, payload_json, total_active_minutes,
    source, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(project_path, day) DO UPDATE SET
    payload_json         = excluded.payload_json,
    total_active_minutes = excluded.total_active_minutes,
    source               = excluded.source,
    updated_at           = excluded.updated_at
WHERE excluded.source = 'reflection'
   OR daily_productivity_snapshot.source != 'reflection'
   OR daily_productivity_snapshot.total_active_minutes = 0`,
		s.ProjectPath, s.Day, s.PayloadJSON, s.TotalActiveMinutes,
		s.Source, s.CreatedAt, s.UpdatedAt)
	if err != nil {
		return fmt.Errorf("store: upsert daily productivity snapshot: %w", err)
	}
	return nil
}

// GetDailyProductivitySnapshot returns the snapshot for one
// (project_path, day) tuple. found=false (with nil error) when no row
// exists — typical for past days that haven't been reflected or
// auto-backfilled yet; the caller is expected to live-compute and
// upsert.
func GetDailyProductivitySnapshot(
	ctx context.Context, db *DB, projectPath, day string,
) (DailyProductivitySnapshot, bool, error) {
	const q = `
SELECT project_path, day, payload_json, total_active_minutes,
       source, created_at, updated_at
FROM daily_productivity_snapshot
WHERE project_path = ? AND day = ?`
	var s DailyProductivitySnapshot
	err := db.Read().QueryRowContext(ctx, q, projectPath, day).Scan(
		&s.ProjectPath, &s.Day, &s.PayloadJSON, &s.TotalActiveMinutes,
		&s.Source, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return DailyProductivitySnapshot{}, false, nil
	}
	if err != nil {
		return DailyProductivitySnapshot{}, false, fmt.Errorf("store: get daily productivity snapshot: %w", err)
	}
	return s, true, nil
}

// ListDailyProductivitySnapshotsForDay returns every project's snapshot
// for one local day, ordered by project_path for stable iteration. Used
// by the productivity handler when aggregating across projects within a
// range — one call per day in the requested window.
func ListDailyProductivitySnapshotsForDay(
	ctx context.Context, db *DB, day string,
) ([]DailyProductivitySnapshot, error) {
	const q = `
SELECT project_path, day, payload_json, total_active_minutes,
       source, created_at, updated_at
FROM daily_productivity_snapshot
WHERE day = ?
ORDER BY project_path ASC`
	rows, err := db.Read().QueryContext(ctx, q, day)
	if err != nil {
		return nil, fmt.Errorf("store: list daily productivity snapshots for day: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	out := make([]DailyProductivitySnapshot, 0)
	for rows.Next() {
		var s DailyProductivitySnapshot
		if err := rows.Scan(&s.ProjectPath, &s.Day, &s.PayloadJSON, &s.TotalActiveMinutes,
			&s.Source, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, fmt.Errorf("store: scan daily productivity snapshot: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListDailyProductivitySnapshotsRange returns every snapshot whose
// (project_path, day) tuple falls inside [fromDay, toDay] inclusive.
// Days compare lexicographically because the format is fixed
// YYYY-MM-DD. Ordered (project_path, day) ASC so the caller can group
// without an additional sort.
func ListDailyProductivitySnapshotsRange(
	ctx context.Context, db *DB, projectPath, fromDay, toDay string,
) ([]DailyProductivitySnapshot, error) {
	const q = `
SELECT project_path, day, payload_json, total_active_minutes,
       source, created_at, updated_at
FROM daily_productivity_snapshot
WHERE project_path = ? AND day >= ? AND day <= ?
ORDER BY day ASC`
	rows, err := db.Read().QueryContext(ctx, q, projectPath, fromDay, toDay)
	if err != nil {
		return nil, fmt.Errorf("store: list daily productivity snapshots range: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	out := make([]DailyProductivitySnapshot, 0)
	for rows.Next() {
		var s DailyProductivitySnapshot
		if err := rows.Scan(&s.ProjectPath, &s.Day, &s.PayloadJSON, &s.TotalActiveMinutes,
			&s.Source, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, fmt.Errorf("store: scan daily productivity snapshot: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

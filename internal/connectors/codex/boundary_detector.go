package codex

// boundary_detector.go — Cross-AI worklog capture for Codex sessions.
//
// When a Codex session has been idle for more than idleThreshold, treat it
// as ended and write a worklog entry via the same single-writer the Claude
// Stop hook uses. This is the differentiator that makes the worklog
// cross-AI rather than Claude-only.
//
// Wiring: NewBoundaryDetector is constructed at app startup but the Tick()
// loop is wired into the daemon in T18 (along with the reflection runner)
// so we keep daemon-wiring changes consolidated.

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/klyne-ai/klyne/internal/store"
	"github.com/klyne-ai/klyne/internal/worklog"
)

// BoundaryDetector finds Codex sessions whose last message is older than
// idleThreshold and have no signature-bearing stop_summaries row yet, then
// writes one worklog entry per session via the shared single-writer.
type BoundaryDetector struct {
	idleThreshold time.Duration
}

// NewBoundaryDetector constructs a detector with the given idle window.
func NewBoundaryDetector(idle time.Duration) *BoundaryDetector {
	return &BoundaryDetector{idleThreshold: idle}
}

type eligibleSession struct {
	ID, ProjectPath string
	LastMsgAt       time.Time
}

// findEligibleSessions returns Codex sessions that:
//   - have cli = 'codex',
//   - have last_msg_at older than now - idleThreshold,
//   - are not in status 'closed',
//   - have no stop_summaries row with a non-empty signature yet.
//
// Capped at 100 per pass to bound work per tick.
func (d *BoundaryDetector) findEligibleSessions(ctx context.Context, db *store.DB) ([]eligibleSession, error) {
	cutoff := time.Now().Add(-d.idleThreshold).UnixMilli()
	const q = `
SELECT s.id, s.project_path, s.last_msg_at FROM sessions s
WHERE s.cli = 'codex' AND s.last_msg_at < ?
  AND COALESCE(s.status,'active') != 'closed'
  AND NOT EXISTS (
    SELECT 1 FROM stop_summaries ss
    WHERE ss.session_id = s.id AND ss.signature IS NOT NULL AND ss.signature != ''
  )
ORDER BY s.last_msg_at ASC LIMIT 100`
	rows, err := db.Read().QueryContext(ctx, q, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []eligibleSession
	for rows.Next() {
		var s eligibleSession
		var lastMs int64
		if err := rows.Scan(&s.ID, &s.ProjectPath, &lastMs); err != nil {
			return nil, err
		}
		s.LastMsgAt = time.UnixMilli(lastMs)
		out = append(out, s)
	}
	return out, rows.Err()
}

// Tick runs one pass: find eligible idle Codex sessions and write one
// worklog entry per session via the single-writer. Per-entry errors are
// logged and swallowed — one bad session must not stop the rest.
func (d *BoundaryDetector) Tick(ctx context.Context, db *store.DB) error {
	sessions, err := d.findEligibleSessions(ctx, db)
	if err != nil {
		return err
	}
	for _, s := range sessions {
		e, err := d.buildEntry(ctx, db, s)
		if err != nil {
			slog.Warn("codex.boundary.build_failed", "session_id", s.ID, "err", err)
			continue
		}
		if _, err := worklog.WriteEntry(ctx, db, e, nil, store.UpsertStopSummaryWithWorklog); err != nil {
			slog.Warn("codex.boundary.write_failed", "session_id", s.ID, "err", err)
		}
	}
	return nil
}

// buildEntry derives a minimal worklog.Entry from the session's recent
// messages. Files + EventTags + CommitSHA derivation is deferred to a
// later refactor once the cross-AI flow is verified end-to-end; the
// conservative defaults here still produce a valid entry with importance
// baseline and a stable signature.
func (d *BoundaryDetector) buildEntry(ctx context.Context, db *store.DB, s eligibleSession) (worklog.Entry, error) {
	var lastUser, lastBash sql.NullString
	var editCount int
	rdb := db.Read()
	// sql.ErrNoRows is expected for sessions that lack a message of the
	// queried kind; we tolerate it and leave the NullString invalid (empty).
	if err := rdb.QueryRowContext(ctx,
		`SELECT content FROM messages WHERE session_id=? AND role='user' ORDER BY ts DESC LIMIT 1`,
		s.ID).Scan(&lastUser); err != nil && err != sql.ErrNoRows {
		return worklog.Entry{}, err
	}
	if err := rdb.QueryRowContext(ctx,
		`SELECT content FROM messages WHERE session_id=? AND LOWER(tool_name)='bash' ORDER BY ts DESC LIMIT 1`,
		s.ID).Scan(&lastBash); err != nil && err != sql.ErrNoRows {
		return worklog.Entry{}, err
	}
	if err := rdb.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages WHERE session_id=? AND LOWER(tool_name) IN ('edit','write','multiedit')`,
		s.ID).Scan(&editCount); err != nil && err != sql.ErrNoRows {
		return worklog.Entry{}, err
	}
	return worklog.Entry{
		SessionID:      s.ID,
		TS:             s.LastMsgAt,
		ProjectPath:    s.ProjectPath,
		CLI:            "codex",
		LastUser:       lastUser.String,
		LastBash:       lastBash.String,
		EditWriteCount: editCount,
		WallTime:       d.idleThreshold,
		ToolCallCount:  editCount,
		EventTags:      []worklog.EventTag{},
	}, nil
}

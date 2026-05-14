// Package store — compact_events DAO.
//
// The compact_events table records every detected /compact event. Rows are
// keyed by (session_id, ts). The ingestion writer goroutine invokes
// InsertCompactEvent each time the Claude compact detector fires; replays
// of the same JSONL line therefore arrive with the same (session_id, ts)
// and are silently deduplicated via INSERT OR IGNORE.
package store

import (
	"context"
	"fmt"
)

// InsertCompactEvent records a /compact event for the given session.
//
// Idempotent: (session_id, ts) is the table's primary key, so re-replays
// of the same source line are absorbed without error and without a second
// row. Returns (inserted, err) where inserted=true means a new row was
// written and inserted=false means an identical row already existed.
//
// before and after are the assistant TokensIn values immediately before
// and after the compaction event. They may be 0 when the detector fired
// in single-signal mode (no surrounding assistant message available); the
// dashboard count metric does not depend on these values, but downstream
// "Restore context" features (W15) use them.
//
// Uses the write handle.
func InsertCompactEvent(ctx context.Context, db *DB, sessionID string, ts, before, after int64) (bool, error) {
	const q = `
INSERT OR IGNORE INTO compact_events
    (session_id, ts, before_token_count, after_token_count)
VALUES
    (?, ?, ?, ?)`

	res, err := db.Write().ExecContext(ctx, q, sessionID, ts, before, after)
	if err != nil {
		return false, fmt.Errorf("store: insert compact_event for session %s @ %d: %w", sessionID, ts, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store: insert compact_event rows-affected: %w", err)
	}
	return rows > 0, nil
}

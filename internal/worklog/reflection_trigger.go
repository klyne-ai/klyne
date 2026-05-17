package worklog

import (
	"context"
	"fmt"
	"time"

	"github.com/klyne-ai/klyne/internal/store"
)

// ShouldFireReflection returns true when the sum of importance scores of
// visible worklog entries written for projectPath since the last
// reflection for that project crosses the given threshold.
//
// The Generative Agents paper (Park et al. 2023) uses importance-sum ≥
// 150 as the synthesis trigger — small, deterministic, dependable.
func ShouldFireReflection(ctx context.Context, db *store.DB, projectPath string, threshold int) (bool, error) {
	var lastRefTS int64
	if err := db.Read().QueryRowContext(ctx,
		`SELECT COALESCE(MAX(ts), 0) FROM worklog_reflections WHERE project_path = ?`,
		projectPath).Scan(&lastRefTS); err != nil {
		return false, fmt.Errorf("worklog: load last reflection ts: %w", err)
	}
	var sum int
	if err := db.Read().QueryRowContext(ctx,
		`SELECT COALESCE(SUM(importance), 0) FROM stop_summaries
         WHERE project_path = ? AND recap_visible = 1 AND ts > ?`,
		projectPath, lastRefTS).Scan(&sum); err != nil {
		return false, fmt.Errorf("worklog: sum importance: %w", err)
	}
	return sum >= threshold, nil
}

// WeeklyCronShouldFire returns true when (a) it's Sunday evening (>= 20:00
// in `now`'s location) and (b) at least one visible entry has been
// written for the project since the last reflection. Skips quiet weeks
// (no point reflecting on no work).
func WeeklyCronShouldFire(ctx context.Context, db *store.DB, projectPath string, now time.Time) (bool, error) {
	if now.Weekday() != time.Sunday || now.Hour() < 20 {
		return false, nil
	}
	var lastRefMs int64
	if err := db.Read().QueryRowContext(ctx,
		`SELECT COALESCE(MAX(ts), 0) FROM worklog_reflections WHERE project_path = ?`,
		projectPath).Scan(&lastRefMs); err != nil {
		return false, fmt.Errorf("worklog: load last reflection ts: %w", err)
	}
	var count int
	if err := db.Read().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM stop_summaries
         WHERE project_path = ? AND recap_visible = 1 AND ts > ?`,
		projectPath, lastRefMs).Scan(&count); err != nil {
		return false, fmt.Errorf("worklog: count visible entries: %w", err)
	}
	return count > 0, nil
}

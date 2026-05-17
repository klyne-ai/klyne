package worklog

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/klyne-ai/klyne/internal/store"
)

// ExportArgs is the input bundle for ExportWeek.
type ExportArgs struct {
	ProjectPath string
	Week        string // ISO week label, e.g. "2026-W20"
	OutputRoot  string // root under which docs/worklog/<week>.md is written
}

// ExportResult captures whether a file was written (quiet weeks produce
// nothing) and, when written, its absolute path.
type ExportResult struct {
	FileWritten bool
	Path        string
}

// IsoWeek returns the ISO 8601 week label for the given instant
// (e.g. "2026-W20"). Years and weeks are zero-padded to two digits
// to keep filenames sortable.
func IsoWeek(t time.Time) string {
	y, w := t.ISOWeek()
	return fmt.Sprintf("%d-W%02d", y, w)
}

// ExportWeek writes <OutputRoot>/docs/worklog/<Week>.md if and only if
// the project has at least one visible worklog entry in that ISO week.
// Quiet weeks produce no file (mirrors release-please's "no commits,
// no release" pattern), so consumers can safely fan this out across
// every project without polluting trees with empty digests.
func ExportWeek(ctx context.Context, db *store.DB, args ExportArgs) (ExportResult, error) {
	start, end, err := parseWeekRange(args.Week)
	if err != nil {
		return ExportResult{}, fmt.Errorf("worklog: export: %w", err)
	}
	rows, err := db.Read().QueryContext(ctx,
		`SELECT cli, session_id, COALESCE(recap_topic,''), COALESCE(ai_drafted_summary,''),
                last_user, ts, importance, files_json
         FROM stop_summaries
         WHERE project_path = ? AND recap_visible = 1
           AND ts >= ? AND ts < ?
         ORDER BY ts ASC`,
		args.ProjectPath, start.UnixMilli(), end.UnixMilli())
	if err != nil {
		return ExportResult{}, fmt.Errorf("worklog: export query: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var entries []string
	for rows.Next() {
		var cli, sid, topic, summary, lastUser, filesJSON string
		var tsMs int64
		var imp int
		if err := rows.Scan(&cli, &sid, &topic, &summary, &lastUser, &tsMs, &imp, &filesJSON); err != nil {
			return ExportResult{}, fmt.Errorf("worklog: export scan: %w", err)
		}
		when := time.UnixMilli(tsMs).Format("Mon 15:04")
		title := topic
		if title == "" {
			title = truncateLine(lastUser, 80)
		}
		sidShort := sid
		if len(sidShort) > 8 {
			sidShort = sidShort[:8]
		}
		entry := fmt.Sprintf("### [%s] %s\n*%s · importance %d · session %s*\n\n%s\n",
			cli, title, when, imp, sidShort, summary)
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return ExportResult{}, fmt.Errorf("worklog: export rows: %w", err)
	}
	if len(entries) == 0 {
		return ExportResult{FileWritten: false}, nil
	}

	dir := filepath.Join(args.OutputRoot, "docs", "worklog")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ExportResult{}, fmt.Errorf("worklog: mkdir: %w", err)
	}
	path := filepath.Join(dir, args.Week+".md")
	body := fmt.Sprintf("# Worklog — %s\n\n%s", args.Week, strings.Join(entries, "\n"))
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return ExportResult{}, fmt.Errorf("worklog: write: %w", err)
	}
	return ExportResult{FileWritten: true, Path: path}, nil
}

// parseWeekRange returns the [Monday-00:00, next-Monday-00:00) UTC
// range for an ISO week label like "2026-W20".
//
// ISO 8601 anchor: week 1 is the week containing Jan 4. Go's
// time.Weekday treats Sunday as 0, but ISO weeks start Monday, so we
// remap Sunday to 7 before subtracting back to the Monday of Jan 4's
// week. Sanity-check: 2026-01-04 is a Sunday → weekStartOfJan4 maps
// to Mon 2025-12-29 (the Monday of 2026-W01), matching ISO 8601.
func parseWeekRange(week string) (start, end time.Time, err error) {
	var y, w int
	if _, err = fmt.Sscanf(week, "%d-W%d", &y, &w); err != nil {
		return
	}
	jan4 := time.Date(y, 1, 4, 0, 0, 0, 0, time.UTC)
	_, ww := jan4.ISOWeek()
	weekday := int(jan4.Weekday())
	if weekday == 0 { // Sunday in Go's weekday encoding
		weekday = 7
	}
	weekStartOfJan4 := jan4.AddDate(0, 0, -(weekday - 1))
	start = weekStartOfJan4.AddDate(0, 0, (w-ww)*7)
	end = start.AddDate(0, 0, 7)
	return
}

// truncateLine collapses newlines and shortens a string to n bytes
// with an ellipsis. Defined here (not in writer.go) so the export
// path has no implicit dependency on session-end internals.
func truncateLine(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

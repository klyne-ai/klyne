package store

import (
	"context"
	"fmt"
	"strings"
)

// AskRow is one stop_summaries row exposed to the Ask Klyne handler.
// Only the columns the prompt-builder reads are projected — keeps the
// query narrow and lets the handler-side code build prompts without
// pulling the heavier StopSummary type.
type AskRow struct {
	SessionID        string
	TsMs             int64
	ProjectPath      string
	AIDraftedSummary string
	// WorklogEntryJSON is raw JSON. The handler renders only the
	// categories it cares about (pending, bugs_fixed, etc.) — keeping
	// it as a string here means the store stays oblivious to the
	// worklog schema.
	WorklogEntryJSON string
}

// LoadAskContext returns every stop_summaries row in [fromMs, toMs]
// (inclusive), optionally filtered to the given project_paths. Empty
// projects means no project filter. Ordered ascending by ts so prompt
// construction is chronological.
func LoadAskContext(ctx context.Context, db *DB, projects []string, fromMs, toMs int64) ([]AskRow, error) {
	var (
		q    strings.Builder
		args []any
	)
	q.WriteString(`SELECT session_id, ts, project_path, ai_drafted_summary, worklog_entry_json
		FROM stop_summaries
		WHERE ts BETWEEN ? AND ?`)
	args = append(args, fromMs, toMs)

	if len(projects) > 0 {
		placeholders := make([]string, len(projects))
		for i, p := range projects {
			placeholders[i] = "?"
			args = append(args, p)
		}
		q.WriteString(" AND project_path IN (")
		q.WriteString(strings.Join(placeholders, ","))
		q.WriteString(")")
	}
	q.WriteString(" ORDER BY ts ASC")

	rows, err := db.Read().QueryContext(ctx, q.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("LoadAskContext: query: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var out []AskRow
	for rows.Next() {
		var r AskRow
		if err := rows.Scan(&r.SessionID, &r.TsMs, &r.ProjectPath,
			&r.AIDraftedSummary, &r.WorklogEntryJSON); err != nil {
			return nil, fmt.Errorf("LoadAskContext: scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("LoadAskContext: rows: %w", err)
	}
	return out, nil
}

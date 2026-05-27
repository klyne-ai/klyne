package store

import (
	"context"
	"errors"
	"fmt"
)

// KlyneLLMUsage is one row of klyne_llm_usage — a record of token usage
// for ONE klyne-spawned `claude` subprocess invocation. The two writers
// are spawnClaudeProductivitySync (operation="productivity_sync") and
// spawnClaudeReflect (operation="reflect"); both parse the CLI's
// `--output-format=json` result envelope to fill the token fields.
//
// Token fields mirror the Anthropic API usage block exactly so we can
// roll up against the user's daily session totals (messages.tokens_in /
// tokens_out / cached_read_tokens / cached_write_tokens) without unit
// conversion.
type KlyneLLMUsage struct {
	ID                int64  `json:"id"`
	ProjectPath       string `json:"project_path"`
	Day               string `json:"day"` // local YYYY-MM-DD
	Operation         string `json:"operation"`
	Model             string `json:"model"`
	InputTokens       int64  `json:"input_tokens"`
	OutputTokens      int64  `json:"output_tokens"`
	CacheReadTokens   int64  `json:"cache_read_tokens"`
	CacheWriteTokens  int64  `json:"cache_write_tokens"`
	DurationMs        int64  `json:"duration_ms"`
	NumTurns          int    `json:"num_turns"`
	SessionID         string `json:"session_id"`
	Status            string `json:"status"`
	CreatedAt         int64  `json:"created_at"`
}

// InsertKlyneLLMUsage appends one row. Idempotency is NOT enforced: the
// caller is expected to invoke this exactly once per subprocess run.
// (The handlers are the only callers and they own the lifecycle.)
func InsertKlyneLLMUsage(ctx context.Context, db *DB, u KlyneLLMUsage) error {
	if u.ProjectPath == "" {
		return errors.New("store: klyne llm usage: project_path required")
	}
	if u.Day == "" {
		return errors.New("store: klyne llm usage: day required")
	}
	if u.Operation == "" {
		return errors.New("store: klyne llm usage: operation required")
	}
	if u.Status == "" {
		u.Status = "ok"
	}
	if u.CreatedAt == 0 {
		return errors.New("store: klyne llm usage: created_at required")
	}
	_, err := db.Write().ExecContext(ctx, `
INSERT INTO klyne_llm_usage (
    project_path, day, operation, model,
    input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
    duration_ms, num_turns, session_id, status, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ProjectPath, u.Day, u.Operation, u.Model,
		u.InputTokens, u.OutputTokens, u.CacheReadTokens, u.CacheWriteTokens,
		u.DurationMs, u.NumTurns, u.SessionID, u.Status, u.CreatedAt)
	if err != nil {
		return fmt.Errorf("store: insert klyne llm usage: %w", err)
	}
	return nil
}

// KlyneLLMUsageOpBreakdown is one row in the per-operation breakdown
// returned by AggregateKlyneLLMUsageForDay. Runs is the number of
// subprocess invocations that day for the operation.
type KlyneLLMUsageOpBreakdown struct {
	Operation        string `json:"operation"`
	Runs             int    `json:"runs"`
	InputTokens      int64  `json:"input_tokens"`
	OutputTokens     int64  `json:"output_tokens"`
	CacheReadTokens  int64  `json:"cache_read_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
	TotalTokens      int64  `json:"total_tokens"`
	DurationMs       int64  `json:"duration_ms"`
}

// KlyneLLMUsageDay is the day-aggregated view: totals + per-operation
// breakdown. Built by AggregateKlyneLLMUsageForDay; total_tokens is the
// sum across all four token kinds so the UI can show one canonical
// figure.
type KlyneLLMUsageDay struct {
	Day              string                     `json:"day"`
	Runs             int                        `json:"runs"`
	InputTokens      int64                      `json:"input_tokens"`
	OutputTokens     int64                      `json:"output_tokens"`
	CacheReadTokens  int64                      `json:"cache_read_tokens"`
	CacheWriteTokens int64                      `json:"cache_write_tokens"`
	TotalTokens      int64                      `json:"total_tokens"`
	DurationMs       int64                      `json:"duration_ms"`
	ByOperation      []KlyneLLMUsageOpBreakdown `json:"by_operation"`
}

// AggregateKlyneLLMUsageForDay returns the day's totals plus a per-
// operation breakdown ordered by total_tokens DESC. Returns a zero-value
// (Runs=0, all token fields 0, empty ByOperation slice) when no rows
// exist for the day — callers can render the tile with 0% share without
// branching on found/not-found.
func AggregateKlyneLLMUsageForDay(
	ctx context.Context, db *DB, day string,
) (KlyneLLMUsageDay, error) {
	out := KlyneLLMUsageDay{Day: day, ByOperation: []KlyneLLMUsageOpBreakdown{}}
	if day == "" {
		return out, errors.New("store: aggregate klyne llm usage: day required")
	}
	const q = `
SELECT operation,
       COUNT(*)                         AS runs,
       COALESCE(SUM(input_tokens), 0)        AS input_tokens,
       COALESCE(SUM(output_tokens), 0)       AS output_tokens,
       COALESCE(SUM(cache_read_tokens), 0)   AS cache_read_tokens,
       COALESCE(SUM(cache_write_tokens), 0)  AS cache_write_tokens,
       COALESCE(SUM(duration_ms), 0)         AS duration_ms
FROM klyne_llm_usage
WHERE day = ?
GROUP BY operation
ORDER BY (COALESCE(SUM(input_tokens),0) + COALESCE(SUM(output_tokens),0) +
          COALESCE(SUM(cache_read_tokens),0) + COALESCE(SUM(cache_write_tokens),0)) DESC,
         operation ASC`
	rows, err := db.Read().QueryContext(ctx, q, day)
	if err != nil {
		return out, fmt.Errorf("store: aggregate klyne llm usage: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var b KlyneLLMUsageOpBreakdown
		if err := rows.Scan(&b.Operation, &b.Runs,
			&b.InputTokens, &b.OutputTokens,
			&b.CacheReadTokens, &b.CacheWriteTokens, &b.DurationMs); err != nil {
			return out, fmt.Errorf("store: scan klyne llm usage row: %w", err)
		}
		b.TotalTokens = b.InputTokens + b.OutputTokens + b.CacheReadTokens + b.CacheWriteTokens
		out.ByOperation = append(out.ByOperation, b)
		out.Runs += b.Runs
		out.InputTokens += b.InputTokens
		out.OutputTokens += b.OutputTokens
		out.CacheReadTokens += b.CacheReadTokens
		out.CacheWriteTokens += b.CacheWriteTokens
		out.DurationMs += b.DurationMs
	}
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("store: iterate klyne llm usage rows: %w", err)
	}
	out.TotalTokens = out.InputTokens + out.OutputTokens + out.CacheReadTokens + out.CacheWriteTokens
	return out, nil
}

// DailyUserTokenTotals is the user's full-day Claude usage rolled up
// from messages.ts (epoch-ms) into the local-day bucket. It is the
// DENOMINATOR for the "Klyne is X% of today's Claude usage" tile —
// includes klyne's own subprocess usage (because the daemon ingests
// every claude session) so share = klyne / total naturally answers
// "how much of my Claude use is klyne".
type DailyUserTokenTotals struct {
	Day              string `json:"day"`
	InputTokens      int64  `json:"input_tokens"`
	OutputTokens     int64  `json:"output_tokens"`
	CacheReadTokens  int64  `json:"cache_read_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
	TotalTokens      int64  `json:"total_tokens"`
	MessageCount     int64  `json:"message_count"`
}

// GetDailyUserTokenTotals sums every message landed in [startMs, endMs)
// across all sessions. startMs/endMs are epoch-ms covering the LOCAL
// calendar day — the API handler computes them via
// time.ParseInLocation("2006-01-02", day, time.Local).
//
// Returns zeroes (not an error) when no messages exist for the window,
// so callers can render share=0 without a branch.
func GetDailyUserTokenTotals(
	ctx context.Context, db *DB, day string, startMs, endMs int64,
) (DailyUserTokenTotals, error) {
	out := DailyUserTokenTotals{Day: day}
	if day == "" {
		return out, errors.New("store: daily user token totals: day required")
	}
	if endMs <= startMs {
		return out, errors.New("store: daily user token totals: endMs must be > startMs")
	}
	const q = `
SELECT COALESCE(SUM(tokens_in), 0),
       COALESCE(SUM(tokens_out), 0),
       COALESCE(SUM(cached_read_tokens), 0),
       COALESCE(SUM(cached_write_tokens), 0),
       COUNT(*)
FROM messages
WHERE ts >= ? AND ts < ?`
	err := db.Read().QueryRowContext(ctx, q, startMs, endMs).Scan(
		&out.InputTokens, &out.OutputTokens,
		&out.CacheReadTokens, &out.CacheWriteTokens,
		&out.MessageCount)
	if err != nil {
		return out, fmt.Errorf("store: daily user token totals: %w", err)
	}
	out.TotalTokens = out.InputTokens + out.OutputTokens + out.CacheReadTokens + out.CacheWriteTokens
	return out, nil
}

// Package usagestats computes tokscale-inspired aggregates over klyne's
// local message store: per-day token + cost rollups, model-by-cost
// breakdown, activity heatmap (GitHub-style), streaks, peak hour, and
// favorite-model derivation.
//
// All inputs come from the existing SQLite store. Deterministic, no AI
// calls, no network — fits klyne's local-first contract.
//
// Inspired by junhoyeo/tokscale's Overview / Daily / Stats tabs.
package usagestats

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"

	"github.com/klyne-ai/klyne/internal/cost"
	"github.com/klyne-ai/klyne/internal/store"
)

// Filter scopes the aggregation.
type Filter struct {
	// CLI restricts to "claude" or "codex". Empty includes both.
	CLI string
	// SinceMs is an epoch-ms lower bound; 0 includes all time.
	SinceMs int64
	// Days, when > 0, sets SinceMs to N*24h ago. Convenience field.
	Days int
}

// effectiveSince returns the inclusive lower bound used by all queries.
func (f Filter) effectiveSince() int64 {
	if f.SinceMs > 0 {
		return f.SinceMs
	}
	if f.Days > 0 {
		return time.Now().Add(-time.Duration(f.Days) * 24 * time.Hour).UnixMilli()
	}
	return 0
}

// DailyRow is one calendar day's aggregate.
type DailyRow struct {
	// Date is the UTC calendar day (YYYY-MM-DD).
	Date       string  `json:"date"`
	DayStartMs int64   `json:"day_start_ms"`
	Input      int64   `json:"input"`
	Output     int64   `json:"output"`
	CacheRead  int64   `json:"cache_read"`
	CacheWrite int64   `json:"cache_write"`
	Total      int64   `json:"total"`
	CostUSD    float64 `json:"cost_usd"`
	Messages   int64   `json:"messages"`
}

// ModelRow is one model's per-message rollup.
type ModelRow struct {
	Model       string  `json:"model"`
	Input       int64   `json:"input"`
	Output      int64   `json:"output"`
	CacheRead   int64   `json:"cache_read"`
	CacheWrite  int64   `json:"cache_write"`
	Total       int64   `json:"total"`
	CostUSD     float64 `json:"cost_usd"`
	Sessions    int     `json:"sessions"`
	SharePct    float64 `json:"share_pct"`
}

// HeatmapCell is one date in the activity heatmap. Intensity buckets
// follow GitHub's "less / more" scale: 0 (none) → 4 (max).
type HeatmapCell struct {
	Date      string `json:"date"`
	Weekday   int    `json:"weekday"` // 0 = Sunday, 6 = Saturday
	Messages  int64  `json:"messages"`
	Intensity int    `json:"intensity"`
}

// Stats is the cross-session digest computed by Compute.
type Stats struct {
	From            string         `json:"from"`
	To              string         `json:"to"`
	TotalMessages   int64          `json:"total_messages"`
	TotalSessions   int            `json:"total_sessions"`
	TotalInput      int64          `json:"total_input"`
	TotalOutput     int64          `json:"total_output"`
	TotalCacheRead  int64          `json:"total_cache_read"`
	TotalCacheWrite int64          `json:"total_cache_write"`
	TotalCostUSD    float64        `json:"total_cost_usd"`
	FavoriteModel   string         `json:"favorite_model"`
	PeakHour        int            `json:"peak_hour"`     // 0..23, local time
	PeakHourLocal   string         `json:"peak_hour_local"`
	CurrentStreak   int            `json:"current_streak"`
	LongestStreak   int            `json:"longest_streak"`
	ActiveDays      int            `json:"active_days"`
	WindowDays      int            `json:"window_days"`
	Daily           []DailyRow     `json:"daily"`
	Models          []ModelRow     `json:"models"`
	Heatmap         []HeatmapCell  `json:"heatmap,omitempty"`
}

// Compute runs the engine-aware rollups for f and returns a complete
// Stats. heatmapWeeks controls how many weeks back the heatmap covers
// (typical: 12 for a 3-month view; 52 for a full year). Pass 0 to skip
// the heatmap computation.
func Compute(ctx context.Context, db *store.DB, engine *cost.Engine, f Filter, heatmapWeeks int) (*Stats, error) {
	sinceMs := f.effectiveSince()

	out := &Stats{}
	if sinceMs == 0 {
		out.From = "all-time"
	} else {
		out.From = time.UnixMilli(sinceMs).UTC().Format("2006-01-02")
	}
	out.To = time.Now().UTC().Format("2006-01-02")

	// 1) Per-day rollup --------------------------------------------------
	daily, err := computeDaily(ctx, db, engine, f.CLI, sinceMs)
	if err != nil {
		return nil, err
	}
	out.Daily = daily
	for _, d := range daily {
		out.TotalInput += d.Input
		out.TotalOutput += d.Output
		out.TotalCacheRead += d.CacheRead
		out.TotalCacheWrite += d.CacheWrite
		out.TotalCostUSD += d.CostUSD
		out.TotalMessages += d.Messages
	}
	out.ActiveDays = len(daily)
	out.WindowDays = max(out.ActiveDays, 1)

	// 2) Per-model rollup ------------------------------------------------
	models, totalSessions, err := computeModels(ctx, db, engine, f.CLI, sinceMs, out.TotalCostUSD)
	if err != nil {
		return nil, err
	}
	out.Models = models
	out.TotalSessions = totalSessions
	if len(models) > 0 {
		out.FavoriteModel = models[0].Model // already sorted by cost DESC
	}

	// 3) Peak hour (local TZ) -------------------------------------------
	out.PeakHour, out.PeakHourLocal, err = computePeakHour(ctx, db, f.CLI, sinceMs)
	if err != nil {
		return nil, err
	}

	// 4) Streaks --------------------------------------------------------
	out.CurrentStreak, out.LongestStreak = computeStreaks(daily)

	// 5) Heatmap --------------------------------------------------------
	if heatmapWeeks > 0 {
		out.Heatmap = computeHeatmap(daily, heatmapWeeks)
	}

	return out, nil
}

// computeDaily aggregates messages by UTC calendar day.
func computeDaily(ctx context.Context, db *store.DB, engine *cost.Engine, cliFilter string, sinceMs int64) ([]DailyRow, error) {
	q := `SELECT m.ts, COALESCE(m.tokens_in,0), COALESCE(m.tokens_out,0),
	             COALESCE(m.cached_read_tokens,0), COALESCE(m.cached_write_tokens,0),
	             COALESCE(m.model,'')
	      FROM messages m JOIN sessions s ON s.id = m.session_id
	      WHERE m.ts >= ?`
	args := []any{sinceMs}
	if cliFilter != "" {
		q += " AND s.cli = ?"
		args = append(args, cliFilter)
	}
	rows, err := db.Read().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("usagestats: daily query: %w", err)
	}
	defer rows.Close()
	return scanDaily(rows, engine)
}

func scanDaily(rows *sql.Rows, engine *cost.Engine) ([]DailyRow, error) {
	byDay := map[string]*DailyRow{}
	for rows.Next() {
		var ts, in, outTok, cr, cw int64
		var model string
		if err := rows.Scan(&ts, &in, &outTok, &cr, &cw, &model); err != nil {
			return nil, fmt.Errorf("usagestats: scan: %w", err)
		}
		date := time.UnixMilli(ts).UTC().Format("2006-01-02")
		d, ok := byDay[date]
		if !ok {
			t := time.UnixMilli(ts).UTC()
			dayStart := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
			d = &DailyRow{Date: date, DayStartMs: dayStart.UnixMilli()}
			byDay[date] = d
		}
		d.Input += in
		d.Output += outTok
		d.CacheRead += cr
		d.CacheWrite += cw
		d.Total += in + outTok
		d.Messages++
		if engine != nil && model != "" {
			d.CostUSD += engine.Cost(in, outTok, cr, cw, model)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("usagestats: iter: %w", err)
	}
	out := make([]DailyRow, 0, len(byDay))
	for _, v := range byDay {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date > out[j].Date }) // newest first
	return out, nil
}

func computeModels(ctx context.Context, db *store.DB, engine *cost.Engine, cliFilter string, sinceMs int64, total float64) ([]ModelRow, int, error) {
	q := `SELECT m.session_id, COALESCE(m.tokens_in,0), COALESCE(m.tokens_out,0),
	             COALESCE(m.cached_read_tokens,0), COALESCE(m.cached_write_tokens,0),
	             COALESCE(m.model,'')
	      FROM messages m JOIN sessions s ON s.id = m.session_id
	      WHERE m.ts >= ?`
	args := []any{sinceMs}
	if cliFilter != "" {
		q += " AND s.cli = ?"
		args = append(args, cliFilter)
	}
	rows, err := db.Read().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("usagestats: models query: %w", err)
	}
	defer rows.Close()

	type modelAgg struct {
		ModelRow
		sessionSet map[string]struct{}
	}
	byModel := map[string]*modelAgg{}
	allSessions := map[string]struct{}{}
	for rows.Next() {
		var sid, model string
		var in, outTok, cr, cw int64
		if err := rows.Scan(&sid, &in, &outTok, &cr, &cw, &model); err != nil {
			return nil, 0, fmt.Errorf("usagestats: scan: %w", err)
		}
		if model == "" {
			continue
		}
		allSessions[sid] = struct{}{}
		m, ok := byModel[model]
		if !ok {
			m = &modelAgg{
				ModelRow:   ModelRow{Model: model},
				sessionSet: map[string]struct{}{},
			}
			byModel[model] = m
		}
		m.Input += in
		m.Output += outTok
		m.CacheRead += cr
		m.CacheWrite += cw
		m.Total += in + outTok
		m.sessionSet[sid] = struct{}{}
		if engine != nil {
			m.CostUSD += engine.Cost(in, outTok, cr, cw, model)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("usagestats: iter: %w", err)
	}

	out := make([]ModelRow, 0, len(byModel))
	for _, v := range byModel {
		v.Sessions = len(v.sessionSet)
		if total > 0 {
			v.SharePct = v.CostUSD / total * 100
		}
		out = append(out, v.ModelRow)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CostUSD != out[j].CostUSD {
			return out[i].CostUSD > out[j].CostUSD
		}
		return out[i].Model < out[j].Model
	})
	return out, len(allSessions), nil
}

// computePeakHour returns the hour of day (0..23, local TZ) with the
// highest message count and a human-readable form like "14:00 (UTC+5:30)".
func computePeakHour(ctx context.Context, db *store.DB, cliFilter string, sinceMs int64) (int, string, error) {
	q := `SELECT m.ts FROM messages m JOIN sessions s ON s.id = m.session_id WHERE m.ts >= ?`
	args := []any{sinceMs}
	if cliFilter != "" {
		q += " AND s.cli = ?"
		args = append(args, cliFilter)
	}
	rows, err := db.Read().QueryContext(ctx, q, args...)
	if err != nil {
		return 0, "", fmt.Errorf("usagestats: peak-hour query: %w", err)
	}
	defer rows.Close()
	counts := [24]int{}
	loc := time.Local
	for rows.Next() {
		var ts int64
		if err := rows.Scan(&ts); err != nil {
			return 0, "", fmt.Errorf("usagestats: scan: %w", err)
		}
		h := time.UnixMilli(ts).In(loc).Hour()
		counts[h]++
	}
	if err := rows.Err(); err != nil {
		return 0, "", fmt.Errorf("usagestats: iter: %w", err)
	}
	best := 0
	for i, c := range counts {
		if c > counts[best] {
			best = i
		}
	}
	if counts[best] == 0 {
		return 0, "", nil
	}
	_, offsetSec := time.Now().In(loc).Zone()
	tzLabel := fmt.Sprintf("UTC%+03d:%02d", offsetSec/3600, (abs(offsetSec)%3600)/60)
	return best, fmt.Sprintf("%02d:00 (%s)", best, tzLabel), nil
}

// computeStreaks counts consecutive UTC days with at least one message.
// Returns (current, longest). "current" is the active run ending today
// (or yesterday). "longest" is the maximum run length ever observed.
func computeStreaks(daily []DailyRow) (current, longest int) {
	if len(daily) == 0 {
		return 0, 0
	}
	// daily is newest-first by date string. Convert to a set keyed by date.
	have := make(map[string]bool, len(daily))
	for _, d := range daily {
		have[d.Date] = true
	}
	today := time.Now().UTC()
	cursor := today
	// Allow the streak to end "today or yesterday" so an active streak
	// is not broken by the user having not yet logged in today.
	if !have[cursor.Format("2006-01-02")] {
		cursor = cursor.AddDate(0, 0, -1)
	}
	for have[cursor.Format("2006-01-02")] {
		current++
		cursor = cursor.AddDate(0, 0, -1)
	}

	// Longest streak: walk every recorded day and look forward.
	dates := make([]string, 0, len(daily))
	for k := range have {
		dates = append(dates, k)
	}
	sort.Strings(dates) // ascending
	run := 0
	var prev time.Time
	for i, d := range dates {
		t, _ := time.Parse("2006-01-02", d)
		if i == 0 || t.Sub(prev) > 24*time.Hour+time.Minute {
			// Not consecutive — restart.
			run = 1
		} else {
			run++
		}
		if run > longest {
			longest = run
		}
		prev = t
	}
	return current, longest
}

// computeHeatmap builds GitHub-style cells for the last `weeks` weeks.
// Each cell carries a message count and a 0..4 intensity bucket derived
// from quantiles of non-zero days.
func computeHeatmap(daily []DailyRow, weeks int) []HeatmapCell {
	if weeks <= 0 {
		return nil
	}
	now := time.Now().UTC()
	// Quantile thresholds for intensity bucket. Compute from non-zero days.
	nonzero := make([]int64, 0, len(daily))
	for _, d := range daily {
		if d.Messages > 0 {
			nonzero = append(nonzero, d.Messages)
		}
	}
	sort.Slice(nonzero, func(i, j int) bool { return nonzero[i] < nonzero[j] })
	q := func(p float64) int64 {
		if len(nonzero) == 0 {
			return 0
		}
		idx := int(float64(len(nonzero)-1) * p)
		return nonzero[idx]
	}
	thresholds := [3]int64{q(0.25), q(0.50), q(0.85)}

	have := map[string]int64{}
	for _, d := range daily {
		have[d.Date] = d.Messages
	}

	totalDays := weeks * 7
	out := make([]HeatmapCell, 0, totalDays)
	for i := totalDays - 1; i >= 0; i-- {
		day := now.AddDate(0, 0, -i)
		date := day.Format("2006-01-02")
		msgs := have[date]
		cell := HeatmapCell{
			Date:     date,
			Weekday:  int(day.Weekday()),
			Messages: msgs,
		}
		switch {
		case msgs == 0:
			cell.Intensity = 0
		case msgs <= thresholds[0]:
			cell.Intensity = 1
		case msgs <= thresholds[1]:
			cell.Intensity = 2
		case msgs <= thresholds[2]:
			cell.Intensity = 3
		default:
			cell.Intensity = 4
		}
		out = append(out, cell)
	}
	return out
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

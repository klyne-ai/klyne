package usagestats

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/cost"
	"github.com/klyne-ai/klyne/internal/store"
)

func openDB(t *testing.T) *store.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "stats.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func newCostEngine(t *testing.T) *cost.Engine {
	t.Helper()
	e, err := cost.New(config.Defaults())
	if err != nil {
		t.Fatalf("cost engine: %v", err)
	}
	return e
}

func seedSession(t *testing.T, db *store.DB, id, cli, project string) {
	t.Helper()
	s := &connectors.Session{
		ID: id, CLI: connectors.CLI(cli), ProjectPath: project,
		StartedAt: 1, LastMsgAt: 1, Status: connectors.SessionStatusActive,
		Model: "claude-sonnet-4-5", RawPath: "/tmp/" + id,
	}
	if err := store.UpsertSession(context.Background(), db, s); err != nil {
		t.Fatalf("upsert: %v", err)
	}
}

var msgSeq int64

func seedMsg(t *testing.T, db *store.DB, sid string, ts int64, model string, tIn, tOut, cr int64) {
	t.Helper()
	msgSeq++
	m := &connectors.Message{
		ID:               sid + "-" + time.UnixMilli(ts).Format("150405") + "-" + strconvI64(msgSeq),
		SessionID:        sid,
		CLI:              connectors.CLIClaude,
		Role:             connectors.RoleAssistant,
		TokensIn:         tIn,
		TokensOut:        tOut,
		CachedReadTokens: cr,
		Model:            model,
		Ts:               ts,
	}
	if err := store.InsertMessage(context.Background(), db, m); err != nil {
		t.Fatalf("insert: %v", err)
	}
}

func strconvI64(n int64) string {
	return strconv.FormatInt(n, 10)
}

func dayMs(y, mo, d int) int64 {
	return time.Date(y, time.Month(mo), d, 12, 0, 0, 0, time.UTC).UnixMilli()
}

func TestComputeDaily_AggregatesByUtcDay(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	engine := newCostEngine(t)
	seedSession(t, db, "s1", "claude", "/proj")

	// Two messages on May 1, one on May 2.
	seedMsg(t, db, "s1", dayMs(2026, 5, 1)+1, "claude-sonnet-4-5", 1000, 50, 200)
	seedMsg(t, db, "s1", dayMs(2026, 5, 1)+2, "claude-sonnet-4-5", 2000, 100, 1500)
	seedMsg(t, db, "s1", dayMs(2026, 5, 2), "claude-sonnet-4-5", 500, 25, 100)

	rows, err := computeDaily(ctx, db, engine, "", 0)
	if err != nil {
		t.Fatalf("daily: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 day rows, got %d (%+v)", len(rows), rows)
	}
	// Newest first.
	if rows[0].Date != "2026-05-02" {
		t.Errorf("first row date = %q, want 2026-05-02", rows[0].Date)
	}
	if rows[1].Input != 3000 || rows[1].Output != 150 {
		t.Errorf("May 1 row aggregation wrong: %+v", rows[1])
	}
	if rows[1].Messages != 2 {
		t.Errorf("May 1 message count = %d, want 2", rows[1].Messages)
	}
}

func TestComputeModels_SortedByCostDesc(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	engine := newCostEngine(t)
	seedSession(t, db, "sA", "claude", "/p")
	seedSession(t, db, "sB", "claude", "/p")
	// claude-sonnet-4-5 (cheaper) vs claude-opus-4-7 (pricier).
	seedMsg(t, db, "sA", dayMs(2026, 5, 1), "claude-sonnet-4-5", 100_000, 1000, 50_000)
	seedMsg(t, db, "sB", dayMs(2026, 5, 1)+1, "claude-opus-4-7", 100_000, 1000, 50_000)

	models, sessions, err := computeModels(ctx, db, engine, "", 0, 0)
	if err != nil {
		t.Fatalf("models: %v", err)
	}
	if len(models) < 2 {
		t.Fatalf("want 2 models, got %+v", models)
	}
	if models[0].Model != "claude-opus-4-7" {
		t.Errorf("expected opus first (more expensive), got %s", models[0].Model)
	}
	if sessions != 2 {
		t.Errorf("session count = %d, want 2", sessions)
	}
}

func TestComputeStreaks_ConsecutiveAndLongest(t *testing.T) {
	today := time.Now().UTC()
	// Build daily rows: today, yesterday, day-before, then gap, then 4-day run.
	rows := []DailyRow{
		{Date: today.Format("2006-01-02"), Messages: 1},
		{Date: today.AddDate(0, 0, -1).Format("2006-01-02"), Messages: 1},
		{Date: today.AddDate(0, 0, -2).Format("2006-01-02"), Messages: 1},
		{Date: today.AddDate(0, 0, -10).Format("2006-01-02"), Messages: 1},
		{Date: today.AddDate(0, 0, -11).Format("2006-01-02"), Messages: 1},
		{Date: today.AddDate(0, 0, -12).Format("2006-01-02"), Messages: 1},
		{Date: today.AddDate(0, 0, -13).Format("2006-01-02"), Messages: 1},
	}
	cur, longest := computeStreaks(rows)
	if cur != 3 {
		t.Errorf("current streak = %d, want 3", cur)
	}
	if longest != 4 {
		t.Errorf("longest streak = %d, want 4", longest)
	}
}

func TestComputeStreaks_AllowsYesterdayEnd(t *testing.T) {
	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	dayBefore := time.Now().UTC().AddDate(0, 0, -2).Format("2006-01-02")
	rows := []DailyRow{{Date: yesterday, Messages: 1}, {Date: dayBefore, Messages: 1}}
	cur, _ := computeStreaks(rows)
	if cur != 2 {
		t.Errorf("current streak = %d, want 2 (ending yesterday is allowed)", cur)
	}
}

func TestComputeHeatmap_BucketsByQuantile(t *testing.T) {
	today := time.Now().UTC()
	rows := []DailyRow{
		{Date: today.Format("2006-01-02"), Messages: 100},                 // top quartile → 4
		{Date: today.AddDate(0, 0, -1).Format("2006-01-02"), Messages: 50},
		{Date: today.AddDate(0, 0, -2).Format("2006-01-02"), Messages: 10},
		{Date: today.AddDate(0, 0, -3).Format("2006-01-02"), Messages: 5},
	}
	cells := computeHeatmap(rows, 2) // 2 weeks = 14 cells
	if len(cells) != 14 {
		t.Fatalf("want 14 cells, got %d", len(cells))
	}
	// Last cell is today.
	last := cells[len(cells)-1]
	if last.Date != today.Format("2006-01-02") {
		t.Errorf("last cell = %s, want %s", last.Date, today.Format("2006-01-02"))
	}
	if last.Intensity == 0 {
		t.Errorf("today has messages — intensity should be > 0, got %d", last.Intensity)
	}
	// At least one empty day in the 14-day window (we only seeded 4 days).
	empty := 0
	for _, c := range cells {
		if c.Intensity == 0 {
			empty++
		}
	}
	if empty == 0 {
		t.Errorf("expected at least one empty cell in 14-day window")
	}
}

func TestCompute_EndToEnd(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	engine := newCostEngine(t)
	seedSession(t, db, "s1", "claude", "/p")
	// 2 messages today, 1 message 3 days ago.
	now := time.Now().UTC()
	seedMsg(t, db, "s1", now.UnixMilli(), "claude-sonnet-4-5", 1000, 50, 200)
	seedMsg(t, db, "s1", now.UnixMilli()+1, "claude-sonnet-4-5", 500, 25, 100)
	seedMsg(t, db, "s1", now.AddDate(0, 0, -3).UnixMilli(), "claude-sonnet-4-5", 200, 10, 50)

	stats, err := Compute(ctx, db, engine, Filter{Days: 30}, 4)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if stats.TotalMessages != 3 {
		t.Errorf("total messages = %d, want 3", stats.TotalMessages)
	}
	if stats.TotalSessions != 1 {
		t.Errorf("total sessions = %d, want 1", stats.TotalSessions)
	}
	if stats.ActiveDays != 2 {
		t.Errorf("active days = %d, want 2 (today + 3 days ago)", stats.ActiveDays)
	}
	if stats.FavoriteModel != "claude-sonnet-4-5" {
		t.Errorf("favorite model = %q, want claude-sonnet-4-5", stats.FavoriteModel)
	}
	if len(stats.Daily) != 2 {
		t.Errorf("daily rows = %d, want 2", len(stats.Daily))
	}
	if len(stats.Heatmap) != 28 {
		t.Errorf("heatmap cells = %d, want 28 (4 weeks)", len(stats.Heatmap))
	}
}

func TestCompute_CLIFilter(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	engine := newCostEngine(t)
	seedSession(t, db, "claude-s", "claude", "/p")
	seedSession(t, db, "codex-s", "codex", "/p")
	now := time.Now().UTC().UnixMilli()
	seedMsg(t, db, "claude-s", now, "claude-sonnet-4-5", 100, 10, 50)
	seedMsg(t, db, "codex-s", now+1, "gpt-5", 200, 20, 100)

	claudeOnly, err := Compute(ctx, db, engine, Filter{CLI: "claude", Days: 1}, 0)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if claudeOnly.TotalMessages != 1 {
		t.Errorf("claude only count = %d, want 1", claudeOnly.TotalMessages)
	}
}

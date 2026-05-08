package usage

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// jsonlWithFutureResets builds a snapshot file whose `resets_at` values
// are well in the future relative to wall-clock time, so the
// stale-window decay path doesn't fire and tests assert the raw
// percentages we wrote.
func jsonlWithFutureResets() string {
	now := time.Now()
	fiveH := now.Add(2 * time.Hour).Unix()
	sevenD := now.Add(5 * 24 * time.Hour).Unix()
	return fmt.Sprintf(`
{"type":"event_msg","timestamp":"2026-05-06T17:48:20.282Z","payload":{"type":"token_count","rate_limits":{"limit_id":"codex","plan_type":"plus","primary":{"used_percent":9.0,"window_minutes":300,"resets_at":%d},"secondary":{"used_percent":19.0,"window_minutes":10080,"resets_at":%d}}}}
{"type":"event_msg","timestamp":"2026-05-06T18:48:20.282Z","payload":{"type":"token_count","rate_limits":{"limit_id":"codex","plan_type":"team","primary":{"used_percent":42.0,"window_minutes":300,"resets_at":%d},"secondary":{"used_percent":28.0,"window_minutes":10080,"resets_at":%d}}}}
{"type":"event_msg","timestamp":"2026-05-06T19:00:00.000Z","payload":{"type":"agent_message","content":"hello"}}
`, fiveH-3600, sevenD-3600, fiveH, sevenD)
}

// writeRolloutFile creates a rollout-*.jsonl under the year/month/day
// nesting Codex uses.
func writeRolloutFile(t *testing.T, root string, content string, mtime time.Time) string {
	t.Helper()
	dir := filepath.Join(root, "2026", "05", "06")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Use a unique suffix per call so multiple files coexist.
	name := "rollout-" + mtime.Format("2006-01-02T15-04-05") + ".jsonl"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	return path
}

func TestCodexSnapshot_LatestSnapshotWins(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeRolloutFile(t, root, jsonlWithFutureResets(), time.Now())

	r := NewCodexSnapshotReader(root)
	got, err := r.Latest()
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if got == nil {
		t.Fatal("Latest: got nil OAuthUsage")
	}
	if got.SubscriptionType != "team" {
		t.Errorf("SubscriptionType = %q, want %q (latest event wins)", got.SubscriptionType, "team")
	}
	if got.FiveHour == nil || got.FiveHour.UtilizationPct != 42.0 {
		t.Errorf("FiveHour = %+v, want 42.0", got.FiveHour)
	}
	if got.FiveHour.ResetsAt <= 0 {
		t.Errorf("FiveHour.ResetsAt should be a future epoch-ms, got %d", got.FiveHour.ResetsAt)
	}
	if got.SevenDay == nil || got.SevenDay.UtilizationPct != 28.0 {
		t.Errorf("SevenDay = %+v, want 28.0", got.SevenDay)
	}
	if got.SevenDaySonnet != nil {
		t.Errorf("SevenDaySonnet should be nil for Codex, got %+v", got.SevenDaySonnet)
	}
}

func TestCodexSnapshot_NewestFileWins(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	older := time.Now().Add(-2 * time.Hour)
	newer := time.Now().Add(-1 * time.Hour)

	// Older session has 9% / 19%; newer has 42% / 28%.
	writeRolloutFile(t, root,
		`{"type":"event_msg","payload":{"type":"token_count","rate_limits":{"plan_type":"plus","primary":{"used_percent":9.0,"window_minutes":300,"resets_at":1778103417},"secondary":{"used_percent":19.0,"window_minutes":10080,"resets_at":1778595093}}}}`+"\n",
		older)
	writeRolloutFile(t, root,
		`{"type":"event_msg","payload":{"type":"token_count","rate_limits":{"plan_type":"team","primary":{"used_percent":42.0,"window_minutes":300,"resets_at":1778103500},"secondary":{"used_percent":28.0,"window_minutes":10080,"resets_at":1778595200}}}}`+"\n",
		newer)

	r := NewCodexSnapshotReader(root)
	got, err := r.Latest()
	if err != nil || got == nil {
		t.Fatalf("Latest: %+v / err=%v", got, err)
	}
	if got.SubscriptionType != "team" {
		t.Errorf("expected newer file to win (team plan), got %q", got.SubscriptionType)
	}
}

func TestCodexSnapshot_NoSessionsReturnsNil(t *testing.T) {
	t.Parallel()
	root := t.TempDir() // empty
	r := NewCodexSnapshotReader(root)
	got, err := r.Latest()
	if err != nil {
		t.Errorf("expected no error for empty dir, got %v", err)
	}
	if got != nil {
		t.Errorf("expected nil OAuthUsage, got %+v", got)
	}
}

func TestCodexSnapshot_MissingRootReturnsNil(t *testing.T) {
	t.Parallel()
	r := NewCodexSnapshotReader(filepath.Join(t.TempDir(), "definitely-not-here"))
	got, err := r.Latest()
	if err != nil || got != nil {
		t.Errorf("expected (nil, nil) for missing root, got (%+v, %v)", got, err)
	}
}

func TestCodexSnapshot_FileWithoutRateLimitsSkipped(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	// File has no token_count events at all.
	writeRolloutFile(t, root,
		`{"type":"event_msg","payload":{"type":"agent_message","content":"hi"}}`+"\n",
		time.Now())
	r := NewCodexSnapshotReader(root)
	got, err := r.Latest()
	if err != nil || got != nil {
		t.Errorf("expected (nil, nil) when no rate-limits events, got (%+v, %v)", got, err)
	}
}

func TestCodexSnapshot_StaleWindowDecaysToZero(t *testing.T) {
	t.Parallel()
	// Snapshot says 5h window resets at 2026-01-01; "now" is 2026-05-07.
	// The window has long since rolled over — we should report 0%.
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	src := &codexRateLimitRaw{
		PlanType: "team",
		Primary: &codexRateLimitWindowRaw{
			UsedPercent:   69.0,
			WindowMinutes: 300,
			ResetsAt:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Unix(),
		},
		Secondary: &codexRateLimitWindowRaw{
			UsedPercent:   28.0,
			WindowMinutes: 10080,
			ResetsAt:      time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC).Unix(),
		},
	}
	got := convertCodexSnapshotAt(src, now)
	if got.FiveHour == nil {
		t.Fatal("FiveHour should not be nil")
	}
	if got.FiveHour.UtilizationPct != 0 {
		t.Errorf("expired 5h window should decay to 0%%, got %v", got.FiveHour.UtilizationPct)
	}
	if got.FiveHour.ResetsAt != 0 {
		t.Errorf("expired window should clear ResetsAt, got %d", got.FiveHour.ResetsAt)
	}
	// 7d window is still valid — should pass through unchanged.
	if got.SevenDay == nil || got.SevenDay.UtilizationPct != 28.0 {
		t.Errorf("valid 7d window should pass through, got %+v", got.SevenDay)
	}
}

func TestCodexSnapshot_CachesAcrossCalls(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeRolloutFile(t, root, jsonlWithFutureResets(), time.Now())

	r := NewCodexSnapshotReader(root)
	first, err := r.Latest()
	if err != nil || first == nil {
		t.Fatalf("first Latest: %+v / %v", first, err)
	}

	// Append a different snapshot to the file. If the cache is honoured,
	// the second call returns the OLD value. If not, it picks up the new one.
	f, err := os.OpenFile(filepath.Join(root, "2026", "05", "06",
		"rollout-"+time.Now().Format("2006-01-02T15-04-05")+".jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err == nil {
		_, _ = f.WriteString(`{"type":"event_msg","payload":{"type":"token_count","rate_limits":{"plan_type":"team","primary":{"used_percent":99.0,"window_minutes":300,"resets_at":1}}}}` + "\n")
		_ = f.Close()
	}

	second, err := r.Latest()
	if err != nil || second == nil {
		t.Fatalf("second Latest: %+v / %v", second, err)
	}
	if second.FiveHour.UtilizationPct == 99.0 {
		t.Errorf("expected cached snapshot, but got fresh value (%v)", second.FiveHour.UtilizationPct)
	}
}

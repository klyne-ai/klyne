package bench

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/app"
	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/store"
)

// TestIdleRAM measures the process RSS after constructing an App (but before
// Start) and idling for 30 seconds. It reads runtime.MemStats as a proxy for
// "heap + stack" RAM.
//
// Spec §12 budget: idle RAM < 40 MB.
//
// Proxy note: We use 30 s idle rather than 1 h because:
//  1. CI runners have a 10-minute job limit.
//  2. The spec allows shorter proxies when documented.
//
// The 1-hour idle test can be run manually:
//
//	KLYNE_IDLE_SECONDS=3600 go test -run TestIdleRAM ./internal/bench/...
//
// This test runs only on darwin and linux (OS-gated) where MemStats is
// representative. Windows is skipped because VirtualAlloc differs from
// RSS semantics used in the spec.
//
// This test also skips with -short.
func TestIdleRAM(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping idle RAM test in -short mode")
	}
	switch runtime.GOOS {
	case "darwin", "linux":
		// proceed
	default:
		t.Skipf("idle RAM test skipped on %s", runtime.GOOS)
	}

	cfg := minimalConfig(t)

	// Force GC before measuring baseline.
	runtime.GC()
	runtime.GC()

	var baseStats runtime.MemStats
	runtime.ReadMemStats(&baseStats)

	a, err := app.BuildOnly(cfg)
	if err != nil {
		t.Fatalf("app.BuildOnly: %v", err)
	}

	// Idle for a short period — 30 s is long enough to show memory growth
	// from background goroutines but short enough for CI.
	const idleSeconds = 30
	t.Logf("idling for %d seconds...", idleSeconds)
	time.Sleep(idleSeconds * time.Second)

	runtime.GC()
	runtime.GC()

	var afterStats runtime.MemStats
	runtime.ReadMemStats(&afterStats)

	_ = a // keep app alive during idle

	heapInUse := afterStats.HeapInuse
	stackSys := afterStats.StackSys
	totalMB := float64(heapInUse+stackSys) / (1024 * 1024)

	t.Logf("HeapInuse: %d bytes (%.1f MB)", heapInUse, float64(heapInUse)/(1024*1024))
	t.Logf("StackSys:  %d bytes (%.1f MB)", stackSys, float64(stackSys)/(1024*1024))
	t.Logf("Total (heap+stack): %.2f MB", totalMB)

	// Spec §12 budget: < 40 MB idle.
	const budgetMB = 40.0
	if totalMB >= budgetMB {
		t.Errorf("idle RAM %.2f MB exceeds budget %.0f MB", totalMB, budgetMB)
	}
}

// TestActiveRAM measures RAM with one live session and a 5K-message DB.
//
// Spec §12 budget: active RAM (1 session, 5K msg DB) < 80 MB.
//
// This test uses runtime.MemStats (heap + stack) as the RAM proxy. It skips
// on -short and on non-darwin/linux platforms.
func TestActiveRAM(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping active RAM test in -short mode")
	}
	switch runtime.GOOS {
	case "darwin", "linux":
		// proceed
	default:
		t.Skipf("active RAM test skipped on %s", runtime.GOOS)
	}

	tmpDir := t.TempDir()
	db := SeedStore(t, tmpDir, 5_000)
	t.Cleanup(func() { _ = db.Close() })

	cfg := minimalConfig(t)
	cfg.Paths.DB = tmpDir + "/bench.db"

	// Force GC before measuring.
	runtime.GC()
	runtime.GC()

	a, err := app.BuildOnly(cfg)
	if err != nil {
		t.Fatalf("app.BuildOnly: %v", err)
	}

	// Simulate "active": list messages for the seeded session.
	ctx := context.Background()
	_, err = store.ListMessagesBySession(ctx, db, "bench-session-0001", 100, 0)
	if err != nil {
		t.Fatalf("ListMessagesBySession: %v", err)
	}

	// Search to activate the FTS5 virtual table pages.
	_, err = store.Search(ctx, db, "hello world", 20, store.SearchSortRelevance)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	runtime.GC()
	runtime.GC()

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	_ = a

	totalMB := float64(ms.HeapInuse+ms.StackSys) / (1024 * 1024)

	t.Logf("HeapInuse: %d bytes (%.1f MB)", ms.HeapInuse, float64(ms.HeapInuse)/(1024*1024))
	t.Logf("StackSys:  %d bytes (%.1f MB)", ms.StackSys, float64(ms.StackSys)/(1024*1024))
	t.Logf("Total (heap+stack): %.2f MB", totalMB)

	// Spec §12 budget: < 80 MB with 1 active session + 5K msg DB.
	const budgetMB = 80.0
	if totalMB >= budgetMB {
		t.Errorf("active RAM %.2f MB exceeds budget %.0f MB", totalMB, budgetMB)
	}
}

// minimalConfig returns a *config.Config that points all connectors to
// non-existent paths (so no fsnotify watchers try to stat real dirs) and
// disables AI (no provider needed in benches).
func minimalConfig(t testing.TB) *config.Config {
	t.Helper()
	cfg := config.Defaults()
	cfg.Connectors.Claude.Enabled = false
	cfg.Connectors.Codex.Enabled = false
	cfg.AI.SummaryModel = config.AIModelOff
	cfg.AI.TitleModel = config.AIModelOff
	cfg.Paths.DB = t.TempDir() + "/ram.db"
	cfg.Paths.PricingOverride = ""
	cfg.Server.Addr = "127.0.0.1:0"
	return cfg
}

// makeSession builds a minimal *connectors.Session for test seeding.
func makeSession(sessionID string) *connectors.Session {
	return &connectors.Session{
		ID:          sessionID,
		CLI:         connectors.CLIClaude,
		ProjectPath: "/bench/project",
		StartedAt:   time.Now().UnixMilli(),
		LastMsgAt:   time.Now().UnixMilli(),
		Status:      connectors.SessionStatusActive,
		RawPath:     "/bench/.claude/sessions/bench.jsonl",
	}
}

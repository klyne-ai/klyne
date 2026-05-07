package bench

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestColdStart measures the time from process launch to the first HTTP 200
// from /healthz.
//
// Spec §12 budget: < 200 ms on a developer laptop.
// CI relaxation: We allow 2000 ms because GitHub Actions ubuntu-latest
// runners are slower than dev machines, processes have cold caches, and
// sqlite journal recovery may add latency. The test asserts < 2000 ms in CI;
// the docs/perf.md table documents both the laptop baseline and the CI bound.
//
// Methodology:
//   - Set HOME to a temp dir so the daemon writes config/db to an isolated
//     location, not ~/.agentdeck.
//   - Write a config.toml that binds to a specific port and disables
//     all connectors (no fsnotify watches) so the daemon starts in <200 ms.
//   - Poll /healthz every 10 ms from the moment the process is started.
//   - Record time-to-first-200 as the cold-start metric.
//
// This test is skipped:
//   - With -short flag (dev runs that avoid subprocess overhead).
//   - When bin/agentdeck does not exist (pre-build environments).
func TestColdStart(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping cold-start subprocess test in -short mode")
	}

	binPath := binaryPath(t)
	if _, err := os.Stat(binPath); os.IsNotExist(err) {
		t.Skip("bin/agentdeck not found; run 'make build' first")
	}

	// Create a temp home dir so the daemon doesn't touch ~/.agentdeck.
	fakeHome := t.TempDir()
	agentdeckDir := filepath.Join(fakeHome, ".agentdeck")
	if err := os.MkdirAll(agentdeckDir, 0o700); err != nil {
		t.Fatalf("mkdir .agentdeck: %v", err)
	}

	// Use a fixed port (unlikely to be in use on CI).
	const testPort = "17879"
	const testAddr = "127.0.0.1:" + testPort

	// Write a minimal config that binds to the test port and disables
	// all connectors so startup is as fast as possible.
	configTOML := fmt.Sprintf(`[server]
addr = "%s"

[paths]
db = "%s/agentdeck.db"

[connectors.claude]
enabled = false

[connectors.codex]
enabled = false

[ai]
summary_model = "off"
title_model = "off"
embed_model = "off"
`, testAddr, agentdeckDir)

	configPath := filepath.Join(agentdeckDir, "config.toml")
	if err := os.WriteFile(configPath, []byte(configTOML), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binPath, "start", "--no-open")
	// Override HOME so the daemon reads our config.toml.
	cmd.Env = append(os.Environ(), "HOME="+fakeHome)

	startTime := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()

	// Poll /healthz until we get a 200 or timeout.
	healthURL := "http://" + testAddr + "/healthz"
	var firstOKElapsed time.Duration
	const maxWait = 10 * time.Second
	const pollInterval = 10 * time.Millisecond

	deadline := time.Now().Add(maxWait)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	for time.Now().Before(deadline) {
		resp, err := client.Get(healthURL) //nolint:gosec,noctx
		if err == nil && resp.StatusCode == http.StatusOK {
			firstOKElapsed = time.Since(startTime)
			_ = resp.Body.Close()
			break
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		time.Sleep(pollInterval)
	}

	if firstOKElapsed == 0 {
		t.Fatalf("daemon did not become healthy within %v (port %s may be in use; try rerunning)",
			maxWait, testPort)
	}

	ms := firstOKElapsed.Milliseconds()
	t.Logf("cold-start to first /healthz 200: %d ms", ms)

	// Relaxed CI budget: 2000 ms.
	// Spec §12 documents 200 ms as the developer-laptop target; the CI
	// runner bound is 10x that to account for process spawn and sqlite WAL
	// recovery overhead.
	ciBudgetMs := int64(2000)
	if ms > ciBudgetMs {
		t.Errorf("cold-start %d ms exceeds CI budget %d ms (spec §12 target: 200 ms on dev laptop)",
			ms, ciBudgetMs)
	}
}

// binaryPath returns the absolute path to bin/agentdeck relative to this
// test file's location (two directories up from internal/bench/).
func binaryPath(t testing.TB) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// filename = .../internal/bench/cold_start_test.go
	// repo root = two dirs up
	repoRoot := filepath.Join(filepath.Dir(filename), "..", "..")
	return filepath.Join(repoRoot, "bin", "agentdeck")
}

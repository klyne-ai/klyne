package bench

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestBinarySize asserts that bin/klyne is under 25 MB.
//
// Spec §12 budget: binary size < 25 MB.
//
// The orchestrator confirmed the binary is ~15 MB as of the Wave 4 baseline.
// This test skips when the binary does not exist (pre-build environments)
// rather than failing — see CI workflow which runs make build first.
func TestBinarySize(t *testing.T) {
	path := repoBinaryPath(t)
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		t.Skipf("bin/klyne not found at %s; run 'make build' first", path)
	}
	if err != nil {
		t.Fatalf("stat binary: %v", err)
	}

	sizeBytes := info.Size()
	sizeMB := float64(sizeBytes) / (1024 * 1024)

	t.Logf("binary size: %.2f MB (%d bytes)", sizeMB, sizeBytes)

	const budgetMB = 25.0
	if sizeMB >= budgetMB {
		t.Errorf("binary size %.2f MB exceeds budget %.0f MB", sizeMB, budgetMB)
	}
}

// BenchmarkBinarySize is the benchmark-flavoured wrapper for scripts/bench.sh.
func BenchmarkBinarySize(b *testing.B) {
	path := repoBinaryPath(b)
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		b.Skipf("bin/klyne not found at %s; run 'make build' first", path)
	}
	if err != nil {
		b.Fatalf("stat binary: %v", err)
	}

	sizeBytes := info.Size()
	sizeMB := float64(sizeBytes) / (1024 * 1024)

	b.ReportMetric(sizeMB, "binary_MB")

	const budgetMB = 25.0
	if sizeMB >= budgetMB {
		b.Errorf("binary size %.2f MB exceeds budget %.0f MB", sizeMB, budgetMB)
	} else {
		b.Logf("binary size %.2f MB (budget: %.0f MB) PASS", sizeMB, budgetMB)
	}
}

// repoBinaryPath returns the absolute path to bin/klyne by locating the
// repo root two directories above the current test file.
func repoBinaryPath(tb testing.TB) string {
	tb.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		tb.Fatal("runtime.Caller failed")
	}
	// filename = .../internal/bench/binary_test.go
	// two dirs up = repo root
	repoRoot := filepath.Join(filepath.Dir(filename), "..", "..")
	return filepath.Join(repoRoot, "bin", "agentdeck")
}

package bench

import (
	"fmt"
	"os/exec"
	"sort"
)

// Percentile returns the p-th percentile (0–100) of a pre-sorted slice of
// nanosecond durations. Returns 0 for an empty slice.
//
// Used by SSE latency and FTS5 search benchmarks to compute p95.
func Percentile(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 100 {
		return sorted[len(sorted)-1]
	}
	// Linear interpolation index.
	idx := p / 100.0 * float64(len(sorted)-1)
	lo := int(idx)
	hi := lo + 1
	if hi >= len(sorted) {
		return sorted[lo]
	}
	frac := idx - float64(lo)
	return sorted[lo] + int64(frac*float64(sorted[hi]-sorted[lo]))
}

// SortedCopy returns a sorted copy of latencies (nanoseconds). Does not
// mutate the original slice.
func SortedCopy(latencies []int64) []int64 {
	cp := make([]int64, len(latencies))
	copy(cp, latencies)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	return cp
}

// RunCmd executes a shell command and returns its combined output. Useful
// for cold-start and binary-size tests that shell out to the built binary.
func RunCmd(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("RunCmd %q: %w\n%s", name, err, out)
	}
	return out, nil
}

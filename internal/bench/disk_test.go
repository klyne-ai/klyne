package bench

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDiskFootprint seeds 5,000 messages, measures the SQLite DB file size
// (including WAL and SHM), and compares against a synthetic JSONL size
// estimate.
//
// Spec §12 budget: DB on-disk footprint < 2× JSONL size.
//
// Methodology:
//   - JSONL baseline: the JSON-encoded size of each synthetic message
//     (ID ~30 bytes + SessionID ~20 bytes + fields + 200-byte content =
//     approx 450 bytes/line). This is a conservative JSONL estimate;
//     real Claude JSONL lines embed tool_use blocks and are larger.
//   - DB total: main .db file + .db-wal + .db-shm (WAL mode auxiliary files).
//     SQLite stores the messages table plus an FTS5 virtual table (messages_fts)
//     and its shadow tables, which roughly doubles the raw row cost.
//
// Note: After a clean checkpoint, the WAL shrinks to near-zero. In the bench
// we close the DB (which triggers a passive checkpoint) and sum the files
// afterward.
func TestDiskFootprint(t *testing.T) {
	const msgCount = 5_000
	// Approximate JSON-encoded size per synthetic message:
	// base fields + 200-byte content + JSON overhead ≈ 450 bytes.
	// This is a conservative lower bound; real JSONL lines with tool_use
	// blocks are 1–4 KB each, which would make the ratio even better.
	const avgJSONLBytesPerMsg = 450

	tmpDir := t.TempDir()
	db := SeedStore(t, tmpDir, msgCount)

	// Close the DB so SQLite checkpoints the WAL into the main file.
	if err := db.Close(); err != nil {
		t.Fatalf("db close: %v", err)
	}

	// Sum all SQLite-related files in tmpDir.
	dbBytes := fileSizeSum(t, tmpDir, "bench.db", "bench.db-wal", "bench.db-shm")
	jsonlBytes := int64(msgCount * avgJSONLBytesPerMsg)

	ratio := float64(dbBytes) / float64(jsonlBytes)
	bytesPerMsg := float64(dbBytes) / float64(msgCount)

	t.Logf("DB size:         %d bytes (%.1f KB)", dbBytes, float64(dbBytes)/1024)
	t.Logf("JSONL estimate:  %d bytes (%.1f KB)", jsonlBytes, float64(jsonlBytes)/1024)
	t.Logf("DB/JSONL ratio:  %.2f×", ratio)
	t.Logf("Bytes per msg:   %.1f", bytesPerMsg)

	// Spec §12 budget: < 2× JSONL size.
	const budgetRatio = 2.0
	if ratio >= budgetRatio {
		t.Errorf("disk footprint ratio %.2f× exceeds budget %.1f× (DB=%d JSONL=%d)",
			ratio, budgetRatio, dbBytes, jsonlBytes)
	}
}

// BenchmarkDiskFootprint is the benchmark-flavoured version for scripts/bench.sh.
func BenchmarkDiskFootprint(b *testing.B) {
	const msgCount = 5_000
	const avgJSONLBytesPerMsg = 450

	for i := 0; i < b.N; i++ {
		tmpDir := b.TempDir()
		db := SeedStore(b, tmpDir, msgCount)
		if err := db.Close(); err != nil {
			b.Fatalf("db close: %v", err)
		}

		dbBytes := fileSizeSum(b, tmpDir, "bench.db", "bench.db-wal", "bench.db-shm")
		jsonlBytes := int64(msgCount * avgJSONLBytesPerMsg)
		ratio := float64(dbBytes) / float64(jsonlBytes)

		b.ReportMetric(ratio, "db/jsonl-ratio")
		b.ReportMetric(float64(dbBytes)/float64(msgCount), "bytes/msg")

		const budgetRatio = 2.0
		if ratio >= budgetRatio {
			b.Errorf("disk footprint ratio %.2f× exceeds budget %.1f×", ratio, budgetRatio)
		}
	}
}

// fileSizeSum returns the sum of the sizes of the named files in dir.
// Files that do not exist are counted as 0 bytes (WAL/SHM may not exist
// after checkpoint).
func fileSizeSum(tb testing.TB, dir string, names ...string) int64 {
	tb.Helper()
	var total int64
	for _, name := range names {
		info, err := os.Stat(filepath.Join(dir, name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			tb.Fatalf("stat %s: %v", name, err)
		}
		total += info.Size()
	}
	return total
}

package usage

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/mohitpatell/agentdeck/internal/api"
)

// defaultCodexSessionsDir returns the v1 default Codex sessions root.
// Empty string when the user's home directory cannot be resolved — the
// caller will surface this as "Codex live data unavailable".
func defaultCodexSessionsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex", "sessions")
}

// Codex doesn't expose an OAuth/usage HTTP endpoint the way Claude does.
// Instead, the official CLI writes the canonical rate-limit snapshot back
// into the session JSONL on every response. We piggyback on that data —
// it's the exact same payload the official UI surfaces, no network call,
// no auth, no API surface to maintain.
//
// Event shape (one JSONL line per emitted event):
//
//	{
//	  "type": "event_msg",
//	  "timestamp": "2026-05-06T17:48:20.282Z",
//	  "payload": {
//	    "type": "token_count",
//	    "rate_limits": {
//	      "limit_id":  "codex",
//	      "plan_type": "team",
//	      "primary":   {"used_percent": 69.0, "window_minutes": 300,   "resets_at": 1778103417},
//	      "secondary": {"used_percent": 28.0, "window_minutes": 10080, "resets_at": 1778595093}
//	    }
//	  }
//	}
//
// `primary` is the rolling 5-hour window (300 min); `secondary` is the
// 7-day window (10080 min). `resets_at` is epoch SECONDS, not ms.

// codexScanLimit caps how many of the most-recent Codex session files
// codexSnapshot will inspect. We always find what we need in the newest
// file in practice — this limit just guards against pathological dirs.
const codexScanLimit = 8

// CodexSnapshotReader returns the latest rate-limit snapshot the Codex CLI
// has written. Cheap because of a 30s TTL cache. Safe to call concurrently.
type CodexSnapshotReader struct {
	root string

	mu       sync.Mutex
	cached   *api.OAuthUsage
	cachedAt time.Time
}

// NewCodexSnapshotReader builds a reader rooted at the user's Codex
// sessions directory. Pass an empty root to use the v1 default
// (~/.codex/sessions).
func NewCodexSnapshotReader(root string) *CodexSnapshotReader {
	if root == "" {
		root = defaultCodexSessionsDir()
	}
	return &CodexSnapshotReader{root: root}
}

// codexSnapshotTTL is how long a successful read stays cached. Codex
// writes a fresh snapshot on every turn, so 30s feels live without
// thrashing the filesystem when /usage is polled.
const codexSnapshotTTL = 30 * time.Second

// Latest returns the most recently observed rate-limit snapshot, served
// from cache when fresh. Returns (nil, nil) when no Codex session files
// exist or none carry a rate-limits event — callers treat that as
// "Codex live data unavailable" and fall back gracefully.
func (r *CodexSnapshotReader) Latest() (*api.OAuthUsage, error) {
	r.mu.Lock()
	if !r.cachedAt.IsZero() && time.Since(r.cachedAt) < codexSnapshotTTL {
		out := r.cached
		r.mu.Unlock()
		return out, nil
	}
	r.mu.Unlock()

	usage, err := r.readFresh()

	r.mu.Lock()
	r.cached = usage
	if err != nil {
		// Half the TTL on error so a flap doesn't get retried every call.
		r.cachedAt = time.Now().Add(-codexSnapshotTTL / 2)
	} else {
		r.cachedAt = time.Now()
	}
	r.mu.Unlock()
	return usage, err
}

// readFresh walks the Codex sessions directory newest-first and returns
// the latest snapshot found. Returns (nil, nil) when none exist.
func (r *CodexSnapshotReader) readFresh() (*api.OAuthUsage, error) {
	files, err := listRolloutFiles(r.root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("codex snapshot: walk %s: %w", r.root, err)
	}
	if len(files) == 0 {
		return nil, nil
	}

	// Files are sorted newest-first. Scan the first few until we find
	// one with a rate-limits event.
	limit := codexScanLimit
	if limit > len(files) {
		limit = len(files)
	}
	for i := 0; i < limit; i++ {
		snap, err := scanFileForSnapshot(files[i])
		if err != nil {
			// Tolerate per-file failures — the file may be mid-write.
			continue
		}
		if snap != nil {
			return snap, nil
		}
	}
	return nil, nil
}

// fileEntry pairs a path with its mtime so we can sort newest-first.
type fileEntry struct {
	path  string
	mtime time.Time
}

// listRolloutFiles walks `root` and returns every rollout-*.jsonl, sorted
// newest-first by mtime. Returns nil + nil when root doesn't exist —
// callers treat that as the "Codex never installed" case.
func listRolloutFiles(root string) ([]string, error) {
	if _, err := os.Stat(root); err != nil {
		return nil, err
	}

	var out []fileEntry
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Skip unreadable subtrees rather than aborting the whole walk.
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		// rollout-2026-05-06T22-17-20-019dfe2f-d452-7031-...-jsonl
		if filepath.Ext(name) != ".jsonl" {
			return nil
		}
		if len(name) < 8 || name[:8] != "rollout-" {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		out = append(out, fileEntry{path: path, mtime: info.ModTime()})
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	sort.Slice(out, func(i, j int) bool { return out[i].mtime.After(out[j].mtime) })
	paths := make([]string, len(out))
	for i, e := range out {
		paths[i] = e.path
	}
	return paths, nil
}

// codexEvent is the minimal envelope we need to read.
type codexEvent struct {
	Type    string `json:"type"`
	Payload struct {
		Type       string             `json:"type"`
		RateLimits *codexRateLimitRaw `json:"rate_limits"`
	} `json:"payload"`
}

type codexRateLimitRaw struct {
	PlanType  string                  `json:"plan_type"`
	Primary   *codexRateLimitWindowRaw `json:"primary"`
	Secondary *codexRateLimitWindowRaw `json:"secondary"`
}

type codexRateLimitWindowRaw struct {
	UsedPercent   float64 `json:"used_percent"`
	WindowMinutes int64   `json:"window_minutes"`
	// ResetsAt is epoch SECONDS in the Codex JSONL (not ms — confirmed
	// against live data 2026-05-07).
	ResetsAt int64 `json:"resets_at"`
}

// scanFileForSnapshot reads the file forward and returns the LAST
// rate-limits snapshot encountered. Scanning forward (rather than
// reading-from-end) keeps the implementation simple; Codex sessions are
// rarely larger than a few MB and the line scanner allocates a single
// reusable buffer.
func scanFileForSnapshot(path string) (*api.OAuthUsage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck

	// Codex tool results can be hundreds of KB. Match the Claude
	// connector's 16 MiB buffer so we don't truncate.
	const maxLine = 16 * 1024 * 1024
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), maxLine)

	var latest *codexRateLimitRaw
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev codexEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		if ev.Type != "event_msg" || ev.Payload.Type != "token_count" {
			continue
		}
		if ev.Payload.RateLimits == nil {
			continue
		}
		// Keep the most recent snapshot — JSONL is append-only, so the
		// last one we see is the freshest.
		latest = ev.Payload.RateLimits
	}
	if err := scanner.Err(); err != nil {
		// Stop on scanner error but return any snapshot we already have.
		if latest == nil {
			return nil, err
		}
	}
	if latest == nil {
		return nil, nil
	}
	return convertCodexSnapshot(latest), nil
}

// convertCodexSnapshot maps the Codex JSONL shape into the contract DTO.
// `primary` (5h, 300 min) → FiveHour; `secondary` (7d, 10080 min) →
// SevenDay. SevenDaySonnet is left nil — it's an Anthropic-specific
// concept Codex doesn't surface.
//
// Stale-snapshot handling: unlike Claude's live OAuth call, Codex data
// is only as fresh as the last CLI turn. If a snapshot's `resets_at` is
// already in the past, the rate-limit window has rolled over since the
// CLI last wrote — we report 0% (the window definitely reset) so the
// badge doesn't show a permanently-stuck percentage when the user hasn't
// run codex in a while.
func convertCodexSnapshot(src *codexRateLimitRaw) *api.OAuthUsage {
	return convertCodexSnapshotAt(src, time.Now())
}

// convertCodexSnapshotAt is the testable variant — clock injected.
func convertCodexSnapshotAt(src *codexRateLimitRaw, now time.Time) *api.OAuthUsage {
	out := &api.OAuthUsage{SubscriptionType: src.PlanType}
	if src.Primary != nil {
		out.FiveHour = decayIfExpired(src.Primary, now)
	}
	if src.Secondary != nil {
		out.SevenDay = decayIfExpired(src.Secondary, now)
	}
	return out
}

// decayIfExpired converts a raw Codex rate-limit window to the contract
// shape, zeroing the utilization when the window has rolled over since
// the snapshot was written.
func decayIfExpired(w *codexRateLimitWindowRaw, now time.Time) *api.OAuthWindow {
	resetsMs := w.ResetsAt * 1000 // seconds → ms
	if resetsMs > 0 && resetsMs < now.UnixMilli() {
		return &api.OAuthWindow{
			UtilizationPct: 0,
			ResetsAt:       0, // unknown; the next request will write a fresh anchor
		}
	}
	return &api.OAuthWindow{
		UtilizationPct: w.UsedPercent,
		ResetsAt:       resetsMs,
	}
}

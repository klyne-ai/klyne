package contexthealth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// transition.go — per-session advisory state cache.
//
// Each advisor trigger fires at most once between clearance events.
// To enforce that across distinct invocations of `klyne advise`
// (which run as fresh subprocesses from the UserPromptSubmit hook),
// we persist a tiny JSON file under ~/.klyne/advisor-state.json.
//
// State layout:
//
//	{
//	  "sessions": {
//	    "<session_id>": {
//	      "fired": ["stale", "acceleration"],
//	      "last_seen": 1715600000000
//	    }
//	  },
//	  "five_hour": {
//	    "fired": ["window_50"],
//	    "last_measured_ms": 1715600000000,
//	    "last_pct": 52.0
//	  }
//	}
//
// Reads are tolerant: a missing or malformed file produces an empty
// state. Writes are best-effort: any I/O error is returned to the
// caller but also logged for the hook to surface in stderr.

// TriggerKind names a trigger for the persistence layer. Strings live
// here (not in the rendering layer) so the file format is stable.
type TriggerKind string

const (
	TriggerStale          TriggerKind = "stale"
	TriggerAcceleration   TriggerKind = "acceleration"
	TriggerHardCeiling    TriggerKind = "hard_ceiling"
	TriggerFiveHourWarn   TriggerKind = "window_50"
	TriggerFiveHourUrgent TriggerKind = "window_75"
)

// AllTriggers returns every trigger kind. Used by clearance logic.
func AllTriggers() []TriggerKind {
	return []TriggerKind{
		TriggerStale,
		TriggerAcceleration,
		TriggerHardCeiling,
		TriggerFiveHourWarn,
		TriggerFiveHourUrgent,
	}
}

// SessionState is the per-session entry in the advisor state file.
type SessionState struct {
	// Fired is the set of triggers that have already fired and not
	// yet cleared. Sorted on disk for determinism.
	Fired []TriggerKind `json:"fired"`
	// LastSeenMs is the epoch-ms timestamp of the latest assistant
	// message we saw on this session — used to garbage-collect
	// stale per-session entries on read.
	LastSeenMs int64 `json:"last_seen_ms"`
}

// FiveHourState is the global 5-hour-window entry. Lives outside
// the per-session map because it spans every session in the home dir.
type FiveHourState struct {
	// Fired is the set of window thresholds that have already fired
	// and not yet cleared (e.g. ["window_50"] when the 50% threshold
	// has been crossed but 75% has not).
	Fired []TriggerKind `json:"fired"`
	// LastMeasuredMs is the epoch-ms timestamp of the most recent
	// 5-hour-window measurement. Used to throttle re-computation by
	// hookFiveHourCacheMs from the consumer side.
	LastMeasuredMs int64 `json:"last_measured_ms"`
	// LastPct is the most recent computed 5-hour-window consumption
	// percentage. Mirrored from the cross-session aggregator so the
	// hook can return without re-walking when the cache is fresh.
	LastPct float64 `json:"last_pct"`
}

// AdvisorState is the on-disk shape of advisor-state.json.
type AdvisorState struct {
	Sessions map[string]*SessionState `json:"sessions"`
	FiveHour FiveHourState            `json:"five_hour"`
}

// stateFileLock serialises concurrent reads/writes from multiple
// hook invocations on the same machine. The hook is single-process
// in normal use, but parallel Claude Code sessions could race.
var stateFileLock sync.Mutex

// DefaultStatePath returns ~/.klyne/advisor-state.json. The
// directory is created on first write.
func DefaultStatePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home: %w", err)
	}
	return filepath.Join(home, ".klyne", "advisor-state.json"), nil
}

// LoadState reads path and returns the parsed AdvisorState. Returns
// an empty state when the file is missing or malformed — advisor
// behaviour must never block on a stale state file.
func LoadState(path string) AdvisorState {
	stateFileLock.Lock()
	defer stateFileLock.Unlock()
	body, err := os.ReadFile(path) //nolint:gosec
	if err != nil || len(body) == 0 {
		return emptyState()
	}
	var st AdvisorState
	if err := json.Unmarshal(body, &st); err != nil {
		return emptyState()
	}
	if st.Sessions == nil {
		st.Sessions = map[string]*SessionState{}
	}
	return st
}

// SaveState writes path atomically (via the standard rename idiom)
// and returns any I/O error. Caller is expected to log but not block
// the user prompt on failure.
func SaveState(path string, st AdvisorState) error {
	stateFileLock.Lock()
	defer stateFileLock.Unlock()

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	// Sort the per-session Fired slices and the FiveHour Fired slice
	// for deterministic on-disk output.
	for _, s := range st.Sessions {
		sort.Slice(s.Fired, func(i, j int) bool { return s.Fired[i] < s.Fired[j] })
	}
	sort.Slice(st.FiveHour.Fired, func(i, j int) bool {
		return st.FiveHour.Fired[i] < st.FiveHour.Fired[j]
	})

	body, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		// Try to clean up the tmp file on rename failure.
		if rmErr := os.Remove(tmp); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			return fmt.Errorf("rename failed (%w); tmp cleanup also failed (%v)", err, rmErr)
		}
		return fmt.Errorf("rename %s -> %s: %w", tmp, path, err)
	}
	return nil
}

// emptyState returns a freshly initialised AdvisorState.
func emptyState() AdvisorState {
	return AdvisorState{
		Sessions: map[string]*SessionState{},
	}
}

// HasFired reports whether the given trigger has already fired for
// the given session under this state.
func (s AdvisorState) HasFired(sessionID string, trig TriggerKind) bool {
	row := s.Sessions[sessionID]
	if row == nil {
		return false
	}
	for _, t := range row.Fired {
		if t == trig {
			return true
		}
	}
	return false
}

// HasFiredFiveHour reports whether the given five-hour trigger has
// already fired since the last clearance.
func (s AdvisorState) HasFiredFiveHour(trig TriggerKind) bool {
	for _, t := range s.FiveHour.Fired {
		if t == trig {
			return true
		}
	}
	return false
}

// MarkFired adds the given trigger to the session's fired set.
// Idempotent — duplicate calls are a no-op.
func (s *AdvisorState) MarkFired(sessionID string, trig TriggerKind, lastSeenMs int64) {
	if s.Sessions == nil {
		s.Sessions = map[string]*SessionState{}
	}
	row := s.Sessions[sessionID]
	if row == nil {
		row = &SessionState{}
		s.Sessions[sessionID] = row
	}
	if !contains(row.Fired, trig) {
		row.Fired = append(row.Fired, trig)
	}
	if lastSeenMs > row.LastSeenMs {
		row.LastSeenMs = lastSeenMs
	}
}

// MarkFiredFiveHour adds the trigger to the global five-hour set.
func (s *AdvisorState) MarkFiredFiveHour(trig TriggerKind, pct float64, measuredMs int64) {
	if !contains(s.FiveHour.Fired, trig) {
		s.FiveHour.Fired = append(s.FiveHour.Fired, trig)
	}
	s.FiveHour.LastPct = pct
	s.FiveHour.LastMeasuredMs = measuredMs
}

// ClearTrigger removes a trigger from the session's fired set.
// Called when the trigger condition no longer holds, so the next
// re-trip can fire fresh.
func (s *AdvisorState) ClearTrigger(sessionID string, trig TriggerKind) {
	row := s.Sessions[sessionID]
	if row == nil {
		return
	}
	row.Fired = without(row.Fired, trig)
}

// ClearFiveHour removes a five-hour trigger from the global set.
func (s *AdvisorState) ClearFiveHour(trig TriggerKind) {
	s.FiveHour.Fired = without(s.FiveHour.Fired, trig)
}

// GarbageCollect drops session entries whose LastSeenMs is older than
// nowMs - retainMs. Called opportunistically by the hook so the file
// does not grow unbounded for users with long-lived setups.
func (s *AdvisorState) GarbageCollect(nowMs, retainMs int64) {
	if s.Sessions == nil {
		return
	}
	cutoff := nowMs - retainMs
	for id, row := range s.Sessions {
		if row.LastSeenMs < cutoff {
			delete(s.Sessions, id)
		}
	}
}

// contains is a tiny helper because Go's stdlib slice helper is
// generic in 1.21+ and we don't depend on it here for clarity.
func contains(xs []TriggerKind, t TriggerKind) bool {
	for _, x := range xs {
		if x == t {
			return true
		}
	}
	return false
}

// without returns a copy of xs with t removed. Returns the original
// slice when t is absent (no allocation in the common path).
func without(xs []TriggerKind, t TriggerKind) []TriggerKind {
	idx := -1
	for i, x := range xs {
		if x == t {
			idx = i
			break
		}
	}
	if idx < 0 {
		return xs
	}
	out := make([]TriggerKind, 0, len(xs)-1)
	out = append(out, xs[:idx]...)
	out = append(out, xs[idx+1:]...)
	return out
}

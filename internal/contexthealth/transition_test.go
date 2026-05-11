package contexthealth

import (
	"path/filepath"
	"testing"
)

func TestState_LoadEmptyOrMissing(t *testing.T) {
	dir := t.TempDir()
	st := LoadState(filepath.Join(dir, "missing.json"))
	if len(st.Sessions) != 0 {
		t.Fatalf("expected empty Sessions on missing file, got %d", len(st.Sessions))
	}
	if len(st.FiveHour.Fired) != 0 {
		t.Fatalf("expected empty FiveHour.Fired on missing file, got %d", len(st.FiveHour.Fired))
	}
}

func TestState_MarkAndPersist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "advisor-state.json")

	st := LoadState(path)
	st.MarkFired("sess1", TriggerStale, 1000)
	st.MarkFiredFiveHour(TriggerFiveHourWarn, 52.0, 1500)
	if err := SaveState(path, st); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	got := LoadState(path)
	if !got.HasFired("sess1", TriggerStale) {
		t.Fatalf("expected stale fired for sess1")
	}
	if !got.HasFiredFiveHour(TriggerFiveHourWarn) {
		t.Fatalf("expected window_50 fired")
	}
	if got.FiveHour.LastPct != 52.0 {
		t.Fatalf("LastPct=%v, want 52.0", got.FiveHour.LastPct)
	}
}

func TestState_ClearAllowsRefire(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "advisor-state.json")

	st := LoadState(path)
	st.MarkFired("sess1", TriggerAcceleration, 2000)
	if err := SaveState(path, st); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	st = LoadState(path)
	st.ClearTrigger("sess1", TriggerAcceleration)
	if err := SaveState(path, st); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	got := LoadState(path)
	if got.HasFired("sess1", TriggerAcceleration) {
		t.Fatalf("expected acceleration cleared after ClearTrigger")
	}
}

func TestState_GarbageCollectsStaleSessions(t *testing.T) {
	st := AdvisorState{
		Sessions: map[string]*SessionState{
			"old": {Fired: []TriggerKind{TriggerStale}, LastSeenMs: 1000},
			"new": {Fired: []TriggerKind{TriggerStale}, LastSeenMs: 9000},
		},
	}
	st.GarbageCollect(10_000, 5_000)
	if _, ok := st.Sessions["old"]; ok {
		t.Fatalf("expected old session GCed")
	}
	if _, ok := st.Sessions["new"]; !ok {
		t.Fatalf("expected new session retained")
	}
}

func TestState_MarkIsIdempotent(t *testing.T) {
	st := emptyState()
	st.MarkFired("sess1", TriggerStale, 1000)
	st.MarkFired("sess1", TriggerStale, 2000)
	row := st.Sessions["sess1"]
	if row == nil {
		t.Fatalf("expected sess1 row to exist")
	}
	if got := len(row.Fired); got != 1 {
		t.Fatalf("Fired len = %d, want 1", got)
	}
	if row.LastSeenMs != 2000 {
		t.Fatalf("LastSeenMs = %d, want 2000 (max wins)", row.LastSeenMs)
	}
}

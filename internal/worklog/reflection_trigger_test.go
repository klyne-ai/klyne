package worklog

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/store"
)

func newTriggerTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "trigger.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func seedStopSummaryWithImportance(t *testing.T, db *store.DB, project, cli, sessionID string, importance int, ts time.Time) {
	t.Helper()
	_, err := db.Write().ExecContext(context.Background(),
		`INSERT INTO stop_summaries (
            session_id, ts, project_path, cli, summary, last_user, last_bash, files_json,
            recap_visible, recap_topic, ai_drafted_summary, draft_state, signature, importance, last_accessed_at
        ) VALUES (?, ?, ?, ?, '', '', '', '[]', 1, '', '', 'proposed', ?, ?, ?)`,
		sessionID, ts.UnixMilli(), project, cli, "sig-"+sessionID, importance, ts.UnixMilli())
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func TestReflectionTriggerImportanceSum_BelowThreshold(t *testing.T) {
	db := newTriggerTestDB(t)
	for i := 0; i < 5; i++ {
		seedStopSummaryWithImportance(t, db, "/p", "claude", fmt.Sprintf("s%d", i), 8, time.Now())
	}
	// 5 × 8 = 40, well below 150
	fire, err := ShouldFireReflection(context.Background(), db, "/p", 150)
	if err != nil {
		t.Fatal(err)
	}
	if fire {
		t.Errorf("40 < 150, must not fire yet")
	}
}

func TestReflectionTriggerImportanceSum_AboveThreshold(t *testing.T) {
	db := newTriggerTestDB(t)
	// Seed 16 × 10 = 160 — above 150 threshold
	for i := 0; i < 16; i++ {
		seedStopSummaryWithImportance(t, db, "/p", "codex", fmt.Sprintf("big%d", i), 10, time.Now())
	}
	fire, err := ShouldFireReflection(context.Background(), db, "/p", 150)
	if err != nil {
		t.Fatal(err)
	}
	if !fire {
		t.Errorf("160 ≥ 150, must fire")
	}
}

func TestReflectionTriggerScopedByProject(t *testing.T) {
	db := newTriggerTestDB(t)
	// 200 importance in /other, 0 in /p
	for i := 0; i < 20; i++ {
		seedStopSummaryWithImportance(t, db, "/other", "claude", fmt.Sprintf("other%d", i), 10, time.Now())
	}
	fire, err := ShouldFireReflection(context.Background(), db, "/p", 150)
	if err != nil {
		t.Fatal(err)
	}
	if fire {
		t.Errorf("threshold must be per-project; /p has no entries")
	}
}

func TestWeeklyCronShouldFire_RequiresSundayEvening(t *testing.T) {
	db := newTriggerTestDB(t)
	seedStopSummaryWithImportance(t, db, "/p", "claude", "s1", 8, time.Now())
	// Mon 12:00 UTC — should NOT fire even though there are entries
	mon := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	if fire, _ := WeeklyCronShouldFire(context.Background(), db, "/p", mon); fire {
		t.Errorf("Monday noon must not fire weekly cron")
	}
	// Sun 21:00 UTC — should fire
	sun := time.Date(2026, 5, 17, 21, 0, 0, 0, time.UTC)
	if fire, _ := WeeklyCronShouldFire(context.Background(), db, "/p", sun); !fire {
		t.Errorf("Sunday 21:00 UTC must fire weekly cron when entries exist")
	}
}

func TestWeeklyCronSkipsWhenNoEntries(t *testing.T) {
	db := newTriggerTestDB(t)
	sun := time.Date(2026, 5, 17, 21, 0, 0, 0, time.UTC)
	if fire, _ := WeeklyCronShouldFire(context.Background(), db, "/p", sun); fire {
		t.Errorf("Sunday with no entries must not fire (no point reflecting on nothing)")
	}
}

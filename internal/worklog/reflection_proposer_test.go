package worklog

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/store"
)

func newProposerTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "proposer.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func seedProposerEntry(t *testing.T, db *store.DB, project, cli, sessionID, lastUser string, importance int, ts time.Time) {
	t.Helper()
	_, err := db.Write().ExecContext(context.Background(),
		`INSERT INTO stop_summaries (
            session_id, ts, project_path, cli, summary, last_user, last_bash, files_json,
            recap_visible, recap_topic, ai_drafted_summary, draft_state, signature, importance, last_accessed_at
        ) VALUES (?, ?, ?, ?, '', ?, '', '[]', 1, ?, '', 'proposed', ?, ?, ?)`,
		sessionID, ts.UnixMilli(), project, cli, lastUser, "topic-"+sessionID, "sig-"+sessionID, importance, ts.UnixMilli())
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func seedProposerReflection(t *testing.T, db *store.DB, project string, evidence []string, ts time.Time) {
	t.Helper()
	refl := store.Reflection{
		ID:               "ref-" + project + "-" + ts.Format("20060102"),
		TS:               ts.UnixMilli(),
		ProjectPath:      project,
		Tier:             2,
		Title:            "prior reflection",
		BodyMD:           "- prior (evidence: " + evidence[0] + ")\n",
		EvidenceEntryIDs: evidence,
		Importance:       7,
		SummarySource:    "ai",
		State:            "proposed",
		StateChangedAt:   ts.UnixMilli(),
	}
	if err := store.InsertReflection(context.Background(), db, refl); err != nil {
		t.Fatalf("seed reflection: %v", err)
	}
}

func TestLoadPendingEntries_OnlyAfterLastReflection(t *testing.T) {
	db := newProposerTestDB(t)
	now := time.Now()
	old := now.Add(-2 * time.Hour)
	mid := now.Add(-1 * time.Hour)
	fresh := now.Add(-15 * time.Minute)

	seedProposerEntry(t, db, "/p", "claude", "old-1", "old work", 5, old)
	// Reflection cut-off lands between `old` and `mid`.
	seedProposerReflection(t, db, "/p", []string{"old-1"}, old.Add(30*time.Minute))
	seedProposerEntry(t, db, "/p", "claude", "mid-1", "mid work", 6, mid)
	seedProposerEntry(t, db, "/p", "codex", "fresh-1", "fresh work", 7, fresh)

	entries, reason, err := LoadPendingEntries(context.Background(), db, "/p", 150, now, time.Time{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 pending entries since last reflection, got %d", len(entries))
	}
	seen := map[string]bool{}
	for _, e := range entries {
		seen[e.SessionID] = true
	}
	if seen["old-1"] {
		t.Errorf("old-1 must NOT be pending (older than last reflection)")
	}
	if !seen["mid-1"] || !seen["fresh-1"] {
		t.Errorf("mid-1 and fresh-1 must both be pending, got %v", seen)
	}
	// Sum 6+7 = 13, well under threshold 150, no Sunday-evening trigger
	// in most test runs — should fall through to user-invoked unless the
	// CI clock happens to be Sunday evening.
	if now.Weekday() == time.Sunday && now.Hour() >= 20 {
		if reason != "weekly cron (Sunday evening)" {
			t.Errorf("Sunday evening: expected weekly cron reason, got %q", reason)
		}
	} else {
		if reason != "user-invoked" {
			t.Errorf("expected user-invoked reason (sum=13 < 150, not Sunday eve), got %q", reason)
		}
	}
}

func TestLoadPendingEntries_ImportanceSumReason(t *testing.T) {
	db := newProposerTestDB(t)
	now := time.Now()
	// Three entries scoring 60 each → sum 180 ≥ 150 → importance-sum reason.
	seedProposerEntry(t, db, "/p", "claude", "e1", "u1", 60, now.Add(-3*time.Hour))
	seedProposerEntry(t, db, "/p", "claude", "e2", "u2", 60, now.Add(-2*time.Hour))
	seedProposerEntry(t, db, "/p", "codex", "e3", "u3", 60, now.Add(-1*time.Hour))

	_, reason, err := LoadPendingEntries(context.Background(), db, "/p", 150, now, time.Time{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if reason == "" || reason == "user-invoked" {
		t.Errorf("expected importance-sum reason, got %q", reason)
	}
	// Confirm the actual prefix so the format string survives refactors.
	if got := reason; !startsWith(got, "importance-sum") {
		t.Errorf("reason should start with 'importance-sum', got %q", got)
	}
}

func TestLoadPendingEntries_WeeklyCronReason(t *testing.T) {
	db := newProposerTestDB(t)
	// Force "now" to a Sunday at 21:00 UTC so the cron trigger fires.
	sundayEvening := time.Date(2026, 5, 17, 21, 0, 0, 0, time.UTC)
	if sundayEvening.Weekday() != time.Sunday {
		t.Fatalf("test setup error: 2026-05-17 is not a Sunday (got %s)", sundayEvening.Weekday())
	}
	// One low-importance entry so sum stays well under threshold.
	seedProposerEntry(t, db, "/p", "claude", "low-1", "small work", 3, sundayEvening.Add(-2*time.Hour))

	_, reason, err := LoadPendingEntries(context.Background(), db, "/p", 150, sundayEvening, time.Time{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if reason != "weekly cron (Sunday evening)" {
		t.Errorf("expected weekly cron reason, got %q", reason)
	}
}

func TestLoadPendingEntries_NoEntriesUserInvoked(t *testing.T) {
	db := newProposerTestDB(t)
	entries, reason, err := LoadPendingEntries(context.Background(), db, "/p", 150, time.Now(), time.Time{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries on empty db, got %d", len(entries))
	}
	if reason != "user-invoked" {
		t.Errorf("expected user-invoked on empty db, got %q", reason)
	}
}

// startsWith avoids pulling strings as a test-only dependency just to
// keep this assertion readable.
func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// Improvements 2 & 4 (prompt-contract half): ProposalGitBrief renders the
// salience-ranked git facts plus the §7.1 grounding contract that the AI
// host reads before synthesizing — so the reflection leads with
// high-impact work and never invents PR IDs.
func TestProposalGitBrief_IncludesSalienceFactsAndContract(t *testing.T) {
	rep := salienceFixture()
	brief := ProposalGitBrief(rep, "/repos/svc")
	if brief == "" {
		t.Fatal("expected a non-empty git brief for a known repo")
	}
	// Salience facts present and ranked.
	idxFeat := indexOf(brief, "feat/CLI-9-core")
	idxDocs := indexOf(brief, "docs/cleanup")
	if idxFeat < 0 || idxDocs < 0 {
		t.Fatalf("brief must list both branches, got:\n%s", brief)
	}
	if idxFeat > idxDocs {
		t.Errorf("brief must lead with the high-impact branch, got:\n%s", brief)
	}
	// Grounding contract present.
	if indexOf(brief, "Grounding contract") < 0 {
		t.Errorf("brief must embed the §7.1 grounding contract, got:\n%s", brief)
	}
}

// Graceful degradation: an unknown / non-git project yields an empty
// brief, so the existing proposer flow is unchanged for those projects.
func TestProposalGitBrief_EmptyForUnknownProject(t *testing.T) {
	rep := salienceFixture()
	if b := ProposalGitBrief(rep, "/not/a/repo"); b != "" {
		t.Errorf("expected empty brief for non-git project, got:\n%s", b)
	}
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

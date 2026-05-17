package mcpserver

import (
	"context"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/store"
)

// seedStopSummary inserts a stop_summaries row with the migration-015
// worklog columns set. Used by Tasks 9, 10, 11 and their reflection-layer
// successors — keep the signature stable.
func seedStopSummary(t *testing.T, db *store.DB, project, cli, sessionID string, visible bool, importance int, ts time.Time) {
	t.Helper()
	v := 0
	if visible {
		v = 1
	}
	_, err := db.Write().ExecContext(context.Background(),
		`INSERT INTO stop_summaries (
            session_id, ts, project_path, cli, summary, last_user, last_bash, files_json,
            recap_visible, recap_topic, ai_drafted_summary, draft_state, signature, importance, last_accessed_at
        ) VALUES (?, ?, ?, ?, '', '', '', '[]', ?, '', '', 'proposed', ?, ?, ?)`,
		sessionID, ts.UnixMilli(), project, cli, v, "sig-"+sessionID, importance, ts.UnixMilli())
	if err != nil {
		t.Fatalf("seed stop_summary %s: %v", sessionID, err)
	}
}

func TestRecapProjectReturnsVisibleEntries(t *testing.T) {
	withFakeHome(t)
	db := withBootstrapDB(t)
	seedStopSummary(t, db, "/p", "claude", "s1", true, 8, time.Now())
	seedStopSummary(t, db, "/p", "codex", "s2", true, 7, time.Now())
	seedStopSummary(t, db, "/p", "claude", "s3", false, 3, time.Now()) // suppressed

	out, err := handleRecapProject(context.Background(), db, RecapProjectArgs{
		ProjectPath: "/p", SinceDays: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Entries) != 2 {
		t.Errorf("expected 2 visible entries, got %d", len(out.Entries))
	}
	seenCLI := map[string]bool{}
	for _, e := range out.Entries {
		seenCLI[e.CLI] = true
	}
	if !seenCLI["claude"] || !seenCLI["codex"] {
		t.Errorf("must return both CLIs, got %v", seenCLI)
	}
}

func TestRecapProjectFiltersOtherProjects(t *testing.T) {
	withFakeHome(t)
	db := withBootstrapDB(t)
	seedStopSummary(t, db, "/p", "claude", "s1", true, 8, time.Now())
	seedStopSummary(t, db, "/other", "codex", "s2", true, 7, time.Now())

	out, err := handleRecapProject(context.Background(), db, RecapProjectArgs{ProjectPath: "/p", SinceDays: 7})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Entries) != 1 || out.Entries[0].SessionID != "s1" {
		t.Errorf("expected only s1, got %+v", out.Entries)
	}
}

func seedReflection(t *testing.T, db *store.DB, project string, entryIDs []string, title string) {
	t.Helper()
	refl := store.Reflection{
		ID:               "ref-" + project + "-" + title,
		TS:               time.Now().UnixMilli(),
		ProjectPath:      project,
		Tier:             2,
		Title:            title,
		BodyMD:           "- " + title + " (evidence: " + entryIDs[0] + ")\n",
		EvidenceEntryIDs: entryIDs,
		Importance:       7,
		SummarySource:    "ai",
		State:            "proposed",
		StateChangedAt:   time.Now().UnixMilli(),
	}
	if err := store.InsertReflection(context.Background(), db, refl); err != nil {
		t.Fatalf("seed reflection: %v", err)
	}
}

func TestRecapProjectIncludesReflections(t *testing.T) {
	withFakeHome(t)
	db := withBootstrapDB(t)
	seedStopSummary(t, db, "/p", "claude", "s1", true, 8, time.Now())
	seedReflection(t, db, "/p", []string{"s1"}, "Weekly: shipped auth refactor")

	out, err := handleRecapProject(context.Background(), db, RecapProjectArgs{ProjectPath: "/p", SinceDays: 7})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Reflections) != 1 {
		t.Errorf("expected 1 reflection, got %d", len(out.Reflections))
	}
	if len(out.Reflections) > 0 && out.Reflections[0].EvidenceCount != 1 {
		t.Errorf("expected EvidenceCount=1, got %d", out.Reflections[0].EvidenceCount)
	}
}

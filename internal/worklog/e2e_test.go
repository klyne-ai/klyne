package worklog_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/mcpserver"
	"github.com/klyne-ai/klyne/internal/store"
	"github.com/klyne-ai/klyne/internal/worklog"
)

// TestCrossAIHandoffFlow exercises the canonical user flow from the plan:
//
//  1. A Codex session ends — detector writes a worklog entry tagged cli='codex'
//     through worklog.WriteEntry, the single-writer shared by both detectors.
//  2. A fresh Claude session calls bootstrap — the Codex entry surfaces in the
//     cross-AI worklog injection (T11).
//  3. Claude calls recap_project — the Codex entry is returned cross-CLI (T9).
//  4. The bootstrap Markdown rendering tags the entry with its source CLI so
//     the human reviewer can tell who did what.
//
// All three steps share one SQLite database routed via HOME and
// config.DBPath(). This is the acceptance test for "cross-AI works."
func TestCrossAIHandoffFlow(t *testing.T) {
	// Redirect HOME so config.DBPath() resolves under a temp dir; the MCP
	// handlers open the store via config.DBPath() internally, so this swap
	// is what makes them target our scratch DB. We pre-create the ~/.klyne
	// dir because store.Open expects the parent to exist.
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(config.DBPath()), 0o755); err != nil {
		t.Fatalf("mkdir klyne dir: %v", err)
	}

	db, err := store.Open(context.Background(), config.DBPath())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	const proj = "/proj/example"

	// 1. Codex session ends — detector writes entry through the single-writer.
	//    The combination of CommitSHA + decision_recorded + security_relevant +
	//    EditWriteCount=5 should pass every suppression rule and score high.
	codexEntry := worklog.Entry{
		SessionID:      "codex-1",
		TS:             time.Now().Add(-1 * time.Hour),
		ProjectPath:    proj,
		CLI:            "codex",
		LastUser:       "Should we use JWT or sessions for auth?",
		WallTime:       10 * time.Minute,
		ToolCallCount:  30,
		EditWriteCount: 5,
		Files:          []string{"src/auth.go"},
		CommitSHA:      "deadbeef",
		EventTags: []worklog.EventTag{
			worklog.TagCommitLanded,
			worklog.TagDecisionRecorded,
			worklog.TagSecurityRelevantChange,
		},
	}
	res, err := worklog.WriteEntry(context.Background(), db, codexEntry, nil, store.UpsertStopSummaryWithWorklog)
	if err != nil {
		t.Fatalf("worklog.WriteEntry: %v", err)
	}
	if res.RecapVisible != 1 {
		t.Fatalf("codex entry must be visible (commit+decision+security), got recap_visible=%d, suppressed_by=%q", res.RecapVisible, res.SuppressedBy)
	}
	if res.Importance < 9 {
		t.Errorf("commit+decision+security must score >= 9, got %d", res.Importance)
	}

	// 2. Fresh Claude session starts — bootstrap shows the Codex entry.
	//    No prior Claude session exists in this temp HOME; the only worklog
	//    row is the Codex one written above. Bootstrap must surface it.
	_, bootOut, err := mcpserver.HandleBootstrap(context.Background(), nil, mcpserver.BootstrapInput{CWD: proj})
	if err != nil {
		t.Fatalf("HandleBootstrap: %v", err)
	}
	var found bool
	for _, e := range bootOut.WorklogEntries {
		if e.CLI == "codex" && e.SessionID == "codex-1" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("bootstrap must surface the Codex entry to the fresh Claude session; got %d entries: %+v", len(bootOut.WorklogEntries), bootOut.WorklogEntries)
	}

	// 3. Claude calls recap_project — the Codex entry is returned cross-CLI.
	_, recapOut, err := mcpserver.HandleRecapProject(context.Background(), nil, mcpserver.RecapProjectInput{
		ProjectPath: proj,
		SinceDays:   1,
	})
	if err != nil {
		t.Fatalf("HandleRecapProject: %v", err)
	}
	var codexFound bool
	for _, e := range recapOut.Entries {
		if e.CLI == "codex" {
			codexFound = true
		}
	}
	if !codexFound {
		t.Errorf("recap_project must include the Codex entry; got entries: %+v", recapOut.Entries)
	}

	// 4. Markdown must surface the entry with its CLI tag — this is what the
	//    human reviewer sees in the slash-prompt-ready render, so the tag is
	//    the load-bearing UX signal that the handoff is cross-AI.
	if !strings.Contains(bootOut.Markdown, "[codex]") {
		t.Errorf("bootstrap markdown must tag the Codex entry; full markdown:\n%s", bootOut.Markdown)
	}
}

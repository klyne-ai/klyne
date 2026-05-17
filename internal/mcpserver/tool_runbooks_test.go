package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/store"
)

// runbookTestDB stages a temp klyne home + opens the SQLite store
// against it. Used by the integration tests in this file so they
// share one fixture instead of re-wiring HomeDir each time.
func runbookTestDB(t *testing.T) (*store.DB, string) {
	t.Helper()
	tmp := t.TempDir()
	prev := config.HomeDir
	config.HomeDir = func() (string, error) { return tmp, nil }
	t.Cleanup(func() { config.HomeDir = prev })
	if err := os.MkdirAll(filepath.Join(tmp, ".klyne"), 0o755); err != nil {
		t.Fatalf("mkdir .klyne: %v", err)
	}
	db, err := store.Open(config.DBPath())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, tmp
}

// seedRunbookFixture inserts a session and 3 bash messages so the
// proposer has enough data to surface at least one candidate.
// Session ids are prefixed with the projectPath so the same
// fixture can be seeded into multiple projects in one test
// without colliding on the messages PK.
func seedRunbookFixture(t *testing.T, db *store.DB, projectPath string) {
	t.Helper()
	ctx := context.Background()
	prefix := sanitizeProjectForID(projectPath)
	mkSess := func(id string, startMs int64) {
		s := &connectors.Session{
			ID:          prefix + "-" + id,
			CLI:         connectors.CLIClaude,
			ProjectPath: projectPath,
			StartedAt:   startMs,
			LastMsgAt:   startMs + 5_000,
			Status:      connectors.SessionStatusIdle,
			Model:       "claude-sonnet-4-7",
			RawPath:     "/dev/null/" + prefix + "/" + id,
		}
		if err := store.UpsertSession(ctx, db, s); err != nil {
			t.Fatalf("upsert session: %v", err)
		}
	}
	now := time.Now()
	mkSess("sess-a", now.Add(-2*time.Hour).UnixMilli())
	mkSess("sess-b", now.Add(-1*time.Hour).UnixMilli())
	mkSess("sess-c", now.Add(-30*time.Minute).UnixMilli())

	mkMsg := func(sess, role, content string, toolName, toolCmd string, ts int64) {
		full := prefix + "-" + sess
		m := &connectors.Message{
			ID:          full + "-" + role + "-" + toolCmd + "-" + time.Duration(ts).String(),
			SessionID:   full,
			CLI:         connectors.CLIClaude,
			ProjectPath: projectPath,
			Role:        connectors.Role(role),
			Content:     content,
			Ts:          ts,
			TokensIn:    100,
		}
		if toolName != "" {
			m.ToolCalls = []connectors.ToolCall{{
				ID:    toolName + "-id",
				Name:  toolName,
				Input: `{"command":"` + toolCmd + `"}`,
			}}
		}
		if err := store.InsertMessage(ctx, db, m); err != nil {
			t.Fatalf("insert msg: %v", err)
		}
	}

	// Recurring sequence "npm run build" → "npm run deploy" in
	// three different sessions. Each session has its own user
	// prompt at the start so the task-window boundary fires.
	for _, sess := range []string{"sess-a", "sess-b", "sess-c"} {
		base := now.Add(-2 * time.Hour).UnixMilli()
		mkMsg(sess, "user", "deploy please", "", "", base)
		mkMsg(sess, "assistant", "", "Bash", "npm run build", base+1_000)
		mkMsg(sess, "assistant", "", "Bash", "npm run deploy", base+2_000)
	}
}

// sanitizeProjectForID renders a project path into a string safe
// to use as a session-id prefix in fixtures (no slashes / colons).
func sanitizeProjectForID(p string) string {
	out := make([]byte, 0, len(p))
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case c >= 'a' && c <= 'z',
			c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9':
			out = append(out, c)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}

func TestHandleProposeRunbooks_HappyPath(t *testing.T) {
	db, _ := runbookTestDB(t)
	projectPath := "/proj/demo"
	seedRunbookFixture(t, db, projectPath)

	ctx := context.Background()
	_, out, err := HandleProposeRunbooks(ctx, nil, ProposeRunbooksInput{
		ProjectPath: projectPath,
		Limit:       5,
	})
	if err != nil {
		t.Fatalf("HandleProposeRunbooks: %v", err)
	}
	if out.ProjectPath != projectPath {
		t.Errorf("project_path echo wrong: %q", out.ProjectPath)
	}
	if len(out.Candidates) == 0 {
		t.Fatalf("expected at least one candidate from fixture")
	}
	c := out.Candidates[0]
	if c.DistinctSessions < 2 {
		t.Errorf("expected >=2 distinct sessions on top candidate, got %d", c.DistinctSessions)
	}
	if c.Occurrences < 3 {
		t.Errorf("expected >=3 occurrences, got %d", c.Occurrences)
	}
	if c.ID == "" || c.Signature == "" {
		t.Errorf("missing id or signature on candidate: %+v", c)
	}
	if out.Markdown == "" {
		t.Errorf("markdown body should always render, even on success")
	}
}

func TestHandleAcceptRunbook_PersistsAsMemory(t *testing.T) {
	db, _ := runbookTestDB(t)
	projectPath := "/proj/demo-accept"
	seedRunbookFixture(t, db, projectPath)
	ctx := context.Background()

	_, propOut, err := HandleProposeRunbooks(ctx, nil, ProposeRunbooksInput{
		ProjectPath: projectPath,
	})
	if err != nil || len(propOut.Candidates) == 0 {
		t.Fatalf("propose failed or empty: %v / %d", err, len(propOut.Candidates))
	}
	sig := propOut.Candidates[0].Signature

	_, accOut, err := HandleAcceptRunbook(ctx, nil, AcceptRunbookInput{
		Signature:   sig,
		ProjectPath: projectPath,
	})
	if err != nil {
		t.Fatalf("HandleAcceptRunbook: %v", err)
	}
	if accOut.MemoryID == "" {
		t.Fatalf("expected non-empty memory id")
	}
	rows, err := store.ListDecisions(ctx, db, store.DecisionFilter{
		ProjectPath: projectPath,
		Tag:         "runbook",
	})
	if err != nil {
		t.Fatalf("list decisions: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 runbook-tagged memory, got %d", len(rows))
	}
}

func TestHandleDismissRunbook_PreventsRePropose(t *testing.T) {
	db, _ := runbookTestDB(t)
	projectPath := "/proj/demo-dismiss"
	seedRunbookFixture(t, db, projectPath)
	ctx := context.Background()

	_, propOut, _ := HandleProposeRunbooks(ctx, nil, ProposeRunbooksInput{
		ProjectPath: projectPath,
	})
	if len(propOut.Candidates) == 0 {
		t.Fatalf("need a candidate to dismiss")
	}
	sig := propOut.Candidates[0].Signature

	_, _, err := HandleDismissRunbook(ctx, nil, DismissRunbookInput{
		Signature:   sig,
		ProjectPath: projectPath,
	})
	if err != nil {
		t.Fatalf("dismiss: %v", err)
	}

	_, again, _ := HandleProposeRunbooks(ctx, nil, ProposeRunbooksInput{
		ProjectPath: projectPath,
	})
	for _, c := range again.Candidates {
		if c.Signature == sig {
			t.Fatalf("dismissed signature still surfaced: %+v", c)
		}
	}
}

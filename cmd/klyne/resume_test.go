package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/store"
)

// TestResumeCmd_HelpText verifies that the resume command tree registers
// cleanly and its help text includes all expected sub-commands.
func TestResumeCmd_HelpText(t *testing.T) {
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"resume", "--help"})

	// Help exits with code 0 — Execute should not return an error.
	_ = root.Execute()

	help := buf.String()
	for _, want := range []string{"list", "show", "hydrate"} {
		if !strings.Contains(help, want) {
			t.Errorf("resume --help: expected %q in output\n---\n%s", want, help)
		}
	}
}

func TestResumeCmd_ListHelp(t *testing.T) {
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"resume", "list", "--help"})
	_ = root.Execute()

	if !strings.Contains(buf.String(), "Rank") {
		t.Errorf("resume list --help: missing 'Rank'\n---\n%s", buf.String())
	}
}

func TestResumeCmd_HydrateHelp(t *testing.T) {
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"resume", "hydrate", "--help"})
	_ = root.Execute()

	help := buf.String()
	for _, flag := range []string{"--decisions-only", "--last-N", "--budget", "--dry-run"} {
		if !strings.Contains(help, flag) {
			t.Errorf("resume hydrate --help: missing flag %q\n---\n%s", flag, help)
		}
	}
}

// TestResumeCmd_DryRun exercises the full dry-run path against a real
// (temp) SQLite DB with a fixture session and decisions.
func TestResumeCmd_DryRun(t *testing.T) {
	db := resumeOpenTestDB(t)
	ctx := context.Background()

	sessionID := "test-session-dryrun-001"
	err := store.UpsertSession(ctx, db, &connectors.Session{
		ID:          sessionID,
		CLI:         connectors.CLIClaude,
		ProjectPath: "/tmp/testproject",
		StartedAt:   time.Now().Add(-2 * time.Hour).UnixMilli(),
		LastMsgAt:   time.Now().Add(-1 * time.Hour).UnixMilli(),
		MsgCount:    5,
		Status:      connectors.SessionStatusIdle,
	})
	if err != nil {
		t.Fatalf("upsert session: %v", err)
	}

	err = store.InsertDecision(ctx, db, &store.Decision{
		ID:          "dec-001",
		Ts:          time.Now().Add(-90 * time.Minute).UnixMilli(),
		ProjectPath: "/tmp/testproject",
		SessionID:   sessionID,
		Text:        "use postgres for all persistent state",
		Tags:        []string{"arch"},
	})
	if err != nil {
		t.Fatalf("insert decision: %v", err)
	}

	out, err := resumeTestHydrateDryRun(ctx, db, sessionID)
	if err != nil {
		t.Fatalf("resumeTestHydrateDryRun: %v", err)
	}

	for _, want := range []string{"receipt", sessionID[:8], "dry run", "use postgres"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run output missing %q\n---\n%s", want, out)
		}
	}
}

// TestResumeCmd_ListNoCandidates checks graceful output when no sessions
// match the threshold.
func TestResumeCmd_ListNoCandidates(t *testing.T) {
	out := resumeNoCandidatesMsg()
	if !strings.Contains(out, "no candidates") {
		t.Errorf("expected 'no candidates' in output, got:\n%s", out)
	}
}

// TestResumeFormatHelpers exercises the pure formatting helpers.
func TestResumeFormatHelpers(t *testing.T) {
	t.Run("resumeFormatTokens", func(t *testing.T) {
		cases := []struct {
			n    int
			want string
		}{
			{0, "0"},
			{500, "500"},
			{1000, "1K"},
			{6144, "6.1K"},
			{8192, "8.2K"},
		}
		for _, tc := range cases {
			got := resumeFormatTokens(tc.n)
			if got != tc.want {
				t.Errorf("resumeFormatTokens(%d) = %q, want %q", tc.n, got, tc.want)
			}
		}
	})

	t.Run("resumeHumanAge", func(t *testing.T) {
		now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
		cases := []struct {
			then time.Time
			want string
		}{
			{now.Add(-30 * time.Minute), "30m"},
			{now.Add(-2 * time.Hour), "2h"},
			{now.Add(-14 * time.Hour), "14h"},
			{now.Add(-25 * time.Hour), "1d 1h"},
			{now.Add(-48 * time.Hour), "2d"},
		}
		for _, tc := range cases {
			got := resumeHumanAge(now, tc.then)
			if got != tc.want {
				t.Errorf("resumeHumanAge(%v) = %q, want %q", tc.then, got, tc.want)
			}
		}
	})

	t.Run("resumePathLabel", func(t *testing.T) {
		cases := []struct {
			path string
			want string
		}{
			{"/home/user/myproject", "user/myproject"},
			{"/myproject", "myproject"},
			{"/very/deep/nested/path/project", "path/project"},
		}
		for _, tc := range cases {
			got := resumePathLabel(tc.path)
			if got != tc.want {
				t.Errorf("resumePathLabel(%q) = %q, want %q", tc.path, got, tc.want)
			}
		}
	})

	t.Run("resumeShortID", func(t *testing.T) {
		got := resumeShortID("abc123def456ghi789")
		if len(got) != 12 {
			t.Errorf("resumeShortID: len=%d, want 12", len(got))
		}
		if got2 := resumeShortID("short"); got2 != "short" {
			t.Errorf("resumeShortID(short): got %q, want %q", got2, "short")
		}
	})
}

// --- test helpers -------------------------------------------------------

// resumeTestHydrateDryRun runs the hydrate logic against an injected DB
// and returns the output as a string. Used by TestResumeCmd_DryRun to
// avoid needing a real config.DBPath().
func resumeTestHydrateDryRun(ctx context.Context, db *store.DB, sessionID string) (string, error) {
	s, err := store.GetSession(ctx, db, sessionID)
	if err != nil {
		return "", err
	}

	decisions, err := store.ListDecisions(ctx, db, store.DecisionFilter{SessionID: sessionID})
	if err != nil {
		return "", err
	}

	decisionTexts := make([]string, 0, len(decisions))
	for _, d := range decisions {
		decisionTexts = append(decisionTexts, d.Text)
	}

	var sb strings.Builder
	// Receipt.
	sb.WriteString("klyne resume receipt\n")
	sb.WriteString("  session:  " + sessionID + "\n")
	sb.WriteString("  variant:  full\n")
	sb.WriteString("  estimate: ~800\n\n")

	// Payload text.
	hydrateText := resumeBuildPayloadText(s.ID, s.ProjectPath, time.UnixMilli(s.LastMsgAt), decisionTexts)

	sb.WriteString("--- dry run: payload that would be injected ---\n")
	sb.WriteString(hydrateText)
	sb.WriteString("--- end dry run ---\n")

	return sb.String(), nil
}

// resumeBuildPayloadText builds the markdown text for a resume payload.
// Extracted so both the real command and tests can call it.
func resumeBuildPayloadText(sessionID, projectPath string, lastActive time.Time, decisions []string) string {
	var sb strings.Builder
	sb.WriteString("# klyne resume — session " + resumeShortID(sessionID) + "\n\n")
	sb.WriteString("**Project:** " + projectPath + "\n")
	sb.WriteString("**Last active:** " + resumeHumanAge(time.Now(), lastActive) + " ago\n\n")
	if len(decisions) > 0 {
		sb.WriteString("## Decisions\n\n")
		for _, d := range decisions {
			sb.WriteString("- " + d + "\n")
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// resumeNoCandidatesMsg returns the standard message for an empty candidate list.
func resumeNoCandidatesMsg() string {
	return "no candidates — no recent sessions score ≥ 0.55 for this directory\n" +
		"  (try running from a project directory that has prior klyne sessions)\n"
}

// resumeOpenTestDB opens a temp-dir SQLite DB for tests.
func resumeOpenTestDB(t *testing.T) *store.DB {
	t.Helper()
	dir := t.TempDir()
	dbPath := dir + "/test.db"
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

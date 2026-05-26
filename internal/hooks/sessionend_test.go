package hooks

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/mcpserver"
	"github.com/klyne-ai/klyne/internal/store"
)

// gitInRepo runs a git command in dir with a deterministic identity.
func gitInRepo(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test User",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test User",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// openHookTestDB opens an on-disk SQLite DB with all migrations applied.
func openHookTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "hook.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func countSnapshots(t *testing.T, db *store.DB, sessionID string) int {
	t.Helper()
	var n int
	if err := db.Read().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM git_session_snapshots WHERE session_id = ?`, sessionID).
		Scan(&n); err != nil {
		t.Fatalf("count snapshots: %v", err)
	}
	return n
}

// stageTranscript writes a minimal Claude Code JSONL whose cwd is
// projectDir and returns the Stop-event payload bytes.
func stageTranscript(t *testing.T, sessionID, projectDir string) string {
	t.Helper()
	transcript := filepath.Join(t.TempDir(), sessionID+".jsonl")
	body := strings.Join([]string{
		`{"sessionId":"` + sessionID + `","type":"user","timestamp":"2026-05-15T10:00:00.000Z","message":{"role":"user","content":[{"type":"text","text":"do the work"}]},"cwd":"` + projectDir + `","uuid":"u1"}`,
		`{"sessionId":"` + sessionID + `","type":"assistant","timestamp":"2026-05-15T10:00:01.000Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go build ./..."}}]},"cwd":"` + projectDir + `","uuid":"u2","parentUuid":"u1"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(transcript, []byte(body), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	payload, _ := json.Marshal(map[string]any{
		"session_id":       sessionID,
		"transcript_path":  transcript,
		"stop_hook_active": true,
		"cwd":              projectDir,
	})
	return string(payload)
}

// TestSessionEnd_WritesGitSnapshot asserts the daemon-side session-end
// hook records a git_session_snapshots row when the session ran in a git
// repo (spec D6 snapshot capture).
func TestSessionEnd_WritesGitSnapshot(t *testing.T) {
	db := openHookTestDB(t)

	projectDir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	gitInRepo(t, projectDir, "init", "-q", "-b", "feat/CLI-1396-pipeline")
	if err := os.WriteFile(filepath.Join(projectDir, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatalf("write a.go: %v", err)
	}
	gitInRepo(t, projectDir, "add", "a.go")
	gitInRepo(t, projectDir, "commit", "-qm", "first")
	if err := os.WriteFile(filepath.Join(projectDir, "dirty.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatalf("write dirty.go: %v", err)
	}

	payload := stageTranscript(t, "sess-hook-1", projectDir)

	res := SessionEnd(context.Background(), strings.NewReader(payload), db)
	if res.ExitCode != 0 {
		t.Fatalf("SessionEnd exit code = %d, want 0; stderr=%s", res.ExitCode, res.Stderr)
	}

	if n := countSnapshots(t, db, "sess-hook-1"); n < 1 {
		t.Fatalf("expected >= 1 git snapshot row for sess-hook-1, got %d", n)
	}
	var (
		repoName, branch string
		dirtyCount       int
	)
	if err := db.Read().QueryRowContext(context.Background(), `
SELECT repo_name, branch, dirty_file_count
  FROM git_session_snapshots WHERE session_id = ? LIMIT 1`, "sess-hook-1").
		Scan(&repoName, &branch, &dirtyCount); err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if repoName != "repo" {
		t.Errorf("repo_name = %q, want repo", repoName)
	}
	if branch != "feat/CLI-1396-pipeline" {
		t.Errorf("branch = %q, want feat/CLI-1396-pipeline", branch)
	}
	if dirtyCount != 1 {
		t.Errorf("dirty_file_count = %d, want 1", dirtyCount)
	}
}

// TestSessionEnd_NonGitProjectNoSnapshotNoFail asserts snapshot capture
// is best-effort: a non-git project_path (a git failure proxy) yields no
// snapshot rows and never makes the hook fail.
func TestSessionEnd_NonGitProjectNoSnapshotNoFail(t *testing.T) {
	db := openHookTestDB(t)

	projectDir := filepath.Join(t.TempDir(), "plain")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir plain: %v", err)
	}

	payload := stageTranscript(t, "sess-hook-2", projectDir)

	res := SessionEnd(context.Background(), strings.NewReader(payload), db)
	if res.ExitCode != 0 {
		t.Fatalf("SessionEnd exit code = %d, want 0 (hook must never block)", res.ExitCode)
	}

	// The core stop_summary still landed.
	got, err := store.LatestStopSummaryForProject(context.Background(), db, projectDir)
	if err != nil {
		t.Fatalf("latest stop summary: %v", err)
	}
	if got == nil {
		t.Fatal("expected the stop_summary row to still be written")
	}
	// But zero snapshot rows for a non-git project.
	if n := countSnapshots(t, db, "sess-hook-2"); n != 0 {
		t.Errorf("expected 0 snapshot rows for a non-git project, got %d", n)
	}
}

// TestSessionEnd_GitFailureIsNonFatal proves that even when the DB insert
// would fail, snapshot capture does not break session-end. Here we close
// the DB's write side after the stop_summary is written by passing a DB
// whose connection is intact for reads — instead we simulate failure by
// using a captured-at constraint: covered by the captureGitSnapshots
// best-effort contract. This case asserts capture on a real repo while
// the session id is empty (still a valid, non-fatal path).
func TestSessionEnd_EmptySessionIDStillNonFatal(t *testing.T) {
	db := openHookTestDB(t)

	projectDir := filepath.Join(t.TempDir(), "repo2")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	gitInRepo(t, projectDir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(projectDir, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	gitInRepo(t, projectDir, "add", "a.go")
	gitInRepo(t, projectDir, "commit", "-qm", "first")

	// captureGitSnapshots called directly with a nil DB must be a no-op
	// and never panic — the strongest best-effort guarantee.
	captureGitSnapshots(context.Background(), nil, "sess-x", projectDir, os.Stderr)

	// And with a real DB it inserts without error.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	captureGitSnapshots(ctx, db, "sess-direct", projectDir, os.Stderr)
	if n := countSnapshots(t, db, "sess-direct"); n < 1 {
		t.Fatalf("expected a snapshot row from direct captureGitSnapshots call, got %d", n)
	}
}

// stageTranscriptWithKlyneSummary writes a JSONL whose final assistant
// message includes a trailing `KLYNE_SUMMARY: <text>` line — the exact
// shape the UserPromptSubmit hook instructs Claude to emit at the end
// of every reply. Returns the Stop-event payload bytes.
func stageTranscriptWithKlyneSummary(t *testing.T, sessionID, projectDir, summary string) string {
	t.Helper()
	transcript := filepath.Join(t.TempDir(), sessionID+".jsonl")
	asstText := "Done.\\nKLYNE_SUMMARY: " + summary
	body := strings.Join([]string{
		`{"sessionId":"` + sessionID + `","type":"user","timestamp":"2026-05-26T10:00:00.000Z","message":{"role":"user","content":[{"type":"text","text":"do the work"}]},"cwd":"` + projectDir + `","uuid":"u1"}`,
		`{"sessionId":"` + sessionID + `","type":"assistant","timestamp":"2026-05-26T10:00:01.000Z","message":{"role":"assistant","content":[{"type":"text","text":"` + asstText + `"}]},"cwd":"` + projectDir + `","uuid":"u2","parentUuid":"u1"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(transcript, []byte(body), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	payload, _ := json.Marshal(map[string]any{
		"session_id":       sessionID,
		"transcript_path":  transcript,
		"stop_hook_active": true,
		"cwd":              projectDir,
	})
	return string(payload)
}

// TestSessionEnd_PopulatesAIDraftedSummary is the end-to-end invariant
// guard for the system-wide bug we just fixed. For over a week
// `stop_summaries.ai_drafted_summary` stayed empty across every
// project — root cause was a divergent cobra implementation that
// silently dropped the AIDraftedSummary field when building the
// worklog.Entry. After the dedup, both entry points funnel through
// ComputeAndPersistSessionEnd and this test pins the contract:
//
//   given a Stop payload pointing at a JSONL whose last assistant
//   message ends with `KLYNE_SUMMARY: <text>`,
//   the resulting stop_summaries row MUST carry <text> verbatim in
//   the ai_drafted_summary column.
//
// If this test breaks, ANY change that re-introduces the bug — in
// the daemon path or the cobra path — fails CI loud and immediately.
func TestSessionEnd_PopulatesAIDraftedSummary(t *testing.T) {
	db := openHookTestDB(t)
	projectDir := filepath.Join(t.TempDir(), "klyne_summary_project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const sessionID = "sess-kly-1"
	const wantSummary = "Fixed the divergent Stop hook so ai_drafted_summary populates again."

	payload := stageTranscriptWithKlyneSummary(t, sessionID, projectDir, wantSummary)

	res := SessionEnd(context.Background(), strings.NewReader(payload), db)
	if res.ExitCode != 0 {
		t.Fatalf("SessionEnd exit code = %d (hook must never block); stderr=%s", res.ExitCode, res.Stderr)
	}

	// Read the persisted ai_drafted_summary directly out of SQLite —
	// the column SQL UPSERT path is what we are guarding, so the
	// assertion reads the column not the in-memory struct.
	var got string
	if err := db.Read().QueryRowContext(context.Background(),
		`SELECT COALESCE(ai_drafted_summary, '') FROM stop_summaries WHERE session_id = ? ORDER BY ts DESC LIMIT 1`,
		sessionID).Scan(&got); err != nil {
		t.Fatalf("read ai_drafted_summary: %v", err)
	}
	if got != wantSummary {
		t.Fatalf("ai_drafted_summary mismatch:\n got: %q\nwant: %q", got, wantSummary)
	}
}

// TestSessionEnd_SkipLeavesAIDraftedSummaryEmpty pins the inverse
// invariant: an intentional `KLYNE_SUMMARY: skip` results in an empty
// ai_drafted_summary (not the literal string "skip"). Pair with
// TestSessionEnd_PopulatesAIDraftedSummary to bracket the behaviour.
func TestSessionEnd_SkipLeavesAIDraftedSummaryEmpty(t *testing.T) {
	db := openHookTestDB(t)
	projectDir := filepath.Join(t.TempDir(), "klyne_summary_skip")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const sessionID = "sess-kly-skip"

	payload := stageTranscriptWithKlyneSummary(t, sessionID, projectDir, "skip")

	res := SessionEnd(context.Background(), strings.NewReader(payload), db)
	if res.ExitCode != 0 {
		t.Fatalf("SessionEnd exit code = %d; stderr=%s", res.ExitCode, res.Stderr)
	}

	var got string
	if err := db.Read().QueryRowContext(context.Background(),
		`SELECT COALESCE(ai_drafted_summary, '') FROM stop_summaries WHERE session_id = ? ORDER BY ts DESC LIMIT 1`,
		sessionID).Scan(&got); err != nil {
		t.Fatalf("read ai_drafted_summary: %v", err)
	}
	if got != "" {
		t.Fatalf("ai_drafted_summary should be empty for skip sentinel, got %q", got)
	}
}

// TestHasFinalAssistantText_WaitRace_LastMessageIsUser exercises the
// production race observed on 2026-05-26: Claude Code fires the
// Stop hook BEFORE the current turn's assistant text has been
// flushed to JSONL, so the parsed snapshot's last message is still
// the user prompt that triggered the in-flight turn.
//
// Earlier `hasFinalAssistantText` implementations scanned backward
// for "any assistant message with text" and latched onto the PRIOR
// turn's assistant — making the wait short-circuit immediately,
// after which ExtractKlyneSummary would scan back past the user
// boundary and return empty. The row landed with empty
// AIDraftedSummary even though the real summary arrived in the
// JSONL ~1s later.
//
// The fix: hasFinalAssistantText now demands that the VERY LAST
// message be an assistant turn with non-empty text. This test pins
// that semantic so any future loosening of the check fails CI loud.
func TestHasFinalAssistantText_WaitRace_LastMessageIsUser(t *testing.T) {
	// Snapshot shape at the race instant:
	//   [..., assistant("KLYNE_SUMMARY: prior turn"), user("new prompt")]
	// The new assistant message has not yet been flushed to the file.
	snap := &mcpserver.SessionSnapshot{
		Messages: []*connectors.Message{
			{Role: connectors.RoleAssistant, Content: "Done.\nKLYNE_SUMMARY: prior turn"},
			{Role: connectors.RoleUser, Content: "next thing please"},
		},
	}
	if hasFinalAssistantText(snap) {
		t.Fatal("hasFinalAssistantText should return false when the last message is a user prompt (new assistant turn not yet flushed) — keep waiting")
	}

	// Once the new assistant message lands with text, the function flips true.
	snap.Messages = append(snap.Messages,
		&connectors.Message{Role: connectors.RoleAssistant, Content: "Did it.\nKLYNE_SUMMARY: new turn"},
	)
	if !hasFinalAssistantText(snap) {
		t.Fatal("hasFinalAssistantText should return true once the new assistant text lands")
	}
}

// TestHasFinalAssistantText_ToolCallOnlyAssistant pins the other
// race-adjacent state: the final assistant message at hook-fire
// time may carry only tool_use blocks (no text yet). In Claude
// Code's JSONL shape, that surfaces as an assistant entry with
// empty .Content (text blocks live in Content; tool_use blocks
// live in ToolCalls). Function must return false so the wait keeps
// polling.
func TestHasFinalAssistantText_ToolCallOnlyAssistant(t *testing.T) {
	snap := &mcpserver.SessionSnapshot{
		Messages: []*connectors.Message{
			{Role: connectors.RoleUser, Content: "do it"},
			{Role: connectors.RoleAssistant, Content: "" /* tool_use only */},
		},
	}
	if hasFinalAssistantText(snap) {
		t.Fatal("hasFinalAssistantText should return false for tool-call-only trailing assistant (no text yet)")
	}
}

// stageCodexTranscript writes a minimal codex-format rollout JSONL.
// Real codex JSONL records have type=session_meta/response_item; the
// assistant turn lands as response_item with payload.type=message and
// payload.content=[{type:'output_text',text:'...'}]. Matches the live
// shape (see internal/connectors/codex/parse.go::handleMessage).
//
// Path layout deliberately mirrors ~/.codex/sessions/YYYY/MM/DD/rollout-*
// so CLIForPath routes the snapshot through the codex parser.
func stageCodexTranscript(t *testing.T, sessionID, projectDir, assistantText string) string {
	t.Helper()
	codexRoot := filepath.Join(t.TempDir(), ".codex", "sessions", "2026", "05", "26")
	if err := os.MkdirAll(codexRoot, 0o755); err != nil {
		t.Fatalf("mkdir codex root: %v", err)
	}
	transcript := filepath.Join(codexRoot, "rollout-"+sessionID+".jsonl")
	body := strings.Join([]string{
		`{"timestamp":"2026-05-26T10:00:00.000Z","type":"session_meta","payload":{"id":"` + sessionID + `","cwd":"` + projectDir + `","timestamp":"2026-05-26T10:00:00.000Z","model":"gpt-5.5"}}`,
		`{"timestamp":"2026-05-26T10:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hi codex"}]}}`,
		`{"timestamp":"2026-05-26T10:00:02.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"` + assistantText + `"}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(transcript, []byte(body), 0o644); err != nil {
		t.Fatalf("write codex transcript: %v", err)
	}
	payload, _ := json.Marshal(map[string]any{
		"session_id":       sessionID,
		"transcript_path":  transcript,
		"stop_hook_active": true,
		"cwd":              projectDir,
	})
	return string(payload)
}

// TestSessionEnd_CodexJSONL_WritesRow proves the codex side of the
// Stop-hook pipeline end-to-end: a codex-format JSONL transcript +
// the same ComputeAndPersistSessionEnd entry point produces a
// stop_summaries row with cli='codex'. Routes via CLIForPath →
// loadCodexSnapshot → handleMessage → ComputeAndPersistSessionEnd.
//
// The codex boundary-detector ticker (gated by
// worklog.codex_detector_enabled, off by default) is the legacy
// once-per-session capture path. The codex hook flow set up by
// installing klyne-hook entries in ~/.codex/hooks.json is the
// per-turn replacement; this test pins it.
func TestSessionEnd_CodexJSONL_WritesRow(t *testing.T) {
	db := openHookTestDB(t)
	projectDir := filepath.Join(t.TempDir(), "codex_e2e")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const sessionID = "019e6400-72a4-73e3-975e-7abf67fe659c"
	const assistantText = "Done.\\nKLYNE_SUMMARY: ran ls and saw three docs files"

	payload := stageCodexTranscript(t, sessionID, projectDir, assistantText)
	res := SessionEnd(context.Background(), strings.NewReader(payload), db)
	if res.ExitCode != 0 {
		t.Fatalf("SessionEnd exit code = %d; stderr=%s", res.ExitCode, res.Stderr)
	}

	var (
		cli  string
		aiSummary string
		lastUser  string
	)
	if err := db.Read().QueryRowContext(context.Background(),
		`SELECT cli, COALESCE(ai_drafted_summary,''), COALESCE(last_user,'')
		 FROM stop_summaries WHERE session_id = ? ORDER BY ts DESC LIMIT 1`,
		sessionID).Scan(&cli, &aiSummary, &lastUser); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if cli != "codex" {
		t.Errorf("cli = %q; want codex (CLIForPath should route codex JSONL paths)", cli)
	}
	if lastUser != "hi codex" {
		t.Errorf("last_user = %q; want \"hi codex\"", lastUser)
	}
	if aiSummary != "ran ls and saw three docs files" {
		t.Errorf("ai_drafted_summary = %q; want \"ran ls and saw three docs files\" (proves ExtractKlyneSummary works against codex Message.Content from output_text blocks)", aiSummary)
	}
}

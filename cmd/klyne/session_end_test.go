package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/store"
)

// session-end hook integration: build a small Claude Code JSONL on
// disk, feed the Stop-event payload to the hook, then read the
// stop_summaries row back out.

func TestSessionEndHook_PersistsSummary(t *testing.T) {
	tmp := t.TempDir()
	// klyne resolves its db path from config.DBPath() which honours
	// the config.HomeDir hook. Swap it at the package level so the
	// real ~/.klyne is never touched by this test.
	if err := os.MkdirAll(filepath.Join(tmp, ".klyne"), 0o755); err != nil {
		t.Fatalf("mkdir .klyne: %v", err)
	}
	prevHome := config.HomeDir
	config.HomeDir = func() (string, error) { return tmp, nil }
	t.Cleanup(func() { config.HomeDir = prevHome })

	// 1. Stage a tiny Claude Code transcript with a user prompt,
	//    an assistant tool-call sequence, and a final user prompt.
	projectDir := filepath.Join(tmp, "projects", "demo")
	if err := os.MkdirAll(filepath.Join(tmp, ".claude", "projects", "encoded"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	transcriptPath := filepath.Join(tmp, ".claude", "projects", "encoded", "demo-session.jsonl")
	body := strings.Join([]string{
		// Minimal Claude Code JSONL lines. The exact shape comes from
		// the claudeparse connector; this fixture is loose because
		// LoadSnapshot uses the same parser at runtime.
		`{"sessionId":"sess-demo","type":"user","timestamp":"2026-05-15T10:00:00.000Z","message":{"role":"user","content":[{"type":"text","text":"please deploy"}]},"cwd":"` + projectDir + `","uuid":"u1"}`,
		`{"sessionId":"sess-demo","type":"assistant","timestamp":"2026-05-15T10:00:01.000Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"npm run build"}}]},"cwd":"` + projectDir + `","uuid":"u2","parentUuid":"u1"}`,
		`{"sessionId":"sess-demo","type":"assistant","timestamp":"2026-05-15T10:00:02.000Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t2","name":"Read","input":{"file_path":"README.md"}}]},"cwd":"` + projectDir + `","uuid":"u3","parentUuid":"u2"}`,
		`{"sessionId":"sess-demo","type":"assistant","timestamp":"2026-05-15T10:00:03.000Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t3","name":"Bash","input":{"command":"npm run deploy"}}]},"cwd":"` + projectDir + `","uuid":"u4","parentUuid":"u3"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(transcriptPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	// 2. Build the Stop-event payload Claude Code would feed in.
	event := map[string]any{
		"session_id":       "sess-demo",
		"transcript_path":  transcriptPath,
		"stop_hook_active": true,
		"cwd":              projectDir,
	}
	payload, _ := json.Marshal(event)

	// 3. Run the hook body directly.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := computeAndPersistSessionEnd(ctx, strings.NewReader(string(payload))); err != nil {
		t.Fatalf("hook body: %v", err)
	}

	// 4. Read the stop_summary back from klyne's DB.
	db, err := store.Open(config.DBPath())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	got, err := store.LatestStopSummaryForProject(ctx, db, projectDir)
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if got == nil {
		t.Fatal("expected a stop_summary row")
	}
	if got.SessionID != "sess-demo" {
		t.Errorf("session_id = %q, want sess-demo", got.SessionID)
	}
	if !strings.Contains(got.Summary, "session-end summary") {
		t.Errorf("summary body missing header: %q", got.Summary)
	}
	if got.LastBash != "npm run deploy" {
		t.Errorf("last_bash = %q, want %q", got.LastBash, "npm run deploy")
	}
	if got.LastUser != "please deploy" {
		t.Errorf("last_user = %q, want %q", got.LastUser, "please deploy")
	}
	if len(got.Files) == 0 || got.Files[0] != "README.md" {
		t.Errorf("expected README.md in files, got %+v", got.Files)
	}
}

func TestSessionEndHook_NoStdinNoError(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	ctx := context.Background()
	if err := computeAndPersistSessionEnd(ctx, strings.NewReader("")); err != nil {
		t.Fatalf("empty stdin should be a silent no-op, got: %v", err)
	}
}

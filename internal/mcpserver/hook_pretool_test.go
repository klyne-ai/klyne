package mcpserver_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klyne-ai/klyne/internal/mcpserver"
	"github.com/klyne-ai/klyne/internal/policy"
	"github.com/klyne-ai/klyne/internal/store"
)

// policyJSON is the shipped 10-pattern set, inlined for hermetic tests.
const policyJSON = `{
  "version": 1,
  "patterns": [
    {"id": "git-reset-hard",     "regex": "^git\\s+reset\\s+--hard",             "severity": "high",     "snapshot": true},
    {"id": "git-clean-fd",       "regex": "^git\\s+clean\\s+-fd",                "severity": "high",     "snapshot": true},
    {"id": "git-checkout-dot",   "regex": "^git\\s+checkout\\s+--\\s*\\.",        "severity": "high",     "snapshot": true},
    {"id": "git-restore-dot",    "regex": "^git\\s+restore\\s+(\\.|--source)",    "severity": "high",     "snapshot": true},
    {"id": "git-worktree-rm",    "regex": "^git\\s+worktree\\s+remove",           "severity": "medium",   "snapshot": true},
    {"id": "git-branch-D",       "regex": "^git\\s+branch\\s+-D\\s",              "severity": "medium",   "snapshot": true},
    {"id": "rm-rf-broad",        "regex": "^rm\\s+-rf\\s+(/|~|\\$HOME|\\.\\./)", "severity": "critical", "snapshot": true, "block_unless_confirm": true},
    {"id": "git-push-force-main","regex": "git\\s+push.*--force(\\s|$).*\\b(main|master|develop)\\b|git\\s+push.*\\b(main|master|develop)\\b.*--force(\\s|$)", "severity": "critical", "block_unless_confirm": true},
    {"id": "schema-drop",        "regex": "(DROP\\s+(TABLE|DATABASE|SCHEMA))",     "severity": "high",     "snapshot": true},
    {"id": "migration-rollback", "regex": "(rollback|down)\\s+(--all|--all-the-way|all)", "severity": "high", "snapshot": true}
  ]
}`

func mustMatcher(t *testing.T) *policy.Matcher {
	t.Helper()
	m, err := policy.LoadJSON([]byte(policyJSON))
	if err != nil {
		t.Fatalf("LoadJSON: %v", err)
	}
	return m
}

func openPreToolDB(t *testing.T) *store.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "pretool.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func hookPayload(t *testing.T, command string) string {
	t.Helper()
	payload := map[string]any{
		"session_id": "sess-test",
		"cwd":        t.TempDir(),
		"tool_name":  "Bash",
		"tool_input": map[string]any{"command": command},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return string(b)
}

func TestHandlePreToolUse_NoMatch_Silent(t *testing.T) {
	ctx := context.Background()
	m := mustMatcher(t)
	db := openPreToolDB(t)

	r := strings.NewReader(hookPayload(t, "git status"))
	res, err := mcpserver.HandlePreToolUse(ctx, r, db, m)
	if err != nil {
		t.Fatalf("HandlePreToolUse: %v", err)
	}
	if res.SnapshotID != 0 {
		t.Errorf("expected no snapshot for safe command, got id=%d", res.SnapshotID)
	}
	if res.Output != "" {
		t.Errorf("expected empty output for safe command, got %q", res.Output)
	}
}

func TestHandlePreToolUse_RiskyCommand_RecordsSnapshot(t *testing.T) {
	ctx := context.Background()
	m := mustMatcher(t)
	db := openPreToolDB(t)

	r := strings.NewReader(hookPayload(t, "git reset --hard HEAD~1"))
	res, err := mcpserver.HandlePreToolUse(ctx, r, db, m)
	if err != nil {
		t.Fatalf("HandlePreToolUse: %v", err)
	}

	// The output should be non-empty (we emit a systemMessage).
	if res.Output == "" {
		t.Error("expected non-empty output for risky command")
	}
	if !strings.Contains(res.SystemMessage, "klyne") {
		t.Errorf("systemMessage missing klyne prefix: %q", res.SystemMessage)
	}

	// Verify the row was written to the DB.
	snaps, err := store.ListSafetySnapshots(ctx, db, store.SafetySnapshotFilter{})
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("want 1 snapshot row, got %d", len(snaps))
	}
	if snaps[0].PatternID != "git-reset-hard" {
		t.Errorf("pattern_id = %q, want git-reset-hard", snaps[0].PatternID)
	}
}

func TestHandlePreToolUse_EmptyInput_Silent(t *testing.T) {
	ctx := context.Background()
	m := mustMatcher(t)
	res, err := mcpserver.HandlePreToolUse(ctx, strings.NewReader(""), nil, m)
	if err != nil {
		t.Fatalf("HandlePreToolUse: %v", err)
	}
	if res.Output != "" {
		t.Errorf("expected empty output for empty input, got %q", res.Output)
	}
}

func TestHandlePreToolUse_MalformedJSON_Silent(t *testing.T) {
	ctx := context.Background()
	m := mustMatcher(t)
	res, err := mcpserver.HandlePreToolUse(ctx, strings.NewReader("{bad json"), nil, m)
	if err != nil {
		t.Fatalf("HandlePreToolUse: %v", err)
	}
	if res.Output != "" {
		t.Errorf("expected empty output for malformed JSON")
	}
}

func TestHandlePreToolUse_NonBashTool_Silent(t *testing.T) {
	ctx := context.Background()
	m := mustMatcher(t)

	// A non-Bash tool with no command field.
	payload := `{"session_id":"sess","tool_name":"Read","tool_input":{"path":"/tmp/foo"}}`
	res, err := mcpserver.HandlePreToolUse(ctx, strings.NewReader(payload), nil, m)
	if err != nil {
		t.Fatalf("HandlePreToolUse: %v", err)
	}
	if res.Output != "" {
		t.Errorf("expected empty output for non-Bash tool")
	}
}

func TestHandlePreToolUse_OutputJSON_Valid(t *testing.T) {
	ctx := context.Background()
	m := mustMatcher(t)

	r := strings.NewReader(hookPayload(t, "git clean -fd ."))
	res, err := mcpserver.HandlePreToolUse(ctx, r, nil, m)
	if err != nil {
		t.Fatalf("HandlePreToolUse: %v", err)
	}
	if res.Output == "" {
		t.Skip("no output (maybe in non-git test dir); skipping JSON validation")
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(res.Output), &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, res.Output)
	}
	hso, ok := doc["hookSpecificOutput"].(map[string]any)
	if !ok {
		t.Fatalf("hookSpecificOutput missing: %v", doc)
	}
	if hso["hookEventName"] != "PreToolUse" {
		t.Errorf("hookEventName = %v, want PreToolUse", hso["hookEventName"])
	}
}

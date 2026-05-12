package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/store"
)

// redirectHome points config.HomeDir() (and therefore config.DBPath()) at a
// fresh temp directory for the duration of the test. The returned path is
// the test's home root; the DB lives at config.DBPath() under it.
func redirectHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := config.HomeDir
	config.HomeDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { config.HomeDir = orig })
	return dir
}

// seedDB inserts one session with three messages, two Bash + one Read —
// enough material for the top / patterns / roast commands to produce
// non-trivial output.
func seedDB(t *testing.T, dbPath string) {
	t.Helper()
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open seed db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()

	s := &connectors.Session{
		ID:          "seed-A",
		CLI:         connectors.CLIClaude,
		ProjectPath: "/proj/x",
		StartedAt:   1_700_000_000_000,
		LastMsgAt:   1_700_000_000_000,
		Status:      connectors.SessionStatusActive,
		Model:       "claude-sonnet-4-5",
		RawPath:     "/tmp/seed.jsonl",
	}
	if err := store.UpsertSession(ctx, db, s); err != nil {
		t.Fatalf("UpsertSession: %v", err)
	}
	mk := func(id string, ts int64, name string, isErr bool, tokIn, cachedRead int64, cost float64) *connectors.Message {
		return &connectors.Message{
			ID:               id,
			SessionID:        s.ID,
			CLI:              connectors.CLIClaude,
			Role:             connectors.RoleAssistant,
			ToolCalls:        []connectors.ToolCall{{ID: id + "-c", Name: name}},
			ToolResults:      []connectors.ToolResult{{ID: id + "-c", IsError: isErr}},
			TokensIn:         tokIn,
			TokensOut:        20,
			CachedReadTokens: cachedRead,
			CostUSD:          cost,
			Model:            "claude-sonnet-4-5",
			Ts:               ts,
		}
	}
	msgs := []*connectors.Message{
		mk("m1", 1_700_000_000_001, "Bash", false, 1_000, 200, 0.10),
		mk("m2", 1_700_000_000_002, "Bash", true, 1_000, 200, 0.05),
		mk("m3", 1_700_000_000_003, "Read", false, 1_000, 500, 0.02),
	}
	for _, m := range msgs {
		if err := store.InsertMessage(ctx, db, m); err != nil {
			t.Fatalf("InsertMessage %s: %v", m.ID, err)
		}
	}
}

func runRootCmd(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := newRootCmd()
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs(args)
	err = root.ExecuteContext(context.Background())
	return out.String(), errb.String(), err
}

func TestTopCmd_E2E(t *testing.T) {
	redirectHome(t)
	if err := os.MkdirAll(filepath.Dir(config.DBPath()), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	seedDB(t, config.DBPath())

	out, _, err := runRootCmd(t, "top", "--json")
	if err != nil {
		t.Fatalf("klyne top: %v\n%s", err, out)
	}
	var resp struct {
		Tools []struct {
			Name       string `json:"name"`
			Count      int    `json:"count"`
			ErrorCount int    `json:"error_count"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	// Bash should be #1 with 2 calls, 1 error.
	if len(resp.Tools) == 0 || resp.Tools[0].Name != "Bash" {
		t.Fatalf("expected Bash first, got %+v", resp.Tools)
	}
	if resp.Tools[0].Count != 2 || resp.Tools[0].ErrorCount != 1 {
		t.Errorf("Bash row = %+v; want Count=2 ErrorCount=1", resp.Tools[0])
	}
}

func TestRoastCmd_E2E_NoSessions(t *testing.T) {
	redirectHome(t)
	if err := os.MkdirAll(filepath.Dir(config.DBPath()), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Open + close to create an empty DB so the CLI can attach.
	db, err := store.Open(config.DBPath())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_ = db.Close()

	out, _, err := runRootCmd(t, "roast")
	if err != nil {
		t.Fatalf("klyne roast: %v\n%s", err, out)
	}
	if !strings.Contains(out, "No sessions ingested yet") {
		t.Errorf("expected empty-state roast, got:\n%s", out)
	}
}

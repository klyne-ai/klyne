package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withFakeHomeForCursor isolates HOME for the installer tests so we
// don't touch the developer's real ~/.cursor/. Distinct from
// withFakeHomeForCodex / withFakeHome to avoid package-level name
// collisions across test files.
func withFakeHomeForCursor(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	return tmp
}

// TestInstallCursorHooks_FreshInstall asserts that on a fresh
// machine (no ~/.cursor/hooks.json) the installer creates the file
// with `version: 1`, every event from cursorHookEvents wired to the
// canonical klyne-hook command, and a per-event Added action.
func TestInstallCursorHooks_FreshInstall(t *testing.T) {
	home := withFakeHomeForCursor(t)
	const bin = "/opt/klyne/bin/klyne-hook"

	rep, err := InstallCursorHooks(bin)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if len(rep.Events) != len(cursorHookEvents) {
		t.Fatalf("rep.Events covered %d events; want %d", len(rep.Events), len(cursorHookEvents))
	}
	for _, e := range cursorHookEvents {
		if rep.Events[e] != InstallActionAdded {
			t.Errorf("event %q = %q on fresh install; want %q", e, rep.Events[e], InstallActionAdded)
		}
	}

	body, err := os.ReadFile(filepath.Join(home, ".cursor", "hooks.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		t.Fatalf("parse: %v\n%s", err, body)
	}
	if v, _ := root["version"].(float64); int(v) != 1 {
		t.Errorf("version = %v; want 1", root["version"])
	}
	hooks, _ := root["hooks"].(map[string]any)
	wantCmd := bin + " cursor"
	for _, e := range cursorHookEvents {
		arr, ok := hooks[e].([]any)
		if !ok || len(arr) == 0 {
			t.Errorf("event %q missing", e)
			continue
		}
		entry := arr[0].(map[string]any)
		if cmd, _ := entry["command"].(string); cmd != wantCmd {
			t.Errorf("event %q command = %q; want %q", e, cmd, wantCmd)
		}
	}
}

// TestInstallCursorHooks_Idempotent asserts re-running on identical
// state reports AlreadyInstalled for every event.
func TestInstallCursorHooks_Idempotent(t *testing.T) {
	withFakeHomeForCursor(t)
	const bin = "/opt/klyne/bin/klyne-hook"

	if _, err := InstallCursorHooks(bin); err != nil {
		t.Fatalf("first install: %v", err)
	}
	rep, err := InstallCursorHooks(bin)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	for e, a := range rep.Events {
		if a != InstallActionAlreadyInstalled {
			t.Errorf("event %q = %q on second install; want %q", e, a, InstallActionAlreadyInstalled)
		}
	}
}

// TestInstallCursorHooks_PreservesForeignHooks asserts the installer
// only touches its own klyne-named entries — any hook another tool
// (e.g. claude-mem's worker) has registered stays intact.
func TestInstallCursorHooks_PreservesForeignHooks(t *testing.T) {
	home := withFakeHomeForCursor(t)
	hooks := filepath.Join(home, ".cursor", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(hooks), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const seed = `{
  "version": 1,
  "hooks": {
    "stop": [
      { "command": "/opt/other-tool/run --on=stop" }
    ]
  }
}`
	if err := os.WriteFile(hooks, []byte(seed), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := InstallCursorHooks("/opt/klyne/bin/klyne-hook"); err != nil {
		t.Fatalf("install: %v", err)
	}
	body, _ := os.ReadFile(hooks)
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		t.Fatalf("parse: %v", err)
	}
	stopArr := root["hooks"].(map[string]any)["stop"].([]any)
	foreignSeen, klyneSeen := false, false
	for _, raw := range stopArr {
		cmd, _ := raw.(map[string]any)["command"].(string)
		if strings.Contains(cmd, "other-tool") {
			foreignSeen = true
		}
		if strings.Contains(cmd, "klyne") {
			klyneSeen = true
		}
	}
	if !foreignSeen {
		t.Error("foreign stop hook stripped — installer must only touch klyne entries")
	}
	if !klyneSeen {
		t.Error("klyne stop hook not installed")
	}
}

// TestInstallCursorHooks_RewritesLegacyKlyneEntry asserts that if an
// older klyne install left a stale command path, the installer
// upgrades it in-place.
func TestInstallCursorHooks_RewritesLegacyKlyneEntry(t *testing.T) {
	home := withFakeHomeForCursor(t)
	hooks := filepath.Join(home, ".cursor", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(hooks), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const seed = `{
  "version": 1,
  "hooks": {
    "sessionStart": [
      { "command": "/old/path/klyne-hook cursor" }
    ]
  }
}`
	if err := os.WriteFile(hooks, []byte(seed), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	rep, err := InstallCursorHooks("/new/path/klyne-hook")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if rep.Events["sessionStart"] != InstallActionUpdated {
		t.Errorf("sessionStart action = %q; want %q (legacy klyne entry should be replaced)", rep.Events["sessionStart"], InstallActionUpdated)
	}

	body, _ := os.ReadFile(hooks)
	if !strings.Contains(string(body), "/new/path/klyne-hook cursor") {
		t.Errorf("new command not written:\n%s", body)
	}
	if strings.Contains(string(body), "/old/path/klyne-hook") {
		t.Errorf("legacy klyne command not stripped:\n%s", body)
	}
}

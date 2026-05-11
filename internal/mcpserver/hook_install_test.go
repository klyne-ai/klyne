package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// installAdvisorHookFixture stages a fake HOME directory and
// returns the path InstallAdvisorHook would write to.
func installAdvisorHookFixture(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	settings := filepath.Join(dir, ".claude", "settings.json")
	return dir, settings
}

func TestInstallAdvisorHook_AddsToFreshSettings(t *testing.T) {
	_, settings := installAdvisorHookFixture(t)
	report, err := InstallAdvisorHook("/usr/local/bin/klyne")
	if err != nil {
		t.Fatalf("InstallAdvisorHook: %v", err)
	}
	if report.Action != InstallActionAdded {
		t.Fatalf("Action=%q, want %q", report.Action, InstallActionAdded)
	}
	body, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	hooks := doc["hooks"].(map[string]any)
	entries := hooks["UserPromptSubmit"].([]any)
	if len(entries) != 1 {
		t.Fatalf("entries=%d, want 1; doc=%s", len(entries), string(body))
	}
	first := entries[0].(map[string]any)
	if first["matcher"] != "*" {
		t.Fatalf("matcher=%v, want '*'", first["matcher"])
	}
	inner := first["hooks"].([]any)
	if len(inner) != 1 {
		t.Fatalf("inner=%d, want 1", len(inner))
	}
	cmd := inner[0].(map[string]any)["command"].(string)
	if !strings.Contains(cmd, "klyne") || !strings.HasSuffix(cmd, "advise") {
		t.Fatalf("unexpected command %q", cmd)
	}
}

func TestInstallAdvisorHook_PreservesExistingHooks(t *testing.T) {
	dir, settings := installAdvisorHookFixture(t)
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Pre-existing settings.json with an unrelated PreToolUse hook
	// and a different UserPromptSubmit matcher (e.g. "/git-*").
	existing := map[string]any{
		"theme": "dark",
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"matcher": "Bash",
					"hooks": []any{
						map[string]any{"type": "command", "command": "/usr/bin/audit-bash"},
					},
				},
			},
			"UserPromptSubmit": []any{
				map[string]any{
					"matcher": "/git-*",
					"hooks": []any{
						map[string]any{"type": "command", "command": "/usr/bin/git-helper"},
					},
				},
			},
		},
	}
	body, _ := json.MarshalIndent(existing, "", "  ")
	if err := os.WriteFile(settings, body, 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_ = dir

	report, err := InstallAdvisorHook("/usr/local/bin/klyne")
	if err != nil {
		t.Fatalf("InstallAdvisorHook: %v", err)
	}
	if report.Action != InstallActionAdded {
		t.Fatalf("Action=%q, want %q", report.Action, InstallActionAdded)
	}

	updated, _ := os.ReadFile(settings)
	var doc map[string]any
	if err := json.Unmarshal(updated, &doc); err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	hooks := doc["hooks"].(map[string]any)

	// PreToolUse should be untouched.
	preTool := hooks["PreToolUse"].([]any)
	if len(preTool) != 1 {
		t.Fatalf("PreToolUse altered: %d entries", len(preTool))
	}

	// UserPromptSubmit should now have BOTH the user's "/git-*"
	// and a fresh "*" entry with klyne advise.
	subs := hooks["UserPromptSubmit"].([]any)
	if len(subs) != 2 {
		t.Fatalf("UserPromptSubmit entries=%d, want 2; doc=%s", len(subs), string(updated))
	}
	found := false
	for _, raw := range subs {
		entry := raw.(map[string]any)
		if entry["matcher"] != "*" {
			continue
		}
		inner := entry["hooks"].([]any)
		for _, h := range inner {
			if cmd, _ := h.(map[string]any)["command"].(string); strings.Contains(cmd, "klyne") {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("klyne advise hook not found in updated settings")
	}

	// Theme survived.
	if doc["theme"] != "dark" {
		t.Fatalf("theme lost: %v", doc["theme"])
	}
}

func TestInstallAdvisorHook_Idempotent(t *testing.T) {
	_, settings := installAdvisorHookFixture(t)
	if _, err := InstallAdvisorHook("/usr/local/bin/klyne"); err != nil {
		t.Fatalf("first install: %v", err)
	}
	first, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	report, err := InstallAdvisorHook("/usr/local/bin/klyne")
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if report.Action != InstallActionAlreadyInstalled {
		t.Fatalf("Action=%q, want already-installed", report.Action)
	}
	second, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("re-read settings: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("file changed on idempotent re-install")
	}
}

func TestInstallAdvisorHook_UpdatesStaleBinaryPath(t *testing.T) {
	_, settings := installAdvisorHookFixture(t)
	if _, err := InstallAdvisorHook("/old/path/klyne"); err != nil {
		t.Fatalf("first install: %v", err)
	}

	report, err := InstallAdvisorHook("/new/path/klyne")
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if report.Action != InstallActionUpdated {
		t.Fatalf("Action=%q, want %q", report.Action, InstallActionUpdated)
	}
	body, _ := os.ReadFile(settings)
	if !strings.Contains(string(body), "/new/path/klyne advise") {
		t.Fatalf("new binary path not present: %s", body)
	}
	if strings.Contains(string(body), "/old/path/klyne advise") {
		t.Fatalf("old binary path still present: %s", body)
	}
}

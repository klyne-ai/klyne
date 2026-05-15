package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallPreCompactHook_AddsToFreshSettings(t *testing.T) {
	withFakeHome(t)

	report, err := InstallPreCompactHook("/usr/local/bin/klyne")
	if err != nil {
		t.Fatalf("InstallPreCompactHook: %v", err)
	}
	if report.Action != InstallActionAdded {
		t.Fatalf("Action=%q, want %q", report.Action, InstallActionAdded)
	}

	path, _ := claudeSettingsPath()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	hooks := doc["hooks"].(map[string]any)
	entries, ok := hooks["PreCompact"].([]any)
	if !ok || len(entries) == 0 {
		t.Fatalf("PreCompact entries missing: %v", doc)
	}
	found := false
	for _, raw := range entries {
		h, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if cmd, _ := h["command"].(string); strings.Contains(cmd, "klyne") && strings.HasSuffix(cmd, " precompact") {
			found = true
		}
	}
	if !found {
		t.Fatalf("klyne precompact hook not found in settings: %s", body)
	}
}

func TestInstallPreCompactHook_Idempotent(t *testing.T) {
	_, settings := installAdvisorHookFixture(t)

	if _, err := InstallPreCompactHook("/usr/local/bin/klyne"); err != nil {
		t.Fatalf("first install: %v", err)
	}
	first, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}

	report, err := InstallPreCompactHook("/usr/local/bin/klyne")
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

func TestInstallPreCompactHook_UpdatesStaleBinaryPath(t *testing.T) {
	_, settings := installAdvisorHookFixture(t)

	if _, err := InstallPreCompactHook("/old/path/klyne"); err != nil {
		t.Fatalf("first install: %v", err)
	}
	report, err := InstallPreCompactHook("/new/path/klyne")
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if report.Action != InstallActionUpdated {
		t.Fatalf("Action=%q, want updated", report.Action)
	}
	body, _ := os.ReadFile(settings)
	if !strings.Contains(string(body), "/new/path/klyne precompact") {
		t.Fatalf("new path not present: %s", body)
	}
	if strings.Contains(string(body), "/old/path/klyne precompact") {
		t.Fatalf("old path still present: %s", body)
	}
}

func TestInstallPreCompactHook_PreservesExistingHooks(t *testing.T) {
	home := withFakeHome(t)
	settings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}

	// Pre-install advisor and pretool hooks.
	existing := map[string]any{
		"hooks": map[string]any{
			"UserPromptSubmit": []any{
				map[string]any{
					"matcher": "*",
					"hooks": []any{
						map[string]any{"type": "command", "command": "/usr/local/bin/klyne advise"},
					},
				},
			},
			"PreToolUse": []any{
				map[string]any{
					"matcher": "*",
					"hooks": []any{
						map[string]any{"type": "command", "command": "/usr/local/bin/klyne pretool"},
					},
				},
			},
		},
	}
	body, _ := json.MarshalIndent(existing, "", "  ")
	if err := os.WriteFile(settings, body, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := InstallPreCompactHook("/usr/local/bin/klyne"); err != nil {
		t.Fatalf("install: %v", err)
	}

	updated, _ := os.ReadFile(settings)
	var doc map[string]any
	if err := json.Unmarshal(updated, &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	hooks := doc["hooks"].(map[string]any)

	// UserPromptSubmit should be untouched.
	if _, ok := hooks["UserPromptSubmit"]; !ok {
		t.Fatal("UserPromptSubmit missing after install")
	}
	// PreToolUse should be untouched.
	if _, ok := hooks["PreToolUse"]; !ok {
		t.Fatal("PreToolUse missing after install")
	}
	// PreCompact should have been added.
	if _, ok := hooks["PreCompact"]; !ok {
		t.Fatal("PreCompact missing after install")
	}
}

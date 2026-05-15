package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)


func TestInstallPreToolHook_AddsToFreshSettings(t *testing.T) {
	withFakeHome(t)

	report, err := InstallPreToolHook("/usr/local/bin/klyne")
	if err != nil {
		t.Fatalf("InstallPreToolHook: %v", err)
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
	entries, ok := hooks["PreToolUse"].([]any)
	if !ok || len(entries) == 0 {
		t.Fatalf("PreToolUse entries missing: %v", doc)
	}
	found := false
	for _, raw := range entries {
		entry := raw.(map[string]any)
		inner, _ := entry["hooks"].([]any)
		for _, h := range inner {
			if cmd, _ := h.(map[string]any)["command"].(string); strings.Contains(cmd, "klyne") && strings.HasSuffix(cmd, " pretool") {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("klyne pretool hook not found in settings: %s", body)
	}
}

func TestInstallPreToolHook_Idempotent(t *testing.T) {
	_, settings := installAdvisorHookFixture(t)

	if _, err := InstallPreToolHook("/usr/local/bin/klyne"); err != nil {
		t.Fatalf("first install: %v", err)
	}
	first, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}

	report, err := InstallPreToolHook("/usr/local/bin/klyne")
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

func TestInstallPreToolHook_UpdatesStaleBinaryPath(t *testing.T) {
	_, settings := installAdvisorHookFixture(t)

	if _, err := InstallPreToolHook("/old/path/klyne"); err != nil {
		t.Fatalf("first install: %v", err)
	}
	report, err := InstallPreToolHook("/new/path/klyne")
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if report.Action != InstallActionUpdated {
		t.Fatalf("Action=%q, want updated", report.Action)
	}
	body, _ := os.ReadFile(settings)
	if !strings.Contains(string(body), "/new/path/klyne pretool") {
		t.Fatalf("new path not present: %s", body)
	}
	if strings.Contains(string(body), "/old/path/klyne pretool") {
		t.Fatalf("old path still present: %s", body)
	}
}

func TestInstallPreToolHook_PreservesExistingAdvisorHook(t *testing.T) {
	home := withFakeHome(t)
	settings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}

	// Pre-install the advisor hook.
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
		},
	}
	body, _ := json.MarshalIndent(existing, "", "  ")
	if err := os.WriteFile(settings, body, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := InstallPreToolHook("/usr/local/bin/klyne"); err != nil {
		t.Fatalf("install: %v", err)
	}

	updated, _ := os.ReadFile(settings)
	var doc map[string]any
	if err := json.Unmarshal(updated, &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	hooks := doc["hooks"].(map[string]any)

	// UserPromptSubmit should be untouched.
	ups, ok := hooks["UserPromptSubmit"].([]any)
	if !ok || len(ups) != 1 {
		t.Fatalf("UserPromptSubmit altered: %v", hooks["UserPromptSubmit"])
	}

	// PreToolUse should have been added.
	_, ok = hooks["PreToolUse"]
	if !ok {
		t.Fatal("PreToolUse missing after install")
	}
}

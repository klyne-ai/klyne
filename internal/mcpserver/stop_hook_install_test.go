package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stop-hook install — parallels hook_install_test.go but for the
// Stop event (session-end summary writer).

func TestInstallStopHook_AddsToFreshSettings(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	settings := filepath.Join(dir, ".claude", "settings.json")

	report, err := InstallStopHook("/usr/local/bin/klyne")
	if err != nil {
		t.Fatalf("InstallStopHook: %v", err)
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
	hooks, ok := doc["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("hooks missing: %s", body)
	}
	entries, ok := hooks["Stop"].([]any)
	if !ok || len(entries) != 1 {
		t.Fatalf("Stop entries not as expected: %v", entries)
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
	if !strings.Contains(cmd, "klyne") || !strings.HasSuffix(cmd, "session-end") {
		t.Fatalf("unexpected command %q", cmd)
	}
}

func TestInstallStopHook_Idempotent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	if _, err := InstallStopHook("/usr/local/bin/klyne"); err != nil {
		t.Fatalf("first install: %v", err)
	}
	report, err := InstallStopHook("/usr/local/bin/klyne")
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if report.Action != InstallActionAlreadyInstalled {
		t.Fatalf("second-run action = %q, want already-installed", report.Action)
	}
}

func TestInstallStopHook_PreservesAdvisorHook(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	// First install the advisor hook.
	if _, err := InstallAdvisorHook("/usr/local/bin/klyne"); err != nil {
		t.Fatalf("install advisor: %v", err)
	}
	// Then install the Stop hook.
	if _, err := InstallStopHook("/usr/local/bin/klyne"); err != nil {
		t.Fatalf("install stop: %v", err)
	}

	settings := filepath.Join(dir, ".claude", "settings.json")
	body, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	hooks := doc["hooks"].(map[string]any)

	if _, ok := hooks["UserPromptSubmit"].([]any); !ok {
		t.Fatalf("advisor hook lost after Stop install: %s", body)
	}
	if _, ok := hooks["Stop"].([]any); !ok {
		t.Fatalf("Stop hook missing: %s", body)
	}
}

func TestInstallStopHook_UpdatesStaleEntry(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	settings := filepath.Join(dir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Pre-existing stale klyne session-end entry pointing at an old binary path.
	existing := map[string]any{
		"hooks": map[string]any{
			"Stop": []any{
				map[string]any{
					"matcher": "*",
					"hooks": []any{
						map[string]any{
							"type":    "command",
							"command": "/old/path/to/klyne session-end",
						},
					},
				},
			},
		},
	}
	body, _ := json.MarshalIndent(existing, "", "  ")
	if err := os.WriteFile(settings, body, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	report, err := InstallStopHook("/new/path/to/klyne")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if report.Action != InstallActionUpdated {
		t.Fatalf("Action=%q, want updated", report.Action)
	}
	body2, _ := os.ReadFile(settings)
	if strings.Contains(string(body2), "/old/path/to/klyne") {
		t.Fatalf("stale entry not replaced: %s", body2)
	}
	if !strings.Contains(string(body2), "/new/path/to/klyne session-end") {
		t.Fatalf("new entry not present: %s", body2)
	}
}

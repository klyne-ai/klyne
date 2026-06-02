package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withFakeHomeForCodex redirects HOME to a temp dir for the duration
// of the test so the installer's filepath resolution lands under it
// instead of touching the developer's real ~/.codex/. Returns the
// temp HOME. Named to avoid collision with sessions_test.go's
// withFakeHome helper.
func withFakeHomeForCodex(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	return tmp
}

// TestInstallCodexHooks_FreshInstall asserts that on a clean machine
// (no ~/.codex/hooks.json, no [features] section in config.toml) the
// installer creates both files with the canonical entries and the
// `hooks = true` feature flag.
func TestInstallCodexHooks_FreshInstall(t *testing.T) {
	home := withFakeHomeForCodex(t)
	const bin = "/opt/klyne/bin/klyne-hook"

	rep, err := InstallCodexHooks(bin)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	for _, a := range []InstallAction{rep.SessionStart, rep.UserPrompt, rep.PreToolUse, rep.Stop} {
		if a != InstallActionAdded {
			t.Errorf("expected each event to be %q on fresh install, got %q", InstallActionAdded, a)
		}
	}
	if rep.FeatureFlag != InstallActionAdded {
		t.Errorf("feature flag = %q; want %q on fresh install", rep.FeatureFlag, InstallActionAdded)
	}

	// hooks.json shape
	body, err := os.ReadFile(filepath.Join(home, ".codex", "hooks.json"))
	if err != nil {
		t.Fatalf("read hooks.json: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		t.Fatalf("parse hooks.json: %v\n%s", err, body)
	}
	hooks, _ := root["hooks"].(map[string]any)
	for _, event := range []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "Stop"} {
		arr, ok := hooks[event].([]any)
		if !ok || len(arr) == 0 {
			t.Errorf("event %q missing or empty", event)
			continue
		}
		entry := arr[0].(map[string]any)
		inner := entry["hooks"].([]any)
		hookObj := inner[0].(map[string]any)
		cmd, _ := hookObj["command"].(string)
		if !strings.HasPrefix(cmd, bin+" ") {
			t.Errorf("event %q command = %q; want prefix %q", event, cmd, bin+" ")
		}
	}

	// config.toml content
	cfg, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	if !strings.Contains(string(cfg), "[features]") {
		t.Errorf("config.toml missing [features] section:\n%s", cfg)
	}
	if !strings.Contains(string(cfg), "hooks = true") {
		t.Errorf("config.toml missing hooks=true:\n%s", cfg)
	}
}

// TestInstallCodexHooks_Idempotent asserts that re-running the
// installer on the same state reports AlreadyInstalled for every
// surface and does not modify either file.
func TestInstallCodexHooks_Idempotent(t *testing.T) {
	withFakeHomeForCodex(t)
	const bin = "/opt/klyne/bin/klyne-hook"

	if _, err := InstallCodexHooks(bin); err != nil {
		t.Fatalf("first install: %v", err)
	}
	rep, err := InstallCodexHooks(bin)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	for label, a := range map[string]InstallAction{
		"SessionStart": rep.SessionStart,
		"UserPrompt":   rep.UserPrompt,
		"PreToolUse":   rep.PreToolUse,
		"Stop":         rep.Stop,
		"FeatureFlag":  rep.FeatureFlag,
	} {
		if a != InstallActionAlreadyInstalled {
			t.Errorf("%s = %q on second install; want %q", label, a, InstallActionAlreadyInstalled)
		}
	}
}

// TestInstallCodexHooks_MigratesDeprecatedFlag asserts the installer
// upgrades the legacy `codex_hooks = true` flag to the canonical
// `hooks = true` form in a single pass — the same migration codex-cli
// 0.133 requires.
func TestInstallCodexHooks_MigratesDeprecatedFlag(t *testing.T) {
	home := withFakeHomeForCodex(t)
	cfg := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const before = `model = 'gpt-5.5'

[features]
codex_hooks = true
js_repl = false

[mcp_servers]
`
	if err := os.WriteFile(cfg, []byte(before), 0o600); err != nil {
		t.Fatalf("seed config.toml: %v", err)
	}

	rep, err := InstallCodexHooks("/opt/klyne/bin/klyne-hook")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if rep.FeatureFlag != InstallActionUpdated {
		t.Errorf("feature flag action = %q; want %q (migration from codex_hooks → hooks)",
			rep.FeatureFlag, InstallActionUpdated)
	}

	got, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	gotS := string(got)
	if strings.Contains(gotS, "codex_hooks") {
		t.Errorf("legacy `codex_hooks` not removed:\n%s", gotS)
	}
	if !strings.Contains(gotS, "hooks = true") {
		t.Errorf("canonical `hooks = true` missing:\n%s", gotS)
	}
	// Surrounding sections should be preserved untouched.
	if !strings.Contains(gotS, "[mcp_servers]") {
		t.Errorf("[mcp_servers] section lost in migration:\n%s", gotS)
	}
	if !strings.Contains(gotS, "model = 'gpt-5.5'") {
		t.Errorf("non-features content lost in migration:\n%s", gotS)
	}
}

// TestInstallCodexHooks_PreservesForeignHooks asserts the installer
// only touches its own klyne-named hook entries — any other hook the
// user has installed (e.g. claude-mem's worker) stays intact.
func TestInstallCodexHooks_PreservesForeignHooks(t *testing.T) {
	home := withFakeHomeForCodex(t)
	hooks := filepath.Join(home, ".codex", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(hooks), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const seed = `{
  "hooks": {
    "Stop": [
      {
        "hooks": [
          { "type": "command", "command": "/opt/other-tool/run --on=stop" }
        ]
      }
    ]
  }
}`
	if err := os.WriteFile(hooks, []byte(seed), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := InstallCodexHooks("/opt/klyne/bin/klyne-hook"); err != nil {
		t.Fatalf("install: %v", err)
	}

	body, _ := os.ReadFile(hooks)
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		t.Fatalf("parse: %v\n%s", err, body)
	}
	stopEntries := root["hooks"].(map[string]any)["Stop"].([]any)

	foreignSeen := false
	klyneSeen := false
	for _, raw := range stopEntries {
		entry := raw.(map[string]any)
		for _, h := range entry["hooks"].([]any) {
			cmd, _ := h.(map[string]any)["command"].(string)
			if strings.Contains(cmd, "other-tool") {
				foreignSeen = true
			}
			if strings.Contains(cmd, "klyne") {
				klyneSeen = true
			}
		}
	}
	if !foreignSeen {
		t.Error("foreign Stop hook command was stripped — installer must only touch klyne entries")
	}
	if !klyneSeen {
		t.Error("klyne Stop hook command was not installed")
	}
}

// TestInstallCodexHooks_PrefixCollision_NotAlreadyInstalled guards the
// feature-flag key parser: a sibling key like `hooks_experimental =
// true` must NOT be misread as the canonical `hooks = true`. Before the
// fix, a `HasPrefix(trimmed,"hooks") && Contains(trimmed,"true")` check
// reported AlreadyInstalled and never wrote the real flag, so Codex
// capture silently never fired. After the fix the installer must add
// `hooks = true`.
func TestInstallCodexHooks_PrefixCollision_NotAlreadyInstalled(t *testing.T) {
	home := withFakeHomeForCodex(t)
	cfg := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const before = `[features]
hooks_experimental = true
`
	if err := os.WriteFile(cfg, []byte(before), 0o600); err != nil {
		t.Fatalf("seed config.toml: %v", err)
	}

	rep, err := InstallCodexHooks("/opt/klyne/bin/klyne-hook")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if rep.FeatureFlag == InstallActionAlreadyInstalled {
		t.Fatalf("feature flag action = AlreadyInstalled; want Added — `hooks_experimental` must not satisfy the `hooks` flag")
	}

	got, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	gotS := string(got)
	if !strings.Contains(gotS, "hooks = true") {
		t.Errorf("canonical `hooks = true` was not written:\n%s", gotS)
	}
	if !strings.Contains(gotS, "hooks_experimental = true") {
		t.Errorf("sibling key `hooks_experimental` was clobbered:\n%s", gotS)
	}
}

// TestInstallCodexHooks_QuotedUntrueNotTrue guards the value parser: a
// string value like `hooks = "untrue"` must NOT count as the boolean
// `true`. Before the fix a `Contains(trimmed,"true")` check matched it
// and reported AlreadyInstalled. After the fix the installer recognises
// the `hooks` key but sees the value is not the boolean true, so it
// FLIPS the line to `hooks = true` (Updated).
func TestInstallCodexHooks_QuotedUntrueNotTrue(t *testing.T) {
	home := withFakeHomeForCodex(t)
	cfg := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const before = `[features]
hooks = "untrue"
`
	if err := os.WriteFile(cfg, []byte(before), 0o600); err != nil {
		t.Fatalf("seed config.toml: %v", err)
	}

	rep, err := InstallCodexHooks("/opt/klyne/bin/klyne-hook")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if rep.FeatureFlag == InstallActionAlreadyInstalled {
		t.Fatalf("feature flag action = AlreadyInstalled; want Updated — `hooks = \"untrue\"` is a string, not the boolean true")
	}

	got, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(got), "hooks = true") {
		t.Errorf("`hooks = true` was not written:\n%s", string(got))
	}
}

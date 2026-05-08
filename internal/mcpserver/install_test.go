package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestDetectAvailablePlatforms_NoConfigs(t *testing.T) {
	withFakeHome(t)
	got := DetectAvailablePlatforms()
	if len(got) != 0 {
		t.Errorf("got %v, want empty (no configs exist)", got)
	}
}

func TestDetectAvailablePlatforms_OnlyClaude(t *testing.T) {
	home := withFakeHome(t)
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatalf("write claude: %v", err)
	}
	got := DetectAvailablePlatforms()
	if len(got) != 1 || got[0] != PlatformClaude {
		t.Errorf("got %v, want [claude]", got)
	}
}

func TestDetectAvailablePlatforms_OnlyCodex(t *testing.T) {
	home := withFakeHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatalf("mkdir codex: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte(""), 0o600); err != nil {
		t.Fatalf("write codex: %v", err)
	}
	got := DetectAvailablePlatforms()
	if len(got) != 1 || got[0] != PlatformCodex {
		t.Errorf("got %v, want [codex]", got)
	}
}

func TestInstallForPlatform_ClaudeAdded(t *testing.T) {
	home := withFakeHome(t)
	// Pre-existing file with another server — must be preserved.
	existing := `{"mcpServers":{"other":{"command":"/usr/bin/other","args":["go"]}}}`
	path := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	report, err := InstallForPlatform(PlatformClaude, "/usr/local/bin/agentdeck")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if report.Action != InstallActionAdded {
		t.Errorf("Action = %q, want %q", report.Action, InstallActionAdded)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("parse back: %v", err)
	}
	servers, _ := parsed["mcpServers"].(map[string]any)
	if _, ok := servers["other"]; !ok {
		t.Errorf("existing 'other' server was lost: %v", servers)
	}
	ad, ok := servers["klyne"].(map[string]any)
	if !ok {
		t.Fatalf("klyne entry missing or wrong shape: %v", servers["klyne"])
	}
	if ad["command"] != "/usr/local/bin/agentdeck" {
		t.Errorf("command = %v, want /usr/local/bin/agentdeck", ad["command"])
	}
	args, _ := ad["args"].([]any)
	if len(args) != 1 || args[0] != "mcp" {
		t.Errorf("args = %v, want [mcp]", args)
	}
}

func TestInstallForPlatform_ClaudeIdempotent(t *testing.T) {
	home := withFakeHome(t)
	path := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := InstallForPlatform(PlatformClaude, "/bin/agentdeck"); err != nil {
		t.Fatalf("install 1: %v", err)
	}
	r2, err := InstallForPlatform(PlatformClaude, "/bin/agentdeck")
	if err != nil {
		t.Fatalf("install 2: %v", err)
	}
	if r2.Action != InstallActionAlreadyInstalled {
		t.Errorf("second install Action = %q, want %q", r2.Action, InstallActionAlreadyInstalled)
	}
}

// TestInstallForPlatform_RemovesLegacyAgentdeckEntry pins the migration
// behaviour from the rebrand: the install command writes the new
// `klyne` entry AND removes the legacy `agentdeck` entry from the
// config so users don't end up with both registered after upgrading.
func TestInstallForPlatform_ClaudeRemovesLegacyAgentdeckEntry(t *testing.T) {
	home := withFakeHome(t)
	path := filepath.Join(home, ".claude.json")
	// Pre-existing config carries the LEGACY "agentdeck" entry from
	// before the rebrand. Seed must use the literal old key.
	if err := os.WriteFile(path, []byte(`{"mcpServers":{"agentdeck":{"command":"/old/agentdeck","args":["mcp"]}}}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := InstallForPlatform(PlatformClaude, "/new/klyne"); err != nil {
		t.Fatalf("install: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("parse: %v", err)
	}
	servers, _ := parsed["mcpServers"].(map[string]any)
	if _, ok := servers["agentdeck"]; ok {
		t.Errorf("legacy 'agentdeck' entry was not removed: %v", servers)
	}
	if _, ok := servers["klyne"]; !ok {
		t.Errorf("'klyne' entry was not added: %v", servers)
	}
}

func TestInstallForPlatform_CodexRemovesLegacyAgentdeckEntry(t *testing.T) {
	home := withFakeHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(home, ".codex", "config.toml")
	// Pre-existing config carries the LEGACY [mcp_servers.agentdeck] table.
	existing := "model = \"gpt-5\"\n\n[mcp_servers.agentdeck]\ncommand = \"/old/agentdeck\"\nargs = [\"mcp\"]\n"
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := InstallForPlatform(PlatformCodex, "/new/klyne"); err != nil {
		t.Fatalf("install: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var parsed map[string]any
	if err := toml.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("parse: %v", err)
	}
	servers, _ := parsed["mcp_servers"].(map[string]any)
	if _, ok := servers["agentdeck"]; ok {
		t.Errorf("legacy 'agentdeck' entry was not removed: %v", servers)
	}
	if _, ok := servers["klyne"]; !ok {
		t.Errorf("'klyne' entry was not added: %v", servers)
	}
}

func TestInstallForPlatform_ClaudeUpdatesStalePath(t *testing.T) {
	home := withFakeHome(t)
	path := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(path, []byte(`{"mcpServers":{"klyne":{"command":"/old/path","args":["mcp"]}}}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	r, err := InstallForPlatform(PlatformClaude, "/new/path/agentdeck")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if r.Action != InstallActionUpdated {
		t.Errorf("Action = %q, want %q", r.Action, InstallActionUpdated)
	}
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), "/new/path/agentdeck") {
		t.Errorf("file did not pick up new path: %s", body)
	}
}

func TestInstallForPlatform_CodexAdded(t *testing.T) {
	home := withFakeHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(home, ".codex", "config.toml")
	// Pre-existing TOML with hand-curated content the install must preserve.
	existing := `model = "gpt-5"
personality = "pragmatic"

[features]
codex_hooks = true

[projects."/Users/x/proj-a"]
trust_level = "trusted"
`
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	r, err := InstallForPlatform(PlatformCodex, "/usr/local/bin/agentdeck")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if r.Action != InstallActionAdded {
		t.Errorf("Action = %q, want %q", r.Action, InstallActionAdded)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var parsed map[string]any
	if err := toml.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("parse back: %v", err)
	}
	// Pre-existing keys preserved.
	if parsed["model"] != "gpt-5" {
		t.Errorf("model lost: %v", parsed["model"])
	}
	feats, ok := parsed["features"].(map[string]any)
	if !ok || feats["codex_hooks"] != true {
		t.Errorf("features.codex_hooks lost: %v", parsed["features"])
	}
	projects, _ := parsed["projects"].(map[string]any)
	if _, ok := projects["/Users/x/proj-a"]; !ok {
		t.Errorf("projects entry lost: %v", projects)
	}
	servers, _ := parsed["mcp_servers"].(map[string]any)
	ad, ok := servers["klyne"].(map[string]any)
	if !ok {
		t.Fatalf("klyne entry missing: %v", servers)
	}
	if ad["command"] != "/usr/local/bin/agentdeck" {
		t.Errorf("command = %v", ad["command"])
	}
}

func TestInstallForPlatform_CodexIdempotent(t *testing.T) {
	home := withFakeHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(home, ".codex", "config.toml")
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := InstallForPlatform(PlatformCodex, "/bin/agentdeck"); err != nil {
		t.Fatalf("install 1: %v", err)
	}
	r2, err := InstallForPlatform(PlatformCodex, "/bin/agentdeck")
	if err != nil {
		t.Fatalf("install 2: %v", err)
	}
	if r2.Action != InstallActionAlreadyInstalled {
		t.Errorf("second install Action = %q, want %q", r2.Action, InstallActionAlreadyInstalled)
	}
}

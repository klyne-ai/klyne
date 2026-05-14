package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInstallSkills_Added verifies that a fresh install creates the
// klyne-health skill bundle, returns the "added" action, and writes the
// expected SKILL.md content into ~/.claude/skills/klyne-health/.
func TestInstallSkills_Added(t *testing.T) {
	home := withFakeHome(t)

	report, err := InstallSkills()
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if report.Action != InstallActionAdded {
		t.Errorf("Action = %q, want %q", report.Action, InstallActionAdded)
	}
	if report.Files == 0 {
		t.Error("Files = 0, want > 0")
	}
	if len(report.Skills) == 0 {
		t.Fatal("Skills empty, want at least klyne-health")
	}

	wantSkill := "klyne-health"
	found := false
	for _, s := range report.Skills {
		if s == wantSkill {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Skills = %v, want to contain %q", report.Skills, wantSkill)
	}

	skillFile := filepath.Join(home, ".claude", "skills", wantSkill, "SKILL.md")
	body, err := os.ReadFile(skillFile)
	if err != nil {
		t.Fatalf("read installed skill: %v", err)
	}
	bodyStr := string(body)

	// Frontmatter contract — the description is what Claude matches on
	// for auto-invocation. Locking the structure here keeps a future
	// edit from accidentally breaking the bundle's discoverability.
	if !strings.HasPrefix(bodyStr, "---\n") {
		t.Error("SKILL.md does not start with YAML frontmatter delimiter")
	}
	if !strings.Contains(bodyStr, "name: klyne-health") {
		t.Error("SKILL.md missing required `name: klyne-health` frontmatter")
	}
	if !strings.Contains(bodyStr, "description:") {
		t.Error("SKILL.md missing required `description:` frontmatter")
	}
	// The body must reference the MCP tool it wraps — otherwise the
	// skill can't actually do anything.
	if !strings.Contains(bodyStr, "mcp__klyne__get_context_health") {
		t.Error("SKILL.md does not reference mcp__klyne__get_context_health")
	}
}

// TestInstallSkills_AlreadyInstalled verifies idempotency: re-running
// install on byte-identical content reports "already-installed".
func TestInstallSkills_AlreadyInstalled(t *testing.T) {
	withFakeHome(t)

	if _, err := InstallSkills(); err != nil {
		t.Fatalf("first install: %v", err)
	}
	report, err := InstallSkills()
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if report.Action != InstallActionAlreadyInstalled {
		t.Errorf("Action = %q, want %q", report.Action, InstallActionAlreadyInstalled)
	}
}

// TestInstallSkills_Updated verifies that re-running install over a
// stale SKILL.md rewrites it and reports "updated".
func TestInstallSkills_Updated(t *testing.T) {
	home := withFakeHome(t)

	if _, err := InstallSkills(); err != nil {
		t.Fatalf("first install: %v", err)
	}
	// Corrupt the installed file so the next install must rewrite it.
	stale := filepath.Join(home, ".claude", "skills", "klyne-health", "SKILL.md")
	if err := os.WriteFile(stale, []byte("# stale\n"), 0o644); err != nil {
		t.Fatalf("corrupt skill: %v", err)
	}
	report, err := InstallSkills()
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if report.Action != InstallActionUpdated {
		t.Errorf("Action = %q, want %q", report.Action, InstallActionUpdated)
	}
}

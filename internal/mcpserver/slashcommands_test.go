package mcpserver

import (
	"os"
	"path/filepath"
	"testing"
)

// TestInstallSlashCommands_RemovesStaleFiles seeds the destination
// directory with an .md file that does NOT exist in the embed FS and
// verifies that re-running InstallSlashCommands removes it. This is
// the behaviour that lets us retire `/klyne:tokens` and `/klyne:health`
// cleanly when their files are dropped from slashcommands/.
func TestInstallSlashCommands_RemovesStaleFiles(t *testing.T) {
	home := withFakeHome(t)
	dest := filepath.Join(home, ".claude", "commands", "klyne")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatalf("mkdir dest: %v", err)
	}
	stale := filepath.Join(dest, "no-longer-in-embed.md")
	if err := os.WriteFile(stale, []byte("legacy"), 0o644); err != nil {
		t.Fatalf("seed stale file: %v", err)
	}

	if _, err := InstallSlashCommands(); err != nil {
		t.Fatalf("InstallSlashCommands: %v", err)
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale file should have been removed; stat err = %v", err)
	}
}

// TestInstallSlashCommands_KeepsUnrelatedSubdirsAndNonMdFiles confirms
// the cleanup pass is conservative: it only removes top-level .md files
// whose basename is not in the embed FS. Sub-directories and non-.md
// files are left alone — the user might have parked unrelated commands
// there.
func TestInstallSlashCommands_KeepsUnrelatedSubdirsAndNonMdFiles(t *testing.T) {
	home := withFakeHome(t)
	dest := filepath.Join(home, ".claude", "commands", "klyne")
	if err := os.MkdirAll(filepath.Join(dest, "user-subdir"), 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	nonMd := filepath.Join(dest, "user-notes.txt")
	if err := os.WriteFile(nonMd, []byte("notes"), 0o644); err != nil {
		t.Fatalf("seed non-md file: %v", err)
	}

	if _, err := InstallSlashCommands(); err != nil {
		t.Fatalf("InstallSlashCommands: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dest, "user-subdir")); err != nil {
		t.Errorf("user subdir should be preserved: %v", err)
	}
	if _, err := os.Stat(nonMd); err != nil {
		t.Errorf("non-md file should be preserved: %v", err)
	}
}

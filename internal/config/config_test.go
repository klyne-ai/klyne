package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// setTempConfigDir redirects ConfigDir (and therefore ConfigFile, DBPath,
// PricingOverridePath) to a temporary directory for the duration of the test.
// It overrides both HomeDir and, on Windows, the USERPROFILE env var.
func setTempConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	origHomeDir := HomeDir
	HomeDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { HomeDir = origHomeDir })

	// On Windows ConfigDir() may read USERPROFILE before calling HomeDir.
	// Override that env var too so the path is deterministic.
	origProfile := os.Getenv("USERPROFILE")
	if err := os.Setenv("USERPROFILE", dir); err == nil {
		t.Cleanup(func() { _ = os.Setenv("USERPROFILE", origProfile) })
	}

	return dir
}

// -----------------------------------------------------------------------
// TestLoad_DefaultsWhenMissing
// -----------------------------------------------------------------------

func TestLoad_DefaultsWhenMissing(t *testing.T) {
	setTempConfigDir(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}

	// The returned config must match the documented defaults.
	def := Defaults()
	if cfg.Server.Addr != def.Server.Addr {
		t.Errorf("Server.Addr = %q; want %q", cfg.Server.Addr, def.Server.Addr)
	}
	if cfg.Connectors.Claude.Enabled != def.Connectors.Claude.Enabled {
		t.Errorf("Claude.Enabled = %v; want %v", cfg.Connectors.Claude.Enabled, def.Connectors.Claude.Enabled)
	}
	// The [ai] table was removed when daemon-side AI synthesis was
	// retired (2026-05-21) — Defaults() no longer carries AI.* fields.

	// Load() must have written the file so a subsequent call finds it.
	cfgFile := ConfigFile()
	if _, statErr := os.Stat(cfgFile); os.IsNotExist(statErr) {
		t.Errorf("Load() did not write config file at %s", cfgFile)
	}
}

// -----------------------------------------------------------------------
// TestSave_Load_RoundTrip
// -----------------------------------------------------------------------

func TestSave_Load_RoundTrip(t *testing.T) {
	setTempConfigDir(t)

	// Modify a few fields and save.
	cfg := Defaults()
	cfg.Server.Addr = "127.0.0.1:9999"
	cfg.Connectors.Codex.Enabled = false

	if err := Save(cfg); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	// Load it back.
	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() after Save() error: %v", err)
	}

	if loaded.Server.Addr != "127.0.0.1:9999" {
		t.Errorf("Server.Addr = %q; want %q", loaded.Server.Addr, "127.0.0.1:9999")
	}
	if loaded.Connectors.Codex.Enabled != false {
		t.Errorf("Codex.Enabled = true; want false")
	}
}

// -----------------------------------------------------------------------
// TestSave_Atomic_NoCorruption
// -----------------------------------------------------------------------

// TestSave_Atomic_NoCorruption verifies that a crash after creating the
// temp file but before the rename leaves the original config intact.
// We simulate the "crash" by writing directly to the temp file and then
// NOT renaming it (i.e., we just leave the orphaned temp file).
func TestSave_Atomic_NoCorruption(t *testing.T) {
	setTempConfigDir(t)
	cfgDir := ConfigDir()

	// Ensure the dir exists.
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Write an original config.
	original := Defaults()
	original.Server.Addr = "127.0.0.1:1111"
	if err := Save(original); err != nil {
		t.Fatalf("initial Save() error: %v", err)
	}

	// Simulate a mid-write crash: create a temp file with garbage content
	// next to the config file, then do NOT rename it.
	tmp, err := os.CreateTemp(cfgDir, ".config-*.toml.tmp")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	if _, err := tmp.WriteString("THIS IS CORRUPT DATA !!!"); err != nil {
		t.Fatalf("WriteString: %v", err)
	}
	_ = tmp.Close()
	// tmp file left on disk — simulates crash before rename.

	// The real config file must still be intact.
	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() after simulated crash: %v", err)
	}
	if loaded.Server.Addr != "127.0.0.1:1111" {
		t.Errorf("Server.Addr = %q; want %q (original corrupted?)", loaded.Server.Addr, "127.0.0.1:1111")
	}

	// Clean up leftover tmp.
	_ = os.Remove(tmp.Name())
}

// -----------------------------------------------------------------------
// TestLoad_InvalidTOML
// -----------------------------------------------------------------------

func TestLoad_InvalidTOML(t *testing.T) {
	setTempConfigDir(t)

	// Write a deliberately broken TOML file.
	cfgFile := ConfigFile()
	if err := os.MkdirAll(filepath.Dir(cfgFile), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	broken := []byte("this is [not valid\ntoml ={{ garbage")
	if err := os.WriteFile(cfgFile, broken, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := Load()
	if err == nil {
		t.Fatal("Load() with invalid TOML should return an error, got nil")
	}

	// The bad file must NOT have been overwritten.
	contents, readErr := os.ReadFile(cfgFile)
	if readErr != nil {
		t.Fatalf("ReadFile after failed Load(): %v", readErr)
	}
	if string(contents) != string(broken) {
		t.Errorf("Load() silently overwrote the invalid config file")
	}
}

// -----------------------------------------------------------------------
// Additional coverage: Save creates the directory when absent
// -----------------------------------------------------------------------

func TestSave_CreatesDir(t *testing.T) {
	setTempConfigDir(t)

	// The config dir should NOT exist yet.
	cfgDir := ConfigDir()
	if _, err := os.Stat(cfgDir); !os.IsNotExist(err) {
		t.Skip("directory already exists, skip")
	}

	if err := Save(Defaults()); err != nil {
		t.Fatalf("Save() without pre-existing dir: %v", err)
	}

	if _, err := os.Stat(cfgDir); os.IsNotExist(err) {
		t.Errorf("Save() did not create config directory %s", cfgDir)
	}
}

// -----------------------------------------------------------------------
// TestLoad_ReadError — non-NotExist read error surfaces correctly
// -----------------------------------------------------------------------

func TestLoad_ReadError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission-based tests are unreliable on Windows")
	}
	setTempConfigDir(t)

	// Create the config dir and a config.toml that is a directory
	// (not a file) — os.ReadFile will fail with a non-NotExist error.
	cfgFile := ConfigFile()
	if err := os.MkdirAll(cfgFile, 0o700); err != nil {
		t.Fatalf("MkdirAll to create dir-as-file: %v", err)
	}

	_, err := Load()
	if err == nil {
		t.Fatal("Load() expected an error when config path is a directory, got nil")
	}
}

// -----------------------------------------------------------------------
// TestConfigDir_HomeDirError — HomeDir returns error → fallback path
// -----------------------------------------------------------------------

func TestConfigDir_HomeDirError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fallback test not meaningful on Windows (USERPROFILE used first)")
	}
	orig := HomeDir
	HomeDir = func() (string, error) { return "", errors.New("no home") }
	t.Cleanup(func() { HomeDir = orig })

	got := ConfigDir()
	// Should fall back to "./.klyne" (relative), not panic.
	if got == "" {
		t.Error("ConfigDir() returned empty string on HomeDir error")
	}
	if filepath.Base(got) != ".klyne" {
		t.Errorf("ConfigDir() base = %q; want .klyne", filepath.Base(got))
	}
}

// -----------------------------------------------------------------------
// TestSave_MkdirAllError — Save returns error when dir cannot be created
// -----------------------------------------------------------------------

func TestSave_MkdirAllError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission-based tests are unreliable on Windows")
	}
	setTempConfigDir(t)

	// Make the parent of ConfigDir a read-only file (not a dir) so that
	// MkdirAll fails.
	home, _ := HomeDir()
	// Create a plain file at the path where .klyne should be created.
	blocker := filepath.Join(home, ".klyne")
	if err := os.WriteFile(blocker, []byte("blocker"), 0o600); err != nil {
		t.Fatalf("WriteFile blocker: %v", err)
	}

	err := Save(Defaults())
	if err == nil {
		t.Fatal("Save() expected error when config dir is a file, got nil")
	}
}

// -----------------------------------------------------------------------
// TestLoad_DefaultsMissingAndSaveFails — when defaults write fails we get
// the Save error back (covers the saveErr != nil branch in Load).
// -----------------------------------------------------------------------

func TestLoad_DefaultsMissingAndSaveFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission-based tests are unreliable on Windows")
	}
	setTempConfigDir(t)

	// Make the home directory a read-only file so that Save() → MkdirAll
	// fails, which propagates back through Load().
	home, _ := HomeDir()
	blocker := filepath.Join(home, ".klyne")
	if err := os.WriteFile(blocker, []byte("blocker"), 0o600); err != nil {
		t.Fatalf("WriteFile blocker: %v", err)
	}

	_, err := Load()
	if err == nil {
		t.Fatal("Load() should return error when Save(defaults) fails, got nil")
	}
}

// -----------------------------------------------------------------------
// TestSave_WriteError — Save returns error when the temp file write fails.
// We achieve this by making the config dir read-only AFTER creation so
// os.CreateTemp fails (directory not writable).
// -----------------------------------------------------------------------

func TestSave_CreateTempError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission-based tests are unreliable on Windows")
	}
	setTempConfigDir(t)
	cfgDir := ConfigDir()

	// Create the dir first.
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// Make it read-only so CreateTemp fails.
	if err := os.Chmod(cfgDir, 0o500); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(cfgDir, 0o700) })

	err := Save(Defaults())
	if err == nil {
		t.Fatal("Save() expected error when dir is read-only, got nil")
	}
}

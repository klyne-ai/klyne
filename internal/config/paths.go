// Package config provides loading, saving, and path resolution for
// ~/.klyne/config.toml. Loader implementation lives in config.go;
// this file handles per-OS path resolution.
package config

import (
	"os"
	"path/filepath"
	"runtime"
)

// HomeDir is a package-level variable pointing to os.UserHomeDir.
// Override in tests via t.TempDir() to keep the filesystem clean.
var HomeDir = os.UserHomeDir

// ConfigDir returns the klyne configuration directory:
//   - macOS / Linux: ~/.klyne
//   - Windows:       %USERPROFILE%\.klyne
//
// It calls HomeDir() each time so tests can swap the implementation.
func ConfigDir() string {
	if runtime.GOOS == "windows" {
		// On Windows prefer %USERPROFILE% over %APPDATA% to match the spec.
		if profile := os.Getenv("USERPROFILE"); profile != "" {
			return filepath.Join(profile, ".klyne")
		}
	}
	home, err := HomeDir()
	if err != nil {
		// Fall back to the working directory if the home directory cannot
		// be determined (e.g., no passwd entry in a container).
		return filepath.Join(".", ".klyne")
	}
	return filepath.Join(home, ".klyne")
}

// ConfigFile returns the path to the TOML configuration file.
func ConfigFile() string {
	return filepath.Join(ConfigDir(), "config.toml")
}

// DBPath returns the path to the SQLite database file.
func DBPath() string {
	return filepath.Join(ConfigDir(), "klyne.db")
}

// PricingOverridePath returns the path to the optional user-supplied
// pricing.json (LiteLLM-style; see internal/cost/pricing_schema.go).
func PricingOverridePath() string {
	return filepath.Join(ConfigDir(), "pricing.json")
}

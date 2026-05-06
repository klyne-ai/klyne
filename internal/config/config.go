// Package config provides loading, saving, and path resolution for
// ~/.agentdeck/config.toml.
//
// The typed Config struct and its Defaults() constructor live in
// schema.go (W0-owned). This file owns only the I/O layer.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

// Load reads the configuration file returned by ConfigFile().
//
// If the file does not exist Load returns Defaults() and immediately
// persists them so subsequent calls find a real file.
//
// If the file exists but cannot be parsed, Load returns an error and
// does NOT overwrite the file.
func Load() (*Config, error) {
	path := ConfigFile()

	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("config: read %s: %w", path, err)
		}
		// File is absent — start from defaults and persist.
		cfg := Defaults()
		if saveErr := Save(cfg); saveErr != nil {
			return nil, fmt.Errorf("config: write defaults to %s: %w", path, saveErr)
		}
		return cfg, nil
	}

	// Start from defaults so any fields absent in the file keep their
	// documented default values.
	cfg := Defaults()
	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	return cfg, nil
}

// Save writes cfg to the file returned by ConfigFile() atomically:
//
//  1. MkdirAll to ensure the config directory exists.
//  2. Write to a temporary file in the same directory.
//  3. fsync the temp file so data is on disk before the rename.
//  4. os.Rename (atomic on POSIX, best-effort on Windows).
//
// A crash between steps 2 and 4 leaves the original file untouched.
func Save(cfg *Config) error {
	path := ConfigFile()

	// Ensure the directory exists.
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("config: mkdir %s: %w", dir, err)
	}

	// Marshal to TOML.
	data, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("config: marshal: %w", err)
	}

	// Write to a temp file in the same directory so rename is atomic.
	tmp, err := os.CreateTemp(dir, ".config-*.toml.tmp")
	if err != nil {
		return fmt.Errorf("config: create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()

	// Clean up the temp file if anything goes wrong before the rename.
	success := false
	defer func() {
		if !success {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("config: write temp file: %w", err)
	}
	// fsync — flush kernel buffers to disk.
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("config: sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("config: close temp file: %w", err)
	}

	// Atomic rename.
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("config: rename %s → %s: %w", tmpName, path, err)
	}

	success = true
	return nil
}

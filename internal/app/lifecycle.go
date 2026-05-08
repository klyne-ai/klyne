// Package app — daemon lifecycle helpers.
//
// This file owns:
//   - Pidfile read / write / remove (atomic best-effort).
//   - Path resolution for the daemon pidfile.
//
// Signal handling itself lives in cmd/klyne/start.go via signal.NotifyContext;
// only the pidfile primitives are factored out here so they can be unit-tested
// with a tempdir HOME.
package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/klyne-ai/klyne/internal/config"
)

// PidfileName is the filename within ConfigDir() that holds the running
// daemon's PID. The full path is daemonPidfilePath().
const PidfileName = "daemon.pid"

// PidfilePath returns the absolute path to the daemon pidfile inside the
// agentdeck config directory (e.g. ~/.klyne/daemon.pid).
func PidfilePath() string {
	return filepath.Join(config.ConfigDir(), PidfileName)
}

// WritePidfile writes the current process PID to PidfilePath(), creating
// any missing parent directories with 0o700. Existing pidfiles are
// overwritten — start.go uses this after verifying no live daemon is running.
//
// Atomic-ish: writes to a temp file, fsyncs, then renames. A crash
// between mkdir and rename leaves the original pidfile untouched.
func WritePidfile(pid int) error {
	path := PidfilePath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("app: mkdir %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".pid-*.tmp")
	if err != nil {
		return fmt.Errorf("app: create temp pidfile: %w", err)
	}
	tmpName := tmp.Name()
	success := false
	defer func() {
		if !success {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.WriteString(strconv.Itoa(pid) + "\n"); err != nil {
		return fmt.Errorf("app: write temp pidfile: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("app: sync temp pidfile: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("app: close temp pidfile: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("app: rename pidfile: %w", err)
	}
	success = true
	return nil
}

// ReadPidfile reads the daemon PID from PidfilePath().
//
// Returns os.ErrNotExist (wrapped) when the pidfile is absent — callers
// should treat this as "no daemon running" rather than an error.
func ReadPidfile() (int, error) {
	path := PidfilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, fmt.Errorf("app: read pidfile %s: %w", path, err)
		}
		return 0, fmt.Errorf("app: read pidfile %s: %w", path, err)
	}
	pidStr := strings.TrimSpace(string(data))
	if pidStr == "" {
		return 0, fmt.Errorf("app: pidfile %s is empty", path)
	}
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return 0, fmt.Errorf("app: parse pidfile %s: %w", path, err)
	}
	if pid <= 0 {
		return 0, fmt.Errorf("app: pidfile %s contains invalid pid %d", path, pid)
	}
	return pid, nil
}

// RemovePidfile deletes the pidfile if present. Idempotent: missing file
// is not an error.
func RemovePidfile() error {
	path := PidfilePath()
	err := os.Remove(path)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("app: remove pidfile %s: %w", path, err)
}

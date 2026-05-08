package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/klyne-ai/klyne/internal/config"
)

// withTempHome redirects config.HomeDir to t.TempDir() for the duration of
// the test, restoring the original at cleanup.
func withTempHome(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	orig := config.HomeDir
	config.HomeDir = func() (string, error) { return tmp, nil }
	t.Cleanup(func() { config.HomeDir = orig })
	return tmp
}

func TestPidfile_WriteReadRemove(t *testing.T) {
	withTempHome(t)

	if err := WritePidfile(12345); err != nil {
		t.Fatalf("WritePidfile: %v", err)
	}

	pid, err := ReadPidfile()
	if err != nil {
		t.Fatalf("ReadPidfile: %v", err)
	}
	if pid != 12345 {
		t.Fatalf("pid = %d, want 12345", pid)
	}

	if err := RemovePidfile(); err != nil {
		t.Fatalf("RemovePidfile: %v", err)
	}

	// Idempotent removal.
	if err := RemovePidfile(); err != nil {
		t.Fatalf("RemovePidfile (second call): %v", err)
	}

	// Reading after removal returns os.ErrNotExist.
	if _, err := ReadPidfile(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ReadPidfile after remove: err = %v, want os.ErrNotExist", err)
	}
}

func TestPidfile_PathInsideConfigDir(t *testing.T) {
	tmp := withTempHome(t)
	want := filepath.Join(tmp, ".klyne", PidfileName)
	if got := PidfilePath(); got != want {
		t.Fatalf("PidfilePath = %q, want %q", got, want)
	}
}

func TestPidfile_RejectsBadContents(t *testing.T) {
	withTempHome(t)
	// Write a garbage pidfile by hand.
	dir := filepath.Dir(PidfilePath())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(PidfilePath(), []byte("not-a-number\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := ReadPidfile(); err == nil {
		t.Fatal("ReadPidfile must error on non-numeric content")
	}
}

func TestPidfile_RejectsEmpty(t *testing.T) {
	withTempHome(t)
	dir := filepath.Dir(PidfilePath())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(PidfilePath(), []byte(""), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := ReadPidfile(); err == nil {
		t.Fatal("ReadPidfile must error on empty content")
	}
}

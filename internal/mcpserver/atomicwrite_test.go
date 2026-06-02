package mcpserver

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestWriteFileAtomic_ReplacesAtomically asserts the helper fully
// replaces the target's contents and applies the requested perms.
func TestWriteFileAtomic_ReplacesAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("OLD CONTENTS"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	want := []byte(`{"mcpServers":{"klyne":{}}}`)
	if err := writeFileAtomic(path, want, 0o600); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("contents = %q; want %q", got, want)
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("perm = %o; want 600", perm)
		}
	}
}

// TestWriteFileAtomic_LeavesNoTempBehind asserts a successful write
// leaves only the target file in the directory — no leftover ".tmp-*"
// sibling.
func TestWriteFileAtomic_LeavesNoTempBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := writeFileAtomic(path, []byte("data"), 0o600); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "config.json" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("dir contents = %v; want exactly [config.json] (temp not cleaned up)", names)
	}
}

// TestWriteFileAtomic_FailureLeavesOriginalIntact simulates an
// interrupted rename by making the target directory unwritable so the
// final os.Rename fails. The pre-existing target must be left byte-for-
// byte intact (the whole point of the atomic write) and no partial temp
// sibling may remain.
func TestWriteFileAtomic_FailureLeavesOriginalIntact(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permission semantics differ on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory write permissions")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	const original = "ORIGINAL — MUST SURVIVE"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Make the directory read+exec only: CreateTemp (which needs write)
	// will fail, so the rename never happens and the original is
	// untouched. Restore perms afterward so t.TempDir cleanup succeeds.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if err := writeFileAtomic(path, []byte("NEW CONTENTS"), 0o600); err == nil {
		t.Fatalf("expected writeFileAtomic to fail on an unwritable directory")
	}

	// Restore write so we can inspect, then assert the original survived
	// and no temp sibling leaked.
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("restore chmod: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read original: %v", err)
	}
	if string(got) != original {
		t.Errorf("original corrupted: got %q; want %q", got, original)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "config.json" {
			t.Errorf("leftover temp file after failed write: %q", e.Name())
		}
	}
}

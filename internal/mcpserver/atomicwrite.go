package mcpserver

import (
	"fmt"
	"os"
	"path/filepath"
)

// atomicwrite.go — crash-safe replacement for os.WriteFile when the
// target is one of the user's primary AI-CLI configs (~/.claude.json,
// ~/.claude/settings.json, ~/.codex/config.toml, ~/.cursor/hooks.json,
// ~/.codex/hooks.json). Those files hold the user's entire host state
// (every project, every other MCP server, every other hook). A plain
// os.WriteFile opens O_TRUNC and writes in place, so a crash / power
// loss / ENOSPC mid-write leaves a truncated or empty config — and the
// installer runs on every binary upgrade.
//
// writeFileAtomic writes to a temp file in the SAME directory (so the
// final os.Rename is a same-filesystem atomic metadata swap rather than
// a cross-device copy), fsyncs the data to disk, fixes the perms, and
// renames over the target. On any error the temp file is removed, so an
// interrupted write never leaves a partial sibling behind and the
// original target is left untouched.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp for %s: %w", path, err)
	}
	tmpName := tmp.Name()

	// Best-effort cleanup: removed on any error path before return. On
	// the success path the temp no longer exists (renamed away), so the
	// Remove is a harmless no-op.
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}

	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("write temp for %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync temp for %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close temp for %s: %w", path, err)
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("chmod temp for %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("rename temp over %s: %w", path, err)
	}
	return nil
}

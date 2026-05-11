package mcpserver

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Slash-command install
// =====================
// Claude Code v2.1.116 has a known issue where MCP-prompt slash
// commands (`/<server>:<name>`) silently fail to execute. To give
// klyne users the slash UX regardless of that bug, we ship a parallel
// surface as Claude Code *markdown* slash commands — a separate,
// reliable mechanism. Each command is a thin wrapper that instructs
// the AI to invoke the matching klyne MCP tool.
//
// The five files under slashcommands/*.md are embedded into the binary
// at build time and unpacked to ~/.claude/commands/klyne/<name>.md by
// `klyne mcp install`. Once written, Claude Code surfaces them as
// /klyne:health, /klyne:sessions, /klyne:handoff, /klyne:search,
// /klyne:precompact in every project on the user's machine.
//
// Idempotent: re-running install overwrites with the current bundled
// content.

//go:embed slashcommands/*.md
var slashCommandsFS embed.FS

// SlashCommandsReport carries the outcome of InstallSlashCommands.
type SlashCommandsReport struct {
	// Dir is the destination directory the commands were written to.
	Dir string
	// Action is "added", "updated", or "already-installed".
	Action InstallAction
	// Files is the count of command files written or verified.
	Files int
}

// claudeCommandsDir returns ~/.claude/commands/klyne. Returns ("", error)
// when HOME is unresolvable — same contract as the other path helpers.
func claudeCommandsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home: %w", err)
	}
	return filepath.Join(home, ".claude", "commands", "klyne"), nil
}

// InstallSlashCommands writes the embedded slash-command markdown
// files to ~/.claude/commands/klyne/. Creates the directory tree when
// missing. Returns a report indicating whether the destination was
// freshly created (added), had at least one file rewritten (updated),
// or matched the bundled content byte-for-byte (already-installed).
func InstallSlashCommands() (*SlashCommandsReport, error) {
	dest, err := claudeCommandsDir()
	if err != nil {
		return nil, err
	}

	// Track whether the destination directory existed before this
	// install so we can report "added" vs "updated" precisely.
	preexisted := true
	if _, err := os.Stat(dest); os.IsNotExist(err) {
		preexisted = false
	} else if err != nil {
		return nil, fmt.Errorf("stat %s: %w", dest, err)
	}

	if err := os.MkdirAll(dest, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dest, err)
	}

	entries, err := fs.ReadDir(slashCommandsFS, "slashcommands")
	if err != nil {
		return nil, fmt.Errorf("read embedded slash commands: %w", err)
	}

	rewroteAny := false
	written := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		content, err := fs.ReadFile(slashCommandsFS, "slashcommands/"+name)
		if err != nil {
			return nil, fmt.Errorf("read embedded %s: %w", name, err)
		}
		target := filepath.Join(dest, name)
		existing, readErr := os.ReadFile(target)
		switch {
		case os.IsNotExist(readErr):
			// New file
		case readErr != nil:
			return nil, fmt.Errorf("read %s: %w", target, readErr)
		case bytes.Equal(existing, content):
			written++
			continue
		}
		if err := os.WriteFile(target, content, 0o644); err != nil {
			return nil, fmt.Errorf("write %s: %w", target, err)
		}
		rewroteAny = true
		written++
	}

	action := InstallActionAlreadyInstalled
	switch {
	case !preexisted:
		action = InstallActionAdded
	case rewroteAny:
		action = InstallActionUpdated
	}
	return &SlashCommandsReport{Dir: dest, Action: action, Files: written}, nil
}

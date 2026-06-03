package mcpserver

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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
// The six files under slashcommands/*.md are embedded into the binary
// at build time and unpacked to ~/.claude/commands/klyne/<name>.md by
// `klyne mcp install`. Once written, Claude Code surfaces them as
// /klyne:health, /klyne:sessions, /klyne:handoff, /klyne:search,
// /klyne:precompact, /klyne:tokens in every project on the user's machine.
//
// Idempotent: re-running install overwrites with the current bundled
// content.

//go:embed slashcommands/*.md
var slashCommandsFS embed.FS

// SlashCommandBody returns the embedded markdown body for the named
// slash command (e.g. "productivity-sync", "reflect"), with the YAML
// front-matter block stripped. This is what the Codex engine inlines as
// a prompt: Codex has no `/klyne:` slash-command surface, so the
// instructions that Claude Code would resolve from the command file are
// passed inline instead. The MCP tools the body calls
// (mcp__klyne__list_stop_summaries_for_day, record_productivity_card,
// …) come from the klyne server already wired into ~/.codex/config.toml.
//
// name is the bare command name without extension. Returns an error when
// the command is not bundled.
func SlashCommandBody(name string) (string, error) {
	raw, err := fs.ReadFile(slashCommandsFS, "slashcommands/"+name+".md")
	if err != nil {
		return "", fmt.Errorf("mcpserver: slash command %q not found: %w", name, err)
	}
	return stripFrontMatter(string(raw)), nil
}

// stripFrontMatter removes a leading YAML front-matter block delimited by
// `---` lines (the `description:` header the markdown commands carry), so
// only the instruction body is inlined into a prompt. Content without a
// front-matter block is returned unchanged (trimmed).
func stripFrontMatter(s string) string {
	t := strings.TrimLeft(s, "\ufeff \t\r\n")
	if !strings.HasPrefix(t, "---") {
		return strings.TrimSpace(s)
	}
	// Drop the opening delimiter line, then everything up to and
	// including the closing `---` line.
	rest := t[len("---"):]
	if i := strings.Index(rest, "\n---"); i >= 0 {
		after := rest[i+len("\n---"):]
		if nl := strings.IndexByte(after, '\n'); nl >= 0 {
			return strings.TrimSpace(after[nl+1:])
		}
		return ""
	}
	return strings.TrimSpace(s)
}

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
	embedded := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		embedded[name] = struct{}{}
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

	// Cleanup pass: remove any .md file in the destination that is no
	// longer in the embed FS. This is how retiring a slashcommand
	// (deleting its file under slashcommands/) actually propagates to
	// the user's ~/.claude/commands/klyne/ directory on the next
	// `klyne mcp install`. Conservative — only top-level .md files are
	// considered; subdirectories and other extensions are left alone.
	destEntries, err := os.ReadDir(dest)
	if err != nil {
		return nil, fmt.Errorf("read dest dir %s: %w", dest, err)
	}
	for _, de := range destEntries {
		if de.IsDir() {
			continue
		}
		name := de.Name()
		if filepath.Ext(name) != ".md" {
			continue
		}
		if _, ok := embedded[name]; ok {
			continue
		}
		if err := os.Remove(filepath.Join(dest, name)); err != nil {
			return nil, fmt.Errorf("remove stale %s: %w", name, err)
		}
		rewroteAny = true
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

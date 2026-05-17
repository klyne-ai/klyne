package mcpserver

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Claude auto-memory reader
// =========================
// Claude Code's own "auto memory" system (per the system prompt's
// `# auto memory` section) writes per-project Markdown files under
// ~/.claude/projects/<encoded-cwd>/memory/. Each file carries a small
// YAML frontmatter block (name / description / type / etc) and a body.
//
// These files live OUTSIDE klyne's SQLite store but are the densest
// per-project context the agent has — bootstrap was previously
// ignoring them and labelling its own (often empty) SQLite output as
// "Project memories", which made bootstrap useless for projects
// where the user had relied on Claude's auto-memory.
//
// This file is a pure reader. No writes. The parser tolerates files
// without frontmatter (returns them with empty metadata + the full
// content as Body) so any .md the user dropped into the directory is
// still surfaced.

// ClaudeAutoMemoryEntry is one .md file read from the auto-memory dir.
// The optional fields are populated from the frontmatter when present.
type ClaudeAutoMemoryEntry struct {
	File        string `json:"file"`                  // basename, e.g. "project_foo.md"
	Name        string `json:"name,omitempty"`        // frontmatter `name`
	Description string `json:"description,omitempty"` // frontmatter `description`
	Type        string `json:"type,omitempty"`        // frontmatter `type` (user / feedback / project / reference)
	Body        string `json:"body,omitempty"`        // everything after the frontmatter; empty when file had no body
}

// ClaudeAutoMemory is the full read of the auto-memory dir for one
// project. Dir is always populated (even when missing) so the agent
// can tell the user where it looked.
type ClaudeAutoMemory struct {
	Dir     string                  `json:"dir"`               // resolved absolute path
	Index   string                  `json:"index,omitempty"`   // contents of MEMORY.md (the index), verbatim
	Entries []ClaudeAutoMemoryEntry `json:"entries,omitempty"` // sibling .md files, sorted by File
}

// claudeAutoMemoryDir resolves <home>/.claude/projects/<encoded-cwd>/memory.
// Returns "" when cwd is not absolute (EncodeCWD's contract).
func claudeAutoMemoryDir(home, cwd string) string {
	enc := EncodeCWD(cwd)
	if enc == "" {
		return ""
	}
	return filepath.Join(home, ".claude", "projects", enc, "memory")
}

// ReadClaudeAutoMemory scans the auto-memory dir for projectCwd and
// returns the parsed entries + the verbatim MEMORY.md index.
//
// Tolerates a missing directory — returns a populated Dir field with
// empty Entries / Index so the caller can render "no auto-memory
// found at <path>" if it wants. Returns an error only on unexpected
// I/O failures (permission denied, etc).
func ReadClaudeAutoMemory(home, projectCwd string) (ClaudeAutoMemory, error) {
	dir := claudeAutoMemoryDir(home, projectCwd)
	out := ClaudeAutoMemory{Dir: dir}
	if dir == "" {
		return out, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return out, fmt.Errorf("read auto-memory dir %s: %w", dir, err)
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		body, err := os.ReadFile(path) //nolint:gosec
		if err != nil {
			// Skip unreadable files rather than failing the whole call.
			continue
		}
		// MEMORY.md is the index — surface it raw under .Index, not
		// as a regular entry.
		if e.Name() == "MEMORY.md" {
			out.Index = string(body)
			continue
		}
		entry := parseAutoMemoryFile(e.Name(), string(body))
		out.Entries = append(out.Entries, entry)
	}
	sort.Slice(out.Entries, func(i, j int) bool {
		return out.Entries[i].File < out.Entries[j].File
	})
	return out, nil
}

// parseAutoMemoryFile splits a Markdown file into (frontmatter, body).
// The frontmatter is a `---`-delimited block at the very top of the
// file. Inside it, only top-level `key: value` lines are recognised —
// Claude's writer does not produce nested YAML so this matches its
// output exactly and avoids pulling in a yaml dependency.
func parseAutoMemoryFile(name, raw string) ClaudeAutoMemoryEntry {
	out := ClaudeAutoMemoryEntry{File: name}
	sc := bufio.NewScanner(strings.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	lines := make([]string, 0, 32)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}

	// No frontmatter — entire file is the body.
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r") != "---" {
		out.Body = strings.TrimRight(raw, "\n")
		return out
	}

	// Find the closing `---`.
	closeIdx := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r") == "---" {
			closeIdx = i
			break
		}
	}
	if closeIdx == -1 {
		// Open frontmatter never closed — treat entire file as body.
		out.Body = strings.TrimRight(raw, "\n")
		return out
	}

	for _, kv := range lines[1:closeIdx] {
		colon := strings.IndexByte(kv, ':')
		if colon <= 0 {
			continue
		}
		key := strings.TrimSpace(kv[:colon])
		val := strings.TrimSpace(kv[colon+1:])
		switch strings.ToLower(key) {
		case "name":
			out.Name = val
		case "description":
			out.Description = val
		case "type":
			out.Type = val
		}
	}
	out.Body = strings.TrimSpace(strings.Join(lines[closeIdx+1:], "\n"))
	return out
}

// RenderClaudeAutoMemoryAsMarkdown turns a ClaudeAutoMemory into a
// Markdown section suitable for embedding in the bootstrap output.
// Always emits a section header so the agent's output shape is stable
// whether the dir exists or not.
func RenderClaudeAutoMemoryAsMarkdown(m ClaudeAutoMemory) string {
	var b strings.Builder
	if len(m.Entries) == 0 && strings.TrimSpace(m.Index) == "" {
		b.WriteString("_(none)_\n\n")
		return b.String()
	}
	for _, e := range m.Entries {
		title := e.Name
		if title == "" {
			title = e.File
		}
		typ := e.Type
		if typ == "" {
			typ = "untyped"
		}
		fmt.Fprintf(&b, "- **%s** (%s) — `%s`\n", title, typ, e.File)
		if e.Description != "" {
			fmt.Fprintf(&b, "  - %s\n", oneLine(e.Description))
		}
	}
	b.WriteString("\n")
	return b.String()
}

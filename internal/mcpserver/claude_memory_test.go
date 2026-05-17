package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadClaudeAutoMemory_MissingDirReturnsEmpty(t *testing.T) {
	home := withFakeHome(t)
	got, err := ReadClaudeAutoMemory(home, "/no/such/project")
	if err != nil {
		t.Fatalf("ReadClaudeAutoMemory: %v", err)
	}
	if got.Dir == "" {
		t.Errorf("Dir should be populated even when missing, for the agent to know where it looked")
	}
	if len(got.Entries) != 0 {
		t.Errorf("Entries len = %d, want 0", len(got.Entries))
	}
	if got.Index != "" {
		t.Errorf("Index should be empty for missing dir, got %q", got.Index)
	}
}

func TestReadClaudeAutoMemory_ParsesFrontmatter(t *testing.T) {
	home := withFakeHome(t)
	cwd := "/tmp/proj-auto-memory"
	dir := filepath.Join(home, ".claude", "projects", EncodeCWD(cwd), "memory")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// MEMORY.md (index).
	if err := os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte("- [foo](foo.md) — short hook\n"), 0o644); err != nil {
		t.Fatalf("write MEMORY.md: %v", err)
	}
	// A project memory.
	body := "---\nname: foo memory\ndescription: notes about foo\ntype: project\n---\nfoo body line 1\nfoo body line 2\n"
	if err := os.WriteFile(filepath.Join(dir, "project_foo.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write project_foo: %v", err)
	}
	// A file without frontmatter — must still be surfaced, with empty fields.
	if err := os.WriteFile(filepath.Join(dir, "raw_note.md"), []byte("just a body\n"), 0o644); err != nil {
		t.Fatalf("write raw_note: %v", err)
	}

	got, err := ReadClaudeAutoMemory(home, cwd)
	if err != nil {
		t.Fatalf("ReadClaudeAutoMemory: %v", err)
	}
	if !strings.Contains(got.Index, "[foo](foo.md)") {
		t.Errorf("Index missing pointer line: %q", got.Index)
	}
	if len(got.Entries) != 2 {
		t.Fatalf("Entries len = %d, want 2 (project_foo + raw_note)", len(got.Entries))
	}

	byFile := map[string]ClaudeAutoMemoryEntry{}
	for _, e := range got.Entries {
		byFile[e.File] = e
	}
	pf := byFile["project_foo.md"]
	if pf.Name != "foo memory" {
		t.Errorf("project_foo.Name = %q, want %q", pf.Name, "foo memory")
	}
	if pf.Description != "notes about foo" {
		t.Errorf("project_foo.Description = %q", pf.Description)
	}
	if pf.Type != "project" {
		t.Errorf("project_foo.Type = %q, want project", pf.Type)
	}
	if !strings.Contains(pf.Body, "foo body line 1") {
		t.Errorf("project_foo.Body missing line 1: %q", pf.Body)
	}

	rn := byFile["raw_note.md"]
	if rn.Name != "" || rn.Type != "" {
		t.Errorf("raw_note frontmatter should be empty, got %+v", rn)
	}
	if !strings.Contains(rn.Body, "just a body") {
		t.Errorf("raw_note.Body missing body: %q", rn.Body)
	}
}

func TestRenderClaudeAutoMemoryAsMarkdown_EmptyShape(t *testing.T) {
	out := RenderClaudeAutoMemoryAsMarkdown(ClaudeAutoMemory{Dir: "/x"})
	if !strings.Contains(out, "_(none)_") {
		t.Errorf("empty render must contain _(none)_, got: %s", out)
	}
}

func TestRenderClaudeAutoMemoryAsMarkdown_WithEntries(t *testing.T) {
	mem := ClaudeAutoMemory{
		Dir:   "/x/memory",
		Index: "- [foo](foo.md) — hook\n",
		Entries: []ClaudeAutoMemoryEntry{
			{File: "project_foo.md", Name: "foo memory", Description: "notes", Type: "project", Body: "body text"},
		},
	}
	out := RenderClaudeAutoMemoryAsMarkdown(mem)
	for _, want := range []string{"foo memory", "(project)", "project_foo.md", "notes", "### Index (MEMORY.md)", "### Entries"} {
		if !strings.Contains(out, want) {
			t.Errorf("Markdown missing %q\n%s", want, out)
		}
	}
	idxAt := strings.Index(out, "### Index (MEMORY.md)")
	entAt := strings.Index(out, "### Entries")
	if idxAt < 0 || entAt < 0 || idxAt >= entAt {
		t.Errorf("expected Index subsection before Entries; idxAt=%d, entAt=%d\n%s", idxAt, entAt, out)
	}
}

func TestRenderClaudeAutoMemoryAsMarkdown_IndexOnly(t *testing.T) {
	mem := ClaudeAutoMemory{
		Dir:   "/x/memory",
		Index: "- [foo](foo.md) — hook line\n",
	}
	out := RenderClaudeAutoMemoryAsMarkdown(mem)
	if !strings.Contains(out, "### Index (MEMORY.md)") {
		t.Errorf("Index-only render missing header:\n%s", out)
	}
	if !strings.Contains(out, "[foo](foo.md)") {
		t.Errorf("Index-only render missing the index line:\n%s", out)
	}
	if strings.Contains(out, "_(none)_") {
		t.Errorf("Index-only render should not show _(none)_:\n%s", out)
	}
}

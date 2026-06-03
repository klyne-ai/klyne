package mcpserver

import (
	"strings"
	"testing"
)

func TestSlashCommandBody_StripsFrontMatterAndExists(t *testing.T) {
	body, err := SlashCommandBody("productivity-sync")
	if err != nil {
		t.Fatalf("SlashCommandBody: %v", err)
	}
	if strings.HasPrefix(strings.TrimSpace(body), "---") {
		t.Error("front-matter not stripped: body still starts with ---")
	}
	if strings.Contains(body, "description:") && strings.Index(body, "description:") < 5 {
		t.Error("leading description: front-matter leaked into body")
	}
	if !strings.Contains(body, "project_path") {
		t.Error("expected the instruction body to mention project_path")
	}
}

func TestSlashCommandBody_UnknownErrors(t *testing.T) {
	if _, err := SlashCommandBody("does-not-exist"); err == nil {
		t.Error("expected error for unknown slash command")
	}
}

func TestStripFrontMatter_NoFrontMatter(t *testing.T) {
	in := "just a body\nno front matter\n"
	if got := stripFrontMatter(in); got != "just a body\nno front matter" {
		t.Errorf("stripFrontMatter(no-fm) = %q", got)
	}
}

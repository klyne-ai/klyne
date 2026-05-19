package instructions

import (
	"strings"
	"testing"

	"github.com/klyne-ai/klyne/internal/store"
)

func TestDeriveTitle_Empty(t *testing.T) {
	if got := deriveTitle(""); got != "" {
		t.Errorf("deriveTitle(\"\") = %q, want \"\"", got)
	}
	if got := deriveTitle("   \n\n   "); got != "" {
		t.Errorf("deriveTitle(whitespace) = %q, want \"\"", got)
	}
}

func TestDeriveTitle_SingleLine(t *testing.T) {
	got := deriveTitle("rotate api keys monthly")
	if got != "rotate api keys monthly" {
		t.Errorf("got %q", got)
	}
}

func TestDeriveTitle_MultilineFirstLine(t *testing.T) {
	in := "for labstack changes we have four working dirs\nsteps:\n1. ..."
	got := deriveTitle(in)
	want := "for labstack changes we have four working dirs"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDeriveTitle_TruncatesAt60Runes(t *testing.T) {
	long := strings.Repeat("x", 80)
	got := deriveTitle(long)
	runes := []rune(got)
	if len(runes) != 61 {
		t.Fatalf("len = %d, want 61 (60 + ellipsis)", len(runes))
	}
	if runes[60] != '…' {
		t.Errorf("expected ellipsis suffix, got %q", string(runes[60]))
	}
}

func TestDeriveTitle_SkipsBlankFirstLine(t *testing.T) {
	got := deriveTitle("\n\nactual title\nbody")
	if got != "actual title" {
		t.Errorf("got %q", got)
	}
}

func TestRenderFooter_Zero(t *testing.T) {
	if got := renderFooter(0); got != "" {
		t.Errorf("renderFooter(0) = %q, want \"\"", got)
	}
}

func TestRenderFooter_Negative(t *testing.T) {
	if got := renderFooter(-3); got != "" {
		t.Errorf("renderFooter(-3) = %q, want \"\"", got)
	}
}

func TestRenderFooter_Positive(t *testing.T) {
	got := renderFooter(5)
	want := "+5 more, call mcp__klyne__recall to see all"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderInventory_Empty(t *testing.T) {
	if got := renderInventory(nil, "Project runbooks (path: /repo)", 0); got != "" {
		t.Errorf("nil slice should yield empty string, got %q", got)
	}
	if got := renderInventory([]store.Decision{}, "Project runbooks", 0); got != "" {
		t.Errorf("empty slice should yield empty string, got %q", got)
	}
}

func TestRenderInventory_TwoRowsNoFooter(t *testing.T) {
	rows := []store.Decision{
		{ID: "d-abc", Text: "rotate api keys monthly"},
		{ID: "d-def", Text: "labstack worktree branch\nstep 1\nstep 2"},
	}
	got := renderInventory(rows, "Project runbooks (path: /repo)", 0)

	if !strings.Contains(got, "# Project runbooks (path: /repo)") {
		t.Errorf("missing heading; got:\n%s", got)
	}
	if !strings.Contains(got, "  - d-abc  rotate api keys monthly") {
		t.Errorf("missing first row; got:\n%s", got)
	}
	if !strings.Contains(got, "  - d-def  labstack worktree branch") {
		t.Errorf("missing second row (first line only); got:\n%s", got)
	}
	if strings.Contains(got, "more, call mcp__klyne__recall") {
		t.Errorf("should not include footer when hiddenCount=0; got:\n%s", got)
	}
}

func TestRenderInventory_WithFooter(t *testing.T) {
	rows := []store.Decision{{ID: "d-1", Text: "title"}}
	got := renderInventory(rows, "Global runbooks", 7)
	if !strings.Contains(got, "+7 more, call mcp__klyne__recall to see all") {
		t.Errorf("missing footer; got:\n%s", got)
	}
}

func TestRenderDirective_ContainsKeyPhrases(t *testing.T) {
	got := renderDirective()
	for _, want := range []string{
		"klyne tracks runbooks for this project",
		"mcp__klyne__recall",
		"Operational asks",
		"Topical match",
		"confirm BEFORE executing",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("directive missing %q\nfull text:\n%s", want, got)
		}
	}
}

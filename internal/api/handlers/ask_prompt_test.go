package handlers

import (
	"strings"
	"testing"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/store"
)

func TestBuildAskPrompt_IncludesSessionsAndQuestion(t *testing.T) {
	t.Parallel()
	rows := []store.AskRow{
		{
			SessionID: "s1", TsMs: 1779091935000,
			ProjectPath: "/p/a", AIDraftedSummary: "Investigated UI bug",
			WorklogEntryJSON: `{"bugs_fixed":[{"summary":"fix nav"}],"pending":[]}`,
		},
		{
			SessionID: "s2", TsMs: 1779178335000,
			ProjectPath: "/p/a", AIDraftedSummary: "Merged feature X",
			WorklogEntryJSON: `{"features_picked":[{"summary":"X"}]}`,
		},
	}
	req := api.AskRequest{
		Projects: []string{"/p/a"},
		FromMs:   1779091935000, ToMs: 1779200000000,
		Question: "How many bugs were fixed?",
		History: []api.AskMessage{
			{Role: "user", Content: "Earlier question"},
			{Role: "assistant", Content: "Earlier answer"},
		},
	}

	prompt := buildAskPrompt(req, rows)

	for _, want := range []string{
		"Projects:", "/p/a",
		"Sessions: 2",
		"Session 1",
		"Investigated UI bug",
		"fix nav",
		"Merged feature X",
		"Earlier question",
		"Earlier answer",
		"How many bugs were fixed?",
		"Rules",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q\nfull prompt:\n%s", want, prompt)
		}
	}
}

func TestBuildAskPrompt_OmitsEmptyCategories(t *testing.T) {
	t.Parallel()
	rows := []store.AskRow{
		{
			SessionID: "s1", TsMs: 1, ProjectPath: "/p",
			AIDraftedSummary: "x",
			WorklogEntryJSON: `{"bugs_fixed":[],"pending":[{"summary":"P1"}],"blockers":[]}`,
		},
	}
	req := api.AskRequest{FromMs: 0, ToMs: 10, Question: "?"}
	prompt := buildAskPrompt(req, rows)

	if !strings.Contains(prompt, "P1") {
		t.Errorf("prompt should mention pending item P1\n%s", prompt)
	}
	if strings.Contains(prompt, "bugs_fixed") {
		t.Errorf("prompt should omit empty bugs_fixed category\n%s", prompt)
	}
	if strings.Contains(prompt, "blockers") {
		t.Errorf("prompt should omit empty blockers category\n%s", prompt)
	}
}

func TestBuildAskPrompt_ProjectsAllWhenEmpty(t *testing.T) {
	t.Parallel()
	rows := []store.AskRow{{SessionID: "s1", TsMs: 1, ProjectPath: "/x", AIDraftedSummary: "x", WorklogEntryJSON: "{}"}}
	req := api.AskRequest{FromMs: 0, ToMs: 10, Question: "?"}
	prompt := buildAskPrompt(req, rows)
	if !strings.Contains(prompt, "Projects: all") {
		t.Errorf("expected 'Projects: all' when no filter\n%s", prompt)
	}
}

func TestBuildAskPrompt_HandlesEmptyAISummary(t *testing.T) {
	t.Parallel()
	rows := []store.AskRow{{SessionID: "s1", TsMs: 1, ProjectPath: "/x", AIDraftedSummary: "", WorklogEntryJSON: `{"pending":[{"summary":"p"}]}`}}
	req := api.AskRequest{FromMs: 0, ToMs: 10, Question: "?"}
	prompt := buildAskPrompt(req, rows)
	if !strings.Contains(prompt, "(empty)") {
		t.Errorf("expected '(empty)' placeholder for missing ai_summary\n%s", prompt)
	}
}

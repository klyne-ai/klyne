package mcpserver

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/connectors"
)

func msgUser(text string) *connectors.Message {
	return &connectors.Message{Role: connectors.RoleUser, Content: text}
}

func msgAssistant(text string) *connectors.Message {
	return &connectors.Message{Role: connectors.RoleAssistant, Content: text}
}

func TestExtractTicketHints_RequiresTwoMentionsOrURL(t *testing.T) {
	msgs := []*connectors.Message{
		msgUser("hey let's look at CLI-1362 — needs the Payment Link pill."),
		msgAssistant("ok looking at CLI-1362"),
		msgUser("also CLI-1362 has the Track Order recovery requirement"),
		msgUser("there is also PROJ-77 mentioned once which should be ignored"),
		msgUser("see https://linear.app/clinikk/issue/CLI-1361 for the backend"),
	}
	got := extractTicketHints(msgs)
	sort.Slice(got, func(i, j int) bool { return got[i].Key < got[j].Key })

	want := []TicketHint{
		// CLI-1361 — appears only inside a URL, qualifies via FromURL.
		{Key: "CLI-1361", Mentions: 0, FromURL: true},
		// CLI-1362 — 2 mentions across user turns (assistant doesn't count).
		{Key: "CLI-1362", Mentions: 2, FromURL: false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestExtractTicketHints_AssistantOnlyMentionsDoNotCount(t *testing.T) {
	msgs := []*connectors.Message{
		msgAssistant("CLI-1362 looks tricky"),
		msgAssistant("CLI-1362 second mention from assistant"),
	}
	if got := extractTicketHints(msgs); len(got) != 0 {
		t.Fatalf("expected 0 hints, got %+v", got)
	}
}

func TestExtractLinkedURLs_DeduplicatesPreservingOrder(t *testing.T) {
	msgs := []*connectors.Message{
		msgUser("see https://linear.app/clinikk/issue/CLI-1362"),
		msgUser("and https://github.com/clinikk/foo/pull/42 too"),
		msgUser("re-pasting https://linear.app/clinikk/issue/CLI-1362"),
	}
	got := extractLinkedURLs(msgs)
	want := []string{
		"https://linear.app/clinikk/issue/CLI-1362",
		"https://github.com/clinikk/foo/pull/42",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// readToolCall builds a Message carrying a single Read-style tool
// call for the given file path, timestamped at ts (epoch-ms).
func readToolCall(path string, ts int64) *connectors.Message {
	input, _ := json.Marshal(map[string]string{"file_path": path})
	return &connectors.Message{
		Role: connectors.RoleAssistant,
		Ts:   ts,
		ToolCalls: []connectors.ToolCall{
			{Name: "Read", Input: string(input)},
		},
	}
}

func TestExtractPlanOfRecord_MostRecentMatchingFileWins(t *testing.T) {
	now := time.Now().UnixMilli()
	minAgo := func(ago time.Duration) int64 { return now - ago.Milliseconds() }
	msgs := []*connectors.Message{
		readToolCall("docs/superpowers/specs/2026-05-15-spec.md", minAgo(2*time.Hour)),
		readToolCall("src/services/foo.js", minAgo(1*time.Hour)), // not a plan
		readToolCall("docs/superpowers/plans/2026-05-15-labstack.md", minAgo(30*time.Minute)),
		readToolCall("docs/superpowers/plans/2026-05-15-labstack.md", minAgo(5*time.Minute)),
	}
	got := extractPlanOfRecord(msgs, now)
	if got == nil {
		t.Fatal("expected non-nil plan-of-record")
	}
	if got.Path != "docs/superpowers/plans/2026-05-15-labstack.md" {
		t.Errorf("path = %q, want plans/...", got.Path)
	}
	if got.ReadCount != 2 {
		t.Errorf("ReadCount = %d, want 2", got.ReadCount)
	}
}

func TestExtractPlanOfRecord_NoMatchReturnsNil(t *testing.T) {
	now := time.Now().UnixMilli()
	msgs := []*connectors.Message{
		readToolCall("src/foo.js", now-1000),
		readToolCall("README.md", now-500),
	}
	if got := extractPlanOfRecord(msgs, now); got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func TestExtractPlanOfRecord_AlsoMatchesResearchAnd00Plan(t *testing.T) {
	now := time.Now().UnixMilli()
	msgs := []*connectors.Message{
		readToolCall("docs/research/worklog/00-plan.md", now-1000),
	}
	got := extractPlanOfRecord(msgs, now)
	if got == nil || got.Path != "docs/research/worklog/00-plan.md" {
		t.Fatalf("got %+v, want 00-plan.md", got)
	}
}

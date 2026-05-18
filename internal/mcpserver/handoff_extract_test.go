package mcpserver

import (
	"reflect"
	"sort"
	"testing"

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

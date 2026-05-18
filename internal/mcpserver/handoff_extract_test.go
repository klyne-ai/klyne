package mcpserver

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/contexthealth"
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

// todoWriteCall builds a Message carrying a single TodoWrite
// tool call. Items is marshalled into the standard Claude Code
// shape: {"todos": [{"content": "...", "status": "..."}, ...]}.
func todoWriteCall(items []TodoItem) *connectors.Message {
	type wireItem struct {
		Content string `json:"content"`
		Status  string `json:"status"`
	}
	wire := make([]wireItem, 0, len(items))
	for _, it := range items {
		wire = append(wire, wireItem{Content: it.Content, Status: it.Status})
	}
	payload, _ := json.Marshal(map[string]any{"todos": wire})
	return &connectors.Message{
		Role: connectors.RoleAssistant,
		ToolCalls: []connectors.ToolCall{
			{Name: "TodoWrite", Input: string(payload)},
		},
	}
}

func TestExtractTodos_PicksMostRecentTodoWrite(t *testing.T) {
	older := todoWriteCall([]TodoItem{
		{Content: "stale", Status: "pending"},
	})
	newer := todoWriteCall([]TodoItem{
		{Content: "wire role gate", Status: "in_progress"},
		{Content: "add short-circuit", Status: "pending"},
		{Content: "ship docs", Status: "completed"},
	})
	in, pend := extractTodos([]*connectors.Message{older, newer})
	if len(in) != 1 || in[0].Content != "wire role gate" {
		t.Errorf("in_progress = %+v, want one entry 'wire role gate'", in)
	}
	if len(pend) != 1 || pend[0].Content != "add short-circuit" {
		t.Errorf("pending = %+v, want one entry 'add short-circuit'", pend)
	}
}

func TestExtractTodos_NoTodoWriteReturnsEmpty(t *testing.T) {
	in, pend := extractTodos([]*connectors.Message{msgUser("hi")})
	if len(in) != 0 || len(pend) != 0 {
		t.Fatalf("expected empty, got in=%v pend=%v", in, pend)
	}
}

func TestExtractTodos_MalformedJSONReturnsEmpty(t *testing.T) {
	bad := &connectors.Message{
		Role: connectors.RoleAssistant,
		ToolCalls: []connectors.ToolCall{
			{Name: "TodoWrite", Input: "{not json"},
		},
	}
	in, pend := extractTodos([]*connectors.Message{bad})
	if len(in) != 0 || len(pend) != 0 {
		t.Fatalf("expected empty on bad JSON, got in=%v pend=%v", in, pend)
	}
}

func TestBranchFromMessages_FirstNonEmptyWins(t *testing.T) {
	msgs := []*connectors.Message{
		{Role: connectors.RoleUser},
		{Role: connectors.RoleAssistant, GitBranch: "feat/labstack-integration"},
		{Role: connectors.RoleUser, GitBranch: "other"},
	}
	if got := branchFromMessages(msgs); got != "feat/labstack-integration" {
		t.Fatalf("got %q, want feat/labstack-integration", got)
	}
}

func TestBranchFromMessages_EmptyOnNoBranch(t *testing.T) {
	msgs := []*connectors.Message{{Role: connectors.RoleUser}}
	if got := branchFromMessages(msgs); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestBuildAnchorFiles_HonoursRelevanceOrderAndDirtyLabels(t *testing.T) {
	verdict := contexthealth.RelevanceVerdict{
		Files: []contexthealth.FileRelevance{
			{Path: "/repo/src/a.js", Score: 0.9, LastTouchTs: 100, Stale: false},
			{Path: "/repo/src/b.js", Score: 0.5, LastTouchTs: 80, Stale: false},
			{Path: "/repo/docs/stale.md", Score: 0.1, LastTouchTs: 10, Stale: true},
		},
	}
	dirty := map[string]bool{"/repo/src/a.js": true}
	got, staleCount := buildAnchorFiles(verdict, dirty, true, /*now=*/ 200)
	if staleCount != 1 {
		t.Errorf("staleCount = %d, want 1", staleCount)
	}
	if len(got) != 2 {
		t.Fatalf("got %d anchors, want 2", len(got))
	}
	if got[0].Path != "/repo/src/a.js" || !got[0].Dirty || got[0].DirtyUnknown {
		t.Errorf("first anchor wrong: %+v", got[0])
	}
	if got[1].Path != "/repo/src/b.js" || got[1].Dirty || got[1].DirtyUnknown {
		t.Errorf("second anchor wrong: %+v", got[1])
	}
}

func TestBuildAnchorFiles_DirtyUnknownWhenGitFailed(t *testing.T) {
	verdict := contexthealth.RelevanceVerdict{
		Files: []contexthealth.FileRelevance{
			{Path: "/repo/x.js", Score: 0.9, LastTouchTs: 100, Stale: false},
		},
	}
	got, _ := buildAnchorFiles(verdict, nil, false, 200)
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	if !got[0].DirtyUnknown || got[0].Dirty {
		t.Errorf("expected DirtyUnknown=true Dirty=false, got %+v", got[0])
	}
}

func TestBuildAnchorFiles_CapsAtSix(t *testing.T) {
	files := make([]contexthealth.FileRelevance, 0, 10)
	for i := 0; i < 10; i++ {
		files = append(files, contexthealth.FileRelevance{
			Path:        fmt.Sprintf("/repo/f%d.js", i),
			Score:       float64(10-i) / 10.0,
			LastTouchTs: int64(100 - i),
		})
	}
	verdict := contexthealth.RelevanceVerdict{Files: files}
	got, _ := buildAnchorFiles(verdict, nil, true, 200)
	if len(got) != 6 {
		t.Fatalf("got %d, want cap of 6", len(got))
	}
}

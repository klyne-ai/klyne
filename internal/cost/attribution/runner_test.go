package attribution

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/store"
)

// ---- helper builders -------------------------------------------------------

func assistantMsg(id, sessionID, gitBranch, cwd string, ts int64, tcs ...connectors.ToolCall) *connectors.Message {
	return &connectors.Message{
		ID:          id,
		SessionID:   sessionID,
		ProjectPath: "/proj/test",
		Role:        connectors.RoleAssistant,
		GitBranch:   gitBranch,
		Cwd:         cwd,
		Ts:          ts,
		TokensIn:    1000,
		TokensOut:   200,
		ToolCalls:   tcs,
	}
}

func userMsg(id, sessionID, gitBranch, cwd string, ts int64) *connectors.Message {
	return &connectors.Message{
		ID:          id,
		SessionID:   sessionID,
		ProjectPath: "/proj/test",
		Role:        connectors.RoleUser,
		GitBranch:   gitBranch,
		Cwd:         cwd,
		Ts:          ts,
		TokensIn:    100,
	}
}

func bashTC(id, cmd string) connectors.ToolCall {
	// Properly JSON-encode the command so bashCommand() can parse it.
	b, _ := json.Marshal(cmd)
	return connectors.ToolCall{
		ID:    id,
		Name:  "Bash",
		Input: `{"command":` + string(b) + `}`,
	}
}

func commitTC(id string) connectors.ToolCall {
	return bashTC(id, `git commit -m "feat: ship it"`)
}

func withResult(msg *connectors.Message, tcID, output string) *connectors.Message {
	msg.ToolResults = append(msg.ToolResults, connectors.ToolResult{
		ID:     tcID,
		Output: "abc1234def56789012345678901234567890\n",
	})
	return msg
}

// ---- tests -----------------------------------------------------------------

func TestBuildSpans_EmptyMessages(t *testing.T) {
	spans, err := buildSpans(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(spans) != 0 {
		t.Errorf("expected 0 spans for empty input, got %d", len(spans))
	}
}

func TestBuildSpans_AllExploration(t *testing.T) {
	// Messages with no git commit → single exploration span.
	msgs := []*connectors.Message{
		userMsg("u1", "sess-1", "feat/auth", "/proj", 1000),
		assistantMsg("a1", "sess-1", "feat/auth", "/proj", 2000, bashTC("tc1", "npm test")),
		userMsg("u2", "sess-1", "feat/auth", "/proj", 3000),
	}
	spans, err := buildSpans(msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(spans) != 1 {
		t.Fatalf("expected 1 exploration span, got %d", len(spans))
	}
	if spans[0].Bucket != "exploration" {
		t.Errorf("bucket = %q, want exploration", spans[0].Bucket)
	}
	if spans[0].ExplorationID == "" {
		t.Errorf("exploration_id should be set")
	}
	if spans[0].MsgCount != 3 {
		t.Errorf("msg_count = %d, want 3", spans[0].MsgCount)
	}
}

func TestBuildSpans_CommitClosesSpan(t *testing.T) {
	// Sequence: 3 messages then git commit → one commit span + one exploration.
	// SHA must be 40 hex chars for extractGitSHA to match.
	const commitSHA = "abc1234def5678901234567890abcdef12345678"
	commitMsg := assistantMsg("a3", "sess-1", "main", "/proj", 4000, commitTC("tc-commit"))
	commitMsg.ToolResults = []connectors.ToolResult{
		{ID: "tc-commit", Output: "[main " + commitSHA + "] feat: ship it\n"},
	}

	msgs := []*connectors.Message{
		userMsg("u1", "sess-1", "main", "/proj", 1000),
		assistantMsg("a1", "sess-1", "main", "/proj", 2000, bashTC("tc1", "go test ./...")),
		userMsg("u2", "sess-1", "main", "/proj", 3000),
		commitMsg,
		// One more message after commit → exploration span.
		userMsg("u3", "sess-1", "main", "/proj", 5000),
	}

	spans, err := buildSpans(msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(spans) != 2 {
		t.Fatalf("expected 2 spans (1 commit + 1 exploration), got %d", len(spans))
	}

	// Find commit span.
	var commitSpan, exploreSpan *store.WorkSpan
	for i := range spans {
		switch spans[i].Bucket {
		case "commit":
			commitSpan = &spans[i]
		case "exploration":
			exploreSpan = &spans[i]
		}
	}
	if commitSpan == nil {
		t.Fatal("no commit span found")
	}
	if exploreSpan == nil {
		t.Fatal("no exploration span found")
	}
	if commitSpan.CommitSHA == "" {
		t.Errorf("commit_sha should be set on commit span")
	}
}

func TestBuildSpans_TokenAccumulation(t *testing.T) {
	msgs := []*connectors.Message{
		{
			ID: "m1", SessionID: "s1", Role: connectors.RoleAssistant,
			GitBranch: "main", Cwd: "/proj", Ts: 1000,
			TokensIn: 1000, TokensOut: 200, CachedReadTokens: 400, CachedWriteTokens: 100,
		},
		{
			ID: "m2", SessionID: "s1", Role: connectors.RoleUser,
			GitBranch: "main", Cwd: "/proj", Ts: 2000,
			TokensIn: 500, TokensOut: 0,
		},
	}
	spans, err := buildSpans(msgs)
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	sp := spans[0]
	// fresh = 1000 - 400 - 100 = 500 from m1, 500 from m2 = 1000
	if sp.TokensFresh != 1000 {
		t.Errorf("tokens_fresh = %d, want 1000", sp.TokensFresh)
	}
	if sp.TokensCacheRead != 400 {
		t.Errorf("tokens_cache_read = %d, want 400", sp.TokensCacheRead)
	}
	if sp.TokensCacheWrite != 100 {
		t.Errorf("tokens_cache_write = %d, want 100", sp.TokensCacheWrite)
	}
	if sp.TokensOut != 200 {
		t.Errorf("tokens_out = %d, want 200", sp.TokensOut)
	}
}

func TestBuildSpans_MultipleBranches(t *testing.T) {
	// Two branches → two independent exploration spans.
	msgs := []*connectors.Message{
		userMsg("u1", "s1", "feat/a", "/proj", 1000),
		userMsg("u2", "s2", "feat/b", "/proj", 1500),
		assistantMsg("a1", "s1", "feat/a", "/proj", 2000, bashTC("tc1", "ls")),
		assistantMsg("a2", "s2", "feat/b", "/proj", 2500, bashTC("tc2", "pwd")),
	}
	spans, err := buildSpans(msgs)
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 2 {
		t.Fatalf("expected 2 spans (one per branch), got %d", len(spans))
	}
	branches := map[string]bool{}
	for _, sp := range spans {
		branches[sp.GitBranch] = true
	}
	if !branches["feat/a"] || !branches["feat/b"] {
		t.Errorf("branches not correctly attributed: %v", branches)
	}
}

func TestIsGitCommit(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		{"git commit -m 'fix: bug'", true},
		{"git commit --amend", true},
		{"git commit", true},
		{"cd /tmp && git commit -m 'x'", true},
		{"git status", false},
		{"echo git commit", false},
		{"git checkout main", false},
		{"", false},
	}
	for _, c := range cases {
		got := isGitCommit(c.cmd)
		if got != c.want {
			t.Errorf("isGitCommit(%q) = %v, want %v", c.cmd, got, c.want)
		}
	}
}

func TestExtractGitSHA(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"[main abc1234def5678901234567890abcdef12345678] feat: ship", "abc1234def5678901234567890abcdef12345678"},
		{"abc1234def5678901234567890abcdef12345678\n", "abc1234def5678901234567890abcdef12345678"},
		{"abc1234 (short sha)", "abc1234"},
		{"no sha here", ""},
		{"HEAD is now at deadbeef1 message", "deadbeef1"},
	}
	for _, c := range cases {
		got := extractGitSHA(c.input)
		if got != c.want {
			t.Errorf("extractGitSHA(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestBashCommand(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{`{"command":"git status"}`, "git status"},
		{`{"command":"npm test","env":{}}`, "npm test"},
		{`"git commit -m foo"`, "git commit -m foo"},
		{`git status`, "git status"},
	}
	for _, c := range cases {
		tc := connectors.ToolCall{Name: "Bash", Input: c.input}
		got := bashCommand(tc)
		if got != c.want {
			t.Errorf("bashCommand(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestExtractCommitSubject(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		want string
	}{
		{"plain -m double quotes", `git commit -m "fix: thing"`, "fix: thing"},
		{"plain -m single quotes", `git commit -m 'feat: add x'`, "feat: add x"},
		{"-am combined flag", `git commit -am "wip: stash"`, "wip: stash"},
		{"long-form --message=", `git commit --message="docs: update"`, "docs: update"},
		{"truncated >60 chars", `git commit -m "` + strings.Repeat("a", 80) + `"`, strings.Repeat("a", 57) + "..."},
		{"heredoc body subject",
			"git commit -m \"$(cat <<'EOF'\nfeat: heredoc subject\n\nbody line\nEOF\n)\"",
			"feat: heredoc subject"},
		{"no -m flag", `git commit --amend`, ""},
		{"empty cmd", ``, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := extractCommitSubject(c.cmd); got != c.want {
				t.Errorf("extractCommitSubject = %q, want %q", got, c.want)
			}
		})
	}
}

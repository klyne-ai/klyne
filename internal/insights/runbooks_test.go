package insights

import (
	"testing"

	"github.com/klyne-ai/klyne/internal/connectors"
)

func TestNormaliseCommand(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "npm run build", "npm run build"},
		{"abs path", "ls /Users/alice/projects/foo/bar", "ls $PATH"},
		{"home path", "cat ~/.ssh/config", "cat $HOME_PATH"},
		{"uuid", "kubectl logs 550e8400-e29b-41d4-a716-446655440000", "kubectl logs $UUID"},
		{"sha", "git show abcdef0123456789abcdef0123456789abcdef01", "git show $SHA"},
		{"ip", "ssh 192.168.1.42 echo ok", "ssh $IP echo ok"},
		{"port", "curl http://localhost:8080/health", "curl http://localhost:$PORT/health"},
		{"iso ts", "echo 2026-05-14T18:30:00Z", "echo $TS"},
		{"long quoted", `git commit -m "fix flaky test on macOS arm64"`, "git commit -m $STRING"},
		{"short quoted preserved", `echo "hi"`, `echo "hi"`},
		{"collapsed semicolons", "true ;;; false", "true ; false"},
		{"whitespace collapse", "echo    hello   world", "echo hello world"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normaliseCommand(tc.in)
			if got != tc.want {
				t.Errorf("normaliseCommand(%q)\n  got:  %q\n  want: %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestExtractBashCommand_JSONInput(t *testing.T) {
	got := extractBashCommand(`{"command":"npm run build","description":"build"}`)
	if got != "npm run build" {
		t.Errorf("extractBashCommand JSON got %q, want %q", got, "npm run build")
	}
}

func TestExtractBashCommand_RawString(t *testing.T) {
	got := extractBashCommand("ls -la")
	if got != "ls -la" {
		t.Errorf("extractBashCommand raw got %q, want %q", got, "ls -la")
	}
}

func TestExtractBashCommand_Empty(t *testing.T) {
	if got := extractBashCommand(""); got != "" {
		t.Errorf("expected empty for empty input, got %q", got)
	}
	if got := extractBashCommand(`{"command":"   "}`); got != "" {
		t.Errorf("expected empty for whitespace command, got %q", got)
	}
}

func TestExtractTaskWindows(t *testing.T) {
	msgs := []*connectors.Message{
		{Role: connectors.RoleUser, Ts: 100, Content: "deploy please"},
		{Role: connectors.RoleAssistant, Ts: 101, ToolCalls: []connectors.ToolCall{
			{Name: "Bash", Input: `{"command":"npm run build"}`},
		}},
		{Role: connectors.RoleAssistant, Ts: 102, ToolCalls: []connectors.ToolCall{
			{Name: "Bash", Input: `{"command":"npm run deploy"}`},
		}},
		{Role: connectors.RoleUser, Ts: 200, Content: "now test"},
		{Role: connectors.RoleAssistant, Ts: 201, ToolCalls: []connectors.ToolCall{
			{Name: "Bash", Input: `{"command":"npm run test"}`},
		}},
	}
	wins := extractTaskWindows(msgs, 0)
	if len(wins) != 2 {
		t.Fatalf("expected 2 task windows, got %d", len(wins))
	}
	if len(wins[0].Commands) != 2 || wins[0].Commands[0] != "npm run build" {
		t.Errorf("first window: got %+v", wins[0])
	}
	if len(wins[1].Commands) != 1 || wins[1].Commands[0] != "npm run test" {
		t.Errorf("second window: got %+v", wins[1])
	}
}

func TestExtractTaskWindows_NonBashIgnored(t *testing.T) {
	msgs := []*connectors.Message{
		{Role: connectors.RoleUser, Ts: 100},
		{Role: connectors.RoleAssistant, Ts: 101, ToolCalls: []connectors.ToolCall{
			{Name: "Read", Input: `{"file_path":"x.go"}`},
			{Name: "Edit", Input: `{}`},
			{Name: "Bash", Input: `{"command":"go test ./..."}`},
		}},
	}
	wins := extractTaskWindows(msgs, 0)
	if len(wins) != 1 || len(wins[0].Commands) != 1 {
		t.Fatalf("expected one bash command extracted, got %+v", wins)
	}
}

func TestCandidateIndex_FrequencyAndSessions(t *testing.T) {
	idx := newCandidateIndex()
	cmds := []string{"npm run build", "npm run deploy"}
	norms := []string{"npm run build", "npm run deploy"}
	idx.add(joinSignature(norms), cmds, norms, "s1", 1000)
	idx.add(joinSignature(norms), cmds, norms, "s1", 2000)
	idx.add(joinSignature(norms), cmds, norms, "s2", 3000)

	cfg := DefaultRunbookConfig()
	out := idx.materialize(cfg, "/proj", 10_000)
	if len(out) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(out))
	}
	c := out[0]
	if c.Occurrences != 3 {
		t.Errorf("occurrences = %d, want 3", c.Occurrences)
	}
	if c.DistinctSessions != 2 {
		t.Errorf("distinct sessions = %d, want 2", c.DistinctSessions)
	}
	if c.SuggestedName == "" {
		t.Errorf("expected non-empty suggested name")
	}
	if c.ID == "" || len(c.ID) != 12 {
		t.Errorf("expected 12-char id, got %q", c.ID)
	}
	if c.FirstSeenMs != 1000 || c.LastSeenMs != 3000 {
		t.Errorf("first/last = %d/%d", c.FirstSeenMs, c.LastSeenMs)
	}
}

func TestCandidateIndex_FiltersBelowMin(t *testing.T) {
	idx := newCandidateIndex()
	cmds := []string{"a", "b"}
	norms := []string{"a", "b"}
	// Only 2 occurrences in 1 session — below MinOccurrences=3 and
	// below MinDistinctSessions=2 in the default config.
	idx.add(joinSignature(norms), cmds, norms, "s1", 1)
	idx.add(joinSignature(norms), cmds, norms, "s1", 2)
	out := idx.materialize(DefaultRunbookConfig(), "/proj", 100)
	if len(out) != 0 {
		t.Errorf("expected 0 candidates below threshold, got %+v", out)
	}
}

func TestCandidateIndex_RankingByScore(t *testing.T) {
	idx := newCandidateIndex()
	a := []string{"a", "b"}
	b := []string{"c", "d"}
	// a: 5 occurrences across 3 sessions (high score)
	for i := 0; i < 5; i++ {
		idx.add(joinSignature(a), a, a, []string{"s1", "s2", "s3", "s1", "s2"}[i], int64(i+1))
	}
	// b: 3 occurrences across 2 sessions (lower score, but still passes threshold)
	idx.add(joinSignature(b), b, b, "s4", 10)
	idx.add(joinSignature(b), b, b, "s4", 11)
	idx.add(joinSignature(b), b, b, "s5", 12)

	out := idx.materialize(DefaultRunbookConfig(), "/proj", 100)
	if len(out) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(out))
	}
	if out[0].Signature != joinSignature(a) {
		t.Errorf("expected higher-frequency candidate first, got %q", out[0].Signature)
	}
	if out[0].Score <= out[1].Score {
		t.Errorf("expected first candidate score > second, got %.3f vs %.3f", out[0].Score, out[1].Score)
	}
}

func TestSuggestRunbookName(t *testing.T) {
	cases := []struct {
		steps []string
		want  string
	}{
		{[]string{"npm run build"}, "npm"},
		{[]string{"npm run build", "npm run deploy"}, "npm-then-npm"},
		{[]string{"./scripts/openbao/bao-secret.sh set $UUID", "kubectl apply -f $PATH"}, "bao-secret-then-kubectl"},
		{[]string{"a", "b", "c", "d"}, "a-and-3-more"},
	}
	for _, tc := range cases {
		got := suggestRunbookName(tc.steps)
		if got != tc.want {
			t.Errorf("suggestRunbookName(%v)\n  got:  %q\n  want: %q", tc.steps, got, tc.want)
		}
	}
}

func TestSignatureShortID_Deterministic(t *testing.T) {
	a := signatureShortID("npm run build ;; npm run deploy")
	b := signatureShortID("npm run build ;; npm run deploy")
	if a != b {
		t.Errorf("expected deterministic id, got %q vs %q", a, b)
	}
	if len(a) != 12 {
		t.Errorf("expected 12-char id, got %d chars", len(a))
	}
}

func TestScoreCandidate_RecencyDecay(t *testing.T) {
	// Use a large positive "now" so subtraction doesn't produce
	// negative lastSeenMs values (the scorer skips decay for
	// non-positive timestamps as a corruption guard).
	const dayMs int64 = 24 * 60 * 60 * 1000
	now := 400 * dayMs
	fresh := scoreCandidate(5, 2, now-dayMs, now)
	old := scoreCandidate(5, 2, now-89*dayMs, now)
	veryOld := scoreCandidate(5, 2, now-365*dayMs, now)
	if fresh <= old {
		t.Errorf("fresh score should beat 89-day-old: %.3f vs %.3f", fresh, old)
	}
	if veryOld == 0 {
		t.Errorf("very old score should be non-zero due to floor")
	}
	if veryOld >= fresh {
		t.Errorf("365-day-old should not beat fresh: %.3f vs %.3f", veryOld, fresh)
	}
}

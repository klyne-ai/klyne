package contexthealth

import (
	"strings"
	"testing"

	"github.com/klyne-ai/klyne/internal/connectors"
)

// systemMsg builds a role=system message with the given content.
func systemMsg(idx int, content string) *connectors.Message {
	return &connectors.Message{
		ID:      "s" + itoa(idx),
		Role:    connectors.RoleSystem,
		Content: content,
		Ts:      int64(idx) * 1000,
	}
}

func TestAttributeSources_Empty(t *testing.T) {
	t.Parallel()
	if got := AttributeSources(nil); got != nil {
		t.Errorf("nil msgs: got %v, want nil", got)
	}
	if got := AttributeSources([]*connectors.Message{}); got != nil {
		t.Errorf("empty msgs: got %v, want nil", got)
	}
}

func TestAttributeSources_StopsAtFirstUserTurn(t *testing.T) {
	t.Parallel()
	// System messages after the first user turn must not be counted.
	msgs := []*connectors.Message{
		systemMsg(0, "mcp: serena — 18K context"),
		userMsg(1, "hello"),
		systemMsg(2, "mcp: github — 12K context"), // after user turn → ignored
	}
	got := AttributeSources(msgs)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1; rows = %+v", len(got), got)
	}
	if got[0].Kind != SourceKindMCP {
		t.Errorf("Kind = %q, want mcp_server", got[0].Kind)
	}
}

func TestAttributeSources_MCPPrefixHeuristic(t *testing.T) {
	t.Parallel()
	msgs := []*connectors.Message{
		systemMsg(0, "mcp: serena session context injected by serena MCP server"),
	}
	got := AttributeSources(msgs)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1; rows = %+v", len(got), got)
	}
	if got[0].Kind != SourceKindMCP {
		t.Errorf("Kind = %q, want mcp_server", got[0].Kind)
	}
}

func TestAttributeSources_WellKnownMCPName(t *testing.T) {
	t.Parallel()
	// A system message whose first line mentions "serena" should resolve to MCP.
	msgs := []*connectors.Message{
		systemMsg(0, "serena: project memory for klyne\n... lots of content ..."),
	}
	got := AttributeSources(msgs)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1; rows = %+v", len(got), got)
	}
	if got[0].Kind != SourceKindMCP {
		t.Errorf("Kind = %q, want mcp_server", got[0].Kind)
	}
	if got[0].Name != "serena MCP" {
		t.Errorf("Name = %q, want %q", got[0].Name, "serena MCP")
	}
}

func TestAttributeSources_SkillPrefixHeuristic(t *testing.T) {
	t.Parallel()
	msgs := []*connectors.Message{
		systemMsg(0, "skill: superpowers — 8K injected"),
	}
	got := AttributeSources(msgs)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1; rows = %+v", len(got), got)
	}
	if got[0].Kind != SourceKindSkill {
		t.Errorf("Kind = %q, want skill", got[0].Kind)
	}
}

func TestAttributeSources_HookPrefixHeuristic(t *testing.T) {
	t.Parallel()
	msgs := []*connectors.Message{
		systemMsg(0, "klyne: hook context injected pre-turn"),
	}
	got := AttributeSources(msgs)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1; rows = %+v", len(got), got)
	}
	if got[0].Kind != SourceKindHook {
		t.Errorf("Kind = %q, want hook", got[0].Kind)
	}
}

func TestAttributeSources_JSONSource(t *testing.T) {
	t.Parallel()
	// A JSON-encoded system message with an "injected_by" field.
	content := `{"injected_by":"github","type":"mcp","content":"repo context..."}`
	msgs := []*connectors.Message{
		systemMsg(0, content),
	}
	got := AttributeSources(msgs)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1; rows = %+v", len(got), got)
	}
	if got[0].Name != "github" {
		t.Errorf("Name = %q, want github", got[0].Name)
	}
	if got[0].Kind != SourceKindMCP {
		t.Errorf("Kind = %q, want mcp_server", got[0].Kind)
	}
}

func TestAttributeSources_UnknownFallback(t *testing.T) {
	t.Parallel()
	msgs := []*connectors.Message{
		systemMsg(0, "Some context that doesn't match any known pattern."),
	}
	got := AttributeSources(msgs)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1; rows = %+v", len(got), got)
	}
	if got[0].Kind != SourceKindUnknown {
		t.Errorf("Kind = %q, want unknown", got[0].Kind)
	}
}

func TestAttributeSources_SortedByTokensDesc(t *testing.T) {
	t.Parallel()
	// Three sources with different content sizes; expect sorted by Tokens desc.
	big := strings.Repeat("x", 4000)
	small := strings.Repeat("y", 400)
	msgs := []*connectors.Message{
		systemMsg(0, "mcp: github\n"+small),
		systemMsg(1, "serena: project context\n"+big),
		systemMsg(2, "klyne: hook data\n"+strings.Repeat("z", 800)),
	}
	got := AttributeSources(msgs)
	if len(got) < 2 {
		t.Fatalf("len = %d, want ≥ 2", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Tokens < got[i].Tokens {
			t.Errorf("rows not sorted desc at [%d] vs [%d]: %d < %d", i-1, i, got[i-1].Tokens, got[i].Tokens)
		}
	}
}

func TestAttributeSources_MergesDuplicateSource(t *testing.T) {
	t.Parallel()
	// Two system messages from the same heuristic-identified source should
	// be merged into one row.
	msgs := []*connectors.Message{
		systemMsg(0, "klyne: hook context part 1"),
		systemMsg(1, "klyne: hook context part 2 with more data"),
	}
	got := AttributeSources(msgs)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1 (merged)", len(got))
	}
	// Total bytes = len(msgs[0].Content) + len(msgs[1].Content)
	expectedBytes := len(msgs[0].Content) + len(msgs[1].Content)
	expectedTokens := charsToTokens(expectedBytes)
	if got[0].Tokens != expectedTokens {
		t.Errorf("Tokens = %d, want %d", got[0].Tokens, expectedTokens)
	}
}

func TestCharsToTokens(t *testing.T) {
	t.Parallel()
	cases := []struct{ bytes, want int }{
		{0, 0},
		{1, 1},
		{4, 1},
		{5, 2},
		{400, 100},
		{1000, 250},
	}
	for _, tc := range cases {
		if got := charsToTokens(tc.bytes); got != tc.want {
			t.Errorf("charsToTokens(%d) = %d, want %d", tc.bytes, got, tc.want)
		}
	}
}

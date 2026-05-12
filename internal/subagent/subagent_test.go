package subagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// writeJSONL writes one JSONL line per element.
func writeJSONL(t *testing.T, path string, rows []map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	for _, r := range rows {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		_, _ = f.Write(b)
		_, _ = f.Write([]byte("\n"))
	}
}

func TestParseFile_ExtractsParentAndAgentFromPath(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Join(dir, "enc-project", "parent-sess-1", "subagents")
	path := filepath.Join(parent, "agent-abc123.jsonl")
	writeJSONL(t, path, []map[string]any{
		{"timestamp": "2026-05-01T10:00:00Z", "cwd": "/proj/x", "message": map[string]any{
			"model": "claude-sonnet-4-5",
			"usage": map[string]any{
				"input_tokens": 100, "output_tokens": 20, "cache_read_input_tokens": 60,
			},
		}},
		{"timestamp": "2026-05-01T10:00:05Z", "cwd": "/proj/x", "message": map[string]any{
			"model": "claude-sonnet-4-5",
			"usage": map[string]any{
				"input_tokens": 200, "output_tokens": 50, "cache_read_input_tokens": 150,
			},
		}},
	})

	stat, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if stat.ParentSessionID != "parent-sess-1" {
		t.Errorf("parent = %q; want parent-sess-1", stat.ParentSessionID)
	}
	if stat.AgentID != "abc123" {
		t.Errorf("agent = %q; want abc123", stat.AgentID)
	}
	if stat.MsgCount != 2 {
		t.Errorf("msgCount = %d; want 2", stat.MsgCount)
	}
	// tokens_in = sum of (input + cache_read + cache_creation) per line
	// line 1: 100 + 60 + 0 = 160; line 2: 200 + 150 + 0 = 350. Total = 510.
	if stat.TokensIn != 510 {
		t.Errorf("TokensIn = %d; want 510", stat.TokensIn)
	}
	if stat.TokensOut != 70 {
		t.Errorf("TokensOut = %d; want 70", stat.TokensOut)
	}
	if stat.CachedRead != 210 {
		t.Errorf("CachedRead = %d; want 210", stat.CachedRead)
	}
}

func TestParseFile_SkipsMalformedLines(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Join(dir, "p", "s", "subagents")
	path := filepath.Join(parent, "agent-x.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// One good, one garbage, one good.
	content := `{"timestamp":"2026-05-01T10:00:00Z","message":{"usage":{"input_tokens":10}}}` + "\n" +
		`{not valid json` + "\n" +
		`{"timestamp":"2026-05-01T10:00:01Z","message":{"usage":{"input_tokens":20}}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	stat, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if stat.MsgCount != 2 || stat.TokensIn != 30 {
		t.Errorf("got %+v; want MsgCount=2 TokensIn=30", stat)
	}
}

func TestAggregate_GroupsByParent(t *testing.T) {
	stats := []Stat{
		{ParentSessionID: "p1", AgentID: "a", TokensIn: 100, MsgCount: 2, EndedAtMs: 200},
		{ParentSessionID: "p1", AgentID: "b", TokensIn: 300, MsgCount: 5, EndedAtMs: 400},
		{ParentSessionID: "p2", AgentID: "c", TokensIn: 50, MsgCount: 1, EndedAtMs: 100},
	}
	out := Aggregate(stats)
	if len(out) != 2 {
		t.Fatalf("expected 2 parents, got %d", len(out))
	}
	if out[0].ParentSessionID != "p1" {
		t.Errorf("first parent = %q; want p1 (more tokens)", out[0].ParentSessionID)
	}
	if out[0].SubagentCount != 2 || out[0].TokensIn != 400 {
		t.Errorf("p1 rollup wrong: %+v", out[0])
	}
	if out[0].LastActivityMs != 400 {
		t.Errorf("LastActivityMs = %d; want 400", out[0].LastActivityMs)
	}
}

func TestParseTs_AcceptsRFC3339AndNano(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"2026-05-01T10:00:00Z", true},
		{"2026-05-01T10:00:00.123Z", true},
		{"", false},
		{"not-a-time", false},
	}
	for i, c := range cases {
		got := parseTs(c.in)
		if (got > 0) != c.want {
			t.Errorf("case %d (%s): got %s want has-value=%v", i, c.in, strconv.FormatInt(got, 10), c.want)
		}
	}
}

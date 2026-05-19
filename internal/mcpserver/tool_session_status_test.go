package mcpserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/contexthealth"
)

// TestFormatSessionStatus_HappyPath_AllSectionsPresent confirms the
// renderer emits the verdict header, the reason, the trajectory
// headline, the sparkline, the per-turn table (5-column variant when
// context window is known), and the bloat-sources list.
func TestFormatSessionStatus_HappyPath_AllSectionsPresent(t *testing.T) {
	now := time.Date(2026, 5, 19, 10, 14, 0, 0, time.UTC).UnixMilli()
	out := GetSessionStatusOutput{
		SessionID:      "abcd1234-very-long-id",
		Path:           "/tmp/session.jsonl",
		Model:          "claude-opus-4-7",
		State:          "drifting",
		Action:         "continue",
		Reason:         "Context fill is climbing but tool outputs remain bounded.",
		ContextFillPct: 14.0,
		MsgCount:       120,
		ContextWindow:  200_000,
		FirstInput:     20_000,
		LatestInput:    28_450,
		PeakInput:      31_200,
		WindowStartMs:  now - 60*60*1000,
		WindowEndMs:    now,
		Points: []contexthealth.TimelinePoint{
			{TsMs: now - 60*60*1000, TotalInput: 20_000, CachedReadTokens: 12_000, EffectiveInput: 8_000},
			{TsMs: now - 45*60*1000, TotalInput: 25_000, CachedReadTokens: 17_000, EffectiveInput: 8_000},
			{TsMs: now - 30*60*1000, TotalInput: 31_200, CachedReadTokens: 22_000, EffectiveInput: 9_200},
			{TsMs: now, TotalInput: 28_450, CachedReadTokens: 20_100, EffectiveInput: 8_350},
		},
		Bloat: []contexthealth.BloatRow{
			{Label: "Read(/big/file.json)", SharePct: 35.0},
			{Label: "Bash(npm test)", SharePct: 18.0},
			{Label: "Read(/another/file.go)", SharePct: 9.0},
		},
	}

	md := formatSessionStatusAsMarkdown(out, time.UTC, "UTC")

	for _, want := range []string{
		"# Session status",
		"`abcd1234`",                 // short session id
		"drifting",                   // state
		"continue",                   // action
		"Context fill:",              // fill label
		"Context fill is climbing",   // reason text
		"## Tokens",
		"## Top context-bloat sources",
		"Read(/big/file.json)",
		"| time",                     // table header start
		"| input tokens",             // table header column
		"| % of context",             // table header column (5-col variant)
		"| cached",
		"| uncached",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("rendered markdown missing %q\n----\n%s\n----", want, md)
		}
	}
}

// TestFormatSessionStatus_NoSession returns a single line when the
// resolver could not find a session under the cwd. No headers, no
// empty sections — keeps the slash-prompt output clean.
func TestFormatSessionStatus_NoSession(t *testing.T) {
	out := GetSessionStatusOutput{
		Reason:   "No Claude Code session found for this working directory.",
		Markdown: "", // renderer fills this
	}
	md := formatSessionStatusAsMarkdown(out, time.UTC, "UTC")
	if !strings.Contains(md, "No Claude Code session") {
		t.Errorf("no-session output should surface the reason verbatim; got:\n%s", md)
	}
	if strings.Contains(md, "## Tokens") || strings.Contains(md, "## Top context-bloat") {
		t.Errorf("no-session output must not include token / bloat headers; got:\n%s", md)
	}
}

// TestFormatSessionStatus_Ambiguous renders the candidate list and
// short-circuits the verdict + token sections.
func TestFormatSessionStatus_Ambiguous(t *testing.T) {
	out := GetSessionStatusOutput{
		Ambiguous: true,
		Candidates: []CandidateRow{
			{SessionID: "s1", Preview: "hello", IsActive: true, ModTime: "2026-05-19T10:00:00Z", MsgCount: 12},
			{SessionID: "s2", Preview: "world", IsActive: false, ModTime: "2026-05-19T09:00:00Z", MsgCount: 30},
		},
	}
	md := formatSessionStatusAsMarkdown(out, time.UTC, "UTC")
	if !strings.Contains(md, "get_session_status") {
		t.Errorf("ambiguous markdown must name the tool to retry: %s", md)
	}
	if !strings.Contains(md, "s1") || !strings.Contains(md, "s2") {
		t.Errorf("ambiguous markdown must list every candidate: %s", md)
	}
	if strings.Contains(md, "## Tokens") || strings.Contains(md, "## Top context-bloat") {
		t.Errorf("ambiguous markdown must not include analysis sections: %s", md)
	}
}

// TestFormatSessionStatus_HealthOnly_TimelineEmpty covers the short-
// session edge case: the verdict is computable but the timeline has
// no qualifying assistant turns yet. The renderer must show the
// verdict and a "no token-timeline rows yet" subline instead of
// failing or printing an empty table.
func TestFormatSessionStatus_HealthOnly_TimelineEmpty(t *testing.T) {
	out := GetSessionStatusOutput{
		SessionID:      "tiny",
		State:          "healthy",
		Action:         "continue",
		Reason:         "Session just started.",
		ContextFillPct: 2.0,
		ContextWindow:  200_000,
		Points:         nil,
	}
	md := formatSessionStatusAsMarkdown(out, time.UTC, "UTC")
	if !strings.Contains(md, "healthy") {
		t.Errorf("verdict must still render when timeline is empty: %s", md)
	}
	if !strings.Contains(md, "## Tokens") {
		t.Errorf("Tokens section header must still render: %s", md)
	}
	if !strings.Contains(md, "no token-timeline rows") {
		t.Errorf("Tokens section must explain the empty timeline: %s", md)
	}
}

// TestFormatSessionStatus_UnknownContextWindow renders the 4-column
// table variant — no '% of context' column when ContextWindow is 0.
func TestFormatSessionStatus_UnknownContextWindow(t *testing.T) {
	now := time.Date(2026, 5, 19, 10, 0, 0, 0, time.UTC).UnixMilli()
	out := GetSessionStatusOutput{
		SessionID:     "xyz",
		State:         "healthy",
		Action:        "continue",
		Reason:        "ok",
		ContextWindow: 0, // unknown
		Points: []contexthealth.TimelinePoint{
			{TsMs: now, TotalInput: 1234, CachedReadTokens: 800, EffectiveInput: 434},
		},
		LatestInput: 1234,
		FirstInput:  1234,
		PeakInput:   1234,
	}
	md := formatSessionStatusAsMarkdown(out, time.UTC, "UTC")
	if strings.Contains(md, "% of context") {
		t.Errorf("4-col table variant must not include '%% of context' header when ContextWindow=0: %s", md)
	}
	if !strings.Contains(md, "input tokens") {
		t.Errorf("4-col table variant must still include 'input tokens' header: %s", md)
	}
}

// TestHandleGetSessionStatus_NoSession returns the no-session
// markdown when the cwd has no candidates.
func TestHandleGetSessionStatus_NoSession(t *testing.T) {
	withFakeHome(t)
	_, out, err := HandleGetSessionStatus(context.Background(), nil, GetSessionStatusInput{
		CWD: "/tmp/proj-no-sessions",
	})
	if err != nil {
		t.Fatalf("HandleGetSessionStatus: %v", err)
	}
	if !strings.Contains(out.Markdown, "No Claude Code session") {
		t.Errorf("expected no-session markdown, got: %s", out.Markdown)
	}
	if out.State != "" {
		t.Errorf("State must be empty when no session: %q", out.State)
	}
	if len(out.Points) != 0 {
		t.Errorf("Points must be empty when no session")
	}
}

// TestHandleGetSessionStatus_HappyPath seeds one session under the
// cwd's project tree, calls the handler, and verifies the Markdown
// contains both the verdict section and the Tokens section.
func TestHandleGetSessionStatus_HappyPath(t *testing.T) {
	home := withFakeHome(t)
	cwd := "/tmp/proj-status-happy"
	dir := filepath.Join(home, ".claude", "projects", EncodeCWD(cwd))
	// Two assistant turns is the minimum the timeline needs to draw
	// first/peak/latest distinctly. Pick token counts that produce a
	// visible trajectory.
	now := time.Now()
	lines := []string{
		fmt.Sprintf(`{"type":"user","sessionId":"sess-happy","timestamp":%q,"message":{"role":"user","content":[{"type":"text","text":"start"}]}}`, now.Add(-10*time.Minute).UTC().Format(time.RFC3339Nano)),
		fmt.Sprintf(`{"type":"assistant","sessionId":"sess-happy","timestamp":%q,"message":{"role":"assistant","model":"claude-opus-4-7","usage":{"input_tokens":5000,"cache_read_input_tokens":2000,"output_tokens":100}}}`, now.Add(-9*time.Minute).UTC().Format(time.RFC3339Nano)),
		fmt.Sprintf(`{"type":"user","sessionId":"sess-happy","timestamp":%q,"message":{"role":"user","content":[{"type":"text","text":"more"}]}}`, now.Add(-5*time.Minute).UTC().Format(time.RFC3339Nano)),
		fmt.Sprintf(`{"type":"assistant","sessionId":"sess-happy","timestamp":%q,"message":{"role":"assistant","model":"claude-opus-4-7","usage":{"input_tokens":12000,"cache_read_input_tokens":8000,"output_tokens":200}}}`, now.Add(-4*time.Minute).UTC().Format(time.RFC3339Nano)),
	}
	writeJSONL(t, dir, "session-happy.jsonl", lines...)

	_, out, err := HandleGetSessionStatus(context.Background(), nil, GetSessionStatusInput{CWD: cwd})
	if err != nil {
		t.Fatalf("HandleGetSessionStatus: %v", err)
	}
	if out.State == "" {
		t.Errorf("State should be populated for a seeded session, got empty; markdown:\n%s", out.Markdown)
	}
	if !strings.Contains(out.Markdown, "# Session status") {
		t.Errorf("happy-path markdown missing verdict header:\n%s", out.Markdown)
	}
	if !strings.Contains(out.Markdown, "## Tokens") {
		t.Errorf("happy-path markdown missing Tokens section:\n%s", out.Markdown)
	}
}

// TestHandleGetSessionStatus_AmbiguousCWD seeds two sessions in the
// same project tree that are both within the active window so the
// resolver cannot pick one — handler must surface candidates without
// guessing.
func TestHandleGetSessionStatus_AmbiguousCWD(t *testing.T) {
	home := withFakeHome(t)
	cwd := "/tmp/proj-status-ambiguous"
	dir := filepath.Join(home, ".claude", "projects", EncodeCWD(cwd))
	now := time.Now()
	// Both sessions sit OUTSIDE the 30s active window (so neither
	// triggers PickActiveSession's preference), forcing the resolver
	// into the ambiguous branch.
	base := now.Add(-2 * time.Hour)
	for i := 1; i <= 2; i++ {
		name := fmt.Sprintf("session-%d.jsonl", i)
		line := fmt.Sprintf(
			`{"type":"user","sessionId":"sess-%d","timestamp":%q,"message":{"role":"user","content":[{"type":"text","text":"a"}]}}`,
			i, base.Add(time.Duration(i)*time.Minute).UTC().Format(time.RFC3339Nano),
		)
		p := writeJSONL(t, dir, name, line)
		mtime := base.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatalf("chtimes %s: %v", name, err)
		}
	}

	_, out, err := HandleGetSessionStatus(context.Background(), nil, GetSessionStatusInput{CWD: cwd})
	if err != nil {
		t.Fatalf("HandleGetSessionStatus: %v", err)
	}
	if !out.Ambiguous {
		t.Errorf("expected Ambiguous=true, got %+v", out)
	}
	if len(out.Candidates) != 2 {
		t.Errorf("expected 2 candidates, got %d", len(out.Candidates))
	}
	if !strings.Contains(out.Markdown, "get_session_status") {
		t.Errorf("ambiguous markdown must reference the tool name; got:\n%s", out.Markdown)
	}
}

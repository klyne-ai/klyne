package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// adviseFixture is a tiny end-to-end harness: it sets HOME to a
// temp directory so config and advisor-state both land under the
// fixture, writes a synthetic Claude Code transcript at a known
// path, and walks the user through running `klyne advise` against
// it.
type adviseFixture struct {
	t       *testing.T
	dir     string
	cwd     string
	homeOld string
}

func newAdviseFixture(t *testing.T) *adviseFixture {
	t.Helper()
	dir := t.TempDir()
	homeOld := os.Getenv("HOME")
	t.Setenv("HOME", dir)
	cwd := filepath.Join(dir, "proj")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("mkdir cwd: %v", err)
	}
	return &adviseFixture{t: t, dir: dir, cwd: cwd, homeOld: homeOld}
}

// claudeTranscriptForCWD writes a Claude Code JSONL transcript at
// the path Claude Code itself would use (~/.claude/projects/<encoded-cwd>/<sessionID>.jsonl).
// Lines are written verbatim — caller controls the content.
func (f *adviseFixture) claudeTranscriptForCWD(sessionID string, lines []string) string {
	f.t.Helper()
	encoded := strings.ReplaceAll(f.cwd, "/", "-")
	dir := filepath.Join(f.dir, ".claude", "projects", encoded)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatalf("mkdir transcript dir: %v", err)
	}
	path := filepath.Join(dir, sessionID+".jsonl")
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		f.t.Fatalf("write transcript: %v", err)
	}
	return path
}

// claudeAssistantLine renders one Claude Code assistant JSONL line.
func claudeAssistantLine(sessionID string, ts time.Time, inputTokens, cacheRead int64) string {
	return fmt.Sprintf(
		`{"type":"assistant","uuid":"u-%d","sessionId":%q,"timestamp":%q,"cwd":"/tmp/proj","message":{"role":"assistant","content":[{"type":"text","text":"reply"}],"id":"msg-%d","model":"claude-sonnet-4.5","usage":{"input_tokens":%d,"output_tokens":1,"cache_read_input_tokens":%d,"cache_creation_input_tokens":0}}}`,
		ts.UnixMilli(), sessionID, ts.UTC().Format(time.RFC3339Nano), ts.UnixMilli(), inputTokens, cacheRead,
	)
}

// claudeUserLine renders one Claude Code user-message JSONL line
// with the given content.
func claudeUserLine(sessionID, content string, ts time.Time) string {
	return fmt.Sprintf(
		`{"type":"user","uuid":"u-%d","sessionId":%q,"timestamp":%q,"cwd":"/tmp/proj","message":{"role":"user","content":[{"type":"text","text":%q}]}}`,
		ts.UnixMilli(), sessionID, ts.UTC().Format(time.RFC3339Nano), content,
	)
}

func TestComputeAdvisory_FiresAccelerationOnDoublingSession(t *testing.T) {
	fix := newAdviseFixture(t)

	now := time.Now()
	lines := []string{}
	// Five baseline turns at 5K input tokens each, then three
	// turns rising 9K, 15K, 25K — same shape the unit tests
	// exercise.
	for i := 0; i < 5; i++ {
		lines = append(lines, claudeAssistantLine("sess1", now.Add(-time.Duration(8-i)*time.Minute), 5_000, 0))
	}
	lines = append(lines,
		claudeAssistantLine("sess1", now.Add(-3*time.Minute), 9_000, 0),
		claudeAssistantLine("sess1", now.Add(-2*time.Minute), 15_000, 0),
		claudeAssistantLine("sess1", now.Add(-1*time.Minute), 25_000, 0),
	)
	fix.claudeTranscriptForCWD("sess1", lines)

	// Run the advisor with cwd pointing at the fixture project.
	stdin := strings.NewReader(fmt.Sprintf(`{"cwd":%q}`, fix.cwd))
	out, err := computeAdvisory(context.Background(), stdin)
	if err != nil {
		t.Fatalf("computeAdvisory: %v", err)
	}
	if out == "" {
		t.Fatalf("expected non-empty advisory, got silent")
	}

	var got hookOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("hook output not valid JSON: %v\n%s", err, out)
	}
	if got.HookSpecificOutput.HookEventName != "UserPromptSubmit" {
		t.Fatalf("hookEventName=%q", got.HookSpecificOutput.HookEventName)
	}
	if !strings.Contains(got.HookSpecificOutput.AdditionalContext, "doubled") {
		t.Fatalf("expected 'doubled' in advisory, got %q", got.HookSpecificOutput.AdditionalContext)
	}
}

func TestComputeAdvisory_SilentWhenHealthy(t *testing.T) {
	fix := newAdviseFixture(t)

	now := time.Now()
	lines := []string{}
	for i := 0; i < 8; i++ {
		lines = append(lines, claudeAssistantLine("sessHealthy", now.Add(-time.Duration(8-i)*time.Minute), 5_000, 0))
	}
	fix.claudeTranscriptForCWD("sessHealthy", lines)

	stdin := strings.NewReader(fmt.Sprintf(`{"cwd":%q}`, fix.cwd))
	out, err := computeAdvisory(context.Background(), stdin)
	if err != nil {
		t.Fatalf("computeAdvisory: %v", err)
	}
	if out != "" {
		t.Fatalf("expected silent on healthy session, got %q", out)
	}
}

func TestComputeAdvisory_NoSessionInCWD(t *testing.T) {
	fix := newAdviseFixture(t)
	stdin := strings.NewReader(fmt.Sprintf(`{"cwd":%q}`, fix.cwd))
	out, err := computeAdvisory(context.Background(), stdin)
	if err != nil {
		t.Fatalf("computeAdvisory: %v", err)
	}
	if out != "" {
		t.Fatalf("expected silent on no-session-found, got %q", out)
	}
}

func TestComputeAdvisory_DisabledViaConfigStaysSilent(t *testing.T) {
	fix := newAdviseFixture(t)

	// Build a session that would normally fire the acceleration
	// trigger.
	now := time.Now()
	lines := []string{}
	for i := 0; i < 5; i++ {
		lines = append(lines, claudeAssistantLine("sessOff", now.Add(-time.Duration(8-i)*time.Minute), 5_000, 0))
	}
	lines = append(lines,
		claudeAssistantLine("sessOff", now.Add(-3*time.Minute), 9_000, 0),
		claudeAssistantLine("sessOff", now.Add(-2*time.Minute), 15_000, 0),
		claudeAssistantLine("sessOff", now.Add(-1*time.Minute), 25_000, 0),
	)
	fix.claudeTranscriptForCWD("sessOff", lines)

	// Write a config with the advisor explicitly disabled.
	klyneDir := filepath.Join(fix.dir, ".klyne")
	if err := os.MkdirAll(klyneDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	configBody := "[advisor]\ndisabled = true\n"
	if err := os.WriteFile(filepath.Join(klyneDir, "config.toml"), []byte(configBody), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	out, err := computeAdvisory(context.Background(),
		strings.NewReader(fmt.Sprintf(`{"cwd":%q}`, fix.cwd)))
	if err != nil {
		t.Fatalf("computeAdvisory: %v", err)
	}
	if out != "" {
		t.Fatalf("expected silent advisor when disabled=true, got %q", out)
	}
}

func TestComputeAdvisory_TransitionPreventsRefire(t *testing.T) {
	fix := newAdviseFixture(t)

	now := time.Now()
	lines := []string{}
	for i := 0; i < 5; i++ {
		lines = append(lines, claudeAssistantLine("sessTrans", now.Add(-time.Duration(8-i)*time.Minute), 5_000, 0))
	}
	lines = append(lines,
		claudeAssistantLine("sessTrans", now.Add(-3*time.Minute), 9_000, 0),
		claudeAssistantLine("sessTrans", now.Add(-2*time.Minute), 15_000, 0),
		claudeAssistantLine("sessTrans", now.Add(-1*time.Minute), 25_000, 0),
	)
	fix.claudeTranscriptForCWD("sessTrans", lines)

	first, err := computeAdvisory(context.Background(),
		strings.NewReader(fmt.Sprintf(`{"cwd":%q}`, fix.cwd)))
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if first == "" {
		t.Fatalf("first call expected to fire")
	}

	second, err := computeAdvisory(context.Background(),
		strings.NewReader(fmt.Sprintf(`{"cwd":%q}`, fix.cwd)))
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if second != "" {
		t.Fatalf("second call should be silent (state recorded), got %q", second)
	}
}

package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestAudit_NegativeLimitDoesNotPanic verifies the regression: a negative
// --limit must not panic via a negative slice bound in the transcript
// discovery (`rows[:limit]`). The flag value is clamped to 0 in runAudit, so
// the command should run cleanly (and simply find/limit to no transcripts).
func TestAudit_NegativeLimitDoesNotPanic(t *testing.T) {
	cmd := newAuditCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--limit", "-1"})

	// The only acceptable failure here is a benign one (e.g. no transcripts,
	// or a DB open warning) — never a panic. runAudit clamps limit to 0 so
	// discoverClaudeTranscripts/discoverCodexTranscripts cannot slice with a
	// negative bound.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("audit-sessions --limit -1 panicked: %v", r)
		}
	}()
	if err := cmd.Execute(); err != nil {
		// An error is fine; a panic is not. Surface it for visibility.
		t.Logf("audit-sessions --limit -1 returned err (acceptable): %v", err)
	}
}

// TestDiscoverTranscripts_ZeroLimit guards the slice-bound directly: a 0
// limit must yield an empty result without panicking, independent of how
// many transcripts exist on the host.
func TestDiscoverTranscripts_ZeroLimit(t *testing.T) {
	claude, err := discoverClaudeTranscripts(0)
	if err != nil && !strings.Contains(err.Error(), "no such file") {
		t.Fatalf("discoverClaudeTranscripts(0): %v", err)
	}
	if len(claude) != 0 {
		t.Errorf("claude limit=0 returned %d paths; want 0", len(claude))
	}
	codex, err := discoverCodexTranscripts(0)
	if err != nil {
		t.Fatalf("discoverCodexTranscripts(0): %v", err)
	}
	if len(codex) != 0 {
		t.Errorf("codex limit=0 returned %d paths; want 0", len(codex))
	}
}

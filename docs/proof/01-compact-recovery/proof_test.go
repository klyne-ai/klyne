// Package compactrecovery contains the reproducible proof that Klyne's
// get_pre_compact_context tool recovers session content the AI itself
// cannot see after /compact runs.
//
// The test in this file is the source of truth for the claim made in
// claim.md. Run it from the repo root:
//
//	go test -v ./docs/proof/01-compact-recovery/
//
// or via the shorthand:
//
//	make proof
package compactrecovery_test

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/mcpserver"
)

// TestProof_CompactRecoveryReturnsContentClaudeCannotSee is the headline
// proof: Klyne reads the JSONL transcript and returns the pre-compact
// turns even though Claude's view (post-compact summary + new turns)
// no longer contains them.
//
// The test asserts three things:
//
//  1. Klyne recovers all 12 pre-compact messages from the fixture.
//  2. Specific high-signal strings present ONLY in the pre-compact
//     turns (file paths, regex literals, the "XX1234" account mask the
//     user manually verified) appear in Klyne's recovered output.
//  3. The same strings do NOT appear in the post-compact lines —
//     confirming Claude's view, after /compact, has lost them.
//
// If this test fails, Klyne is not delivering on the README's headline
// claim. The whole proof contract rests here.
func TestProof_CompactRecoveryReturnsContentClaudeCannotSee(t *testing.T) {
	fixture := fixturePath(t, "fixture.jsonl")

	bundle, err := mcpserver.LoadPreCompactMessages(fixture, 30)
	if err != nil {
		t.Fatalf("LoadPreCompactMessages: %v", err)
	}

	// Assertion 1: the compact boundary was found and metadata decoded.
	if !bundle.FoundCompact {
		t.Fatalf("FoundCompact = false; the fixture's compact_boundary was not detected")
	}
	if bundle.Trigger != "manual" {
		t.Errorf("Trigger = %q, want %q (compactMetadata.trigger from the fixture)", bundle.Trigger, "manual")
	}
	if bundle.PreTokens != 228000 {
		t.Errorf("PreTokens = %d, want 228000 (from the fixture's compactMetadata)", bundle.PreTokens)
	}

	// Assertion 2: the recovered count matches the pre-compact section
	// of the fixture (12 messages — six user/assistant pairs).
	const wantRecovered = 12
	if got := len(bundle.Messages); got != wantRecovered {
		t.Fatalf("recovered %d messages, want %d (pre-compact turn count in fixture)", got, wantRecovered)
	}

	// Assertion 3: high-signal strings present only in the pre-compact
	// turns are present in Klyne's recovery.
	signalsThatMustBeRecovered := []string{
		"bank SMS regex",                                // user msg 1: original task framing
		"rejecting valid HDFC messages",                 // user msg 1: pain description
		"/repo/trackit/src/parser.ts",                   // file path the AI edited
		"asterisk before the masked account",            // assistant reasoning, msg 2
		"XX1234",                                        // masked-account format the user verified
		"HDFC uses 'XX' as the mask",                    // assistant insight, msg 4
		"/repo/trackit/test/fixtures/hdfc-credit.txt",   // fixture path read mid-debug
		"npm test -- src/parser.test.ts",                // exact command run
		"12 passed, 0 failed",                           // test output that confirmed completion
	}

	recoveredCorpus := concatRecoveredContent(bundle.Messages)
	for _, sig := range signalsThatMustBeRecovered {
		if !strings.Contains(recoveredCorpus, sig) {
			t.Errorf("recovered messages missing high-signal string %q\n"+
				"this means Klyne's pre-compact recovery is incomplete — README claim broken",
				sig)
		}
	}

	// Assertion 4: confirm the SAME signals are absent from the
	// post-compact lines. This is the proof that Claude's view,
	// post-compact, does NOT have them — they were genuinely lost.
	postCompactLines := readPostCompactLines(t, fixture)
	for _, sig := range signalsThatMustBeRecovered {
		if strings.Contains(postCompactLines, sig) {
			t.Errorf("signal %q was found in post-compact lines too — fixture is not isolating "+
				"the loss; choose a more unique signal for an honest proof",
				sig)
		}
	}

	// Assertion 5: report the recoverable byte total. This is the
	// quantitative claim Klyne makes — not a token-savings %, just the
	// raw size of context the user would otherwise have to re-explain.
	t.Logf("Klyne recovered %d pre-compact messages, %d bytes of content "+
		"that the AI's post-compact view no longer contains.",
		len(bundle.Messages), len(recoveredCorpus))
}

// TestProof_PostCompactLinesDoNotContainPreCompactSignals is the
// "negative" half of the proof above, run as a standalone assertion
// so a failure here clearly identifies whether the issue is on the
// recovery side (test #1) or the fixture-isolation side (this test).
func TestProof_PostCompactLinesDoNotContainPreCompactSignals(t *testing.T) {
	fixture := fixturePath(t, "fixture.jsonl")
	postCompactLines := readPostCompactLines(t, fixture)

	// These strings MUST NOT appear after the compact_boundary in the
	// fixture. If they do, the proof is contaminated and the headline
	// claim ("Claude can no longer see this content") is unprovable
	// from this fixture.
	mustBeAbsent := []string{
		"bank SMS regex",
		"rejecting valid HDFC messages",
		"XX1234",
		"hdfc-credit.txt",
		"npm test -- src/parser.test.ts",
		"asterisk before the masked account",
	}
	for _, s := range mustBeAbsent {
		if strings.Contains(postCompactLines, s) {
			t.Errorf("post-compact lines contain %q — fixture is not honest", s)
		}
	}
}

// fixturePath returns the absolute path to a fixture in this test's
// directory. Resolves via the test binary's working dir which Go
// guarantees to be the package directory.
func fixturePath(t *testing.T, name string) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Join(wd, name)
}

// concatRecoveredContent flattens every recovered message's body into
// one searchable string. Tool-call inputs and outputs are included
// because the high-signal strings (file paths, regex literals, command
// stems) live in them — that is exactly the kind of content Claude's
// post-compact view loses.
func concatRecoveredContent(msgs []*connectors.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(m.Content)
		b.WriteString("\n")
		for _, tc := range m.ToolCalls {
			b.WriteString(tc.Input)
			b.WriteString("\n")
		}
		for _, tr := range m.ToolResults {
			b.WriteString(tr.Output)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// readPostCompactLines returns every JSONL line in the fixture that
// appears AFTER the compact_boundary line. A simple substring scan is
// sufficient because the fixture is small and the search is just for
// presence/absence of marker strings.
func readPostCompactLines(t *testing.T, fixture string) string {
	t.Helper()
	f, err := os.Open(fixture) //nolint:gosec
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()

	var (
		out         strings.Builder
		seenCompact bool
		sc          = bufio.NewScanner(f)
	)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !seenCompact {
			if strings.Contains(line, "compact_boundary") {
				seenCompact = true
			}
			continue
		}
		out.WriteString(line)
		out.WriteString("\n")
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan fixture: %v", err)
	}
	return out.String()
}


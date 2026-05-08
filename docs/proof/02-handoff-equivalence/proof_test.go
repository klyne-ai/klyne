// Package handoffequivalence contains the reproducible proof that
// Klyne's deterministic handoff captures every structural element a
// fresh Claude Code session needs to continue: project path, files
// touched (with re-use counts), commands run, recent failures, and
// the last few user/assistant turns.
//
// Run from the repo root:
//
//	go test -v ./docs/proof/02-handoff-equivalence/
//	make proof
package handoffequivalence_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klyne-ai/klyne/internal/mcpserver"
)

// TestProof_HandoffStructurallyComplete is the headline assertion: the
// Markdown handoff Klyne emits contains every structural slot a fresh
// session needs. Each section ("Files touched", "Commands run", "Known
// failures", "Last few exchanges") must be present and populated from
// the fixture's actual content.
//
// If this fails, the README's "deterministic, paste-ready handoff"
// claim is broken.
func TestProof_HandoffStructurallyComplete(t *testing.T) {
	snap, err := mcpserver.LoadSnapshot(fixturePath(t, "fixture.jsonl"))
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	md := mcpserver.RenderHandoff(snap)

	mustContain := []string{
		// Headline: short session id + the project path
		"# Handoff from session",
		"/repo/billing",

		// "Recent task" reflects the user's most recent message
		"## Recent task",
		"webhooks/processor.ts",

		// Files touched table — the two files iterated on
		"## Files touched",
		"webhooks/dispatcher.ts",

		// Commands run — at least one npm test invocation
		"## Commands run",
		"npm test",

		// Known failures section captures the ETIMEDOUT failure
		"## Known failures",
		"ETIMEDOUT",

		// Recent exchanges section
		"## Last few exchanges",
	}
	for _, frag := range mustContain {
		if !strings.Contains(md, frag) {
			t.Errorf("handoff missing structural fragment %q\n--- handoff ---\n%s\n--- end handoff ---",
				frag, md)
		}
	}
}

// TestProof_HandoffIsDeterministic is the second half of the claim:
// rendering the same fixture twice produces byte-identical output.
// This is what makes the handoff paste-able with confidence — the
// new session sees the same structured context every time, no model
// variance.
//
// Vanilla Claude's "what did we do?" answer would vary every turn;
// Klyne's does not. That is the difference being asserted here.
func TestProof_HandoffIsDeterministic(t *testing.T) {
	path := fixturePath(t, "fixture.jsonl")

	first, err := mcpserver.LoadSnapshot(path)
	if err != nil {
		t.Fatalf("first LoadSnapshot: %v", err)
	}
	second, err := mcpserver.LoadSnapshot(path)
	if err != nil {
		t.Fatalf("second LoadSnapshot: %v", err)
	}

	md1 := mcpserver.RenderHandoff(first)
	md2 := mcpserver.RenderHandoff(second)

	if md1 != md2 {
		t.Errorf("handoff is not deterministic — rendering twice produced different output")
	}
	if md1 == "" {
		t.Errorf("handoff is empty — fixture didn't produce any rendered Markdown")
	}

	// Sanity: the deterministic markdown is also non-trivial.
	if len(md1) < 200 {
		t.Errorf("handoff is suspiciously short (%d bytes); expected a full structured prompt", len(md1))
	}
}

// TestProof_HandoffSurfacesIterativeReuseHonestly asserts the handoff
// reflects how many times each file was *read or edited*, so the new
// session understands which files are central. The webhook fixture
// touches dispatcher.ts more than once (read + multiple edits); the
// handoff's "Files touched" must surface that.
func TestProof_HandoffSurfacesIterativeReuseHonestly(t *testing.T) {
	snap, err := mcpserver.LoadSnapshot(fixturePath(t, "fixture.jsonl"))
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	md := mcpserver.RenderHandoff(snap)

	// dispatcher.ts is touched 3 times in the fixture: 1 Read + 2 Edits.
	// The handoff renders an "(×N)" suffix when N > 1, so we expect to
	// see the marker on the dispatcher line.
	if !strings.Contains(md, "dispatcher.ts") {
		t.Fatalf("handoff missing dispatcher.ts entirely:\n%s", md)
	}

	// Pull just the dispatcher.ts line(s) out and check for the
	// occurrence-count suffix we know the renderer adds.
	dispatcherLines := []string{}
	for _, line := range strings.Split(md, "\n") {
		if strings.Contains(line, "dispatcher.ts") && strings.HasPrefix(strings.TrimSpace(line), "- ") {
			dispatcherLines = append(dispatcherLines, line)
		}
	}
	if len(dispatcherLines) == 0 {
		t.Fatalf("dispatcher.ts not in any list-item line:\n%s", md)
	}

	// At least one of the bullet rows for dispatcher.ts must carry the
	// "(×" multiplicity marker — otherwise the new session can't tell
	// the file was iterated on, which is exactly the signal the
	// handoff exists to convey.
	sawMultiplicity := false
	for _, l := range dispatcherLines {
		if strings.Contains(l, "(×") {
			sawMultiplicity = true
			break
		}
	}
	if !sawMultiplicity {
		t.Errorf("dispatcher.ts was iterated on 3 times but the handoff didn't mark it as repeated:\n%s",
			strings.Join(dispatcherLines, "\n"))
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

package handoffequivalence_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/klyne-ai/klyne/internal/mcpserver"
)

// TestDumpHandoff is intentionally not a real test — it's a helper
// that prints the rendered handoff so the claim.md author can copy
// the verbatim output into the doc. Run with -v to see it.
//
//	go test -v -run TestDumpHandoff ./docs/proof/02-handoff-equivalence/
//
// Skipped by default to avoid noise on `make proof`.
func TestDumpHandoff(t *testing.T) {
	if os.Getenv("KLYNE_DUMP_HANDOFF") == "" {
		t.Skip("set KLYNE_DUMP_HANDOFF=1 to print the rendered handoff for claim.md")
	}
	wd, _ := os.Getwd()
	snap, err := mcpserver.LoadSnapshot(context.Background(), filepath.Join(wd, "fixture.jsonl"))
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	t.Logf("\n%s", mcpserver.RenderHandoff(snap))
}

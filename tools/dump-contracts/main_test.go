package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// repoRoot returns the absolute path to the klyne repo root, derived
// from this test file's location: <root>/tools/dump-contracts/main_test.go.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// .../tools/dump-contracts/main_test.go -> .../
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
}

// TestDumpContractsOnRealFiles runs dumpFiles against the actual W0
// contract files and asserts the dump contains the structs every
// downstream workstream depends on.
func TestDumpContractsOnRealFiles(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	files := []string{
		filepath.Join(root, "internal/api/contracts.go"),
		filepath.Join(root, "internal/api/sse_events.go"),
		filepath.Join(root, "internal/connectors/connector.go"),
	}

	dump, err := dumpFiles(files)
	if err != nil {
		t.Fatalf("dumpFiles: %v", err)
	}
	if len(dump.Structs) == 0 {
		t.Fatal("dump has no structs")
	}

	// Spot-check: every type that crosses workstream boundaries must
	// appear in the dump. This list is the floor, not the ceiling.
	expected := []string{
		// connectors
		"Message", "Session", "RawEvent", "ToolCall", "ToolResult",
		"PricingTable", "PerTokenRates",
		// api DTOs
		"SessionListResponse", "SessionResponse", "MessageListResponse",
		"RestoreResponse", "SummaryResponse", "SearchResponse", "SearchHit",
		"CostSummaryResponse", "CostBucket",
		"HealthzResponse",
		// SSE events
		"MsgNew", "SummaryReady", "SessionUpdate",
		"CostTick", "ThreadRebuild", "CompactDetected",
	}
	got := make(map[string]bool, len(dump.Structs))
	for _, s := range dump.Structs {
		got[s.Name] = true
	}
	for _, name := range expected {
		if !got[name] {
			t.Errorf("expected struct %q missing from dump", name)
		}
	}

	// Sanity: every Message field has a JSON tag — the wire format
	// promise. (Detected fields without tags would silently break the
	// frontend.)
	for _, s := range dump.Structs {
		if s.Name != "Message" {
			continue
		}
		for _, f := range s.Fields {
			if f.JSON == "" {
				t.Errorf("Message.%s has no json tag", f.Name)
			}
		}
	}
}

// TestRunWritesValidJSON exercises the full run() entrypoint, asserting
// that the -out flag produces a parseable JSON document.
func TestRunWritesValidJSON(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	outPath := filepath.Join(t.TempDir(), "dump.json")

	args := []string{
		"-out", outPath,
		"-files", filepath.Join(root, "internal/api/sse_events.go"),
	}
	if err := run(args, os.Stdout); err != nil {
		t.Fatalf("run: %v", err)
	}

	body, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read out: %v", err)
	}

	var d Dump
	if err := json.Unmarshal(body, &d); err != nil {
		t.Fatalf("unmarshal dump: %v", err)
	}
	if len(d.Structs) == 0 {
		t.Fatal("dump.Structs empty for sse_events.go")
	}

	want := map[string]bool{
		"MsgNew": false, "SummaryReady": false, "SessionUpdate": false,
		"CostTick": false, "ThreadRebuild": false, "CompactDetected": false,
	}
	for _, s := range d.Structs {
		if _, ok := want[s.Name]; ok {
			want[s.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("expected SSE struct %q not in dump", name)
		}
	}
}

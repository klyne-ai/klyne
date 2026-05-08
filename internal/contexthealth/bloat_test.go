package contexthealth

import (
	"strings"
	"testing"

	"github.com/mohitpatell/agentdeck/internal/connectors"
)

// readResult builds the role=tool reply for the readCall at callIdx,
// carrying body bytes of payload so tests can drive predictable
// SharePct attribution.
func readResult(msgIdx, callIdx, body int) *connectors.Message {
	out := make([]byte, body)
	for i := range out {
		out[i] = 'x'
	}
	return &connectors.Message{
		ID:      "t" + itoa(msgIdx),
		Role:    connectors.RoleTool,
		Content: string(out),
		Ts:      int64(msgIdx)*1000 + 500,
		ToolResults: []connectors.ToolResult{{
			ID:     "tc" + itoa(callIdx),
			Output: string(out),
		}},
	}
}

func TestComputeBloat_EmptyInput(t *testing.T) {
	t.Parallel()
	if got := computeBloat(nil); got != nil {
		t.Errorf("computeBloat(nil) = %v, want nil", got)
	}
	if got := computeBloat([]*connectors.Message{}); got != nil {
		t.Errorf("computeBloat(empty) = %v, want nil", got)
	}
}

func TestComputeBloat_NoToolResults(t *testing.T) {
	t.Parallel()
	got := computeBloat([]*connectors.Message{
		userMsg(0, "hi"),
		asstMsg(1, "hello"),
	})
	if got != nil {
		t.Errorf("computeBloat(no tool results) = %v, want nil", got)
	}
}

func TestComputeBloat_SingleFileRead(t *testing.T) {
	t.Parallel()
	got := computeBloat([]*connectors.Message{
		readCall(0, "/repo/big.json"),
		readResult(1, 0, 1000),
	})
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1; rows = %+v", len(got), got)
	}
	if got[0].Label != "Read big.json (1 read)" {
		t.Errorf("Label = %q, want %q", got[0].Label, "Read big.json (1 read)")
	}
	if got[0].Kind != BloatKindFileRead {
		t.Errorf("Kind = %q, want %q", got[0].Kind, BloatKindFileRead)
	}
	if got[0].Count != 1 {
		t.Errorf("Count = %d, want 1", got[0].Count)
	}
	if got[0].ReadCount != 1 || got[0].EditCount != 0 {
		t.Errorf("ReadCount/EditCount = %d/%d, want 1/0", got[0].ReadCount, got[0].EditCount)
	}
	if got[0].SharePct != 100.0 {
		t.Errorf("SharePct = %v, want 100", got[0].SharePct)
	}
}

func TestComputeBloat_GroupsRepeatedReads(t *testing.T) {
	t.Parallel()
	// 3 reads of the same file (200 bytes each) + 1 read of another
	// (100 bytes). Expect two rows; the repeated file dominates.
	got := computeBloat([]*connectors.Message{
		readCall(0, "/repo/lock.json"),
		readResult(1, 0, 200),
		readCall(2, "/repo/lock.json"),
		readResult(3, 2, 200),
		readCall(4, "/repo/lock.json"),
		readResult(5, 4, 200),
		readCall(6, "/repo/other.txt"),
		readResult(7, 6, 100),
	})
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2; rows = %+v", len(got), got)
	}
	if got[0].Label != "Read lock.json (3 reads)" {
		t.Errorf("rows[0].Label = %q, want %q", got[0].Label, "Read lock.json (3 reads)")
	}
	if got[0].Count != 3 || got[0].ReadCount != 3 {
		t.Errorf("rows[0] counts = %d/read=%d, want 3/3", got[0].Count, got[0].ReadCount)
	}
	// 600 / 700 = 85.71% (within float tolerance).
	wantTop := 600.0 / 700.0 * 100.0
	if abs(got[0].SharePct-wantTop) > 0.01 {
		t.Errorf("rows[0].SharePct = %v, want %v", got[0].SharePct, wantTop)
	}
	if got[1].Label != "Read other.txt (1 read)" {
		t.Errorf("rows[1].Label = %q, want %q", got[1].Label, "Read other.txt (1 read)")
	}
}

func TestComputeBloat_GroupsBashByCommandStem(t *testing.T) {
	t.Parallel()
	// Two `npm test` runs and one `git push origin main`. Expect the
	// `npm test` rows to collapse into one bucket.
	got := computeBloat([]*connectors.Message{
		bashCall(0, "npm test --silent"),
		readResult(1, 0, 500),
		bashCall(2, "npm test"),
		readResult(3, 2, 500),
		bashCall(4, "git push origin main"),
		readResult(5, 4, 200),
	})
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2; rows = %+v", len(got), got)
	}
	if got[0].Label != "Bash: npm test" {
		t.Errorf("rows[0].Label = %q, want Bash: npm test", got[0].Label)
	}
	if got[0].Count != 2 {
		t.Errorf("rows[0].Count = %d, want 2", got[0].Count)
	}
	if got[1].Label != "Bash: git push" {
		t.Errorf("rows[1].Label = %q, want Bash: git push", got[1].Label)
	}
}

func TestComputeBloat_CapsAtFiveRows(t *testing.T) {
	t.Parallel()
	// 7 distinct file reads → expect exactly 5 rows back, sorted desc.
	var msgs []*connectors.Message
	for i := 0; i < 7; i++ {
		msgs = append(msgs, readCall(i*2, "/repo/file"+itoa(i)+".go"))
		// Bigger files get higher indices so the smallest two get
		// dropped after sorting.
		msgs = append(msgs, readResult(i*2+1, i*2, 100*(i+1)))
	}
	got := computeBloat(msgs)
	if len(got) != 5 {
		t.Fatalf("len = %d, want 5", len(got))
	}
	// Row 0 must be the largest (file6 = 700 bytes).
	if got[0].Label != "Read file6.go (1 read)" {
		t.Errorf("rows[0].Label = %q, want %q", got[0].Label, "Read file6.go (1 read)")
	}
	// Row 4 must be the 5th-largest (file2 = 300 bytes).
	if got[4].Label != "Read file2.go (1 read)" {
		t.Errorf("rows[4].Label = %q, want %q", got[4].Label, "Read file2.go (1 read)")
	}
}

func TestComputeBloat_UnattributedFallsBackToToolResultKind(t *testing.T) {
	t.Parallel()
	// Tool result without a matching ToolCall — should still be
	// counted, attributed to the generic "Tool result" bucket.
	got := computeBloat([]*connectors.Message{
		readResult(0, 999, 400), // no matching call_id 999
	})
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Kind != BloatKindToolResult {
		t.Errorf("Kind = %q, want %q", got[0].Kind, BloatKindToolResult)
	}
	if got[0].SharePct != 100.0 {
		t.Errorf("SharePct = %v, want 100", got[0].SharePct)
	}
}

func TestComputeBloat_DeterministicOnTies(t *testing.T) {
	t.Parallel()
	// Two reads with identical sizes — labels must come back in
	// alphabetical order on ties so the runner output is stable.
	a := computeBloat([]*connectors.Message{
		readCall(0, "/repo/zeta.go"),
		readResult(1, 0, 500),
		readCall(2, "/repo/alpha.go"),
		readResult(3, 2, 500),
	})
	b := computeBloat([]*connectors.Message{
		readCall(0, "/repo/zeta.go"),
		readResult(1, 0, 500),
		readCall(2, "/repo/alpha.go"),
		readResult(3, 2, 500),
	})
	for i := range a {
		if a[i].Label != b[i].Label {
			t.Errorf("non-deterministic at row %d: %q vs %q", i, a[i].Label, b[i].Label)
		}
	}
	if a[0].Label != "Read alpha.go (1 read)" {
		t.Errorf("expected alpha.go first on tie, got %q", a[0].Label)
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// editCall builds an assistant message that issued a single Edit
// tool call against the given path with non-trivial old_string +
// new_string payloads. The payload size matters because computeBloat
// counts Edit tool-input bytes toward total context cost.
func editCall(idx int, path string) *connectors.Message {
	body := `{"file_path":"` + path + `","old_string":"` +
		strings.Repeat("a", 100) + `","new_string":"` +
		strings.Repeat("b", 100) + `"}`
	return &connectors.Message{
		ID:      "a" + itoa(idx),
		Role:    connectors.RoleAssistant,
		Content: "(editing)",
		Ts:      int64(idx) * 1000,
		ToolCalls: []connectors.ToolCall{{
			ID:    "tc" + itoa(idx),
			Name:  "Edit",
			Input: body,
		}},
	}
}

// editResult builds the role=tool reply that pairs with editCall.
// Edit results are typically small ("File modified successfully")
// so we use a tiny body — the real bloat from an Edit comes from
// the call's old_string/new_string, not the result.
func editResult(msgIdx, callIdx int) *connectors.Message {
	const ack = "File modified successfully"
	return &connectors.Message{
		ID:      "t" + itoa(msgIdx),
		Role:    connectors.RoleTool,
		Content: ack,
		Ts:      int64(msgIdx)*1000 + 500,
		ToolResults: []connectors.ToolResult{{
			ID:     "tc" + itoa(callIdx),
			Output: ack,
		}},
	}
}

func TestComputeBloat_EditOnlyFileLabelledAsEdit(t *testing.T) {
	t.Parallel()
	got := computeBloat([]*connectors.Message{
		editCall(0, "/repo/foo.go"),
		editResult(1, 0),
		editCall(2, "/repo/foo.go"),
		editResult(3, 2),
		editCall(4, "/repo/foo.go"),
		editResult(5, 4),
	})
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1; rows = %+v", len(got), got)
	}
	row := got[0]
	if row.Label != "Edit foo.go (3 edits)" {
		t.Errorf("Label = %q, want %q", row.Label, "Edit foo.go (3 edits)")
	}
	if row.Kind != BloatKindFileEdit {
		t.Errorf("Kind = %q, want %q", row.Kind, BloatKindFileEdit)
	}
	if row.ReadCount != 0 || row.EditCount != 3 {
		t.Errorf("Read/Edit counts = %d/%d, want 0/3", row.ReadCount, row.EditCount)
	}
	if row.Count != 3 {
		t.Errorf("Count = %d, want 3 (sum of Read+Edit)", row.Count)
	}
}

func TestComputeBloat_MixedFileLabelledMixed(t *testing.T) {
	t.Parallel()
	// 2 reads + 2 edits → file_mixed kind, "Read+Edit foo.go" label.
	got := computeBloat([]*connectors.Message{
		readCall(0, "/repo/foo.go"),
		readResult(1, 0, 500),
		editCall(2, "/repo/foo.go"),
		editResult(3, 2),
		readCall(4, "/repo/foo.go"),
		readResult(5, 4, 500),
		editCall(6, "/repo/foo.go"),
		editResult(7, 6),
	})
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1; rows = %+v", len(got), got)
	}
	row := got[0]
	if row.Label != "Read+Edit foo.go (2 reads, 2 edits)" {
		t.Errorf("Label = %q, want mixed label with breakdown", row.Label)
	}
	if row.Kind != BloatKindFileMixed {
		t.Errorf("Kind = %q, want %q", row.Kind, BloatKindFileMixed)
	}
	if row.ReadCount != 2 || row.EditCount != 2 {
		t.Errorf("Read/Edit counts = %d/%d, want 2/2", row.ReadCount, row.EditCount)
	}
}

func TestComputeBloat_EditPayloadCountsTowardSharePct(t *testing.T) {
	t.Parallel()
	// One pure Read (200B result) + one pure Edit (200B old + 100B new).
	// Expect both rows non-trivial; without counting Edit payloads,
	// the Edit row would be ~0% (the result body alone is the small
	// ack message). With the fix, Edit's share reflects its real cost.
	body := `{"file_path":"/repo/big-edit.go","old_string":"` +
		strings.Repeat("x", 200) + `","new_string":"` +
		strings.Repeat("y", 100) + `"}`
	got := computeBloat([]*connectors.Message{
		readCall(0, "/repo/read-only.go"),
		readResult(1, 0, 200),
		// Custom edit call with our exact payload (rather than editCall
		// helper, because we want predictable payload sizes).
		{
			ID: "a2", Role: connectors.RoleAssistant, Ts: 2000,
			ToolCalls: []connectors.ToolCall{{ID: "tc2", Name: "Edit", Input: body}},
		},
		editResult(3, 2),
	})
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	// Find the two rows by label.
	var readRow, editRow *BloatRow
	for i := range got {
		switch {
		case got[i].Kind == BloatKindFileRead:
			readRow = &got[i]
		case got[i].Kind == BloatKindFileEdit:
			editRow = &got[i]
		}
	}
	if readRow == nil || editRow == nil {
		t.Fatalf("expected one Read row and one Edit row, got %+v", got)
	}
	// Edit's contribution is the call payload (200+100=300) plus the
	// 26-char "File modified successfully" ack. Read's is just the
	// 200-byte result. Total denominator = 300 + 26 + 200 = 526.
	if editRow.SharePct < 50 {
		t.Errorf("Edit SharePct = %.1f%%, want at least 50%% (payload should dominate)", editRow.SharePct)
	}
}

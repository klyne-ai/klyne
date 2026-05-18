package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/cost"
	"github.com/klyne-ai/klyne/internal/store"
)

// ---- help-text smoke tests -----------------------------------------------

func TestCostCmd_HelpText(t *testing.T) {
	t.Parallel()
	cmd := newCostCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--help"})
	// Help returns a special error type that cobra doesn't surface as failure.
	_ = cmd.Execute()
	got := out.String()
	if !strings.Contains(got, "cost") {
		t.Errorf("help text missing 'cost': %q", got)
	}
}

func TestCostWeekCmd_HelpText(t *testing.T) {
	t.Parallel()
	cmd := newCostCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"week", "--help"})
	_ = cmd.Execute()
	got := out.String()
	checks := []string{"week", "--since", "--dry-run"}
	for _, c := range checks {
		if !strings.Contains(got, c) {
			t.Errorf("help text missing %q: got %q", c, got)
		}
	}
}

// ---- rendering unit tests ------------------------------------------------

func TestCostRenderDigest_EmptySpans(t *testing.T) {
	t.Parallel()
	eng, err := cost.New(nil)
	if err != nil {
		t.Fatalf("cost.New: %v", err)
	}
	out := costRenderDigest(nil, eng, "2026-05-08 to 2026-05-15", false)
	// With nil spans the digest still renders headers without panicking.
	if !strings.Contains(out, "klyne cost week") {
		t.Errorf("header missing: %q", out)
	}
	if !strings.Contains(out, "Summary") {
		t.Errorf("Summary section missing: %q", out)
	}
}

func TestCostRenderDigest_CommitSpan(t *testing.T) {
	t.Parallel()
	eng, err := cost.New(nil)
	if err != nil {
		t.Fatalf("cost.New: %v", err)
	}
	spans := []store.WorkSpan{
		{
			Bucket:          "commit",
			CommitSHA:       "abc1234def5678",
			GitBranch:       "feat/auth",
			TokensFresh:     320_000,
			TokensCacheRead: 50_000,
			TokensOut:       15_000,
			MsgCount:        30,
			OpenedAt:        1000,
			ClosedAt:        2000,
		},
	}
	out := costRenderDigest(spans, eng, "2026-05-08 to 2026-05-15", false)

	if !strings.Contains(out, "Shipped (closed by commit)") {
		t.Errorf("missing commit bucket row: %q", out)
	}
	if !strings.Contains(out, "abc1234") {
		t.Errorf("missing commit SHA in top spans: %q", out)
	}
	if !strings.Contains(out, "feat/auth") {
		t.Errorf("missing branch name in top spans: %q", out)
	}
}

func TestCostRenderDigest_WasteLoopSpan(t *testing.T) {
	t.Parallel()
	eng, err := cost.New(nil)
	if err != nil {
		t.Fatalf("cost.New: %v", err)
	}
	spans := []store.WorkSpan{
		{
			Bucket:        "exploration",
			ExplorationID: "exp-deadbeef01",
			TokensFresh:   3_750_000, // OAuth retry storm size
			TokensOut:     100_000,
			MsgCount:      47,
			WasteClasses:  []string{"WASTE_LOOP"},
			WasteMeta: map[string]any{
				"waste_loop": map[string]any{
					"hash":  "cafebabe",
					"count": float64(47),
				},
			},
			OpenedAt: 1000,
			ClosedAt: 5000,
		},
	}
	out := costRenderDigest(spans, eng, "2026-05-08 to 2026-05-15", false)

	if !strings.Contains(out, "WASTE_LOOP") {
		t.Errorf("missing WASTE_LOOP in waste summary: %q", out)
	}
	// The "← klyne saved this" marker should appear for the WASTE caught row.
	if !strings.Contains(out, "klyne saved this") {
		t.Errorf("missing 'klyne saved this' callout: %q", out)
	}
	if !strings.Contains(out, "⚠️") {
		t.Errorf("missing warning emoji in top span line: %q", out)
	}
}

func TestCostRenderDigest_DryRunTag(t *testing.T) {
	t.Parallel()
	eng, err := cost.New(nil)
	if err != nil {
		t.Fatalf("cost.New: %v", err)
	}
	out := costRenderDigest(nil, eng, "2026-05-08 to 2026-05-15", true)
	if !strings.Contains(out, "[dry-run]") {
		t.Errorf("expected [dry-run] tag in header: %q", out)
	}
}

// ---- formatter unit tests -----------------------------------------------

func TestCostFormatTokens(t *testing.T) {
	t.Parallel()
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1_000, "1K"},
		{320_000, "320K"},
		{1_000_000, "1.00M"},
		{3_750_000, "3.75M"},
	}
	for _, c := range cases {
		got := costFormatTokens(c.n)
		if got != c.want {
			t.Errorf("costFormatTokens(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestCostFormatUSD(t *testing.T) {
	t.Parallel()
	cases := []struct {
		usd  float64
		want string
	}{
		{0, "$0.00"},
		{1.84, "$1.84"},
		{0.96, "$0.96"},
		{100.0, "$100.00"},
	}
	for _, c := range cases {
		got := costFormatUSD(c.usd)
		if got != c.want {
			t.Errorf("costFormatUSD(%f) = %q, want %q", c.usd, got, c.want)
		}
	}
}

func TestCostParseSince(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input   string
		wantErr bool
		days    int
	}{
		{"7d", false, 7},
		{"14d", false, 14},
		{"30d", false, 30},
		{"1d", false, 1},
		{"", false, 7}, // defaults to 7d
		{"bad", true, 0},
		{"7", true, 0},
	}
	now := time.Now().UnixMilli()
	for _, c := range cases {
		sinceMs, label, err := costParseSince(c.input)
		if c.wantErr {
			if err == nil {
				t.Errorf("costParseSince(%q): expected error, got nil", c.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("costParseSince(%q): unexpected error: %v", c.input, err)
			continue
		}
		expectedMs := now - int64(c.days)*24*60*60*1000
		// Allow 1s of clock drift.
		diff := sinceMs - expectedMs
		if diff < -2000 || diff > 2000 {
			t.Errorf("costParseSince(%q): sinceMs off by %dms", c.input, diff)
		}
		if !strings.Contains(label, "to") {
			t.Errorf("costParseSince(%q): label missing 'to': %q", c.input, label)
		}
	}
}

func TestCostSpanLabel(t *testing.T) {
	t.Parallel()
	cases := []struct {
		sp   store.WorkSpan
		want string
	}{
		{
			store.WorkSpan{Bucket: "commit", CommitSHA: "abc1234def5678", GitBranch: "main"},
			"commit abc1234 (main)",
		},
		{
			store.WorkSpan{Bucket: "exploration", ExplorationID: "exp-deadbeef"},
			"exploration exp-deadbeef",
		},
	}
	for _, c := range cases {
		got := costSpanLabel(c.sp)
		if got != c.want {
			t.Errorf("costSpanLabel(%+v) = %q, want %q", c.sp, got, c.want)
		}
	}
}

// ---- integration smoke: week command against a real temp DB --------------

func TestCostWeekCmd_EmptyDB(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "klyne-test.db")

	db, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close() //nolint:errcheck

	// Simulate the "no spans yet" path using costRenderDigest with empty spans.
	eng, err := cost.New(nil)
	if err != nil {
		t.Fatalf("cost.New: %v", err)
	}

	// No spans written, so ListWorkSpans returns empty.
	spans, err := store.ListWorkSpans(context.Background(), db, store.WorkSpanFilter{Since: 0})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(spans) != 0 {
		t.Fatalf("expected empty spans, got %d", len(spans))
	}

	// The renderer should not panic and should produce headers.
	out := costRenderDigest(spans, eng, "2026-05-08 to 2026-05-15", false)
	if !strings.Contains(out, "Summary") {
		t.Errorf("missing Summary in empty digest: %q", out)
	}
}

func TestCostWeekCmd_WithFixtureSpans(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "klyne-test.db")

	db, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close() //nolint:errcheck

	ctx := context.Background()
	now := time.Now().UnixMilli()

	// Insert fixture spans.
	fixtureSpans := []store.WorkSpan{
		{
			Bucket:      "commit",
			CommitSHA:   "abc1234def56789012345678901234567890",
			GitBranch:   "feat/auth",
			SessionIDs:  []string{"sess-1"},
			TokensFresh: 200_000,
			TokensOut:   10_000,
			MsgCount:    25,
			OpenedAt:    now - 5000,
			ClosedAt:    now - 1000,
		},
		{
			Bucket:       "exploration",
			ExplorationID: "exp-deadbeef01",
			GitBranch:    "feat/auth",
			SessionIDs:   []string{"sess-2"},
			WasteClasses: []string{"WASTE_LOOP"},
			WasteMeta: map[string]any{
				"waste_loop": map[string]any{"hash": "cafebabe", "count": float64(47)},
			},
			TokensFresh: 3_750_000,
			TokensOut:   100_000,
			MsgCount:    47,
			OpenedAt:    now - 10000,
			ClosedAt:    now - 5001,
		},
	}
	for i := range fixtureSpans {
		if err := store.InsertWorkSpan(ctx, db, &fixtureSpans[i]); err != nil {
			t.Fatalf("insert fixture span %d: %v", i, err)
		}
	}

	spans, err := store.ListWorkSpans(ctx, db, store.WorkSpanFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(spans) != 2 {
		t.Fatalf("expected 2 spans, got %d", len(spans))
	}

	eng, err := cost.New(nil)
	if err != nil {
		t.Fatalf("cost.New: %v", err)
	}

	out := costRenderDigest(spans, eng, "2026-05-08 to 2026-05-15", false)

	checks := []string{
		"klyne cost week",
		"Summary",
		"Shipped (closed by commit)",
		"WASTE_LOOP caught",
		"Top spans",
		"WASTE_LOOP",
		"klyne saved this",
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("digest missing %q:\n%s", want, out)
		}
	}
}

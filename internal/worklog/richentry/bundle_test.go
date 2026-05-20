package richentry

import (
	"strings"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/store"
)

// fixtureInputs mirrors a small subset of session `56ccdfb2` from
// the 2026-05-19 fixture — used as a shared sample across tests.
func fixtureInputs() BundleInputs {
	return BundleInputs{
		SessionID:       "56ccdfb2-test-fixture",
		Ts:              time.Date(2026, 5, 19, 19, 20, 45, 0, time.UTC),
		ProjectPath:     "/Users/mohitpatel/Desktop/Learning/operations-app",
		RepoName:        "operations-app",
		BranchName:      "feature/CLI-1325-labstack-integration",
		BranchTicket:    "CLI-1325",
		StopSummaryBody: "# klyne session-end summary\n\nMerged PR #400 after lab_payload work.\n",
		UserMessages:    []string{"build the lab_payload", "now merge it"},
		Commits: []CommitRef{
			{SHA: "e2850e2", Files: []string{"src/lib/utils/labOrderHelpers.ts"}, CommittedAt: time.Date(2026, 5, 19, 11, 51, 0, 0, time.UTC)},
			{SHA: "c5c97a8", Files: []string{"src/components/lab-orders/steps/SlotPickerStep.tsx"}, CommittedAt: time.Date(2026, 5, 19, 11, 59, 0, 0, time.UTC)},
		},
		MergedPRs: []MergedPRRef{
			{Number: 400, Title: "Feature/cli 1325 labstack integration", MergedAt: time.Date(2026, 5, 19, 19, 20, 46, 0, time.UTC), MergeSHA: "e9a1b2a"},
		},
		ActiveIntervals: []Interval{
			{Start: time.Date(2026, 5, 19, 15, 32, 0, 0, time.UTC), End: time.Date(2026, 5, 19, 19, 20, 0, 0, time.UTC)},
		},
		SessionFiles:    []string{"package.json"},
		PriorSessionIDs: nil,
	}
}

// item is a tiny builder so multi-ref test entries read naturally.
func item(refs ...string) []store.WorklogItem {
	return []store.WorklogItem{{Summary: "test", Refs: refs}}
}

// TestBuildBundle_AllowlistAdmitsValidEntry — round-trip check: the
// Allowlist BuildBundle produces must admit an entry that only cites
// tokens from the same inputs. This is the contract the writer relies
// on; if it fails, every LLM output will be rejected.
func TestBuildBundle_AllowlistAdmitsValidEntry(t *testing.T) {
	in := fixtureInputs()
	b := BuildBundle(in)

	entry := store.WorklogEntryJSON{
		SchemaVersion: 1,
		Categories: map[string][]store.WorklogItem{
			"shipped":            item("#400", "e9a1b2a"),
			"features_worked_on": item("e2850e2", "src/lib/utils/labOrderHelpers.ts"),
			"decisions":          item("CLI-1325"),
		},
	}
	if errs := Validate(entry, b.Allowlist); len(errs) != 0 {
		t.Errorf("expected no validation errors; got %+v", errs)
	}
}

// TestBuildBundle_AllowlistRejectsInvented — counter-test: an entry
// citing a fake SHA must still be rejected.
func TestBuildBundle_AllowlistRejectsInvented(t *testing.T) {
	b := BuildBundle(fixtureInputs())
	entry := store.WorklogEntryJSON{
		SchemaVersion: 1,
		Categories: map[string][]store.WorklogItem{
			"shipped": item("aaaaaaaa"),
		},
	}
	if errs := Validate(entry, b.Allowlist); len(errs) == 0 {
		t.Error("expected rejection of invented SHA")
	}
}

// TestBuildBundle_PRMergeSHAIsCitable — the gold narrative pairs PR
// numbers with their merge commit short-shas (e.g. #400 + e9a1b2a).
// The writer must therefore make merge-shas first-class citation
// tokens even though they don't appear in `Commits[]` (the merge
// commit lives on origin/main, not in the turn's commit list).
func TestBuildBundle_PRMergeSHAIsCitable(t *testing.T) {
	b := BuildBundle(fixtureInputs())
	entry := store.WorklogEntryJSON{
		SchemaVersion: 1,
		Categories: map[string][]store.WorklogItem{
			"shipped": item("e9a1b2a"),
		},
	}
	if errs := Validate(entry, b.Allowlist); len(errs) != 0 {
		t.Errorf("PR merge SHA must be in allowlist; got %+v", errs)
	}
}

// TestBuildBundle_PRsNil_DisablesPRCheck — the writer signals
// "fresh repo / no gh cache" by passing MergedPRs=nil; the
// validator then SKIPS PR checks entirely.
func TestBuildBundle_PRsNil_DisablesPRCheck(t *testing.T) {
	in := fixtureInputs()
	in.MergedPRs = nil
	b := BuildBundle(in)
	if b.Allowlist.PRCacheLive {
		t.Error("PRCacheLive must be false when MergedPRs is nil")
	}
	entry := store.WorklogEntryJSON{
		SchemaVersion: 1,
		Categories:    map[string][]store.WorklogItem{"shipped": item("#999")},
	}
	if errs := Validate(entry, b.Allowlist); len(errs) != 0 {
		t.Errorf("unknown PR should pass when cache is nil; got %+v", errs)
	}
}

func TestBuildBundle_PRsEmptySlice_LiveCacheZeroPRs(t *testing.T) {
	in := fixtureInputs()
	in.MergedPRs = []MergedPRRef{} // cache hit, zero PRs
	b := BuildBundle(in)
	if !b.Allowlist.PRCacheLive {
		t.Error("PRCacheLive must be true when MergedPRs is empty slice")
	}
	entry := store.WorklogEntryJSON{
		SchemaVersion: 1,
		Categories:    map[string][]store.WorklogItem{"shipped": item("#999")},
	}
	if errs := Validate(entry, b.Allowlist); len(errs) != 1 {
		t.Errorf("unknown PR should be rejected when cache is live; got %+v", errs)
	}
}

// TestBuildBundle_PromptIncludesEverything — the rendered prompt must
// surface every signal the LLM needs. If a signal isn't in the prompt
// the LLM literally cannot cite it.
func TestBuildBundle_PromptIncludesEverything(t *testing.T) {
	b := BuildBundle(fixtureInputs())
	required := []string{
		"56ccdfb2-test-fixture",                 // session_id
		"operations-app",                        // repo
		"feature/CLI-1325-labstack-integration", // branch
		"CLI-1325",                              // branch ticket
		"Merged PR #400 after lab_payload work", // body content
		"build the lab_payload",                 // user msg
		"e2850e2",                               // commit sha
		"c5c97a8",                               // commit sha
		"#400",                                  // PR
		"Feature/cli 1325 labstack integration", // PR title
		"e9a1b2a",                               // PR merge sha
		"package.json",                          // session file
	}
	for _, want := range required {
		if !strings.Contains(b.Prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

// TestBuildBundle_PromptEmitsCategorySchema — the LLM must see the
// full 15-category list in the prompt so it knows the output
// contract by example.
func TestBuildBundle_PromptEmitsCategorySchema(t *testing.T) {
	b := BuildBundle(fixtureInputs())
	for _, cat := range []string{
		"features_worked_on", "features_picked", "shipped",
		"bugs_found", "bugs_fixed",
		"investigations", "decisions", "config_changes",
		"blockers", "blocked_on", "pending",
		"followups_for_others", "must_remember",
		"mistakes_or_dead_ends", "reviews_given",
	} {
		if !strings.Contains(b.Prompt, cat) {
			t.Errorf("prompt missing category %q", cat)
		}
	}
}

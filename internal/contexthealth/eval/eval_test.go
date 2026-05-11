package eval

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klyne-ai/klyne/internal/contexthealth"
)

// eval_test.go — table-driven tests for the eval runner itself.
//
// These tests do NOT verify the production classifier or advisor;
// they verify that the runner builds correct synthetic inputs from a
// fixture, calls Classify/RenderAdvisor, and aggregates the
// per-fixture rows into the Report metrics. The production thresholds
// are exercised end-to-end by TestScoreOnTestdata, which is the
// baseline the future threshold-tuning PR will compare against.

func TestScore_TableDriven(t *testing.T) {
	tests := []struct {
		name              string
		fx                LabelledFixture
		wantStateMatch    bool
		wantAdvisorMatch  bool
		wantGotState      contexthealth.State
		wantAdvisorFires  bool
	}{
		{
			name: "healthy_fixture_matches",
			fx: LabelledFixture{
				Name:                 "healthy",
				ExpectedState:        contexthealth.StateHealthy,
				ExpectedAdvisorFires: false,
				Shape: SessionShape{
					ContextFillPct: 5,
					AssistantTurns: 3,
					UserTurns:      6,
					TokensInPerTurn: 1000,
				},
			},
			wantStateMatch:   true,
			wantAdvisorMatch: true,
			wantGotState:     contexthealth.StateHealthy,
			wantAdvisorFires: false,
		},
		{
			name: "hard_ceiling_classifier_AND_advisor_fire",
			fx: LabelledFixture{
				Name:                 "rescue_fill",
				ExpectedState:        contexthealth.StateRescueNow,
				ExpectedAdvisorFires: true,
				Shape: SessionShape{
					ContextFillPct: 90,
					AssistantTurns: 3,
					UserTurns:      6,
					TokensInPerTurn: 1000,
				},
			},
			wantStateMatch:   true,
			wantAdvisorMatch: true,
			wantGotState:     contexthealth.StateRescueNow,
			wantAdvisorFires: true,
		},
		{
			name: "mismatched_label_marks_StateMatch_false",
			fx: LabelledFixture{
				// Deliberately wrong expected state to verify the
				// runner reports a mismatch.
				Name:                 "wrong_label",
				ExpectedState:        contexthealth.StateRescueNow,
				ExpectedAdvisorFires: false,
				Shape: SessionShape{
					ContextFillPct: 5,
					AssistantTurns: 3,
					UserTurns:      6,
					TokensInPerTurn: 1000,
				},
			},
			wantStateMatch:   false,
			wantAdvisorMatch: true,
			wantGotState:     contexthealth.StateHealthy,
			wantAdvisorFires: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rep := Score([]LabelledFixture{tc.fx})
			if rep.Total != 1 {
				t.Fatalf("Total=%d, want 1", rep.Total)
			}
			if len(rep.Rows) != 1 {
				t.Fatalf("Rows=%d, want 1", len(rep.Rows))
			}
			row := rep.Rows[0]
			if row.StateMatch != tc.wantStateMatch {
				t.Errorf("StateMatch=%v, want %v (got=%s, expected=%s)",
					row.StateMatch, tc.wantStateMatch, row.GotState, row.ExpectedState)
			}
			if row.AdvisorMatch != tc.wantAdvisorMatch {
				t.Errorf("AdvisorMatch=%v, want %v (got=%v, expected=%v)",
					row.AdvisorMatch, tc.wantAdvisorMatch, row.GotAdvisorFires, row.ExpectedAdvisorFires)
			}
			if row.GotState != tc.wantGotState {
				t.Errorf("GotState=%s, want %s", row.GotState, tc.wantGotState)
			}
			if row.GotAdvisorFires != tc.wantAdvisorFires {
				t.Errorf("GotAdvisorFires=%v, want %v", row.GotAdvisorFires, tc.wantAdvisorFires)
			}
		})
	}
}

func TestReport_Aggregates(t *testing.T) {
	// Mixed batch: two healthy (one correct, one mislabelled), one
	// rescue (correct). Confirms the four headline metrics compute
	// correctly when there are FPs, FNs, and matches mixed together.
	healthyCorrect := LabelledFixture{
		Name:          "h1",
		ExpectedState: contexthealth.StateHealthy,
		Shape:         SessionShape{ContextFillPct: 5, AssistantTurns: 3, UserTurns: 6, TokensInPerTurn: 1000},
	}
	healthyMislabelled := LabelledFixture{
		// Labelled healthy but fill is 90 → classifier returns
		// rescue_now → false positive.
		Name:          "h2_mislabelled",
		ExpectedState: contexthealth.StateHealthy,
		Shape:         SessionShape{ContextFillPct: 90, AssistantTurns: 3, UserTurns: 6, TokensInPerTurn: 1000},
	}
	rescueMislabelled := LabelledFixture{
		// Labelled rescue but fill is 5 → classifier returns
		// healthy → false negative.
		Name:          "r1_mislabelled",
		ExpectedState: contexthealth.StateRescueNow,
		Shape:         SessionShape{ContextFillPct: 5, AssistantTurns: 3, UserTurns: 6, TokensInPerTurn: 1000},
	}

	rep := Score([]LabelledFixture{healthyCorrect, healthyMislabelled, rescueMislabelled})

	if rep.Total != 3 {
		t.Fatalf("Total=%d, want 3", rep.Total)
	}
	if rep.ClassificationCorrect != 1 {
		t.Errorf("ClassificationCorrect=%d, want 1", rep.ClassificationCorrect)
	}
	if rep.HealthyExpected != 2 {
		t.Errorf("HealthyExpected=%d, want 2", rep.HealthyExpected)
	}
	if rep.NonHealthyExpected != 1 {
		t.Errorf("NonHealthyExpected=%d, want 1", rep.NonHealthyExpected)
	}
	if rep.FalsePositives != 1 {
		t.Errorf("FalsePositives=%d, want 1", rep.FalsePositives)
	}
	if rep.FalseNegatives != 1 {
		t.Errorf("FalseNegatives=%d, want 1", rep.FalseNegatives)
	}
	if got, want := rep.ClassificationAccuracy(), 1.0/3.0; abs(got-want) > 1e-9 {
		t.Errorf("ClassificationAccuracy=%v, want %v", got, want)
	}
	if got, want := rep.FalsePositiveRate(), 0.5; abs(got-want) > 1e-9 {
		t.Errorf("FalsePositiveRate=%v, want %v", got, want)
	}
	if got, want := rep.FalseNegativeRate(), 1.0; abs(got-want) > 1e-9 {
		t.Errorf("FalseNegativeRate=%v, want %v", got, want)
	}
}

func TestReport_EmptyFixtureSet(t *testing.T) {
	rep := Score(nil)
	if rep.Total != 0 {
		t.Errorf("Total=%d, want 0", rep.Total)
	}
	if rep.ClassificationAccuracy() != 0 {
		t.Errorf("Accuracy should be 0 with no fixtures")
	}
	if rep.FalsePositiveRate() != 0 {
		t.Errorf("FPR should be 0 with no healthy-labelled fixtures")
	}
	if rep.FalseNegativeRate() != 0 {
		t.Errorf("FNR should be 0 with no non-healthy-labelled fixtures")
	}
}

func TestLoadFixtures_FromTestdata(t *testing.T) {
	// Lock in that the shipped testdata loads cleanly and contains
	// the required spread of expected labels. A future PR adding
	// fixtures must keep this distribution honest.
	fixtures, err := LoadFixtures("testdata")
	if err != nil {
		t.Fatalf("LoadFixtures: %v", err)
	}
	if len(fixtures) < 8 || len(fixtures) > 12 {
		t.Fatalf("expected 8..12 fixtures, got %d", len(fixtures))
	}
	counts := map[contexthealth.State]int{}
	advisorFires := 0
	for _, f := range fixtures {
		counts[f.ExpectedState]++
		if f.ExpectedAdvisorFires {
			advisorFires++
		}
	}
	if counts[contexthealth.StateHealthy] == 0 {
		t.Error("dataset must include at least one healthy-labelled fixture")
	}
	if counts[contexthealth.StateRescueNow] == 0 {
		t.Error("dataset must include at least one rescue_now-labelled fixture")
	}
	if advisorFires == 0 {
		t.Error("dataset must include at least one advisor-fires fixture")
	}
	if advisorFires == len(fixtures) {
		t.Error("dataset must include at least one advisor-stays-quiet fixture")
	}
}

func TestLoadFixtures_DuplicateNameRejected(t *testing.T) {
	dir := t.TempDir()
	body := []byte(`{"name":"dup","expected_state":"healthy","shape":{}}`)
	if err := os.WriteFile(filepath.Join(dir, "a.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFixtures(dir); err == nil {
		t.Fatal("expected duplicate-name error, got nil")
	}
}

func TestLoadFixtures_ArrayFileShape(t *testing.T) {
	dir := t.TempDir()
	body := []byte(`[
	  {"name":"one","expected_state":"healthy","shape":{}},
	  {"name":"two","expected_state":"drifting","shape":{"context_fill_pct":35}}
	]`)
	if err := os.WriteFile(filepath.Join(dir, "batch.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	fx, err := LoadFixtures(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(fx) != 2 {
		t.Fatalf("len=%d, want 2", len(fx))
	}
	if fx[0].Name != "one" || fx[1].Name != "two" {
		t.Errorf("order=[%q,%q], want [one,two]", fx[0].Name, fx[1].Name)
	}
}

func TestWriteReport_Format(t *testing.T) {
	rep := Score([]LabelledFixture{
		{
			Name:          "a",
			ExpectedState: contexthealth.StateHealthy,
			Shape:         SessionShape{ContextFillPct: 5, AssistantTurns: 3, UserTurns: 6, TokensInPerTurn: 1000},
		},
	})
	var buf bytes.Buffer
	WriteReport(&buf, rep)
	s := buf.String()
	for _, must := range []string{
		"klyne contexthealth eval report",
		"fixtures evaluated",
		"classification accuracy",
		"advisor correctness",
		"false-positive rate",
		"false-negative rate",
		"per-fixture detail:",
	} {
		if !strings.Contains(s, must) {
			t.Errorf("report missing %q\n---\n%s\n---", must, s)
		}
	}
}

// TestScoreOnTestdata is the BASELINE. Running the shipped fixtures
// through Score must produce stable headline numbers; any drop here
// is the signal a future threshold-tuning PR will compare against.
// Asserts a minimum — not a fixed value — so that improving the
// thresholds in a future PR does not flake this test.
func TestScoreOnTestdata(t *testing.T) {
	fixtures, err := LoadFixtures("testdata")
	if err != nil {
		t.Fatalf("LoadFixtures: %v", err)
	}
	rep := Score(fixtures)
	if rep.Total == 0 {
		t.Fatal("Total=0; testdata must contain fixtures")
	}
	// Lower-bound the headline metrics so a regression to the
	// classifier or advisor that loses fixtures cannot land
	// silently. 75% is the working baseline: leaves room for one
	// borderline fixture to flip on a future threshold tweak without
	// breaking this test.
	if got := rep.ClassificationAccuracy(); got < 0.75 {
		t.Errorf("classification accuracy regressed: got %.3f, want >= 0.75\n%s",
			got, dumpRows(rep))
	}
	if got := rep.AdvisorAccuracy(); got < 0.75 {
		t.Errorf("advisor accuracy regressed: got %.3f, want >= 0.75\n%s",
			got, dumpRows(rep))
	}
}

// abs is a tiny helper because math.Abs would force a math import for
// what is genuinely a one-line need.
func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// dumpRows returns a multi-line human-readable list of every Row,
// used to make TestScoreOnTestdata failures self-explanatory.
func dumpRows(rep Report) string {
	var b strings.Builder
	for _, r := range rep.Rows {
		b.WriteString("  ")
		b.WriteString(r.Name)
		b.WriteString(": exp=")
		b.WriteString(string(r.ExpectedState))
		b.WriteString(" got=")
		b.WriteString(string(r.GotState))
		b.WriteString(" exp_adv=")
		if r.ExpectedAdvisorFires {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
		b.WriteString(" got_adv=")
		if r.GotAdvisorFires {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
		b.WriteString(" reason=")
		b.WriteString(r.Reason)
		b.WriteString("\n")
	}
	return b.String()
}

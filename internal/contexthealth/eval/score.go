package eval

import (
	"fmt"
	"io"
	"strings"

	"github.com/klyne-ai/klyne/internal/contexthealth"
)

// score.go — runs fixtures through the production classifier and
// advisor and produces a Report.
//
// "Healthy" is treated as the negative class for the FPR/FNR
// computation: a false positive is "classifier said something is
// wrong when nothing was" (predicted non-healthy when expected was
// healthy), a false negative is "classifier said healthy when
// something was actually wrong" (predicted healthy when expected
// was anything else). This matches what a future threshold-tuning
// PR will care about: are we crying wolf, or missing real bloat?

// Row is the per-fixture outcome row in a Report.
type Row struct {
	// Name mirrors LabelledFixture.Name.
	Name string

	// ExpectedState is the labelled-expected classifier state.
	ExpectedState contexthealth.State
	// GotState is what Classify actually returned.
	GotState contexthealth.State
	// StateMatch is true when ExpectedState == GotState.
	StateMatch bool

	// ExpectedAdvisorFires is the labelled-expected advisor outcome
	// (true = should fire, false = should stay silent).
	ExpectedAdvisorFires bool
	// GotAdvisorFires reports whether RenderAdvisor produced a
	// non-empty Line.
	GotAdvisorFires bool
	// AdvisorMatch is true when ExpectedAdvisorFires == GotAdvisorFires.
	AdvisorMatch bool

	// Reason is the classifier's user-facing reason string. Kept on
	// the row for easy debugging from the CLI output.
	Reason string
	// AdvisorFired names which trigger fired (empty when none did).
	AdvisorFired contexthealth.TriggerKind
}

// Report is the aggregate scoring outcome for a fixture set.
type Report struct {
	// Total is the number of fixtures evaluated.
	Total int

	// ClassificationCorrect is the count of fixtures where the
	// classifier returned the expected state.
	ClassificationCorrect int
	// AdvisorCorrect is the count of fixtures where the advisor's
	// fire/quiet decision matched the expected.
	AdvisorCorrect int

	// FalsePositives is the count of fixtures whose expected state
	// is Healthy but the classifier returned something else.
	FalsePositives int
	// FalseNegatives is the count of fixtures whose expected state
	// is NOT Healthy but the classifier returned Healthy.
	FalseNegatives int
	// HealthyExpected counts fixtures labelled Healthy. Denominator
	// for the false-positive rate.
	HealthyExpected int
	// NonHealthyExpected counts fixtures labelled anything else.
	// Denominator for the false-negative rate.
	NonHealthyExpected int

	// Rows is the per-fixture detail, in fixture-load order.
	Rows []Row
}

// ClassificationAccuracy returns the headline accuracy in [0, 1].
// Zero fixtures returns 0 (callers should detect Total==0 and skip
// the metric in their UI).
func (r Report) ClassificationAccuracy() float64 {
	if r.Total == 0 {
		return 0
	}
	return float64(r.ClassificationCorrect) / float64(r.Total)
}

// AdvisorAccuracy returns the advisor-correctness rate in [0, 1].
func (r Report) AdvisorAccuracy() float64 {
	if r.Total == 0 {
		return 0
	}
	return float64(r.AdvisorCorrect) / float64(r.Total)
}

// FalsePositiveRate returns FP / HealthyExpected in [0, 1]. Returns
// zero when no fixture is labelled Healthy (the rate is undefined
// rather than zero, but a zero result is the least misleading thing
// to print in that case — Total inspection tells the user why).
func (r Report) FalsePositiveRate() float64 {
	if r.HealthyExpected == 0 {
		return 0
	}
	return float64(r.FalsePositives) / float64(r.HealthyExpected)
}

// FalseNegativeRate returns FN / NonHealthyExpected in [0, 1].
func (r Report) FalseNegativeRate() float64 {
	if r.NonHealthyExpected == 0 {
		return 0
	}
	return float64(r.FalseNegatives) / float64(r.NonHealthyExpected)
}

// Score runs each fixture through Classify + RenderAdvisor and
// returns the aggregate Report. Pure function over the input slice;
// the production classifier and advisor are both pure too.
func Score(fixtures []LabelledFixture) Report {
	rep := Report{Total: len(fixtures), Rows: make([]Row, 0, len(fixtures))}
	for _, fx := range fixtures {
		row := evaluate(fx)
		rep.Rows = append(rep.Rows, row)

		if row.StateMatch {
			rep.ClassificationCorrect++
		}
		if row.AdvisorMatch {
			rep.AdvisorCorrect++
		}
		if row.ExpectedState == contexthealth.StateHealthy {
			rep.HealthyExpected++
			if !row.StateMatch {
				rep.FalsePositives++
			}
		} else {
			rep.NonHealthyExpected++
			if row.GotState == contexthealth.StateHealthy {
				rep.FalseNegatives++
			}
		}
	}
	return rep
}

// evaluate runs Classify + RenderAdvisor for one fixture and packs
// the outcome into a Row. Split from Score so tests can exercise
// the per-fixture path without building an aggregate.
func evaluate(fx LabelledFixture) Row {
	clsIn, advIn := buildInput(fx)
	verdict := contexthealth.Classify(clsIn)
	advisory := contexthealth.RenderAdvisor(advIn)
	advisorFired := advisory.Line != ""
	return Row{
		Name:                 fx.Name,
		ExpectedState:        fx.ExpectedState,
		GotState:             verdict.State,
		StateMatch:           verdict.State == fx.ExpectedState,
		ExpectedAdvisorFires: fx.ExpectedAdvisorFires,
		GotAdvisorFires:      advisorFired,
		AdvisorMatch:         advisorFired == fx.ExpectedAdvisorFires,
		Reason:               verdict.Reason,
		AdvisorFired:         advisory.Fired,
	}
}

// WriteReport formats the Report as a human-readable summary plus a
// per-fixture table and writes it to w. Used by the `klyne eval
// contexthealth` CLI; tests use it to lock the output shape.
func WriteReport(w io.Writer, rep Report) {
	fmt.Fprintln(w, "klyne contexthealth eval report")
	fmt.Fprintf(w, "  fixtures evaluated      : %d\n", rep.Total)
	fmt.Fprintf(w, "  classification accuracy : %s (%d/%d)\n",
		pct(rep.ClassificationAccuracy()), rep.ClassificationCorrect, rep.Total)
	fmt.Fprintf(w, "  advisor correctness     : %s (%d/%d)\n",
		pct(rep.AdvisorAccuracy()), rep.AdvisorCorrect, rep.Total)
	fmt.Fprintf(w, "  false-positive rate     : %s (%d/%d healthy-labelled)\n",
		pct(rep.FalsePositiveRate()), rep.FalsePositives, rep.HealthyExpected)
	fmt.Fprintf(w, "  false-negative rate     : %s (%d/%d non-healthy-labelled)\n",
		pct(rep.FalseNegativeRate()), rep.FalseNegatives, rep.NonHealthyExpected)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "per-fixture detail:")
	for _, row := range rep.Rows {
		stateMark := okMark(row.StateMatch)
		advMark := okMark(row.AdvisorMatch)
		fmt.Fprintf(w, "  %s %-32s state: %-10s got=%-10s adv: exp=%-5t got=%-5t %s\n",
			combinedMark(stateMark, advMark),
			row.Name, row.ExpectedState, row.GotState,
			row.ExpectedAdvisorFires, row.GotAdvisorFires,
			truncate(row.Reason, 60),
		)
	}
}

// pct renders a 0..1 float as a "%.1f%%" string.
func pct(v float64) string {
	return fmt.Sprintf("%5.1f%%", v*100)
}

// okMark returns "ok" or "!!" for a boolean — used by the CLI's
// per-fixture table.
func okMark(ok bool) string {
	if ok {
		return "ok"
	}
	return "!!"
}

// combinedMark returns "ok" only when BOTH per-row checks passed.
// Lets a scanner spot the first failing fixture instantly.
func combinedMark(state, adv string) string {
	if state == "ok" && adv == "ok" {
		return "[ok]"
	}
	return "[!!]"
}

// truncate caps s at n runes, appending an ellipsis when it had to
// cut.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.TrimRight(s[:n], " ") + "..."
}

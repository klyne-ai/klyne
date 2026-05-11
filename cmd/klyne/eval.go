package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/klyne-ai/klyne/internal/contexthealth/eval"
)

// eval.go — `klyne eval` subcommand group.
//
// This is a developer-facing entry point for the labelled-fixture
// eval runner under internal/contexthealth/eval. The runner measures
// how well today's deterministic classifier + advisor heuristics
// match a hand-labelled dataset; the headline numbers it prints form
// the baseline a future threshold-tuning PR will compare against.
//
// The subcommand is intentionally read-only: it never mutates the
// classifier, the advisor, or any persisted state. It can be safely
// re-run by reviewers without side effects.

// newEvalCmd registers the top-level `klyne eval` group. Today it
// has one child (`contexthealth`); the group is here so future eval
// suites (cost, advisor-only, etc.) can slot in without reshuffling
// the command tree.
func newEvalCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "eval",
		Short: "Run klyne evaluation suites against labelled fixtures",
		Long: `Run klyne evaluation suites against labelled fixtures.

Eval suites measure how well klyne's deterministic heuristics
classify or advise on synthetic-but-realistic session shapes. The
suites are read-only — they observe the production classifier and
advisor through their public APIs and report accuracy / false-
positive / false-negative numbers without modifying behaviour.`,
		SilenceUsage: true,
	}
	c.AddCommand(newEvalContextHealthCmd())
	return c
}

// newEvalContextHealthCmd registers `klyne eval contexthealth`.
// Defaults the fixture directory to the in-tree testdata path so
// `klyne eval contexthealth` works as a smoke test out of the box;
// the --fixtures flag lets reviewers point at an alternate dataset
// when iterating on labels.
func newEvalContextHealthCmd() *cobra.Command {
	var fixturesDir string
	c := &cobra.Command{
		Use:   "contexthealth",
		Short: "Score the contexthealth classifier + advisor against labelled fixtures",
		Long: `Score the contexthealth classifier and advisor against labelled fixtures.

Loads every .json fixture under --fixtures, runs each through the
production Classify and RenderAdvisor functions, and prints a
report covering:

  - classification accuracy (state matches expected label)
  - advisor correctness (fire/quiet matches expected label)
  - false-positive rate (predicted non-healthy when expected healthy)
  - false-negative rate (predicted healthy when expected non-healthy)
  - per-fixture detail row for every fixture in the dataset

The fixture directory defaults to internal/contexthealth/eval/testdata
so this works from the repo root with no flags. Pass --fixtures to
score an alternate dataset.

Exit code is non-zero only when the fixture directory cannot be
loaded; mismatches between expected and predicted labels do NOT
fail the command — the whole point is to see the current accuracy
number, not to gate on it.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fixtures, err := eval.LoadFixtures(fixturesDir)
			if err != nil {
				return fmt.Errorf("load fixtures: %w", err)
			}
			rep := eval.Score(fixtures)
			eval.WriteReport(cmd.OutOrStdout(), rep)
			return nil
		},
		SilenceUsage: true,
	}
	c.Flags().StringVar(&fixturesDir, "fixtures", "internal/contexthealth/eval/testdata",
		"directory of .json fixture files to score against")
	return c
}

package worklog_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// TestDogfoodKillCriteria runs the 6 kill criteria from
// docs/research/worklog/00-plan.md against the live klyne database.
// SKIPPED in normal `go test` runs — requires real worklog data
// accumulated over at least 5 working days of dogfooding.
//
// Run explicitly with:
//
//	GOTOOLCHAIN=auto go test ./internal/worklog/ -run TestDogfoodKillCriteria -v -timeout 30m
//
// Override the DB path:
//
//	KLYNE_DOGFOOD_DB=/path/to/klyne.db go test ./internal/worklog/ -run TestDogfoodKillCriteria -v
//
// The K1, K2, K5, K6 criteria are manual surveys / subjective signals
// that cannot be measured by code alone; their stubs print clear
// instructions for the maintainer to record findings.
func TestDogfoodKillCriteria(t *testing.T) {
	if testing.Short() {
		t.Skip("dogfood test — needs real klyne.db with ≥ 5 days of entries")
	}
	dbPath := resolveDogfoodDBPath(t)
	if _, err := os.Stat(dbPath); err != nil {
		t.Skipf("klyne.db not found at %s (set KLYNE_DOGFOOD_DB to override): %v", dbPath, err)
	}
	db, err := sql.Open("sqlite", dbPath+"?mode=ro")
	if err != nil {
		t.Fatalf("open ro: %v", err)
	}
	defer db.Close()

	// Verify the worklog migration has been applied — the quantitative
	// criteria (K3, K4) read columns added by migration 015. If they
	// are missing the DB was never migrated for the worklog feature;
	// skip rather than fail noisily.
	if err := requireWorklogColumns(context.Background(), db); err != nil {
		t.Skipf("worklog columns missing on %s — run `klyne migrate` and accumulate ≥ 5 days of entries before re-running: %v", dbPath, err)
	}

	crit := []struct {
		name string
		fn   func(context.Context, *sql.DB) (pass bool, msg string, err error)
	}{
		{"K1 dogfood usefulness >= 60pct (manual)", critK1DogfoodUsefulness},
		{"K2 beats existing stack >= 12 of 20 (manual)", critK2CounterfactualAB},
		{"K3 hallucination rate <= 4 of 30 (manual sample)", critK3HallucinationAudit},
		{"K4 <= 60pct reconstructible from base columns (quantitative)", critK4SchemaDrift},
		{"K5 maintainer reaches for worklog (manual)", critK5BoredomSignal},
		{"K6 >= 2 external devs say yes (manual)", critK6ExternalValidation},
	}

	for _, c := range crit {
		c := c
		t.Run(c.name, func(t *testing.T) {
			pass, msg, err := c.fn(context.Background(), db)
			if err != nil {
				t.Fatalf("error running %s: %v", c.name, err)
			}
			if !pass {
				t.Errorf("FAIL — %s", msg)
			} else {
				t.Logf("PASS — %s", msg)
			}
		})
	}
}

// resolveDogfoodDBPath returns the path to the live klyne.db, preferring
// KLYNE_DOGFOOD_DB then ~/.klyne/klyne.db.
func resolveDogfoodDBPath(t *testing.T) string {
	t.Helper()
	if v := os.Getenv("KLYNE_DOGFOOD_DB"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("user home: %v", err)
	}
	return filepath.Join(home, ".klyne", "klyne.db")
}

// requireWorklogColumns probes for the columns migration 015 introduces.
// Returns nil if all three quantitative-criteria columns are present.
func requireWorklogColumns(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(stop_summaries)`)
	if err != nil {
		return fmt.Errorf("pragma: %w", err)
	}
	defer rows.Close()
	have := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return fmt.Errorf("scan: %w", err)
		}
		have[strings.ToLower(name)] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, col := range []string{"recap_visible", "recap_topic", "ai_drafted_summary"} {
		if !have[col] {
			return fmt.Errorf("missing column %s", col)
		}
	}
	return nil
}

// --- K1: dogfood usefulness (manual) ------------------------------------

func critK1DogfoodUsefulness(_ context.Context, _ *sql.DB) (bool, string, error) {
	// Try to answer 20 questions you'd normally ask using ONLY the new
	// recap surface (recap_project / user_recap / bootstrap injection).
	// If useful-answer rate is < 60% → KILL.
	return true, "MANUAL: answer 20 typical questions using only recap_project / user_recap / bootstrap; record the useful-answer rate. Kill if < 60%.", nil
}

// --- K2: counterfactual A/B (manual) ------------------------------------

func critK2CounterfactualAB(_ context.Context, _ *sql.DB) (bool, string, error) {
	// For the SAME 20 questions, also answer using only existing klyne
	// (search_messages + list_decisions + generate_handoff). If the new
	// recap surface does not strictly beat the existing stack on >= 12 of
	// 20 → KILL — you have a /klyne:recap win, not a worklog win.
	return true, "MANUAL: re-answer the same K1 questions using only search_messages + list_decisions + generate_handoff. Kill if recap does not beat the existing stack on >= 12 of 20.", nil
}

// --- K3: hallucination audit (manual sample) ----------------------------

func critK3HallucinationAudit(ctx context.Context, db *sql.DB) (bool, string, error) {
	// Quantitative half: confirm the recall set exists.
	var n int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM stop_summaries WHERE recap_visible = 1 AND ai_drafted_summary != ''`).Scan(&n); err != nil {
		return false, "", err
	}
	if n < 30 {
		return false, fmt.Sprintf("only %d AI-drafted summaries — need >= 30 to audit; keep dogfooding", n), nil
	}
	// Manual half: sample 30 at random; have a fresh Claude check each
	// against the underlying transcript. Kill if > 4 contain a hallucination.
	return true, fmt.Sprintf("MANUAL: %d AI-drafted summaries available — sample 30 at random and audit each against the source transcript. Kill if > 4 hallucinations.", n), nil
}

// --- K4: schema drift (quantitative) ------------------------------------

func critK4SchemaDrift(ctx context.Context, db *sql.DB) (bool, string, error) {
	// Heuristic: how many visible entries have a non-empty recap_topic
	// OR ai_drafted_summary that could NOT be reconstructed from the
	// pre-existing base columns (session_id / last_user / last_bash /
	// files_json)? If > 60% of value is purely in the base columns
	// (i.e., the new columns add no signal), the migration was a view
	// problem, not a write problem → KILL.
	var totalVisible, withNewSignal int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM stop_summaries WHERE recap_visible = 1`).Scan(&totalVisible); err != nil {
		return false, "", err
	}
	if totalVisible == 0 {
		return false, "no visible entries yet — keep dogfooding", nil
	}
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM stop_summaries
         WHERE recap_visible = 1
           AND (COALESCE(recap_topic, '') != '' OR COALESCE(ai_drafted_summary, '') != '')`).Scan(&withNewSignal); err != nil {
		return false, "", err
	}
	pctReconstructable := 100 - (withNewSignal * 100 / totalVisible)
	pass := pctReconstructable <= 60
	msg := fmt.Sprintf("%d/%d visible entries (%d%%) carry value ONLY in pre-existing base columns; threshold <= 60%% reconstructable",
		totalVisible-withNewSignal, totalVisible, pctReconstructable)
	return pass, msg, nil
}

// --- K5: boredom signal (manual) ----------------------------------------

func critK5BoredomSignal(_ context.Context, _ *sql.DB) (bool, string, error) {
	// During week 2, did you reach for /klyne:search or git log instead
	// of the recap surface? If yes → KILL.
	return true, "MANUAL: did you reach for /klyne:search or git log instead of the new recap surface during dogfooding? Kill if yes.", nil
}

// --- K6: external validation (manual) -----------------------------------

func critK6ExternalValidation(_ context.Context, _ *sql.DB) (bool, string, error) {
	// Show recap output to two unrelated devs who use Claude Code daily.
	// If neither says "yes, I'd want this" → demote to personal dotfile.
	return true, "MANUAL: show recap output to two unrelated Claude Code daily users; if neither says \"yes, I'd want this,\" demote to personal dotfile.", nil
}

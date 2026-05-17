package worklog

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/klyne-ai/klyne/internal/store"
)

// LM is the minimum surface the synthesizer needs from a language-model
// client. Production callers pass an Anthropic Haiku-backed implementation;
// tests pass an in-memory fake.
type LM interface {
	Complete(ctx context.Context, prompt string) (string, error)
}

type insightResponse struct {
	Insights []struct {
		Text     string   `json:"text"`
		Evidence []string `json:"evidence"`
	} `json:"insights"`
}

// Synthesize fetches recent visible entries for projectPath since the last
// reflection, prompts the LM for high-level insights, enforces the
// citation invariant on every insight, and persists the result.
func Synthesize(ctx context.Context, db *store.DB, projectPath string, lm LM) (store.Reflection, error) {
	var lastRefMs int64
	_ = db.Read().QueryRowContext(ctx,
		`SELECT COALESCE(MAX(ts), 0) FROM worklog_reflections WHERE project_path = ?`,
		projectPath).Scan(&lastRefMs)

	rows, err := db.Read().QueryContext(ctx,
		`SELECT session_id, cli, COALESCE(recap_topic,''), COALESCE(ai_drafted_summary,''), importance, ts
         FROM stop_summaries
         WHERE project_path = ? AND recap_visible = 1 AND ts > ?
         ORDER BY ts ASC LIMIT 50`,
		projectPath, lastRefMs)
	if err != nil {
		return store.Reflection{}, fmt.Errorf("worklog: load entries: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var skeletons []string
	for rows.Next() {
		var sid, cli, topic, summary string
		var imp int
		var ts int64
		if err := rows.Scan(&sid, &cli, &topic, &summary, &imp, &ts); err != nil {
			return store.Reflection{}, fmt.Errorf("worklog: scan entry: %w", err)
		}
		skeletons = append(skeletons,
			fmt.Sprintf("- id=%s [%s] importance=%d topic=%q summary=%q",
				sid, cli, imp, topic, truncateLine(summary, 200)))
	}
	if err := rows.Err(); err != nil {
		return store.Reflection{}, fmt.Errorf("worklog: iterate entries: %w", err)
	}

	prompt := fmt.Sprintf(`Given the following work-log entries from the past period, what 3 high-level insights can we infer? For each insight, cite the specific entries (by id) that serve as evidence. Output JSON:

{"insights":[{"text":"<insight>","evidence":["<entry-id>",...]}]}

Entries:
%s

Output JSON only.`, strings.Join(skeletons, "\n"))

	raw, err := lm.Complete(ctx, prompt)
	if err != nil {
		return store.Reflection{}, fmt.Errorf("worklog: lm complete: %w", err)
	}

	var resp insightResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return store.Reflection{}, fmt.Errorf("worklog: parse lm response: %w", err)
	}

	var allEvidence []string
	var body strings.Builder
	for _, ins := range resp.Insights {
		if len(ins.Evidence) == 0 {
			return store.Reflection{}, fmt.Errorf("worklog: insight missing evidence — rejecting")
		}
		allEvidence = append(allEvidence, ins.Evidence...)
		body.WriteString(fmt.Sprintf("- %s (evidence: %s)\n", ins.Text, strings.Join(ins.Evidence, ", ")))
	}
	if len(allEvidence) == 0 {
		return store.Reflection{}, fmt.Errorf("worklog: no insights returned")
	}

	refl := store.Reflection{
		ID:               fmt.Sprintf("ref-%d", time.Now().UnixNano()),
		TS:               time.Now().UnixMilli(),
		ProjectPath:      projectPath,
		Tier:             2, // weekly
		Title:            fmt.Sprintf("Weekly reflection — %s", IsoWeek(time.Now())),
		BodyMD:           body.String(),
		EvidenceEntryIDs: allEvidence,
		Importance:       7,
		SummarySource:    "ai",
		State:            "proposed",
		StateChangedAt:   time.Now().UnixMilli(),
	}
	if err := store.InsertReflection(ctx, db, refl); err != nil {
		return store.Reflection{}, fmt.Errorf("worklog: persist reflection: %w", err)
	}
	return refl, nil
}

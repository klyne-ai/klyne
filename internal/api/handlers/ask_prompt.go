package handlers

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/store"
)

// askPromptCategories is the closed set of worklog_entry_json keys we
// render into the prompt. These map to the questions Ask Klyne is
// designed to answer (pending tasks, bugs, features, blockers,
// decisions). Other categories (mistakes_or_dead_ends, must_remember,
// reviews_given, …) are dropped to keep the prompt focused.
var askPromptCategories = []string{
	"pending",
	"features_picked",
	"features_worked_on",
	"bugs_fixed",
	"bugs_found",
	"blockers",
	"decisions",
}

// buildAskPrompt assembles the prompt sent to `claude -p`. Pure
// function to keep it trivially testable; the impure subprocess
// wrapper lives in ask_spawn.go.
func buildAskPrompt(req api.AskRequest, rows []store.AskRow) string {
	var b strings.Builder

	b.WriteString("You are Ask Klyne. Answer the user's question using ONLY the sessions below.\n\n")

	projectsLine := "all"
	if len(req.Projects) > 0 {
		projectsLine = strings.Join(req.Projects, ", ")
	}
	fmt.Fprintf(&b, "Projects: %s\n", projectsLine)
	fmt.Fprintf(&b, "Range:    %s .. %s\n",
		time.UnixMilli(req.FromMs).UTC().Format(time.RFC3339),
		time.UnixMilli(req.ToMs).UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "Sessions: %d\n\n", len(rows))

	for i, r := range rows {
		fmt.Fprintf(&b, "## Session %d — %s — %s\n",
			i+1,
			time.UnixMilli(r.TsMs).UTC().Format("2006-01-02 15:04"),
			r.ProjectPath,
		)
		ai := strings.TrimSpace(r.AIDraftedSummary)
		if ai == "" {
			ai = "(empty)"
		}
		fmt.Fprintf(&b, "ai_summary: %s\n", ai)
		fmt.Fprintf(&b, "worklog:    %s\n\n", renderWorklogCategories(r.WorklogEntryJSON))
	}

	if len(req.History) > 0 {
		b.WriteString("---\nPrior conversation (most recent last):\n")
		for _, m := range req.History {
			fmt.Fprintf(&b, "%s: %s\n", m.Role, m.Content)
		}
		b.WriteString("\n")
	}

	b.WriteString("Current user question:\n")
	b.WriteString(strings.TrimSpace(req.Question))
	b.WriteString("\n\nRules:\n")
	b.WriteString("- Cite session date when listing items.\n")
	b.WriteString("- Say \"no matching entries\" if the data does not support an answer.\n")
	b.WriteString("- Do not invent. If a count is asked, count exactly from the data above.\n")

	return b.String()
}

// renderWorklogCategories takes a worklog_entry_json blob and returns a
// compact one-line summary of only the askPromptCategories that have
// entries. Empty categories are omitted entirely. Falls back to the
// raw text when the blob is unparseable so the model still sees data.
func renderWorklogCategories(raw string) string {
	if strings.TrimSpace(raw) == "" || raw == "{}" {
		return "{}"
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return strings.TrimSpace(raw)
	}
	// worklog_entry_json wraps everything under "categories" in the
	// canonical schema. Some older rows may store categories at the
	// top level — handle both.
	cats, ok := doc["categories"].(map[string]any)
	if !ok {
		cats = doc
	}

	var parts []string
	for _, key := range askPromptCategories {
		v, ok := cats[key]
		if !ok {
			continue
		}
		arr, ok := v.([]any)
		if !ok || len(arr) == 0 {
			continue
		}
		summaries := make([]string, 0, len(arr))
		for _, item := range arr {
			obj, ok := item.(map[string]any)
			if !ok {
				continue
			}
			s, _ := obj["summary"].(string)
			if t, _ := obj["ticket"].(string); t != "" {
				s = fmt.Sprintf("%s (%s)", s, t)
			}
			if s != "" {
				summaries = append(summaries, s)
			}
		}
		if len(summaries) == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s: [%s]", key, strings.Join(summaries, " | ")))
	}
	if len(parts) == 0 {
		return "{}"
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

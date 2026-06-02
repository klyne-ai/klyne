package handlers

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/store"
)

// askContextCap is the maximum number of stop_summaries rows fed into
// a single prompt. Above this the handler keeps the most recent N and
// sets AskResponse.TruncatedToN so the drawer can show an "answer based
// on N of M" affordance.
const askContextCap = 200

// askMaxQuestionLen guards against runaway prompts and keeps the
// stdin/argv payload bounded.
const askMaxQuestionLen = 2000

// askTimeout caps the subprocess. Tuned for interactive UX — most
// answers complete in <10s. Generous enough for large ranges.
const askTimeout = 60 * time.Second

// AskHandler serves POST /api/ask — the chat endpoint for the Ask
// Klyne drawer on the productivity page. Stateless. Each request
// carries its full context (projects, range, history). Mirrors the
// productivity_compile.go pattern: a runCmd seam lets tests inject a
// stub spawner.
type AskHandler struct {
	db     *store.DB
	runCmd func(ctx context.Context, prompt string) (claudeRunResult, error)
}

// NewAskHandler constructs an AskHandler that uses the real `claude`
// CLI subprocess. Tests construct the struct literal directly.
func NewAskHandler(db *store.DB) *AskHandler {
	return &AskHandler{db: db, runCmd: spawnAskClaude}
}

// Run handles POST /api/ask.
func (h *AskHandler) Run(w http.ResponseWriter, r *http.Request) {
	var req api.AskRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	req.Question = strings.TrimSpace(req.Question)
	if req.Question == "" {
		http.Error(w, "question required", http.StatusBadRequest)
		return
	}
	if len(req.Question) > askMaxQuestionLen {
		http.Error(w, "question too long", http.StatusBadRequest)
		return
	}
	if req.ToMs < req.FromMs {
		http.Error(w, "invalid range", http.StatusBadRequest)
		return
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(r.Context(), askTimeout)
	defer cancel()

	rows, err := store.LoadAskContext(ctx, h.db, req.Projects, req.FromMs, req.ToMs)
	if err != nil {
		http.Error(w, fmt.Sprintf("load context: %v", err), http.StatusInternalServerError)
		return
	}

	// Empty-range short-circuit. No subprocess, no LLM bill.
	if len(rows) == 0 {
		writeJSON(w, http.StatusOK, api.AskResponse{
			Answer:       "No sessions recorded in the selected range.",
			SessionsUsed: 0,
			Model:        "",
			DurationMs:   time.Since(start).Milliseconds(),
		})
		return
	}

	truncated := 0
	if len(rows) > askContextCap {
		// Sort by TsMs desc to keep the most recent — recency is a
		// strong-enough proxy for the drawer's "what did I do
		// recently" job.
		sort.Slice(rows, func(i, j int) bool { return rows[i].TsMs > rows[j].TsMs })
		rows = rows[:askContextCap]
		// Restore chronological order for the prompt.
		sort.Slice(rows, func(i, j int) bool { return rows[i].TsMs < rows[j].TsMs })
		truncated = askContextCap
	}

	prompt := buildAskPrompt(req, rows)
	res, runErr := h.runCmd(ctx, prompt)
	if runErr != nil {
		http.Error(w, fmt.Sprintf("ask: %v", runErr), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, api.AskResponse{
		Answer:       res.Output,
		SessionsUsed: len(rows),
		TruncatedToN: truncated,
		Model:        res.Model,
		DurationMs:   time.Since(start).Milliseconds(),
	})
}

// Compile-time check the route constant is referenced from somewhere
// in the package so a future rename can't silently drift.
var _ = api.RouteAsk

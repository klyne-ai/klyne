package handlers

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/resume"
	"github.com/klyne-ai/klyne/internal/store"
)

// restoreTailSize is the number of tail messages to include in the restore bundle.
const restoreTailSize = 20

// RestoreHandler handles GET /sessions/:id/restore.
type RestoreHandler struct {
	db *store.DB
}

// NewRestoreHandler constructs a RestoreHandler.
func NewRestoreHandler(db *store.DB) *RestoreHandler {
	return &RestoreHandler{db: db}
}

// Restore handles GET /sessions/{id}/restore.
// Returns a RestoreResponse containing:
//   - The latest rolling summary (from session_summaries).
//   - The last 20 messages in order.
//   - A pre-formatted Markdown bundle ready to paste into Claude Code.
//   - A resume command string ("claude --resume <id>" or "codex resume --last").
func (h *RestoreHandler) Restore(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	// Fetch session for CLI and project path.
	session, err := store.GetSession(ctx, h.db, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Fetch the latest summary (may be absent).
	var summaryText string
	summary, err := store.LatestSummary(ctx, h.db, id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if summary != nil {
		summaryText = summary.Text
	}

	// Fetch last 20 messages.
	msgs, err := store.ListMessagesBySession(ctx, h.db, id, restoreTailSize, 0)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// If we got exactly restoreTailSize messages and there might be more,
	// we need to get the last 20 — re-query with DESC ordering approach.
	// Since ListMessagesBySession returns ASC, if we get restoreTailSize rows,
	// fetch more and take the tail.
	if len(msgs) == restoreTailSize {
		all, err := store.ListMessagesBySession(ctx, h.db, id, 0, 0)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if len(all) > restoreTailSize {
			msgs = all[len(all)-restoreTailSize:]
		} else {
			msgs = all
		}
	}

	// Build the Markdown bundle.
	markdown := buildMarkdown(id, summaryText, msgs)

	// Build the resume command.
	resumeCmd := buildResumeCmd(session.CLI, id, session.ProjectPath)

	// Build tail slice for response.
	tail := make([]connectors.Message, 0, len(msgs))
	for _, m := range msgs {
		tail = append(tail, *m)
	}

	resp := api.RestoreResponse{
		SessionID:   id,
		Summary:     summaryText,
		Tail:        tail,
		Markdown:    markdown,
		ResumeCmd:   resumeCmd,
		ProjectPath: session.ProjectPath,
		GeneratedAt: time.Now().UnixMilli(),
	}

	writeJSON(w, http.StatusOK, resp)
}

// buildMarkdown constructs the Markdown restore bundle.
// Format matches the golden file in testdata/restore_golden.md.
func buildMarkdown(sessionID, summaryText string, msgs []*connectors.Message) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# Session %s — restore context\n\n", sessionID)

	b.WriteString("## Summary (auto-generated)\n\n")
	if summaryText != "" {
		b.WriteString(summaryText)
		b.WriteString("\n\n")
	} else {
		b.WriteString("*(no summary available)*\n\n")
	}

	b.WriteString("## Last 20 messages\n\n")
	for _, m := range msgs {
		roleLabel := string(m.Role)
		fmt.Fprintf(&b, "**%s:** %s\n\n", roleLabel, m.Content)
	}

	return b.String()
}

// buildResumeCmd returns the appropriate CLI resume command.
func buildResumeCmd(cli connectors.CLI, sessionID, projectPath string) string {
	switch cli {
	case connectors.CLICodex:
		return resume.CodexCmd(sessionID, projectPath)
	default:
		return resume.ClaudeCmd(sessionID, projectPath)
	}
}

package handlers

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/klyne-ai/klyne/internal/store"
	"github.com/klyne-ai/klyne/internal/usage"
)

// UsageHandler serves GET /usage. Returns rolling 5h, 7d, and 7d-Sonnet
// token aggregates for both Claude and Codex CLIs, plus (when available)
// vendor-canonical utilization percentages from Anthropic's
// /api/oauth/usage. The OAuth path is best-effort — token-based estimates
// are always populated as a fallback.
type UsageHandler struct {
	db        *store.DB
	logger    *slog.Logger
	oauth     *usage.OAuthFetcher
	codexSnap *usage.CodexSnapshotReader
	// nowFn is the clock source. Production uses time.Now; tests inject a
	// fixed clock so window boundaries are deterministic.
	nowFn func() time.Time
}

// NewUsageHandler constructs a UsageHandler with production data
// sources: the Claude OAuth fetcher (reads credentials from the macOS
// Keychain) and the Codex JSONL snapshot reader (scans
// ~/.codex/sessions for the latest rate-limits event).
func NewUsageHandler(db *store.DB, logger *slog.Logger) *UsageHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &UsageHandler{
		db:        db,
		logger:    logger,
		oauth:     usage.NewOAuthFetcher(),
		codexSnap: usage.NewCodexSnapshotReader(""),
		nowFn:     time.Now,
	}
}

// Get serves GET /usage.
func (h *UsageHandler) Get(w http.ResponseWriter, r *http.Request) {
	resp, err := usage.Compute(r.Context(), h.db, h.nowFn(), h.oauth, h.codexSnap)
	if err != nil {
		h.logger.Error("usage: compute failed", slog.Any("error", err))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

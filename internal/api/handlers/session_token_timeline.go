package handlers

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/contexthealth"
	"github.com/klyne-ai/klyne/internal/store"
	"github.com/klyne-ai/klyne/internal/usage"
)

// tokenTimelineMessageLimit caps the per-session message scan. Token
// usage is computed off assistant turns only, but the store doesn't
// expose a "role=assistant only" filter, so we read the recent tail.
// 5000 turns is generous — Claude Code sessions cap out well below
// this in practice, and even a megasession would still fit cleanly in
// a single round-trip. The MCP/CLI surface loads the entire JSONL
// snapshot for the same computation; this is the DB-backed analogue.
const tokenTimelineMessageLimit = 5000

// tokenTimelineMinWindow / tokenTimelineMaxWindow bracket the lookback
// duration. Sub-minute windows return near-empty timelines on real
// sessions; very long windows extend smoothly via the no-window path
// anyway. Mirrors the clamp in mcpserver.resolveWindowMs so the two
// surfaces agree on what "window=30m" means.
const (
	tokenTimelineMinWindow = time.Minute
	tokenTimelineMaxWindow = 24 * time.Hour
)

// SessionTokenTimelineHandler serves GET /sessions/{id}/token-timeline.
// It backs the cockpit's per-session token-usage line chart by mirroring
// the same computation the CLI and MCP surfaces already use
// (contexthealth.ComputeTimeline). The shared internal function is the
// single source of truth — this handler is just the HTTP shell that
// loads messages from the store and maps the result to the wire DTO.
type SessionTokenTimelineHandler struct {
	db     *store.DB
	logger *slog.Logger
	// nowFn is the clock source. Production uses time.Now; tests can
	// inject a fixed clock for deterministic window math.
	nowFn func() time.Time
}

// SessionTokenTimelineDeps groups the constructor inputs.
type SessionTokenTimelineDeps struct {
	DB     *store.DB
	Logger *slog.Logger
	NowFn  func() time.Time
}

// NewSessionTokenTimelineHandler constructs a SessionTokenTimelineHandler.
// When NowFn is nil it defaults to time.Now.
func NewSessionTokenTimelineHandler(deps SessionTokenTimelineDeps) *SessionTokenTimelineHandler {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := deps.NowFn
	if now == nil {
		now = time.Now
	}
	return &SessionTokenTimelineHandler{
		db:     deps.DB,
		logger: logger,
		nowFn:  now,
	}
}

// Get serves GET /sessions/{id}/token-timeline.
//
// Query params:
//   - window  Go duration string (e.g. "30m", "5h", "2h30m"); min 1m,
//     max 24h. When set, only assistant turns inside [now-window, now]
//     are returned.
//   - hours   convenience integer hours; ignored when window is set.
//
// When neither is provided, the response describes the entire session
// — matching the default the CLI and MCP surfaces use, so users who
// open a long-paused session see its full history instead of an empty
// 5h window.
func (h *SessionTokenTimelineHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	session, err := store.GetSession(ctx, h.db, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
		h.logger.Error("token-timeline: get session",
			slog.String("id", id), slog.Any("error", err))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	hours, err := queryInt(r, "hours", 0)
	if err != nil || hours < 0 {
		http.Error(w, "invalid hours: must be a non-negative integer", http.StatusBadRequest)
		return
	}
	windowMs, err := resolveTimelineWindowMs(r.URL.Query().Get("window"), hours)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Read the session's messages in chronological order. The store
	// doesn't expose a role filter, but contexthealth.ComputeTimeline
	// already ignores non-assistant rows and zero-token turns, so
	// passing the full slice is correct and matches the MCP path's
	// behaviour (which feeds it a SessionSnapshot containing every
	// row from the JSONL).
	msgs, err := store.ListMessagesBySession(ctx, h.db, id, tokenTimelineMessageLimit, 0)
	if err != nil {
		h.logger.Error("token-timeline: list messages",
			slog.String("id", id), slog.Any("error", err))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	now := h.nowFn().UnixMilli()
	// cap=0 — the rate-limit cap (PlanTier-derived) is irrelevant for
	// the chart. We expose the prefix-size axis (TotalInput) only;
	// the rate-limit advisor lives at /usage. Keeping these axes
	// separate prevents the cockpit chart from being read as a
	// rate-limit projection.
	tl := contexthealth.ComputeTimeline(msgs, now, windowMs, 0)
	if tl.SessionID == "" {
		tl.SessionID = id
	}
	if tl.Model == "" {
		tl.Model = session.Model
	}
	tl.ContextWindow = usage.ContextWindowForModel(tl.Model)

	resp := api.TokenTimelineResponse{
		SessionID:     tl.SessionID,
		Model:         tl.Model,
		ContextWindow: tl.ContextWindow,
		WindowStartMs: tl.WindowStartMs,
		WindowEndMs:   tl.WindowEndMs,
		Points:        toAPITokenTimelinePoints(tl.Points),
		FirstInput:    tl.FirstInput,
		LatestInput:   tl.LatestInput,
		PeakInput:     tl.PeakInput,
		PctOfContext:  tl.PctOfContext(),
	}

	writeJSON(w, http.StatusOK, resp)
}

// toAPITokenTimelinePoints copies the internal timeline rows into the
// wire DTO. A 1:1 field copy — kept explicit so the api package isn't
// forced to import contexthealth into its exported type set.
func toAPITokenTimelinePoints(pts []contexthealth.TimelinePoint) []api.TokenTimelinePoint {
	out := make([]api.TokenTimelinePoint, len(pts))
	for i, p := range pts {
		out[i] = api.TokenTimelinePoint{
			TsMs:              p.TsMs,
			EffectiveInput:    p.EffectiveInput,
			TotalInput:        p.TotalInput,
			CachedReadTokens:  p.CachedReadTokens,
			CachedWriteTokens: p.CachedWriteTokens,
			OutputTokens:      p.OutputTokens,
		}
	}
	return out
}

// resolveTimelineWindowMs decides the lookback in milliseconds.
// Returns 0 (entire-session view) when neither input is set, or a
// clamped value when either is. Invalid inputs return a non-nil error
// so the handler can return 400 instead of silently falling back.
func resolveTimelineWindowMs(window string, hours int) (int64, error) {
	clamp := func(d time.Duration) time.Duration {
		if d < tokenTimelineMinWindow {
			return tokenTimelineMinWindow
		}
		if d > tokenTimelineMaxWindow {
			return tokenTimelineMaxWindow
		}
		return d
	}
	trimmed := strings.TrimSpace(window)
	if trimmed != "" {
		d, err := time.ParseDuration(trimmed)
		if err != nil {
			return 0, errors.New("invalid window: must be a Go duration like \"30m\" or \"5h\"")
		}
		if d <= 0 {
			return 0, errors.New("invalid window: must be positive")
		}
		return int64(clamp(d) / time.Millisecond), nil
	}
	if hours > 0 {
		return int64(clamp(time.Duration(hours)*time.Hour) / time.Millisecond), nil
	}
	return 0, nil
}

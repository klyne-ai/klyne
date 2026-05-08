package handlers

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/store"
	"github.com/klyne-ai/klyne/internal/usage"
)

// CompactRatio is the assumed post-/compact size as a fraction of the
// pre-compact context. Sourced from observed Claude Code behavior
// (typical /compact reduces to 10-20% of the original); 0.15 is the
// midpoint we surface to the UI as a constant for v1.
const CompactRatio = 0.15

// SystemPromptFloor is the approximate token count a fresh session loads
// before the user's first message — system prompt + tools manifest + any
// auto-included project files. 5k is empirically close enough; the
// "restart cost" indicator only needs ballpark numbers.
const SystemPromptFloor int64 = 5000

// uncalibrated is the sentinel returned in *Pct5h fields when we can't
// derive a token-to-percentage scale. The UI renders this as "—".
const uncalibrated float64 = -1.0

// usageMessageScanLimit is the number of recent messages walked when
// looking for the latest assistant message with non-zero tokens_in. 50 is
// generous — assistant messages are interleaved with user/tool turns, but
// we hardly ever need to look more than a handful back.
const usageMessageScanLimit = 50

// SessionUsageHandler serves GET /sessions/{id}/usage. It backs the
// session-detail "context fill + cost-per-turn" indicator.
//
// The handler reuses the same OAuth fetcher and Codex snapshot reader
// that power /usage so the calibrated-percentage path here lines up
// with whatever the dashboard's badge is showing.
type SessionUsageHandler struct {
	db        *store.DB
	oauth     *usage.OAuthFetcher
	codexSnap *usage.CodexSnapshotReader
	logger    *slog.Logger
	// nowFn is the clock source. Production uses time.Now; tests inject
	// a fixed clock so test outputs are deterministic.
	nowFn func() time.Time
}

// SessionUsageDeps groups the constructor inputs. We keep the OAuth
// fetcher and codex snapshot reader injectable so tests can supply
// stubs and so the production wiring can share instances with the
// existing /usage handler.
type SessionUsageDeps struct {
	DB        *store.DB
	OAuth     *usage.OAuthFetcher
	CodexSnap *usage.CodexSnapshotReader
	Logger    *slog.Logger
	NowFn     func() time.Time
}

// NewSessionUsageHandler constructs a SessionUsageHandler. When NowFn is
// nil it defaults to time.Now. When OAuth/CodexSnap are nil the handler
// falls back to the uncalibrated path (all *Pct5h fields = -1).
func NewSessionUsageHandler(deps SessionUsageDeps) *SessionUsageHandler {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := deps.NowFn
	if now == nil {
		now = time.Now
	}
	return &SessionUsageHandler{
		db:        deps.DB,
		oauth:     deps.OAuth,
		codexSnap: deps.CodexSnap,
		logger:    logger,
		nowFn:     now,
	}
}

// Get serves GET /sessions/{id}/usage.
func (h *SessionUsageHandler) Get(w http.ResponseWriter, r *http.Request) {
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
		h.logger.Error("session-usage: get session", slog.String("id", id), slog.Any("error", err))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Walk the most-recent N messages (newest first) looking for the latest
	// assistant turn with a non-zero tokens_in. That count is the closest
	// empirical signal to "what did the CLI actually pack into context for
	// the last request" — it is the number of tokens the next turn will
	// re-send. User messages don't carry input-token totals (the CLI bills
	// the assistant turn); tool-result rows do, but their counts are
	// per-call rather than for the full prompt.
	msgs, err := store.ListMessagesBySessionFiltered(ctx, h.db, id, usageMessageScanLimit, 0, "desc", store.MessageFilter{})
	if err != nil {
		h.logger.Error("session-usage: list messages", slog.String("id", id), slog.Any("error", err))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var (
		contextUsed int64
		modelForRow string
	)
	for _, m := range msgs {
		if m.Role == connectors.RoleAssistant && m.TokensIn > 0 {
			contextUsed = m.TokensIn
			modelForRow = m.Model
			break
		}
	}

	// Resolve the context-window size. We prefer the model on the
	// most-recent assistant message (most accurate); fall back to the
	// session-level model column. Sessions that have never seen an
	// assistant turn return ContextWindow=0, ContextFillPct=0.
	model := modelForRow
	if model == "" {
		model = session.Model
	}
	contextWindow := usage.ContextWindowForModel(model)

	// Codex records per-turn input/context-window data in token_count
	// event_msg rows, not on assistant messages. The store only persists
	// token deltas for aggregate accounting, so read the exact session JSONL
	// to recover the latest last_token_usage snapshot for the UI.
	if session.CLI == connectors.CLICodex {
		if snap, ok, snapErr := usage.LatestCodexContextSnapshot(session.RawPath); snapErr != nil {
			h.logger.Debug("session-usage: codex context snapshot unavailable",
				slog.String("id", id), slog.Any("error", snapErr))
		} else if ok {
			if snap.LastInputTokens > 0 {
				contextUsed = snap.LastInputTokens
			}
			if snap.ModelContextWindow > 0 {
				contextWindow = snap.ModelContextWindow
			}
		}
	}

	var contextFillPct float64
	if contextWindow > 0 {
		contextFillPct = float64(contextUsed) / float64(contextWindow) * 100.0
	}

	resp := api.SessionUsageResponse{
		SessionID:              id,
		Model:                  model,
		ContextWindow:          contextWindow,
		ContextUsed:            contextUsed,
		ContextFillPct:         contextFillPct,
		CompactRatio:           CompactRatio,
		NextTurnPct5h:          uncalibrated,
		CompactedNextTurnPct5h: uncalibrated,
		RestartedNextTurnPct5h: uncalibrated,
		CompactSavingsPct5h:    uncalibrated,
		RestartSavingsPct5h:    uncalibrated,
		CalibratedFromOAuth:    false,
	}

	// Calibrate the 5h-limit cost. We need both:
	//   1. A vendor-canonical utilization percentage for the 5h window.
	//   2. The locally-summed token count for that same window.
	// Their ratio gives us "tokens per 1% of the 5h cap".
	tokensPerPct, calibrated := h.calibrate(ctx, session.CLI)
	if calibrated {
		nextTurn := contextUsed
		if nextTurn <= 0 {
			// No assistant turn yet — model the next request as the
			// system-prompt floor.
			nextTurn = SystemPromptFloor
		}
		resp.NextTurnPct5h = float64(nextTurn) / tokensPerPct
		resp.CompactedNextTurnPct5h = float64(nextTurn) * CompactRatio / tokensPerPct
		resp.RestartedNextTurnPct5h = float64(SystemPromptFloor) / tokensPerPct
		resp.CompactSavingsPct5h = resp.NextTurnPct5h - resp.CompactedNextTurnPct5h
		resp.RestartSavingsPct5h = resp.NextTurnPct5h - resp.RestartedNextTurnPct5h
		resp.CalibratedFromOAuth = true
	}

	writeJSON(w, http.StatusOK, resp)
}

// calibrate returns (tokensPerPct, true) when the handler can map raw
// token counts to "% of the 5h limit", and (0, false) otherwise.
//
// The math: usage.Compute reports the locally-summed token count inside
// the rolling 5h window, AND the vendor's percent-utilization for that
// same window. Their ratio is the per-percent cost. We use it to project
// what the next turn will burn.
//
// The calibration is best-effort. Any failure (compute error, no OAuth
// data, zero utilization, zero tokens) returns false — the handler then
// emits the "uncalibrated" sentinel for every Pct5h field.
func (h *SessionUsageHandler) calibrate(ctx context.Context, cli connectors.CLI) (tokensPerPct float64, ok bool) {
	if h.db == nil {
		return 0, false
	}
	resp, err := usage.Compute(ctx, h.db, h.nowFn(), h.oauth, h.codexSnap)
	if err != nil {
		h.logger.Debug("session-usage: compute failed; falling back to uncalibrated",
			slog.Any("error", err))
		return 0, false
	}

	switch cli {
	case connectors.CLIClaude:
		return calibrateFromCLI(resp.Claude)
	case connectors.CLICodex:
		return calibrateFromCLI(resp.Codex)
	default:
		return 0, false
	}
}

// calibrateFromCLI extracts the (tokens, utilization%) pair from a
// single CLI's UsageCLI block and divides them. Returns (0, false) when
// either input is non-positive.
func calibrateFromCLI(cli api.UsageCLI) (float64, bool) {
	if cli.OAuth == nil || cli.OAuth.FiveHour == nil {
		return 0, false
	}
	pct := cli.OAuth.FiveHour.UtilizationPct
	if pct <= 0 {
		return 0, false
	}
	tokens := cli.Window5h.Tokens
	if tokens <= 0 {
		return 0, false
	}
	return float64(tokens) / pct, true
}

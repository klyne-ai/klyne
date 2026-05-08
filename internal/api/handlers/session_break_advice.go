package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/klyne-ai/klyne/internal/ai"
	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/store"
)

// breakAdviceCacheTTL is the per-session cache lifetime. The advice is
// not time-critical and the underlying conversation only meaningfully
// changes every few minutes; 10 minutes is plenty of "free re-clicks"
// while still letting the user see fresh advice after a long pause.
const breakAdviceCacheTTL = 10 * time.Minute

// breakAdviceMessageWindow is how many of the most-recent messages we
// hand to the AI. Small enough to keep input cost trivial, large enough
// that the model sees the current topic plus its preceding turn.
const breakAdviceMessageWindow = 10

// breakAdviceContentTruncate caps each message's character count before
// we hand it to the model. Tool-result blocks can be enormous; keeping
// per-message size bounded protects the token budget.
const breakAdviceContentTruncate = 1000

// breakAdviceMaxTokens caps the model's reply. The expected JSON response
// is well under 100 tokens; 200 leaves headroom for verbose models without
// risking runaway output cost.
const breakAdviceMaxTokens = 200

// breakAdviceSystemPrompt is the entire instruction surface. Kept terse
// because every token costs money on this call. The model is asked to
// reply in a strict JSON shape so we can parse it without an LLM-side
// schema validator.
const breakAdviceSystemPrompt = `You analyze a Claude Code / Codex CLI session and recommend whether the user should:
  - "start_fresh": their last topic ended; a new session for the next task would save tokens
  - "compact":     they're mid-task; compacting now preserves continuity but kills context bloat
  - "continue":    too early to break, just keep going

Reply with EXACTLY this JSON: {"verdict":"start_fresh|compact|continue","reason":"<one sentence>","topic":"<short label or empty>"}
No markdown, no extra fields, no preamble.`

// breakAdviceFallbackReason is what we return when the AI replies but we
// cannot parse its JSON. Falling back to "continue" is the safest verdict
// because it never wrongly tells the user to break a productive flow.
const breakAdviceFallbackReason = "could not analyze — keep going"

// breakAdviceCallTimeout caps how long we wait for the AI provider. If
// the network is flapping or the upstream model is slow we'd rather
// surface a degraded "continue" than hang the request.
const breakAdviceCallTimeout = 8 * time.Second

// AIProviderFactory builds an ai.Provider on demand. Returning ok=false
// is the convention for "no provider was configured / detected at app
// boot"; the handler then returns the "unavailable" verdict without
// hitting the network.
//
// We use a factory rather than a captured *ai.Provider so wiring can
// live in one place (app.go) and tests can inject deterministic fakes
// without going through the real provider constructors.
type AIProviderFactory func() (provider ai.Provider, model string, ok bool)

// BreakAdviceDeps groups the constructor inputs.
type BreakAdviceDeps struct {
	DB     *store.DB
	Logger *slog.Logger
	// AIFactory returns (provider, model, true) when an AI provider is
	// available, or (nil, "", false) when none is configured. The
	// handler treats the latter as "unavailable verdict" and skips the
	// call entirely.
	AIFactory AIProviderFactory
	// NowFn is the clock source. Tests inject a fixed clock so cache
	// expiry is deterministic.
	NowFn func() time.Time
}

// breakAdviceCacheEntry is one row in the in-memory cache.
type breakAdviceCacheEntry struct {
	resp     api.BreakAdviceResponse
	storedAt time.Time
}

// BreakAdviceHandler serves GET /sessions/{id}/break-advice. It calls a
// small Haiku-class model with the last ~10 messages and asks whether
// the user should compact, restart, or keep going. Results are cached
// in-memory per session for breakAdviceCacheTTL.
type BreakAdviceHandler struct {
	db        *store.DB
	logger    *slog.Logger
	aiFactory AIProviderFactory
	nowFn     func() time.Time

	mu    sync.Mutex
	cache map[string]breakAdviceCacheEntry
}

// NewBreakAdviceHandler constructs a BreakAdviceHandler.
func NewBreakAdviceHandler(deps BreakAdviceDeps) *BreakAdviceHandler {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := deps.NowFn
	if now == nil {
		now = time.Now
	}
	return &BreakAdviceHandler{
		db:        deps.DB,
		logger:    logger,
		aiFactory: deps.AIFactory,
		nowFn:     now,
		cache:     make(map[string]breakAdviceCacheEntry),
	}
}

// Get serves GET /sessions/{id}/break-advice.
func (h *BreakAdviceHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	// 404 first — saves us an AI call when the path is just bogus.
	if _, err := store.GetSession(ctx, h.db, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
		h.logger.Error("break-advice: get session", slog.String("id", id), slog.Any("error", err))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Cache check — hits return immediately, including the "unavailable"
	// verdict so we don't re-detect on every click.
	if cached, ok := h.lookup(id); ok {
		writeJSON(w, http.StatusOK, cached)
		return
	}

	// No provider configured → "unavailable" verdict, cached just like
	// any other answer so the next click is also cheap.
	if h.aiFactory == nil {
		resp := h.unavailableResponse(id)
		h.store(id, resp)
		writeJSON(w, http.StatusOK, resp)
		return
	}
	provider, model, ok := h.aiFactory()
	if !ok || provider == nil {
		resp := h.unavailableResponse(id)
		h.store(id, resp)
		writeJSON(w, http.StatusOK, resp)
		return
	}

	// Pull the last N messages, oldest-first for the model.
	tail, err := store.ListMessagesBySessionFiltered(ctx, h.db, id, breakAdviceMessageWindow, 0, "desc", store.MessageFilter{})
	if err != nil {
		h.logger.Error("break-advice: list messages",
			slog.String("id", id), slog.Any("error", err))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Reverse to chronological so the model reads the conversation in
	// the order the user lived it.
	reverseMessages(tail)

	formatted := formatMessagesForAdvice(tail)

	callCtx, cancel := context.WithTimeout(ctx, breakAdviceCallTimeout)
	defer cancel()

	chatResp, err := provider.Chat(callCtx, ai.ChatRequest{
		Model: model,
		Messages: []ai.Message{{
			Role:    "user",
			Content: "Last messages (oldest first):\n\n" + formatted,
		}},
		MaxTokens:    breakAdviceMaxTokens,
		Temperature:  0.0,
		SystemPrompt: breakAdviceSystemPrompt,
	})
	if err != nil {
		// Network blip / no credential / transport failure — surface as
		// "continue" so the UI stays usable. We don't cache the error
		// path so the next click can retry against a (hopefully)
		// recovered provider.
		h.logger.Warn("break-advice: provider chat failed",
			slog.String("id", id),
			slog.String("provider", provider.Name()),
			slog.Any("error", err))
		resp := api.BreakAdviceResponse{
			SessionID: id,
			Verdict:   api.BreakAdviceContinue,
			Reason:    breakAdviceFallbackReason,
			Provider:  provider.Name(),
			Model:     model,
			CachedAt:  h.nowFn().UnixMilli(),
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}

	resp := parseAdviceResponse(chatResp.Text, id, provider.Name(), model, h.nowFn().UnixMilli())
	h.store(id, resp)
	writeJSON(w, http.StatusOK, resp)
}

// unavailableResponse builds the "no provider configured" response.
func (h *BreakAdviceHandler) unavailableResponse(id string) api.BreakAdviceResponse {
	return api.BreakAdviceResponse{
		SessionID: id,
		Verdict:   api.BreakAdviceUnavailable,
		Reason:    "No AI provider configured. Set ANTHROPIC_API_KEY, OPENAI_API_KEY, or GEMINI_API_KEY to enable advice.",
		CachedAt:  h.nowFn().UnixMilli(),
	}
}

// lookup returns the cached response for id when fresh.
func (h *BreakAdviceHandler) lookup(id string) (api.BreakAdviceResponse, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	entry, ok := h.cache[id]
	if !ok {
		return api.BreakAdviceResponse{}, false
	}
	if h.nowFn().Sub(entry.storedAt) > breakAdviceCacheTTL {
		delete(h.cache, id)
		return api.BreakAdviceResponse{}, false
	}
	return entry.resp, true
}

// store puts resp in the cache.
func (h *BreakAdviceHandler) store(id string, resp api.BreakAdviceResponse) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cache[id] = breakAdviceCacheEntry{resp: resp, storedAt: h.nowFn()}
}

// reverseMessages flips the "newest-first" DB result into chronological
// order in place.
func reverseMessages(xs []*connectors.Message) {
	for i, j := 0, len(xs)-1; i < j; i, j = i+1, j-1 {
		xs[i], xs[j] = xs[j], xs[i]
	}
}

// formatMessagesForAdvice renders each message as `[role] content`,
// truncating content to breakAdviceContentTruncate characters so a
// single tool-output dump can't blow the token budget.
func formatMessagesForAdvice(msgs []*connectors.Message) string {
	parts := make([]string, 0, len(msgs))
	for _, m := range msgs {
		body := m.Content
		if len(body) > breakAdviceContentTruncate {
			body = body[:breakAdviceContentTruncate] + "…"
		}
		parts = append(parts, "["+string(m.Role)+"] "+body)
	}
	return strings.Join(parts, "\n\n")
}

// adviceJSONShape mirrors the strict JSON the system prompt asks for.
type adviceJSONShape struct {
	Verdict string `json:"verdict"`
	Reason  string `json:"reason"`
	Topic   string `json:"topic"`
}

// parseAdviceResponse turns the model's text into a BreakAdviceResponse.
// Malformed / non-conforming text degrades to the "continue" verdict so
// the user never sees an error; the handler logs the raw text for
// debugging without surfacing it to the client.
func parseAdviceResponse(text, sessionID, providerName, model string, nowMs int64) api.BreakAdviceResponse {
	resp := api.BreakAdviceResponse{
		SessionID: sessionID,
		Provider:  providerName,
		Model:     model,
		CachedAt:  nowMs,
	}

	body := extractJSONObject(text)
	var parsed adviceJSONShape
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		resp.Verdict = api.BreakAdviceContinue
		resp.Reason = breakAdviceFallbackReason
		return resp
	}

	switch api.BreakAdviceVerdict(parsed.Verdict) {
	case api.BreakAdviceStartFresh:
		resp.Verdict = api.BreakAdviceStartFresh
	case api.BreakAdviceCompact:
		resp.Verdict = api.BreakAdviceCompact
	case api.BreakAdviceContinue:
		resp.Verdict = api.BreakAdviceContinue
	default:
		resp.Verdict = api.BreakAdviceContinue
		resp.Reason = breakAdviceFallbackReason
		return resp
	}

	resp.Reason = strings.TrimSpace(parsed.Reason)
	if resp.Reason == "" {
		resp.Reason = breakAdviceFallbackReason
	}
	if resp.Verdict == api.BreakAdviceStartFresh {
		resp.SuggestedTopic = strings.TrimSpace(parsed.Topic)
	}
	return resp
}

// extractJSONObject pulls the outermost {...} substring out of text,
// tolerating providers that wrap their reply in markdown fences or
// chatty preamble. Returns the original string when no braces are found
// (the caller's json.Unmarshal will fail and we degrade gracefully).
func extractJSONObject(s string) string {
	first := strings.IndexByte(s, '{')
	last := strings.LastIndexByte(s, '}')
	if first < 0 || last <= first {
		return s
	}
	return s[first : last+1]
}

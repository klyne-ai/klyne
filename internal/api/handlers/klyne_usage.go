package handlers

import (
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/store"
)

// KlyneUsageHandler serves GET /api/klyne-usage?day=YYYY-MM-DD.
//
// Renders the day's klyne-subprocess token totals (productivity-sync +
// reflect runs) alongside the user's full-day Claude usage so the
// productivity dashboard's "Klyne is X% of today's Claude usage" tile
// can compute the share locally. Tokens only — never USD; see the
// migration 024 header for the rationale.
type KlyneUsageHandler struct {
	db *store.DB
}

// NewKlyneUsageHandler constructs the read-only handler. No external
// dependencies — pure DB query.
func NewKlyneUsageHandler(db *store.DB) *KlyneUsageHandler {
	return &KlyneUsageHandler{db: db}
}

// Get handles GET /api/klyne-usage?day=YYYY-MM-DD. Day defaults to
// today (server-local) when the query param is empty.
func (h *KlyneUsageHandler) Get(w http.ResponseWriter, r *http.Request) {
	day := strings.TrimSpace(r.URL.Query().Get("day"))
	if day == "" {
		day = time.Now().Local().Format("2006-01-02")
	}
	t, err := time.ParseInLocation("2006-01-02", day, time.Local)
	if err != nil {
		http.Error(w, "invalid day: must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	startMs := t.UnixMilli()
	endMs := t.AddDate(0, 0, 1).UnixMilli()

	klyneAgg, err := store.AggregateKlyneLLMUsageForDay(r.Context(), h.db, day)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	userTotals, err := store.GetDailyUserTokenTotals(r.Context(), h.db, day, startMs, endMs)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// SharePct: klyne's total / user's total, rounded to one decimal.
	// Guard the divide-by-zero — first day or no traffic yet → 0.
	var share float64
	if userTotals.TotalTokens > 0 {
		share = float64(klyneAgg.TotalTokens) / float64(userTotals.TotalTokens) * 100.0
		share = math.Round(share*10) / 10
	}

	writeJSON(w, http.StatusOK, api.KlyneUsageResponse{
		Day:       day,
		Klyne:     klyneAgg,
		UserTotal: userTotals,
		SharePct:  share,
	})
}

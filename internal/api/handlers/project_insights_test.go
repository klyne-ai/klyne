package handlers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/api/handlers"
	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/store"
)

// newInsightsRouter wires /insights/projects on a fresh chi router.
// Follows the same pattern as newCostRouter / newMemoryRouter.
func newInsightsRouter(t *testing.T, db *store.DB) http.Handler {
	t.Helper()
	r := chi.NewRouter()
	h := handlers.NewProjectInsightsHandler(db)
	r.Get(api.RouteInsightsProjects, h.Get)
	return r
}

// insightsSeedSess describes a session to upsert and the messages to
// insert for it. The session row is created with zero-valued aggregate
// counters; the InsertMessage calls then drive tokens_in / tokens_out /
// cached_read / msg_count / last_msg_at to the correct sums — exactly
// how the real ingestion pipeline behaves.
type insightsSeedSess struct {
	ID          string
	CLI         connectors.CLI
	ProjectPath string
	Model       string
	Msgs        []insightsSeedMsg
	// Compacts is the number of /compact events to insert for this
	// session. All are timestamped slightly after the last message
	// (within 1ms each) so they fall in the same window as the
	// session's activity.
	Compacts int
}

type insightsSeedMsg struct {
	Ts         int64
	TokensIn   int64
	TokensOut  int64
	CachedRead int64
}

func seedInsights(t *testing.T, db *store.DB, rows []insightsSeedSess) {
	t.Helper()
	ctx := context.Background()
	for _, r := range rows {
		// Upsert the session with zero aggregates; messages will drive
		// the totals.
		s := &connectors.Session{
			ID:          r.ID,
			CLI:         r.CLI,
			ProjectPath: r.ProjectPath,
			StartedAt:   0,
			LastMsgAt:   0,
			Model:       r.Model,
			Status:      connectors.SessionStatusIdle,
		}
		if err := store.UpsertSession(ctx, db, s); err != nil {
			t.Fatalf("upsert session %q: %v", r.ID, err)
		}
		var lastTs int64
		for i, mm := range r.Msgs {
			m := &connectors.Message{
				ID:               fmt.Sprintf("%s-m%d", r.ID, i),
				SessionID:        r.ID,
				CLI:              r.CLI,
				Role:             connectors.RoleAssistant,
				Content:          "x",
				Model:            r.Model,
				Ts:               mm.Ts,
				TokensIn:         mm.TokensIn,
				TokensOut:        mm.TokensOut,
				CachedReadTokens: mm.CachedRead,
			}
			if err := store.InsertMessage(ctx, db, m); err != nil {
				t.Fatalf("insert message %q: %v", m.ID, err)
			}
			if mm.Ts > lastTs {
				lastTs = mm.Ts
			}
		}
		for i := 0; i < r.Compacts; i++ {
			_, err := db.Write().ExecContext(ctx, `
				INSERT INTO compact_events (session_id, ts, before_token_count, after_token_count)
				VALUES (?, ?, ?, ?)`,
				r.ID, lastTs+int64(i), 10_000, 1_500)
			if err != nil {
				t.Fatalf("insert compact_event: %v", err)
			}
		}
	}
}

// oneMsg is a one-liner helper for tests that want a single-message
// session with a specific ts and tokens.
func oneMsg(ts, tokensIn, tokensOut, cachedRead int64) []insightsSeedMsg {
	return []insightsSeedMsg{{Ts: ts, TokensIn: tokensIn, TokensOut: tokensOut, CachedRead: cachedRead}}
}

// decodeInsights decodes a /insights/projects response or fatals.
func decodeInsights(t *testing.T, resp *http.Response) api.ProjectInsightsResponse {
	t.Helper()
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body api.ProjectInsightsResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body
}

// Fixed timestamps so window math is unambiguous. "now" is the test's
// reference clock; the windows below are anchored relative to it.
const (
	insightsNow     int64 = 1_700_000_000_000 // arbitrary fixed epoch-ms
	insightsDayMs   int64 = 86_400_000
	insightsWinDays       = 7
)

// dAgo returns the epoch-ms N days before insightsNow.
func dAgo(days int) int64 { return insightsNow - int64(days)*insightsDayMs }

func TestProjectInsights_Empty(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)

	srv := httptest.NewServer(newInsightsRouter(t, db))
	t.Cleanup(srv.Close)

	url := fmt.Sprintf("%s%s?since=%d&until=%d",
		srv.URL, api.RouteInsightsProjects, dAgo(insightsWinDays), insightsNow)
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body := decodeInsights(t, resp)

	if len(body.Projects) != 0 {
		t.Errorf("expected 0 projects, got %d", len(body.Projects))
	}
	if body.Totals.Tokens != 0 {
		t.Errorf("expected zero totals tokens, got %d", body.Totals.Tokens)
	}
}

func TestProjectInsights_HappyPath_SortedByTokens(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	// Project A: 1 session, 1 message, 1000 tokens total.
	// Project B: 1 session, 1 message, 300 tokens total.
	seedInsights(t, db, []insightsSeedSess{
		{
			ID: "s-a-1", CLI: connectors.CLIClaude, ProjectPath: "/proj/a",
			Model: "claude-sonnet-4-6",
			Msgs:  oneMsg(dAgo(1), 600, 400, 200),
		},
		{
			ID: "s-b-1", CLI: connectors.CLICodex, ProjectPath: "/proj/b",
			Model: "gpt-5",
			Msgs:  oneMsg(dAgo(2), 200, 100, 0),
		},
	})

	srv := httptest.NewServer(newInsightsRouter(t, db))
	t.Cleanup(srv.Close)

	url := fmt.Sprintf("%s%s?since=%d&until=%d",
		srv.URL, api.RouteInsightsProjects, dAgo(insightsWinDays), insightsNow)
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body := decodeInsights(t, resp)

	if len(body.Projects) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(body.Projects))
	}
	if body.Projects[0].ProjectPath != "/proj/a" {
		t.Errorf("expected /proj/a first (heavier), got %q", body.Projects[0].ProjectPath)
	}
	if body.Projects[0].Name != "a" {
		t.Errorf("expected Name=a, got %q", body.Projects[0].Name)
	}
	if body.Projects[0].Tokens != 1000 {
		t.Errorf("proj/a Tokens = %d, want 1000", body.Projects[0].Tokens)
	}
	if body.Totals.Tokens != 1300 {
		t.Errorf("Totals.Tokens = %d, want 1300", body.Totals.Tokens)
	}
	if body.Totals.Sessions != 2 {
		t.Errorf("Totals.Sessions = %d, want 2", body.Totals.Sessions)
	}
}

func TestProjectInsights_AgentSplit(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	// One project, two sessions — one Claude, one Codex. Verify the
	// per-agent breakdown sums match.
	seedInsights(t, db, []insightsSeedSess{
		{
			ID: "s-1", CLI: connectors.CLIClaude, ProjectPath: "/proj/x",
			Msgs: oneMsg(dAgo(1), 500, 500, 0),
		},
		{
			ID: "s-2", CLI: connectors.CLICodex, ProjectPath: "/proj/x",
			Msgs: oneMsg(dAgo(2), 100, 300, 0),
		},
	})

	srv := httptest.NewServer(newInsightsRouter(t, db))
	t.Cleanup(srv.Close)

	url := fmt.Sprintf("%s%s?since=%d&until=%d",
		srv.URL, api.RouteInsightsProjects, dAgo(insightsWinDays), insightsNow)
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body := decodeInsights(t, resp)
	if len(body.Projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(body.Projects))
	}
	p := body.Projects[0]
	if p.Claude.Tokens != 1000 {
		t.Errorf("Claude.Tokens = %d, want 1000", p.Claude.Tokens)
	}
	if p.Codex.Tokens != 400 {
		t.Errorf("Codex.Tokens = %d, want 400", p.Codex.Tokens)
	}
	if p.Claude.Sessions != 1 || p.Codex.Sessions != 1 {
		t.Errorf("expected 1 session per agent, got claude=%d codex=%d", p.Claude.Sessions, p.Codex.Sessions)
	}
}

func TestProjectInsights_CacheAndEfficiency(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	// 5 messages each contributing tokens_in=160, tokens_out=40, cached_read=80.
	// Totals: tokens_in=800, tokens_out=200, cached=400, msgs=5.
	// CacheHitPct = 400/800*100 = 50; TokensPerMessage = 1000/5 = 200.
	msgs := make([]insightsSeedMsg, 5)
	for i := range msgs {
		msgs[i] = insightsSeedMsg{Ts: dAgo(1) + int64(i), TokensIn: 160, TokensOut: 40, CachedRead: 80}
	}
	seedInsights(t, db, []insightsSeedSess{
		{ID: "s-1", CLI: connectors.CLIClaude, ProjectPath: "/proj/x", Msgs: msgs},
	})
	srv := httptest.NewServer(newInsightsRouter(t, db))
	t.Cleanup(srv.Close)

	url := fmt.Sprintf("%s%s?since=%d&until=%d",
		srv.URL, api.RouteInsightsProjects, dAgo(insightsWinDays), insightsNow)
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body := decodeInsights(t, resp)
	p := body.Projects[0]
	if p.CacheHitPct < 49.99 || p.CacheHitPct > 50.01 {
		t.Errorf("CacheHitPct = %v, want ~50", p.CacheHitPct)
	}
	if p.TokensPerMessage < 199.99 || p.TokensPerMessage > 200.01 {
		t.Errorf("TokensPerMessage = %v, want ~200", p.TokensPerMessage)
	}
	if p.Messages != 5 {
		t.Errorf("Messages = %d, want 5", p.Messages)
	}
}

func TestProjectInsights_CompactCount(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	seedInsights(t, db, []insightsSeedSess{
		{
			ID: "s-1", CLI: connectors.CLIClaude, ProjectPath: "/proj/x",
			Msgs: oneMsg(dAgo(1), 100, 100, 0), Compacts: 3,
		},
		{
			ID: "s-2", CLI: connectors.CLIClaude, ProjectPath: "/proj/x",
			Msgs: oneMsg(dAgo(2), 50, 50, 0), Compacts: 1,
		},
		// Different project — its compacts should NOT show under /proj/x.
		{
			ID: "s-3", CLI: connectors.CLIClaude, ProjectPath: "/proj/y",
			Msgs: oneMsg(dAgo(1), 10, 10, 0), Compacts: 2,
		},
	})
	srv := httptest.NewServer(newInsightsRouter(t, db))
	t.Cleanup(srv.Close)

	url := fmt.Sprintf("%s%s?since=%d&until=%d",
		srv.URL, api.RouteInsightsProjects, dAgo(insightsWinDays), insightsNow)
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body := decodeInsights(t, resp)

	var x, y *api.ProjectInsight
	for i := range body.Projects {
		switch body.Projects[i].ProjectPath {
		case "/proj/x":
			x = &body.Projects[i]
		case "/proj/y":
			y = &body.Projects[i]
		}
	}
	if x == nil || y == nil {
		t.Fatalf("missing one of the projects: x=%v y=%v", x, y)
	}
	if x.CompactCount != 4 {
		t.Errorf("/proj/x CompactCount = %d, want 4", x.CompactCount)
	}
	if y.CompactCount != 2 {
		t.Errorf("/proj/y CompactCount = %d, want 2", y.CompactCount)
	}
}

func TestProjectInsights_TopSessions(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	// 5 sessions in one project, distinct token counts so the top-3
	// ordering is unambiguous. Each session has one message carrying
	// the full token allotment.
	rows := []insightsSeedSess{}
	for i, tokens := range []int64{100, 500, 300, 700, 200} {
		rows = append(rows, insightsSeedSess{
			ID: fmt.Sprintf("s-%d", i), CLI: connectors.CLIClaude,
			ProjectPath: "/proj/x", Model: "claude-sonnet-4-6",
			Msgs: oneMsg(dAgo(1)+int64(i), tokens, tokens/2, 0),
		})
	}
	seedInsights(t, db, rows)
	srv := httptest.NewServer(newInsightsRouter(t, db))
	t.Cleanup(srv.Close)

	url := fmt.Sprintf("%s%s?since=%d&until=%d&top=3",
		srv.URL, api.RouteInsightsProjects, dAgo(insightsWinDays), insightsNow)
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body := decodeInsights(t, resp)
	if len(body.Projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(body.Projects))
	}
	top := body.Projects[0].TopSessions
	if len(top) != 3 {
		t.Fatalf("expected top=3, got %d", len(top))
	}
	// Expected order: 700+350=1050, 500+250=750, 300+150=450.
	wantOrder := []int64{1050, 750, 450}
	for i, want := range wantOrder {
		if top[i].Tokens != want {
			t.Errorf("TopSessions[%d].Tokens = %d, want %d", i, top[i].Tokens, want)
		}
	}
}

func TestProjectInsights_TrendVsPrior(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	// Current window (last 7d): 1000 tokens.
	// Prior window (previous 7d): 500 tokens.
	// Expected trend = +100%.
	seedInsights(t, db, []insightsSeedSess{
		{
			ID: "s-cur", CLI: connectors.CLIClaude, ProjectPath: "/proj/x",
			Msgs: oneMsg(dAgo(1), 600, 400, 0),
		},
		{
			ID: "s-prior", CLI: connectors.CLIClaude, ProjectPath: "/proj/x",
			Msgs: oneMsg(dAgo(10), 300, 200, 0),
		},
	})
	srv := httptest.NewServer(newInsightsRouter(t, db))
	t.Cleanup(srv.Close)

	url := fmt.Sprintf("%s%s?since=%d&until=%d",
		srv.URL, api.RouteInsightsProjects, dAgo(insightsWinDays), insightsNow)
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body := decodeInsights(t, resp)
	if len(body.Projects) != 1 {
		t.Fatalf("expected 1 project (prior-only doesn't surface), got %d", len(body.Projects))
	}
	p := body.Projects[0]
	if p.Tokens != 1000 {
		t.Errorf("Tokens = %d, want 1000", p.Tokens)
	}
	if p.PriorTokens != 500 {
		t.Errorf("PriorTokens = %d, want 500", p.PriorTokens)
	}
	if p.TrendPct < 99.9 || p.TrendPct > 100.1 {
		t.Errorf("TrendPct = %v, want ~100", p.TrendPct)
	}
}

func TestProjectInsights_TrendUnbounded_NoPrior(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	seedInsights(t, db, []insightsSeedSess{
		{
			ID: "s-1", CLI: connectors.CLIClaude, ProjectPath: "/proj/x",
			Msgs: oneMsg(dAgo(1), 100, 100, 0),
		},
	})
	srv := httptest.NewServer(newInsightsRouter(t, db))
	t.Cleanup(srv.Close)

	// since=0 means "all time" — there is no prior window to compare
	// against, so PriorTokens and TrendPct must be zero.
	resp, err := http.Get(srv.URL + api.RouteInsightsProjects + "?since=0")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body := decodeInsights(t, resp)
	if len(body.Projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(body.Projects))
	}
	if body.Projects[0].PriorTokens != 0 {
		t.Errorf("PriorTokens = %d, want 0 with since=0", body.Projects[0].PriorTokens)
	}
	if body.Projects[0].TrendPct != 0 {
		t.Errorf("TrendPct = %v, want 0 with since=0", body.Projects[0].TrendPct)
	}
}

func TestProjectInsights_DailySparkline(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	// Three messages on three distinct days, same project.
	seedInsights(t, db, []insightsSeedSess{
		{
			ID: "s-1", CLI: connectors.CLIClaude, ProjectPath: "/proj/x",
			Msgs: []insightsSeedMsg{
				{Ts: dAgo(3), TokensIn: 50, TokensOut: 50},
				{Ts: dAgo(2), TokensIn: 50, TokensOut: 50},
				{Ts: dAgo(1), TokensIn: 50, TokensOut: 50},
			},
		},
	})
	srv := httptest.NewServer(newInsightsRouter(t, db))
	t.Cleanup(srv.Close)

	url := fmt.Sprintf("%s%s?since=%d&until=%d",
		srv.URL, api.RouteInsightsProjects, dAgo(insightsWinDays), insightsNow)
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body := decodeInsights(t, resp)
	p := body.Projects[0]
	if len(p.Daily) == 0 {
		t.Fatalf("expected daily points, got none")
	}
	// Days should be ordered oldest -> newest so the UI can plot left-to-right.
	for i := 1; i < len(p.Daily); i++ {
		if p.Daily[i].Day < p.Daily[i-1].Day {
			t.Errorf("daily not sorted ascending at i=%d (%s < %s)", i, p.Daily[i].Day, p.Daily[i-1].Day)
		}
	}
	// Day strings must look like YYYY-MM-DD.
	for _, d := range p.Daily {
		if len(d.Day) != 10 || strings.Count(d.Day, "-") != 2 {
			t.Errorf("malformed day %q, want YYYY-MM-DD", d.Day)
		}
	}
}

func TestProjectInsights_InvalidParams(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	srv := httptest.NewServer(newInsightsRouter(t, db))
	t.Cleanup(srv.Close)

	cases := []struct {
		name string
		q    string
	}{
		{"non-numeric since", "?since=abc"},
		{"negative since", "?since=-1"},
		{"non-numeric until", "?until=abc"},
		{"negative until", "?until=-1"},
		{"non-numeric top", "?top=abc"},
		{"negative top", "?top=-1"},
		{"top too large", "?top=" + strconv.Itoa(1000)},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			resp, err := http.Get(srv.URL + api.RouteInsightsProjects + tc.q)
			if err != nil {
				t.Fatalf("GET: %v", err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("expected 400 for %s, got %d", tc.name, resp.StatusCode)
			}
		})
	}
}

func TestProjectInsights_TopDefaultAndZero(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	rows := []insightsSeedSess{}
	for i := 0; i < 5; i++ {
		rows = append(rows, insightsSeedSess{
			ID: fmt.Sprintf("s-%d", i), CLI: connectors.CLIClaude,
			ProjectPath: "/proj/x",
			Msgs:        oneMsg(dAgo(1)+int64(i), int64(100+i*10), 0, 0),
		})
	}
	seedInsights(t, db, rows)
	srv := httptest.NewServer(newInsightsRouter(t, db))
	t.Cleanup(srv.Close)

	winBase := fmt.Sprintf("?since=%d&until=%d", dAgo(insightsWinDays), insightsNow)

	// Default (no top param) should yield 3.
	resp, err := http.Get(srv.URL + api.RouteInsightsProjects + winBase)
	if err != nil {
		t.Fatalf("GET default: %v", err)
	}
	body := decodeInsights(t, resp)
	if len(body.Projects[0].TopSessions) != 3 {
		t.Errorf("default TopSessions len = %d, want 3", len(body.Projects[0].TopSessions))
	}

	// top=0 disables drill-down entirely.
	resp, err = http.Get(srv.URL + api.RouteInsightsProjects + winBase + "&top=0")
	if err != nil {
		t.Fatalf("GET top=0: %v", err)
	}
	body = decodeInsights(t, resp)
	if len(body.Projects[0].TopSessions) != 0 {
		t.Errorf("top=0 TopSessions len = %d, want 0", len(body.Projects[0].TopSessions))
	}
}

package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/api/handlers"
	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/store"
)

func newAdvisoriesRouter(t *testing.T, db *store.DB) http.Handler {
	t.Helper()
	r := chi.NewRouter()
	h := handlers.NewAdvisoriesHandler(db)
	r.Get(api.RouteAdvisories, h.List)
	return r
}

// openTestDB opens a fresh on-disk SQLite under t.TempDir() and
// runs the standard migrations. Mirrors sessions_test.go's
// pattern so the advisor tests stay aligned with the existing
// fixture style.
func openTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(t.TempDir() + "/advisories.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func seedAdvisoryData(t *testing.T, db *store.DB) {
	t.Helper()
	sess := &connectors.Session{
		ID:          "sess-adv",
		CLI:         connectors.CLIClaude,
		ProjectPath: "/Users/x/Project/foo",
		StartedAt:   1000,
		LastMsgAt:   5000,
		Status:      connectors.SessionStatusIdle,
	}
	if err := store.UpsertSession(context.Background(), db, sess); err != nil {
		t.Fatalf("upsert session: %v", err)
	}

	rows := []struct {
		id      string
		content string
		ts      int64
	}{
		{"m1", "klyne: ~66% of loaded file context is stale relative to your current direction. Files still relevant: foo.go", 1100},
		{"m2", "klyne: per-turn cost has roughly doubled (latest turn ~25K uncached). Continuing here will burn through your 5-hour window faster than starting fresh — /klyne:handoff scope=current keeps the relevant context.", 1200},
		{"m3", "klyne: this session is 80% full — the next turn's prefix will keep growing. Run /klyne:handoff scope=current and start fresh.", 1300},
		{"m4", "klyne: you've used ~52% of your 5-hour window across 2 active sessions. This session is the dominant consumer (45% of the burn).", 1400},
		{"m5", "klyne: you're at ~78% of your 5-hour window — the cheapest next step is /klyne:handoff scope=current and a fresh session, otherwise you'll likely tip over the cap mid-task.", 1500},
		// One non-advisory system message that must NOT appear.
		{"m6", "Session metadata noise", 1600},
	}
	for _, r := range rows {
		msg := &connectors.Message{
			ID:        r.id,
			SessionID: "sess-adv",
			CLI:       connectors.CLIClaude,
			Role:      connectors.RoleSystem,
			Content:   r.content,
			Ts:        r.ts,
		}
		if err := store.InsertMessage(context.Background(), db, msg); err != nil {
			t.Fatalf("insert %s: %v", r.id, err)
		}
	}
}

func TestAdvisories_ListAndClassify(t *testing.T) {
	db := openTestDB(t)
	seedAdvisoryData(t, db)

	srv := httptest.NewServer(newAdvisoriesRouter(t, db))
	defer srv.Close()

	resp, err := http.Get(srv.URL + api.RouteAdvisories)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	var got api.AdvisoryListResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Advisories) != 5 {
		t.Fatalf("len=%d, want 5 advisory rows (non-klyne system messages must be excluded); got %+v",
			len(got.Advisories), got.Advisories)
	}

	// Newest-first ordering: m5 (ts=1500) must be first.
	if got.Advisories[0].MessageID != "m5" {
		t.Fatalf("ordering wrong: first row id=%s, want m5", got.Advisories[0].MessageID)
	}

	// Kind classification — one of each.
	kindByID := map[string]api.AdvisoryKind{}
	for _, a := range got.Advisories {
		kindByID[a.MessageID] = a.Kind
	}
	wantKinds := map[string]api.AdvisoryKind{
		"m1": api.AdvisoryKindStale,
		"m2": api.AdvisoryKindAcceleration,
		"m3": api.AdvisoryKindHardCeiling,
		"m4": api.AdvisoryKindFiveHourWarn,
		"m5": api.AdvisoryKindFiveHourUrgent,
	}
	for id, want := range wantKinds {
		if got := kindByID[id]; got != want {
			t.Errorf("classify %s: got %q, want %q", id, got, want)
		}
	}
}

func TestAdvisories_KindFilter(t *testing.T) {
	db := openTestDB(t)
	seedAdvisoryData(t, db)

	srv := httptest.NewServer(newAdvisoriesRouter(t, db))
	defer srv.Close()

	resp, err := http.Get(srv.URL + api.RouteAdvisories + "?kind=stale")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	var got api.AdvisoryListResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Advisories) != 1 {
		t.Fatalf("len=%d, want 1 (stale only)", len(got.Advisories))
	}
	if got.Advisories[0].Kind != api.AdvisoryKindStale {
		t.Fatalf("kind=%q, want stale", got.Advisories[0].Kind)
	}
}

func TestClassifyAdvisory_KnownPatterns(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    api.AdvisoryKind
	}{
		{"stale", "klyne: ~62% of loaded file context is stale relative to your current direction.", api.AdvisoryKindStale},
		{"acceleration", "klyne: per-turn cost has roughly doubled over the last 3 turns.", api.AdvisoryKindAcceleration},
		{"hard_ceiling", "klyne: this session is 78% full — the next turn's prefix will keep growing.", api.AdvisoryKindHardCeiling},
		{"warn", "klyne: you've used ~52% of your 5-hour window. This session is the dominant consumer.", api.AdvisoryKindFiveHourWarn},
		{"urgent", "klyne: you're at ~78% of your 5-hour window — the cheapest next step is /klyne:handoff.", api.AdvisoryKindFiveHourUrgent},
		{"unknown", "klyne: something different we don't recognise yet.", api.AdvisoryKindUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := handlers.ClassifyAdvisory(tc.content); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

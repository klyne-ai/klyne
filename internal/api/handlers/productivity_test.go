package handlers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/api/handlers"
	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/productivity"
	"github.com/klyne-ai/klyne/internal/store"
)

// newProductivityRouter wires GET /productivity on a fresh chi router,
// mirroring newInsightsRouter exactly.
func newProductivityRouter(t *testing.T, db *store.DB) http.Handler {
	t.Helper()
	r := chi.NewRouter()
	h := handlers.NewProductivityHandler(db)
	r.Get(api.RouteProductivity, h.Get)
	return r
}

// userEmailForTest resolves the same identity productivity.UserEmails()
// will key on: the machine's `git config user.email`, falling back to the
// substrate's seeded alias so IsUser==true for the commits we author.
func userEmailForTest(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "config", "user.email").Output()
	if err == nil {
		if e := strings.TrimSpace(string(out)); e != "" {
			return e
		}
	}
	// Substrate seed alias (see internal/productivity discover.go).
	return "mohitpatel9753@gmail.com"
}

// gitCmd runs a git subcommand in dir and fatals on error. Commits are
// authored as identityEmail so the §6.3 identity filter keeps them.
func gitCmd(t *testing.T, dir, identityEmail string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(),
		"GIT_AUTHOR_NAME=Tester",
		"GIT_AUTHOR_EMAIL="+identityEmail,
		"GIT_COMMITTER_NAME=Tester",
		"GIT_COMMITTER_EMAIL="+identityEmail,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// TestProductivity_HappyPath seeds a session whose project_path is a real
// temp git repo with one commit, hits the endpoint, and asserts the
// response decodes into productivity.Report with Day + ReflectionStatus set.
func TestProductivity_HappyPath(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)

	email := userEmailForTest(t)
	repo := t.TempDir()
	gitCmd(t, repo, email, "init", "-q")
	gitCmd(t, repo, email, "config", "user.email", email)
	gitCmd(t, repo, email, "config", "user.name", "Tester")
	if err := os.WriteFile(repo+"/a.txt", []byte("hello"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	gitCmd(t, repo, email, "add", "a.txt")
	gitCmd(t, repo, email, "commit", "-q", "-m", "feat: initial commit on CLI-1396")

	now := time.Now()
	winSince := now.Add(-2 * time.Hour)
	tsBase := now.Add(-30 * time.Minute).UnixMilli()

	ctx := context.Background()
	s := &connectors.Session{
		ID:          "s-prod-1",
		CLI:         connectors.CLIClaude,
		ProjectPath: repo,
		StartedAt:   0,
		LastMsgAt:   0,
		Status:      connectors.SessionStatusIdle,
	}
	if err := store.UpsertSession(ctx, db, s); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	for i := 0; i < 3; i++ {
		m := &connectors.Message{
			ID:        fmt.Sprintf("s-prod-1-m%d", i),
			SessionID: "s-prod-1",
			CLI:       connectors.CLIClaude,
			Role:      connectors.RoleAssistant,
			Content:   "x",
			Ts:        tsBase + int64(i)*60_000,
		}
		if err := store.InsertMessage(ctx, db, m); err != nil {
			t.Fatalf("insert message: %v", err)
		}
	}

	srv := httptest.NewServer(newProductivityRouter(t, db))
	t.Cleanup(srv.Close)

	url := fmt.Sprintf("%s%s?since=%d&until=%d",
		srv.URL, api.RouteProductivity, winSince.UnixMilli(), now.UnixMilli())
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var rep productivity.Report
	if err := json.NewDecoder(resp.Body).Decode(&rep); err != nil {
		t.Fatalf("decode productivity.Report: %v", err)
	}
	if rep.Day == "" {
		t.Errorf("expected Day to be set, got empty")
	}
	if rep.ReflectionStatus == "" {
		t.Errorf("expected ReflectionStatus to be set, got empty")
	}
	if rep.ReflectionStatus != "missing" {
		t.Errorf("ReflectionStatus = %q, want missing (no reflection seeded)", rep.ReflectionStatus)
	}
	if rep.Nudge == "" {
		t.Errorf("expected a Nudge when no reflection exists")
	}

	var svc *productivity.Service
	for i := range rep.Services {
		if rep.Services[i].ProjectPath == repo || rep.Services[i].Repo != "" {
			svc = &rep.Services[i]
			break
		}
	}
	if svc == nil {
		t.Fatalf("expected at least one service for the seeded repo, got %d services", len(rep.Services))
	}
	if len(svc.Branches) == 0 {
		t.Fatalf("expected at least one branch in service %q", svc.Repo)
	}
}

// TestProductivity_GlobalUnionSessionsAndReflection drives all three
// backend additions end-to-end through the HTTP endpoint:
//
//   - Change 1: two repos worked by parallel sessions with overlapping
//     wall-clock. total_active_minutes must be the GLOBAL union — less
//     than the sum of the per-Service attributed minutes.
//   - Change 2: every contributing session appears in `sessions`, sorted
//     by started_at, each with active_minutes + message_count.
//   - Change 3: a seeded worklog reflection's body_md is surfaced on the
//     matching Service.reflection_markdown.
func TestProductivity_GlobalUnionSessionsAndReflection(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	ctx := context.Background()
	email := userEmailForTest(t)

	// Two real temp git repos, each with one in-window commit.
	mkRepo := func(name string) string {
		repo := t.TempDir()
		gitCmd(t, repo, email, "init", "-q")
		gitCmd(t, repo, email, "config", "user.email", email)
		gitCmd(t, repo, email, "config", "user.name", "Tester")
		if err := os.WriteFile(repo+"/f.txt", []byte(name), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}
		gitCmd(t, repo, email, "add", "f.txt")
		gitCmd(t, repo, email, "commit", "-q", "-m", "feat: work on "+name)
		return repo
	}
	repoA := mkRepo("a")
	repoB := mkRepo("b")

	now := time.Now()
	winSince := now.Add(-6 * time.Hour)
	// Anchor the sessions inside the window. Session A on repoA runs from
	// -5h to -3h (120m). Session B on repoB runs from -4h to -2h (120m).
	// They overlap -4h..-3h. Global union = -5h..-2h = 180m. Sum of
	// per-Service unions = 120 + 120 = 240m. 180 < 240.
	aStart := now.Add(-5 * time.Hour)
	bStart := now.Add(-4 * time.Hour)

	mkSession := func(id, repo string, start time.Time) {
		s := &connectors.Session{
			ID: id, CLI: connectors.CLIClaude, ProjectPath: repo,
			Status: connectors.SessionStatusIdle,
		}
		if err := store.UpsertSession(ctx, db, s); err != nil {
			t.Fatalf("upsert session %s: %v", id, err)
		}
		// Messages every 10m for 120m → one contiguous active interval.
		for m := 0; m <= 120; m += 10 {
			msg := &connectors.Message{
				ID:        fmt.Sprintf("%s-m%d", id, m),
				SessionID: id, CLI: connectors.CLIClaude, Role: connectors.RoleAssistant,
				Content: "x", Ts: start.Add(time.Duration(m) * time.Minute).UnixMilli(),
			}
			if err := store.InsertMessage(ctx, db, msg); err != nil {
				t.Fatalf("insert message: %v", err)
			}
		}
	}
	mkSession("sess-a", repoA, aStart)
	mkSession("sess-b", repoB, bStart)

	// Change 3: seed a worklog reflection for repoA on the window's day.
	reflBody := "## repoA reflection\n- shipped the pipeline work\n"
	if err := store.InsertReflection(ctx, db, store.Reflection{
		ID: "ref-prod-a",
		// reflectionLookup matches the reflection's local calendar date
		// against the report Day, which is the window-start date.
		TS: winSince.UnixMilli(),
		// reflectionLookup matches on project_path = the repo path.
		ProjectPath:      repoA,
		Tier:             1,
		Title:            "Daily reflection",
		BodyMD:           reflBody,
		State:            "proposed",
		SummarySource:    "ai",
		Importance:       7,
		EvidenceEntryIDs: []string{"e1"},
	}); err != nil {
		t.Fatalf("insert reflection: %v", err)
	}

	srv := httptest.NewServer(newProductivityRouter(t, db))
	t.Cleanup(srv.Close)

	url := fmt.Sprintf("%s%s?since=%d&until=%d",
		srv.URL, api.RouteProductivity, winSince.UnixMilli(), now.UnixMilli())
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var rep productivity.Report
	if err := json.NewDecoder(resp.Body).Decode(&rep); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Change 1: headline is the global union, strictly < sum-of-services.
	if rep.TotalActiveMinutes != 180 {
		t.Errorf("total_active_minutes = %d; want 180 (global union of two overlapping 120m sessions)",
			rep.TotalActiveMinutes)
	}
	sumServices := 0
	for _, svc := range rep.Services {
		for _, b := range svc.Branches {
			sumServices += b.AttributedMinutes
		}
	}
	if sumServices != 240 {
		t.Errorf("sum of per-Service AttributedMinutes = %d; want 240 (120 + 120)", sumServices)
	}
	if rep.TotalActiveMinutes >= sumServices {
		t.Errorf("total_active_minutes %d must be STRICTLY < sum-of-services %d",
			rep.TotalActiveMinutes, sumServices)
	}
	if rep.MinutesByCLI["claude"] != 180 {
		t.Errorf("minutes_by_cli[claude] = %d; want 180 (per-CLI global union)", rep.MinutesByCLI["claude"])
	}

	// Change 2: both contributing sessions present, sorted by started_at.
	if len(rep.Sessions) != 2 {
		t.Fatalf("len(sessions) = %d; want 2", len(rep.Sessions))
	}
	if rep.Sessions[0].SessionID != "sess-a" {
		t.Errorf("sessions[0] = %q; want sess-a (earlier start)", rep.Sessions[0].SessionID)
	}
	for _, ss := range rep.Sessions {
		if ss.ActiveMinutes != 120 {
			t.Errorf("session %s active_minutes = %d; want 120", ss.SessionID, ss.ActiveMinutes)
		}
		if ss.MessageCount != 13 {
			t.Errorf("session %s message_count = %d; want 13", ss.SessionID, ss.MessageCount)
		}
		if ss.CLI != "claude" {
			t.Errorf("session %s cli = %q; want claude", ss.SessionID, ss.CLI)
		}
		if ss.Repo == "" {
			t.Errorf("session %s repo is empty", ss.SessionID)
		}
		if ss.StartedAt.IsZero() || ss.EndedAt.IsZero() {
			t.Errorf("session %s missing started/ended anchors", ss.SessionID)
		}
	}

	// Change 3: repoA's Service carries the reflection body_md.
	var svcA *productivity.Service
	for i := range rep.Services {
		if rep.Services[i].ProjectPath == repoA {
			svcA = &rep.Services[i]
		}
	}
	if svcA == nil {
		t.Fatalf("no Service for repoA %q", repoA)
	}
	if svcA.ReflectionMarkdown != reflBody {
		t.Errorf("repoA Service.reflection_markdown = %q; want %q", svcA.ReflectionMarkdown, reflBody)
	}
	if rep.ReflectionStatus != "current" {
		t.Errorf("reflection_status = %q; want current (reflection seeded)", rep.ReflectionStatus)
	}
}

// TestProductivity_EmptyWindow asserts the endpoint returns 200 with a
// well-formed empty report when no sessions fall in the window.
func TestProductivity_EmptyWindow(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)

	srv := httptest.NewServer(newProductivityRouter(t, db))
	t.Cleanup(srv.Close)

	now := time.Now()
	url := fmt.Sprintf("%s%s?since=%d&until=%d",
		srv.URL, api.RouteProductivity, now.Add(-time.Hour).UnixMilli(), now.UnixMilli())
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	// Maps/slices must marshal as {}/[] not null so UI consumers can
	// rely on the contract.
	raw := string(body)
	for _, frag := range []string{`"services":[]`, `"sessions":[]`, `"minutes_by_cli":{}`} {
		if !strings.Contains(raw, frag) {
			t.Errorf("empty-window JSON missing %q; got %s", frag, raw)
		}
	}
	var rep productivity.Report
	if err := json.Unmarshal(body, &rep); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rep.Day == "" {
		t.Errorf("expected Day set even for empty window")
	}
	if len(rep.Services) != 0 {
		t.Errorf("expected 0 services for empty window, got %d", len(rep.Services))
	}
	if rep.TotalActiveMinutes != 0 {
		t.Errorf("expected total_active_minutes 0 for empty window, got %d", rep.TotalActiveMinutes)
	}
}

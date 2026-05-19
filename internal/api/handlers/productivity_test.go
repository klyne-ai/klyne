package handlers_test

import (
	"context"
	"encoding/json"
	"fmt"
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
	var rep productivity.Report
	if err := json.NewDecoder(resp.Body).Decode(&rep); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rep.Day == "" {
		t.Errorf("expected Day set even for empty window")
	}
	if len(rep.Services) != 0 {
		t.Errorf("expected 0 services for empty window, got %d", len(rep.Services))
	}
}

package handlers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/store"
)

// TestCockpit_Threads_HappyPath seeds two sessions with messages and
// asserts the handler returns them ordered by last_msg_at DESC. Also
// pins the JSON shape (threads:[] when empty, never null) and the
// most-recent-git_branch correlated subquery — that subquery exists to
// label the tile with the latest branch the user worked on, and is the
// part most likely to regress under a future query rewrite.
func TestCockpit_Threads_HappyPath(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	ctx := context.Background()

	now := time.Now()

	for _, sess := range []struct {
		id     string
		path   string
		branch string
		off    time.Duration
	}{
		{"s-old", "/proj-a", "feat/old", -2 * time.Hour},
		{"s-new", "/proj-b", "feat/new", -10 * time.Minute},
	} {
		if err := store.UpsertSession(ctx, db, &connectors.Session{
			ID: sess.id, CLI: connectors.CLIClaude, ProjectPath: sess.path,
			Model: "claude-opus-4-7", Status: connectors.SessionStatusIdle,
		}); err != nil {
			t.Fatalf("upsert session: %v", err)
		}
		// Two messages per session: an old one on an older branch, then
		// a newer one on `sess.branch`. The correlated subquery in the
		// handler must pick the newest branch, not the first.
		_ = store.InsertMessage(ctx, db, &connectors.Message{
			ID: sess.id + "-old-branch", SessionID: sess.id, CLI: connectors.CLIClaude,
			Role: connectors.RoleAssistant, Content: "x",
			Ts: now.Add(sess.off).Add(-time.Minute).UnixMilli(), GitBranch: "main",
		})
		_ = store.InsertMessage(ctx, db, &connectors.Message{
			ID: sess.id + "-new-branch", SessionID: sess.id, CLI: connectors.CLIClaude,
			Role: connectors.RoleAssistant, Content: "y",
			Ts: now.Add(sess.off).UnixMilli(), GitBranch: sess.branch,
		})
	}

	srv := httptest.NewServer(newTestRouter(t, db))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + api.RouteCockpitThreads)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}

	var got api.CockpitThreadsResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Threads) != 2 {
		t.Fatalf("want 2 threads, got %d (%+v)", len(got.Threads), got.Threads)
	}
	// last_msg_at DESC → s-new first.
	if got.Threads[0].SessionID != "s-new" || got.Threads[1].SessionID != "s-old" {
		t.Fatalf("wrong order (want s-new, s-old): %+v", got.Threads)
	}
	// Most-recent git_branch must be the second message's branch, not
	// the first — guards the correlated subquery against a regression
	// where someone groups by branch (the previous bug we removed).
	if got.Threads[0].GitBranch != "feat/new" {
		t.Errorf("s-new: git_branch = %q, want feat/new", got.Threads[0].GitBranch)
	}
	if got.Threads[1].GitBranch != "feat/old" {
		t.Errorf("s-old: git_branch = %q, want feat/old", got.Threads[1].GitBranch)
	}
}

// TestCockpit_Threads_EmptyDB returns the empty-slice form, not null.
func TestCockpit_Threads_EmptyDB(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	srv := httptest.NewServer(newTestRouter(t, db))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + api.RouteCockpitThreads)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"threads":[]`) {
		t.Errorf("empty response should marshal `threads:[]`, got %s", body)
	}
}

// TestCockpit_Threads_RejectsBadParams asserts the 400 paths for
// invalid since/limit. These are user-controllable inputs and must not
// pass through to the SQL query unchecked.
func TestCockpit_Threads_RejectsBadParams(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	srv := httptest.NewServer(newTestRouter(t, db))
	t.Cleanup(srv.Close)

	cases := []struct {
		name string
		q    string
	}{
		{"negative since", "?since=-1"},
		{"non-numeric since", "?since=abc"},
		{"zero limit", "?limit=0"},
		{"limit over 100", "?limit=101"},
		{"non-numeric limit", "?limit=abc"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			url := fmt.Sprintf("%s%s%s", srv.URL, api.RouteCockpitThreads, c.q)
			resp, err := http.Get(url)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("%s: status %d, want 400", c.name, resp.StatusCode)
			}
		})
	}
}


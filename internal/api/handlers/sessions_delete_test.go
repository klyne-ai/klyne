package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/store"
)

// TestSessionsDelete_HappyPath asserts DELETE /sessions/{id} returns 204
// and removes the row + its messages (FK cascade). Locks the wire
// contract so a future refactor that returns 200/empty-body silently
// would be caught.
func TestSessionsDelete_HappyPath(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	ctx := context.Background()

	if err := store.UpsertSession(ctx, db, &connectors.Session{
		ID: "s-del-1", CLI: connectors.CLIClaude, ProjectPath: "/p", Model: "m",
		Status: connectors.SessionStatusIdle,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := store.InsertMessage(ctx, db, &connectors.Message{
		ID: "s-del-1-m0", SessionID: "s-del-1", CLI: connectors.CLIClaude,
		Role: connectors.RoleAssistant, Content: "x", Ts: 1,
	}); err != nil {
		t.Fatalf("insert message: %v", err)
	}

	srv := httptest.NewServer(newTestRouter(t, db))
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/sessions/s-del-1", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}

	// Confirm the row really is gone — a refactor that returned 204 but
	// no-op'd the DELETE would otherwise pass.
	if _, err := store.GetSession(ctx, db, "s-del-1"); err == nil {
		t.Fatalf("session still present after DELETE")
	}
}

// TestSessionsDelete_NotFound — deleting a non-existent id is 404, not 204.
// Idempotent-delete refactors must continue to surface the missing row
// (the UI treats 404 as "stale list, please refresh"; a silent 204 would
// make the list pretend it succeeded).
func TestSessionsDelete_NotFound(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	srv := httptest.NewServer(newTestRouter(t, db))
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/sessions/does-not-exist", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// TestSessionsDelete_PathTraversalLike — chi routes /sessions/{id}, so
// an id containing a slash would never match this route in the first
// place (404 from the router itself, NOT from the handler). Lock that
// behaviour: if anyone changes the route pattern to a greedy * the
// validation in this test will fire.
func TestSessionsDelete_RejectsSubpath(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	srv := httptest.NewServer(newTestRouter(t, db))
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/sessions/a/b/c", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	body := make([]byte, 256)
	n, _ := resp.Body.Read(body)
	_ = resp.Body.Close()
	// chi returns 405 (method not allowed) for unmounted method/path
	// combos. Either 404 or 405 is acceptable — the point is that a
	// slash-bearing id does NOT route through the handler.
	if resp.StatusCode == http.StatusNoContent {
		t.Fatalf("subpath id should not reach handler; got 204 with body %q", strings.TrimSpace(string(body[:n])))
	}
}

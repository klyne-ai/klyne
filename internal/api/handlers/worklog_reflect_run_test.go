package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/store"
)

// newReflectTestStore opens a temp SQLite DB with migrations applied. Kept
// local to this file so the test stays in `package handlers` and can poke
// at the handler's unexported runCmd field directly.
func newReflectTestStore(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), t.TempDir()+"/test.db")
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// Test runner wires the handler with a stub runCmd so we never invoke the
// real `claude` binary. The stub captures the projectPath it was asked to
// reflect on so we can assert the handler forwards it verbatim. A custom
// fn overrides the default behavior — used by the cancellation test to
// block on ctx.Done().
type stubRunner struct {
	calls  []string
	output []byte
	err    error
	fn     func(ctx context.Context, projectPath string) ([]byte, error)
}

func (s *stubRunner) run(ctx context.Context, projectPath string) ([]byte, error) {
	s.calls = append(s.calls, projectPath)
	if s.fn != nil {
		return s.fn(ctx, projectPath)
	}
	return s.output, s.err
}

func newReflectRunRouter(t *testing.T, db *store.DB, runner *stubRunner) http.Handler {
	t.Helper()
	r := chi.NewRouter()
	h := &WorklogReflectRunHandler{db: db, runCmd: runner.run}
	r.Post(api.RouteWorklogReflectRun, h.Run)
	return r
}

// seedAllowlist drops one project row into worklog so the allowlist
// check succeeds for that path and rejects every other path.
func seedAllowlist(t *testing.T, db *store.DB, projectPath string) {
	t.Helper()
	ctx := context.Background()
	err := store.UpsertStopSummaryWithWorklog(ctx, db,
		store.StopSummary{
			SessionID:   "allowlist-seed",
			Ts:          10_000,
			ProjectPath: projectPath,
			CLI:         "claude",
			Summary:     "seed",
			LastUser:    "user",
		},
		store.WorklogColumns{RecapVisible: 1, Importance: 5, DraftState: "accepted"},
	)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func TestReflectRun_HappyPath(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	const proj = "/proj/known"
	seedAllowlist(t, db, proj)

	runner := &stubRunner{output: []byte("reflection body")}
	srv := httptest.NewServer(newReflectRunRouter(t, db, runner))
	t.Cleanup(srv.Close)

	body, _ := json.Marshal(map[string]string{"project_path": proj})
	resp, err := http.Post(srv.URL+api.RouteWorklogReflectRun, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var got reflectRunResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != "ok" {
		t.Errorf("Status = %q, want ok", got.Status)
	}
	if got.Output != "reflection body" {
		t.Errorf("Output = %q, want reflection body", got.Output)
	}
	if len(runner.calls) != 1 || runner.calls[0] != proj {
		t.Errorf("runner.calls = %v, want [%q]", runner.calls, proj)
	}
}

func TestReflectRun_RejectsUnknownProject(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	seedAllowlist(t, db, "/proj/known")

	runner := &stubRunner{}
	srv := httptest.NewServer(newReflectRunRouter(t, db, runner))
	t.Cleanup(srv.Close)

	body, _ := json.Marshal(map[string]string{"project_path": "/proj/totally-unknown"})
	resp, err := http.Post(srv.URL+api.RouteWorklogReflectRun, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
	if len(runner.calls) != 0 {
		t.Errorf("runner was invoked %d times for unallowlisted path; want 0", len(runner.calls))
	}
}

func TestReflectRun_RejectsCrossOrigin(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	const proj = "/proj/known"
	seedAllowlist(t, db, proj)

	runner := &stubRunner{}
	srv := httptest.NewServer(newReflectRunRouter(t, db, runner))
	t.Cleanup(srv.Close)

	body, _ := json.Marshal(map[string]string{"project_path": proj})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+api.RouteWorklogReflectRun, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
	if len(runner.calls) != 0 {
		t.Errorf("runner was invoked %d times for cross-origin request; want 0", len(runner.calls))
	}
}

func TestReflectRun_AllowsLoopbackOrigin(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	const proj = "/proj/known"
	seedAllowlist(t, db, proj)

	runner := &stubRunner{output: []byte("ok")}
	srv := httptest.NewServer(newReflectRunRouter(t, db, runner))
	t.Cleanup(srv.Close)

	body, _ := json.Marshal(map[string]string{"project_path": proj})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+api.RouteWorklogReflectRun, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://127.0.0.1:7878")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// TestReflectRun_ClientCancelKillsSubprocess locks in the kill semantics
// the UI's Stop button depends on: when the client aborts the fetch, the
// server's request context fires Done(), which exec.CommandContext uses
// to SIGKILL the child. We can't observe SIGKILL on a stub, but we CAN
// observe ctx.Done() firing inside runCmd within the cancellation window
// — same path the real subprocess wrapper uses.
func TestReflectRun_ClientCancelKillsSubprocess(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	const proj = "/proj/known"
	seedAllowlist(t, db, proj)

	observed := make(chan struct{})
	runner := &stubRunner{
		fn: func(ctx context.Context, _ string) ([]byte, error) {
			<-ctx.Done() // block until client disconnects / context cancelled
			close(observed)
			return []byte("partial"), ctx.Err()
		},
	}
	srv := httptest.NewServer(newReflectRunRouter(t, db, runner))
	t.Cleanup(srv.Close)

	body, _ := json.Marshal(map[string]string{"project_path": proj})
	reqCtx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(reqCtx, http.MethodPost,
		srv.URL+api.RouteWorklogReflectRun, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	// Fire the request in a goroutine so we can cancel it mid-flight.
	errCh := make(chan error, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
		errCh <- err
	}()

	// Wait a beat so the handler has actually entered runner.fn before
	// we cancel — otherwise the test races on the goroutine ordering.
	select {
	case <-runnerEntered(runner):
	case <-time.After(2 * time.Second):
		t.Fatalf("handler never invoked runner")
	}

	cancel()

	select {
	case <-observed:
		// runCmd saw ctx.Done() — exec.CommandContext would have killed
		// the real subprocess at this point.
	case <-time.After(2 * time.Second):
		t.Fatalf("runner did not observe context cancellation within 2s")
	}
	<-errCh // drain
}

// runnerEntered returns a channel that fires once stubRunner.run has been
// called at least once. Polls runner.calls every 5ms.
func runnerEntered(s *stubRunner) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		for {
			if len(s.calls) > 0 {
				close(ch)
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	return ch
}

func TestReflectRun_RejectsMissingBody(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	seedAllowlist(t, db, "/proj/known")

	runner := &stubRunner{}
	srv := httptest.NewServer(newReflectRunRouter(t, db, runner))
	t.Cleanup(srv.Close)

	body, _ := json.Marshal(map[string]string{})
	resp, err := http.Post(srv.URL+api.RouteWorklogReflectRun, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

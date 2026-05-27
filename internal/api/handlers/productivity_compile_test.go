package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/store"
)

// stubCompileRunner stubs out the productivity-sync subprocess so the
// handler can be exercised without a real `claude` binary.
type stubCompileRunner struct {
	calls  []compileCall
	output []byte
	err    error
}

type compileCall struct {
	ProjectPath string
	Day         string
	ModelKey    string
}

func (s *stubCompileRunner) run(ctx context.Context, projectPath, day, modelKey string) (claudeRunResult, error) {
	s.calls = append(s.calls, compileCall{ProjectPath: projectPath, Day: day, ModelKey: modelKey})
	return claudeRunResult{Output: string(s.output), Model: "stub"}, s.err
}

func newCompileRouter(t *testing.T, db *store.DB, runner *stubCompileRunner) http.Handler {
	t.Helper()
	h := &ProductivityCompileHandler{db: db, runCmd: runner.run}
	return api.NewRouter(api.Deps{
		Mounters: []api.RouterMounter{&compileHandlerMounter{h: h}},
	})
}

type compileHandlerMounter struct{ h *ProductivityCompileHandler }

func (m *compileHandlerMounter) Mount(r chi.Router) {
	r.Post(api.RouteProductivityCompile, m.h.Run)
}

func TestProductivityCompile_HappyPath(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	const proj = "/proj/known"
	seedAllowlist(t, db, proj)

	runner := &stubCompileRunner{output: []byte("compiled 1 card: klyne")}
	srv := httptest.NewServer(newCompileRouter(t, db, runner))
	t.Cleanup(srv.Close)

	body, _ := json.Marshal(map[string]string{"project_path": proj, "day": "2026-05-26"})
	resp, err := http.Post(srv.URL+api.RouteProductivityCompile, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var got productivityCompileResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != "ok" {
		t.Errorf("Status = %q, want ok", got.Status)
	}
	if got.Day != "2026-05-26" {
		t.Errorf("Day = %q, want 2026-05-26", got.Day)
	}
	if len(runner.calls) != 1 || runner.calls[0].ProjectPath != proj || runner.calls[0].Day != "2026-05-26" {
		t.Errorf("runner.calls = %+v, want [{proj=%s day=2026-05-26}]", runner.calls, proj)
	}
	if runner.calls[0].ModelKey != "sonnet" {
		t.Errorf("ModelKey = %q, want %q (default when request omits model)", runner.calls[0].ModelKey, "sonnet")
	}
}

func TestProductivityCompile_ExplicitOpus(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	const proj = "/proj/known"
	seedAllowlist(t, db, proj)

	runner := &stubCompileRunner{output: []byte("compiled 1 card: klyne")}
	srv := httptest.NewServer(newCompileRouter(t, db, runner))
	t.Cleanup(srv.Close)

	body, _ := json.Marshal(map[string]string{"project_path": proj, "day": "2026-05-26", "model": "opus"})
	resp, err := http.Post(srv.URL+api.RouteProductivityCompile, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(runner.calls) != 1 || runner.calls[0].ModelKey != "opus" {
		t.Errorf("runner.calls = %+v, want one call with ModelKey=opus", runner.calls)
	}
}

func TestProductivityCompile_RejectsUnknownModel(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	const proj = "/proj/known"
	seedAllowlist(t, db, proj)

	runner := &stubCompileRunner{}
	srv := httptest.NewServer(newCompileRouter(t, db, runner))
	t.Cleanup(srv.Close)

	body, _ := json.Marshal(map[string]string{"project_path": proj, "day": "2026-05-26", "model": "haiku"})
	resp, err := http.Post(srv.URL+api.RouteProductivityCompile, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	rb, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(rb, []byte("unknown model")) {
		t.Errorf("body = %q, want substring %q", string(rb), "unknown model")
	}
	if len(runner.calls) != 0 {
		t.Errorf("runner was invoked despite unknown model")
	}
}

func TestProductivityCompile_RejectsUnknownProject(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	seedAllowlist(t, db, "/proj/known")

	runner := &stubCompileRunner{}
	srv := httptest.NewServer(newCompileRouter(t, db, runner))
	t.Cleanup(srv.Close)

	body, _ := json.Marshal(map[string]string{"project_path": "/proj/totally-unknown", "day": "2026-05-26"})
	resp, err := http.Post(srv.URL+api.RouteProductivityCompile, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
	if len(runner.calls) != 0 {
		t.Errorf("runner was invoked for unallowlisted path")
	}
}

func TestProductivityCompile_RejectsMissingDay(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	const proj = "/proj/known"
	seedAllowlist(t, db, proj)

	runner := &stubCompileRunner{}
	srv := httptest.NewServer(newCompileRouter(t, db, runner))
	t.Cleanup(srv.Close)

	body, _ := json.Marshal(map[string]string{"project_path": proj})
	resp, err := http.Post(srv.URL+api.RouteProductivityCompile, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestProductivityCompile_RejectsBadDay(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	const proj = "/proj/known"
	seedAllowlist(t, db, proj)

	runner := &stubCompileRunner{}
	srv := httptest.NewServer(newCompileRouter(t, db, runner))
	t.Cleanup(srv.Close)

	body, _ := json.Marshal(map[string]string{"project_path": proj, "day": "May 26"})
	resp, err := http.Post(srv.URL+api.RouteProductivityCompile, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

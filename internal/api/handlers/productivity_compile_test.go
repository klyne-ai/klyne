package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/store"
)

// newCompileHandler builds a handler whose registry uses the supplied stub
// runner, so Start/Status exercise the full goroutine path without a real
// `claude` binary.
func newCompileHandler(db *store.DB, run compileRunFn) *ProductivityCompileHandler {
	return &ProductivityCompileHandler{
		db:       db,
		registry: NewCompileJobRegistry(db, run),
	}
}

type compileMounter struct{ h *ProductivityCompileHandler }

func (m *compileMounter) Mount(r chi.Router) {
	r.Post(api.RouteProductivityCompileStart, m.h.Start)
	r.Get(api.RouteProductivityCompileStatus, m.h.Status)
}

func newCompileServer(t *testing.T, h *ProductivityCompileHandler) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(api.NewRouter(api.Deps{Mounters: []api.RouterMounter{&compileMounter{h: h}}}))
	t.Cleanup(srv.Close)
	return srv
}

func postStart(t *testing.T, srv *httptest.Server, body map[string]string) (*http.Response, []byte) {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(srv.URL+api.RouteProductivityCompileStart, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST start: %v", err)
	}
	rb, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, rb
}

func TestCompileStart_NoPending_ReturnsNone(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	seedAllowlist(t, db, "/proj/known") // rollup row, but no floor on 2026-05-26
	h := newCompileHandler(db, func(context.Context, string, string, string) (claudeRunResult, error) {
		t.Fatal("runCmd must not be called when nothing is pending")
		return claudeRunResult{}, nil
	})
	srv := newCompileServer(t, h)

	resp, rb := postStart(t, srv, map[string]string{"day": "2026-05-26"})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !bytes.Contains(rb, []byte(`"status":"none"`)) {
		t.Errorf("body = %s, want status:none", rb)
	}
}

func TestCompileStart_LaunchesJobAndStatusReflectsIt(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	const proj = "/proj/known"
	ts := int64(1_716_700_000_000)
	day := dayForTs(ts)
	seedStopSummary(t, db, proj, "s1", ts, "CLI-1452 did the work")

	h := newCompileHandler(db, func(context.Context, string, string, string) (claudeRunResult, error) {
		return claudeRunResult{Output: "ok", Model: "stub"}, nil
	})
	srv := newCompileServer(t, h)

	resp, rb := postStart(t, srv, map[string]string{"day": day})
	if resp.StatusCode != 200 {
		t.Fatalf("start status = %d, want 200; body=%s", resp.StatusCode, rb)
	}
	var job CompileJob
	if err := json.Unmarshal(rb, &job); err != nil {
		t.Fatalf("decode job: %v (body=%s)", err, rb)
	}
	if job.Status != "running" || len(job.Services) != 1 {
		t.Fatalf("job = %+v, want running with 1 service", job)
	}

	// Poll status until terminal.
	deadline := time.Now().Add(2 * time.Second)
	var final CompileJob
	for time.Now().Before(deadline) {
		sr, err := http.Get(srv.URL + api.RouteProductivityCompileStatus + "?day=" + day)
		if err != nil {
			t.Fatalf("GET status: %v", err)
		}
		srb, _ := io.ReadAll(sr.Body)
		sr.Body.Close()
		_ = json.Unmarshal(srb, &final)
		if final.Status != "" && final.Status != "running" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if final.Status != "done" {
		t.Errorf("final job Status = %q, want done", final.Status)
	}
}

func TestCompileStart_RejectsUnknownModel(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	seedAllowlist(t, db, "/proj/known")
	h := newCompileHandler(db, func(context.Context, string, string, string) (claudeRunResult, error) {
		return claudeRunResult{}, nil
	})
	srv := newCompileServer(t, h)
	resp, rb := postStart(t, srv, map[string]string{"day": "2026-05-26", "model": "haiku"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if !bytes.Contains(rb, []byte("unknown model")) {
		t.Errorf("body = %s, want 'unknown model'", rb)
	}
}

func TestCompileStart_RejectsBadDay(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	h := newCompileHandler(db, nil)
	srv := newCompileServer(t, h)
	resp, _ := postStart(t, srv, map[string]string{"day": "May 26"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestCompileStatus_NoneForUnknownDay(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	h := newCompileHandler(db, nil)
	srv := newCompileServer(t, h)
	resp, err := http.Get(srv.URL + api.RouteProductivityCompileStatus + "?day=2026-01-01")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	rb, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Contains(rb, []byte(`"status":"none"`)) {
		t.Errorf("body = %s, want status:none", rb)
	}
}

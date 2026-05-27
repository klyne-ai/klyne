package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/store"
)

type stubAskRunner struct {
	calls  []string // prompts seen
	output string
	err    error
}

func (s *stubAskRunner) run(ctx context.Context, prompt string) (claudeRunResult, error) {
	s.calls = append(s.calls, prompt)
	envelope := map[string]any{
		"type":   "result",
		"result": s.output,
		"usage": map[string]any{
			"input_tokens":  100,
			"output_tokens": 50,
		},
	}
	raw, _ := json.Marshal(envelope)
	res := parseClaudeRunResult(raw, nil, "stub-model")
	return res, s.err
}

type askHandlerMounter struct{ h *AskHandler }

func (m *askHandlerMounter) Mount(r chi.Router) { r.Post(api.RouteAsk, m.h.Run) }

func newAskRouter(t *testing.T, db *store.DB, runner *stubAskRunner) http.Handler {
	t.Helper()
	h := &AskHandler{db: db, runCmd: runner.run}
	return api.NewRouter(api.Deps{Mounters: []api.RouterMounter{&askHandlerMounter{h: h}}})
}

// seedAskSummary inserts a stop_summaries row with ai_drafted_summary +
// worklog_entry_json populated. Reuses UpsertStopSummaryWithWorklog
// and patches worklog_entry_json directly (the upsert helper does not
// know about that column).
func seedAskSummary(t *testing.T, db *store.DB, sessionID string, ts int64, project, aiSummary, worklogJSON string) {
	t.Helper()
	ctx := context.Background()
	if err := store.UpsertStopSummaryWithWorklog(ctx, db,
		store.StopSummary{
			SessionID: sessionID, Ts: ts, ProjectPath: project, CLI: "claude",
		},
		store.WorklogColumns{AIDraftedSummary: aiSummary},
	); err != nil {
		t.Fatalf("seedAskSummary %s: %v", sessionID, err)
	}
	if worklogJSON != "" {
		if _, err := db.Write().ExecContext(ctx,
			`UPDATE stop_summaries SET worklog_entry_json = ? WHERE session_id = ? AND ts = ?`,
			worklogJSON, sessionID, ts,
		); err != nil {
			t.Fatalf("patch worklog %s: %v", sessionID, err)
		}
	}
}

func TestAsk_EmptyRangeShortCircuits(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	runner := &stubAskRunner{output: "should not run"}

	srv := httptest.NewServer(newAskRouter(t, db, runner))
	t.Cleanup(srv.Close)

	body, _ := json.Marshal(api.AskRequest{
		FromMs: 0, ToMs: 100, Question: "anything?",
	})
	resp, err := http.Post(srv.URL+api.RouteAsk, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var got api.AskResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.SessionsUsed != 0 {
		t.Errorf("SessionsUsed = %d, want 0", got.SessionsUsed)
	}
	if !strings.Contains(strings.ToLower(got.Answer), "no sessions") {
		t.Errorf("Answer = %q, want a 'no sessions' message", got.Answer)
	}
	if len(runner.calls) != 0 {
		t.Errorf("runner invoked despite empty range")
	}
}

func TestAsk_HappyPath(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	seedAskSummary(t, db, "s1", 200, "/p/a", "fixed nav bug", `{"bugs_fixed":[{"summary":"nav"}]}`)

	runner := &stubAskRunner{output: "1 bug was fixed."}
	srv := httptest.NewServer(newAskRouter(t, db, runner))
	t.Cleanup(srv.Close)

	body, _ := json.Marshal(api.AskRequest{
		Projects: []string{"/p/a"},
		FromMs:   100, ToMs: 300,
		Question: "How many bugs?",
	})
	resp, err := http.Post(srv.URL+api.RouteAsk, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var got api.AskResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Answer != "1 bug was fixed." {
		t.Errorf("Answer = %q", got.Answer)
	}
	if got.SessionsUsed != 1 {
		t.Errorf("SessionsUsed = %d, want 1", got.SessionsUsed)
	}
	if got.Model != "stub-model" {
		t.Errorf("Model = %q, want stub-model", got.Model)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("runner.calls = %d, want 1", len(runner.calls))
	}
	if !strings.Contains(runner.calls[0], "How many bugs?") {
		t.Errorf("prompt missing question: %s", runner.calls[0])
	}
	if !strings.Contains(runner.calls[0], "nav") {
		t.Errorf("prompt missing worklog content: %s", runner.calls[0])
	}
}

func TestAsk_RejectsEmptyQuestion(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	runner := &stubAskRunner{}
	srv := httptest.NewServer(newAskRouter(t, db, runner))
	t.Cleanup(srv.Close)

	body, _ := json.Marshal(api.AskRequest{FromMs: 0, ToMs: 100, Question: "   "})
	resp, err := http.Post(srv.URL+api.RouteAsk, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestAsk_RejectsLongQuestion(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	runner := &stubAskRunner{}
	srv := httptest.NewServer(newAskRouter(t, db, runner))
	t.Cleanup(srv.Close)

	long := strings.Repeat("x", 2001)
	body, _ := json.Marshal(api.AskRequest{FromMs: 0, ToMs: 100, Question: long})
	resp, err := http.Post(srv.URL+api.RouteAsk, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestAsk_RejectsInvertedRange(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	runner := &stubAskRunner{}
	srv := httptest.NewServer(newAskRouter(t, db, runner))
	t.Cleanup(srv.Close)

	body, _ := json.Marshal(api.AskRequest{FromMs: 100, ToMs: 50, Question: "?"})
	resp, err := http.Post(srv.URL+api.RouteAsk, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestAsk_SubprocessErrorReturns500(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	seedAskSummary(t, db, "s1", 200, "/p", "x", "")

	runner := &stubAskRunner{err: errors.New("exit status 1: stderr noise")}
	srv := httptest.NewServer(newAskRouter(t, db, runner))
	t.Cleanup(srv.Close)

	body, _ := json.Marshal(api.AskRequest{FromMs: 100, ToMs: 300, Question: "?"})
	resp, err := http.Post(srv.URL+api.RouteAsk, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

func TestAsk_TruncatesLargeContext(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	for i := 0; i < 250; i++ {
		seedAskSummary(t, db, fmt.Sprintf("s%d", i), int64(100+i), "/p", "x", "")
	}
	runner := &stubAskRunner{output: "ok"}
	srv := httptest.NewServer(newAskRouter(t, db, runner))
	t.Cleanup(srv.Close)

	body, _ := json.Marshal(api.AskRequest{FromMs: 0, ToMs: 10_000, Question: "?"})
	resp, err := http.Post(srv.URL+api.RouteAsk, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	var got api.AskResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.SessionsUsed != 200 {
		t.Errorf("SessionsUsed = %d, want 200", got.SessionsUsed)
	}
	if got.TruncatedToN != 200 {
		t.Errorf("TruncatedToN = %d, want 200", got.TruncatedToN)
	}
}

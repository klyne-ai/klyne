package handlers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/mohitpatell/agentdeck/internal/api"
	"github.com/mohitpatell/agentdeck/internal/api/handlers"
	"github.com/mohitpatell/agentdeck/internal/connectors"
	"github.com/mohitpatell/agentdeck/internal/store"
)

// newRestoreRouter builds a chi router with only the restore route wired.
func newRestoreRouter(db *store.DB) http.Handler {
	r := chi.NewRouter()
	h := handlers.NewRestoreHandler(db)
	r.Get(api.RouteSessionRestore, h.Restore)
	return r
}

// TestRestoreEndpoint_NotFound verifies 404 is returned for unknown sessions.
func TestRestoreEndpoint_NotFound(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	router := newRestoreRouter(db)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/sessions/no-such-session/restore")
	if err != nil {
		t.Fatalf("GET /sessions/no-such-session/restore: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

// TestRestoreEndpoint_NoSummary verifies that when there is no summary,
// the markdown says "no summary available".
func TestRestoreEndpoint_NoSummary(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)

	// Seed a session with no summary.
	sess := &connectors.Session{
		ID:          "sess-no-summary",
		CLI:         connectors.CLIClaude,
		ProjectPath: "/tmp/proj",
		StartedAt:   1000,
		LastMsgAt:   2000,
		Status:      connectors.SessionStatusIdle,
	}
	if err := store.UpsertSession(context.Background(), db, sess); err != nil {
		t.Fatalf("upsert session: %v", err)
	}

	router := newRestoreRouter(db)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/sessions/sess-no-summary/restore")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body api.RestoreResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if body.Summary != "" {
		t.Errorf("expected empty summary, got %q", body.Summary)
	}
	if !strings.Contains(body.Markdown, "no summary available") {
		t.Errorf("expected 'no summary available' in markdown:\n%s", body.Markdown)
	}
	if body.SessionID != "sess-no-summary" {
		t.Errorf("expected session_id=sess-no-summary, got %q", body.SessionID)
	}
	if body.ResumeCmd == "" {
		t.Error("expected non-empty resume_cmd")
	}
}

// TestRestoreEndpoint_GoldenFile seeds a known session + summary + messages,
// calls GET /sessions/:id/restore, and compares the Markdown bundle against
// the golden file stored in testdata/restore_golden.md.
func TestRestoreEndpoint_GoldenFile(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)

	const sessionID = "test-session-001"

	// Seed session.
	sess := &connectors.Session{
		ID:          sessionID,
		CLI:         connectors.CLIClaude,
		ProjectPath: "/tmp/webhookservice",
		StartedAt:   1000,
		LastMsgAt:   5000,
		Status:      connectors.SessionStatusIdle,
	}
	if err := store.UpsertSession(context.Background(), db, sess); err != nil {
		t.Fatalf("upsert session: %v", err)
	}

	// Seed summary.
	summary := &store.Summary{
		SessionID: sessionID,
		Text:      "Working on webhookservice Go project. Added per-IP token bucket rate limiting middleware.",
		Model:     "claude-haiku-4",
		TS:        3000,
	}
	if err := store.InsertSummary(context.Background(), db, summary); err != nil {
		t.Fatalf("insert summary: %v", err)
	}

	// Seed messages.
	msgs := []*connectors.Message{
		{
			ID:        "msg-001",
			SessionID: sessionID,
			CLI:       connectors.CLIClaude,
			Role:      connectors.RoleUser,
			Content:   "Hello from msg-001",
			Ts:        1000,
		},
		{
			ID:        "msg-002",
			SessionID: sessionID,
			CLI:       connectors.CLIClaude,
			Role:      connectors.RoleAssistant,
			Content:   "Response from msg-002",
			Ts:        2000,
		},
	}
	for _, m := range msgs {
		if err := store.InsertMessage(context.Background(), db, m); err != nil {
			t.Fatalf("insert message %q: %v", m.ID, err)
		}
	}

	router := newRestoreRouter(db)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/sessions/" + sessionID + "/restore")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body api.RestoreResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Validate key fields.
	if body.SessionID != sessionID {
		t.Errorf("session_id: got %q, want %q", body.SessionID, sessionID)
	}
	if body.Summary != summary.Text {
		t.Errorf("summary mismatch:\n  got:  %q\n  want: %q", body.Summary, summary.Text)
	}
	if len(body.Tail) != 2 {
		t.Errorf("expected 2 tail messages, got %d", len(body.Tail))
	}
	if body.ResumeCmd == "" {
		t.Error("expected non-empty resume_cmd")
	}
	if body.GeneratedAt == 0 {
		t.Error("expected non-zero generated_at")
	}

	// Compare markdown against golden file.
	goldenPath := filepath.Join("testdata", "restore_golden.md")
	goldenBytes, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden file %s: %v", goldenPath, err)
	}
	golden := string(goldenBytes)

	if body.Markdown != golden {
		t.Errorf("markdown does not match golden file.\nGot:\n%s\n\nWant:\n%s",
			body.Markdown, golden)
	}
}

// TestRestoreEndpoint_ResumeCmdClaude verifies the resume_cmd for Claude sessions.
func TestRestoreEndpoint_ResumeCmdClaude(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)

	sess := &connectors.Session{
		ID:          "claude-sess",
		CLI:         connectors.CLIClaude,
		ProjectPath: "/tmp/proj",
		StartedAt:   1000,
		LastMsgAt:   2000,
		Status:      connectors.SessionStatusIdle,
	}
	if err := store.UpsertSession(context.Background(), db, sess); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	router := newRestoreRouter(db)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/sessions/claude-sess/restore")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	var body api.RestoreResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if !strings.HasPrefix(body.ResumeCmd, "claude --resume claude-sess") {
		t.Errorf("unexpected resume_cmd: %q", body.ResumeCmd)
	}
}

// TestRestoreEndpoint_MoreThan20Messages verifies that when there are more than
// 20 messages, the tail contains only the last 20.
func TestRestoreEndpoint_MoreThan20Messages(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)

	sess := &connectors.Session{
		ID:          "sess-many-msgs",
		CLI:         connectors.CLIClaude,
		ProjectPath: "/tmp/proj",
		StartedAt:   1000,
		LastMsgAt:   30000,
		Status:      connectors.SessionStatusIdle,
	}
	if err := store.UpsertSession(context.Background(), db, sess); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Insert 25 messages.
	for i := 0; i < 25; i++ {
		m := &connectors.Message{
			ID:        fmt.Sprintf("msg-%03d", i),
			SessionID: "sess-many-msgs",
			CLI:       connectors.CLIClaude,
			Role:      connectors.RoleUser,
			Content:   fmt.Sprintf("message %d", i),
			Ts:        int64(1000 + i*100),
		}
		if err := store.InsertMessage(context.Background(), db, m); err != nil {
			t.Fatalf("insert message: %v", err)
		}
	}

	router := newRestoreRouter(db)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/sessions/sess-many-msgs/restore")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body api.RestoreResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(body.Tail) != 20 {
		t.Errorf("expected 20 tail messages for 25-message session, got %d", len(body.Tail))
	}
	// Last message should be msg-024.
	if len(body.Tail) > 0 {
		last := body.Tail[len(body.Tail)-1]
		if last.ID != "msg-024" {
			t.Errorf("expected last tail message to be msg-024, got %q", last.ID)
		}
	}
}

// TestRestoreEndpoint_MissingID verifies 400 when id is empty.
// (chi routing provides the id, so we test by hitting the wrong route directly)

// TestRestoreEndpoint_ResumeCmdCodex verifies the resume_cmd for Codex sessions.
func TestRestoreEndpoint_ResumeCmdCodex(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)

	sess := &connectors.Session{
		ID:          "codex-sess",
		CLI:         connectors.CLICodex,
		ProjectPath: "/tmp/proj",
		StartedAt:   1000,
		LastMsgAt:   2000,
		Status:      connectors.SessionStatusIdle,
	}
	if err := store.UpsertSession(context.Background(), db, sess); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	router := newRestoreRouter(db)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/sessions/codex-sess/restore")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	var body api.RestoreResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if body.ResumeCmd != "codex resume --last" {
		t.Errorf("unexpected resume_cmd: %q", body.ResumeCmd)
	}
}

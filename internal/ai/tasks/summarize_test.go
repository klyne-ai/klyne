package tasks_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/ai"
	"github.com/klyne-ai/klyne/internal/ai/tasks"
	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/store"
)

// openTestDB opens a fresh SQLite DB in a temp directory and registers a
// cleanup to close it. The database has all migrations applied.
func openTestDB(t *testing.T) (*store.DB, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, dbPath
}

// seedSession inserts a session row so message inserts don't fail the FK check.
func seedSession(t *testing.T, db *store.DB, sessionID string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UnixMilli()
	_, err := db.Write().ExecContext(ctx, `
		INSERT INTO sessions (id, cli, project_path, started_at, last_msg_at, raw_path)
		VALUES (?, 'claude', '/tmp/proj', ?, ?, '/tmp/proj/sess.jsonl')`,
		sessionID, now, now,
	)
	if err != nil {
		t.Fatalf("seedSession %q: %v", sessionID, err)
	}
}

// seedMessages inserts n messages for the session with predictable content.
func seedMessages(t *testing.T, db *store.DB, sessionID string, n int) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UnixMilli()
	for i := 0; i < n; i++ {
		msg := &connectors.Message{
			ID:        generateID(sessionID, i),
			SessionID: sessionID,
			CLI:       connectors.CLIClaude,
			Role:      connectors.RoleUser,
			Content:   "message content " + itoa(i),
			Ts:        now + int64(i),
		}
		if err := store.InsertMessage(ctx, db, msg); err != nil {
			t.Fatalf("seedMessages InsertMessage #%d: %v", i, err)
		}
	}
}

// fakeProvider is an in-package test double for ai.Provider.
type fakeProvider struct {
	// name is the provider identifier returned by Name().
	name string
	// reply is returned as ChatResponse.Text for every Chat call.
	reply string
	// err is returned by Chat when non-nil.
	err error
	// requests captures all ChatRequests for assertion.
	requests []ai.ChatRequest
	// delay makes Chat block for this duration (to test goroutine separation).
	delay time.Duration
}

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) Chat(_ context.Context, req ai.ChatRequest) (*ai.ChatResponse, error) {
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	f.requests = append(f.requests, req)
	if f.err != nil {
		return nil, f.err
	}
	return &ai.ChatResponse{Text: f.reply, Model: req.Model}, nil
}

func (f *fakeProvider) Embed(_ context.Context, _ ai.EmbedRequest) (*ai.EmbedResponse, error) {
	return nil, ai.ErrUnsupported
}

func (f *fakeProvider) Models() []string { return []string{"fake-model"} }

// generateID produces a deterministic message ID.
func generateID(sessionID string, i int) string {
	return sessionID + "-msg-" + itoa(i)
}

// itoa converts an int to string without importing strconv (avoids import cycle confusion).
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	buf := make([]byte, 0, 10)
	for i > 0 {
		buf = append([]byte{byte('0' + i%10)}, buf...)
		i /= 10
	}
	if neg {
		buf = append([]byte{'-'}, buf...)
	}
	return string(buf)
}

// ── Tests ──────────────────────────────────────────────────────────────────

// TestSummarize_PersistsAndReturns verifies that Summarize persists a summary
// and returns it with a non-zero Version.
func TestSummarize_PersistsAndReturns(t *testing.T) {
	db, _ := openTestDB(t)
	sessID := "sess-sum-001"
	seedSession(t, db, sessID)
	seedMessages(t, db, sessID, 50)

	fp := &fakeProvider{name: "fake", reply: "this is the summary"}
	ctx := context.Background()

	got, err := tasks.Summarize(ctx, db, fp, "fake-model", sessID, 50)
	if err != nil {
		t.Fatalf("Summarize() error: %v", err)
	}
	if got == nil {
		t.Fatal("Summarize() returned nil summary")
	}
	if got.Text != "this is the summary" {
		t.Errorf("Text = %q; want %q", got.Text, "this is the summary")
	}
	if got.Version < 1 {
		t.Errorf("Version = %d; want >= 1", got.Version)
	}
	if got.SessionID != sessID {
		t.Errorf("SessionID = %q; want %q", got.SessionID, sessID)
	}

	// Confirm it is persisted.
	stored, err := store.LatestSummary(ctx, db, sessID)
	if err != nil {
		t.Fatalf("LatestSummary after Summarize: %v", err)
	}
	if stored.Text != "this is the summary" {
		t.Errorf("stored Text = %q; want %q", stored.Text, "this is the summary")
	}
}

// TestSummarize_RollingPriorSummary verifies that a second call to Summarize
// includes the prior summary in the prompt sent to the provider.
func TestSummarize_RollingPriorSummary(t *testing.T) {
	db, _ := openTestDB(t)
	sessID := "sess-sum-002"
	seedSession(t, db, sessID)
	seedMessages(t, db, sessID, 10)

	fp := &fakeProvider{name: "fake", reply: "rolling summary v1"}
	ctx := context.Background()

	// First call — no prior summary.
	_, err := tasks.Summarize(ctx, db, fp, "fake-model", sessID, 10)
	if err != nil {
		t.Fatalf("first Summarize: %v", err)
	}

	fp.reply = "rolling summary v2"
	_, err = tasks.Summarize(ctx, db, fp, "fake-model", sessID, 10)
	if err != nil {
		t.Fatalf("second Summarize: %v", err)
	}

	// The second request should contain the prior summary text.
	if len(fp.requests) < 2 {
		t.Fatalf("expected at least 2 provider requests, got %d", len(fp.requests))
	}
	secondReq := fp.requests[1]

	found := false
	for _, m := range secondReq.Messages {
		if containsStr(m.Content, "rolling summary v1") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("second Summarize prompt did not include prior summary text; messages: %+v", secondReq.Messages)
	}
}

// TestSummarize_ProviderError_Propagates verifies that when the provider
// returns an error, Summarize returns the error and does not persist a summary.
func TestSummarize_ProviderError_Propagates(t *testing.T) {
	db, _ := openTestDB(t)
	sessID := "sess-sum-003"
	seedSession(t, db, sessID)
	seedMessages(t, db, sessID, 5)

	provErr := errors.New("provider 500")
	fp := &fakeProvider{name: "fake", err: provErr}
	ctx := context.Background()

	_, err := tasks.Summarize(ctx, db, fp, "fake-model", sessID, 5)
	if err == nil {
		t.Fatal("Summarize() expected error, got nil")
	}

	// No summary should have been persisted.
	_, storeErr := store.LatestSummary(ctx, db, sessID)
	if !errors.Is(storeErr, sql.ErrNoRows) {
		t.Errorf("expected sql.ErrNoRows after failed Summarize, got: %v", storeErr)
	}
}

// TestSummarize_EmptySession verifies that Summarize works even with no
// messages in the session.
func TestSummarize_EmptySession(t *testing.T) {
	db, _ := openTestDB(t)
	sessID := "sess-sum-004"
	seedSession(t, db, sessID)

	fp := &fakeProvider{name: "fake", reply: "empty session summary"}
	ctx := context.Background()

	got, err := tasks.Summarize(ctx, db, fp, "fake-model", sessID, 50)
	if err != nil {
		t.Fatalf("Summarize() on empty session: %v", err)
	}
	if got.Text != "empty session summary" {
		t.Errorf("Text = %q; want %q", got.Text, "empty session summary")
	}
}

// containsStr is a simple substring check used in tests.
func containsStr(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

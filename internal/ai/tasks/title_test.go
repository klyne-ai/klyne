package tasks_test

import (
	"context"
	"errors"
	"testing"

	"github.com/klyne-ai/klyne/internal/ai/tasks"
)

// TestTitle_HappyPath verifies that Title returns the provider's trimmed
// response text when messages are present.
func TestTitle_HappyPath(t *testing.T) {
	db, _ := openTestDB(t)
	sessID := "sess-title-001"
	seedSession(t, db, sessID)
	seedMessages(t, db, sessID, 3)

	fp := &fakeProvider{name: "fake", reply: "  Debug memory leak issue  "}
	ctx := context.Background()

	title, err := tasks.Title(ctx, db, fp, "fake-model", sessID)
	if err != nil {
		t.Fatalf("Title() error: %v", err)
	}
	if title != "Debug memory leak issue" {
		t.Errorf("Title = %q; want %q", title, "Debug memory leak issue")
	}
}

// TestTitle_EmptySession verifies that Title still calls the provider and
// returns a title even when there are no messages.
func TestTitle_EmptySession(t *testing.T) {
	db, _ := openTestDB(t)
	sessID := "sess-title-002"
	seedSession(t, db, sessID)

	fp := &fakeProvider{name: "fake", reply: "New Session"}
	ctx := context.Background()

	title, err := tasks.Title(ctx, db, fp, "fake-model", sessID)
	if err != nil {
		t.Fatalf("Title() error for empty session: %v", err)
	}
	if title != "New Session" {
		t.Errorf("Title = %q; want %q", title, "New Session")
	}
}

// TestTitle_ProviderError_Propagates verifies that a provider error surfaces
// as a Title error.
func TestTitle_ProviderError_Propagates(t *testing.T) {
	db, _ := openTestDB(t)
	sessID := "sess-title-003"
	seedSession(t, db, sessID)
	seedMessages(t, db, sessID, 2)

	fp := &fakeProvider{name: "fake", err: errors.New("provider error")}
	ctx := context.Background()

	_, err := tasks.Title(ctx, db, fp, "fake-model", sessID)
	if err == nil {
		t.Fatal("Title() expected error, got nil")
	}
}

// TestTitle_UsesSystemPrompt verifies that the prompt sent to the provider
// includes the title-generation system prompt.
func TestTitle_UsesSystemPrompt(t *testing.T) {
	db, _ := openTestDB(t)
	sessID := "sess-title-004"
	seedSession(t, db, sessID)
	seedMessages(t, db, sessID, 2)

	fp := &fakeProvider{name: "fake", reply: "Fix Auth Bug"}
	ctx := context.Background()

	_, err := tasks.Title(ctx, db, fp, "fake-model", sessID)
	if err != nil {
		t.Fatalf("Title(): %v", err)
	}
	if len(fp.requests) == 0 {
		t.Fatal("no provider requests captured")
	}
	req := fp.requests[0]
	if req.SystemPrompt == "" {
		t.Error("Title() sent no system prompt to provider")
	}
}

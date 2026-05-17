package mcpserver

import (
	"context"
	"testing"

	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/store"
)

func TestHandleGetSession_NotFound(t *testing.T) {
	withFakeHome(t)
	_ = withBootstrapDB(t)

	_, out, err := HandleGetSession(context.Background(), nil, GetSessionInput{SessionID: "does-not-exist"})
	if err != nil {
		t.Fatalf("HandleGetSession: %v", err)
	}
	if out.Found {
		t.Errorf("Found = true, want false for missing session")
	}
	if out.Reason == "" {
		t.Errorf("Reason should explain why the session was not found")
	}
}

func TestHandleGetSession_ReturnsMessagesNewestFirst(t *testing.T) {
	withFakeHome(t)
	db := withBootstrapDB(t)
	ctx := context.Background()

	// Seed a session + 3 messages.
	sess := &connectors.Session{
		ID:          "sess-fetch",
		CLI:         connectors.CLIClaude,
		ProjectPath: "/tmp/proj",
		StartedAt:   1000,
		LastMsgAt:   3000,
		Status:      connectors.SessionStatusActive,
	}
	if err := store.UpsertSession(ctx, db, sess); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	for i, ts := range []int64{1000, 2000, 3000} {
		m := &connectors.Message{
			ID:        string(rune('a' + i)),
			SessionID: "sess-fetch",
			Role:      connectors.Role("user"),
			Content:   string(rune('a' + i)),
			Ts:        ts,
		}
		if err := store.InsertMessage(ctx, db, m); err != nil {
			t.Fatalf("insert msg %d: %v", i, err)
		}
	}

	_, out, err := HandleGetSession(context.Background(), nil, GetSessionInput{SessionID: "sess-fetch", Limit: 10, Order: "desc"})
	if err != nil {
		t.Fatalf("HandleGetSession: %v", err)
	}
	if !out.Found {
		t.Fatalf("Found = false, want true")
	}
	if out.SessionID != "sess-fetch" {
		t.Errorf("SessionID = %q, want sess-fetch", out.SessionID)
	}
	if len(out.Messages) != 3 {
		t.Fatalf("Messages len = %d, want 3", len(out.Messages))
	}
	if out.Messages[0].Ts != 3000 {
		t.Errorf("desc order — Messages[0].Ts = %d, want 3000", out.Messages[0].Ts)
	}
}

func TestHandleGetSession_LimitClampedToMax(t *testing.T) {
	withFakeHome(t)
	_ = withBootstrapDB(t)
	_, out, err := HandleGetSession(context.Background(), nil, GetSessionInput{SessionID: "anything", Limit: 999_999})
	if err != nil {
		t.Fatalf("HandleGetSession: %v", err)
	}
	if out.Limit != getSessionMaxLimit {
		t.Errorf("Limit = %d, want clamp to %d", out.Limit, getSessionMaxLimit)
	}
}

func TestHandleGetSession_SinceFilter(t *testing.T) {
	withFakeHome(t)
	db := withBootstrapDB(t)
	ctx := context.Background()

	sess := &connectors.Session{
		ID:          "sess-since",
		CLI:         connectors.CLIClaude,
		ProjectPath: "/tmp/proj",
		StartedAt:   1000,
		LastMsgAt:   3000,
		Status:      connectors.SessionStatusActive,
	}
	if err := store.UpsertSession(ctx, db, sess); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	for i, ts := range []int64{1000, 2000, 3000} {
		m := &connectors.Message{
			ID:        string(rune('a' + i)),
			SessionID: "sess-since",
			Role:      connectors.Role("user"),
			Content:   string(rune('a' + i)),
			Ts:        ts,
		}
		if err := store.InsertMessage(ctx, db, m); err != nil {
			t.Fatalf("insert msg %d: %v", i, err)
		}
	}

	// Since=2000 should drop the ts=1000 message.
	_, out, err := HandleGetSession(context.Background(), nil, GetSessionInput{
		SessionID: "sess-since",
		Limit:     10,
		Since:     2000,
	})
	if err != nil {
		t.Fatalf("HandleGetSession: %v", err)
	}
	if len(out.Messages) != 2 {
		t.Fatalf("Messages len = %d, want 2 (Since filter should drop ts=1000)", len(out.Messages))
	}
	for _, m := range out.Messages {
		if m.Ts < 2000 {
			t.Errorf("Since filter leaked a message with Ts=%d", m.Ts)
		}
	}
}

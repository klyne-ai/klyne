package otelexport

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/cost"
	"github.com/klyne-ai/klyne/internal/store"
)

func openDB(t *testing.T) *store.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "otel.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestEmit_OneSpanPerAssistantMessage(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	engine, err := cost.New(config.Defaults())
	if err != nil {
		t.Fatalf("cost engine: %v", err)
	}

	s := &connectors.Session{
		ID: "sess-1", CLI: connectors.CLIClaude, ProjectPath: "/proj",
		StartedAt: 1, LastMsgAt: 1, Status: connectors.SessionStatusActive,
		Model: "claude-sonnet-4-5", RawPath: "/tmp/x.jsonl",
	}
	if err := store.UpsertSession(ctx, db, s); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	rows := []*connectors.Message{
		{ID: "u1", SessionID: s.ID, CLI: connectors.CLIClaude, Role: connectors.RoleUser, Content: "hi", Ts: 1_000},
		{ID: "a1", SessionID: s.ID, CLI: connectors.CLIClaude, Role: connectors.RoleAssistant, TokensIn: 1000, TokensOut: 50, CachedReadTokens: 200, Model: "claude-sonnet-4-5", Ts: 1_100},
		{ID: "u2", SessionID: s.ID, CLI: connectors.CLIClaude, Role: connectors.RoleUser, Content: "more", Ts: 2_000},
		{ID: "a2", SessionID: s.ID, CLI: connectors.CLIClaude, Role: connectors.RoleAssistant, TokensIn: 2000, TokensOut: 100, CachedReadTokens: 800, Model: "claude-sonnet-4-5", Ts: 2_100},
	}
	for _, m := range rows {
		if err := store.InsertMessage(ctx, db, m); err != nil {
			t.Fatalf("insert %s: %v", m.ID, err)
		}
	}

	var buf bytes.Buffer
	n, err := Emit(ctx, db, engine, &buf, Filter{})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if n != 2 {
		t.Errorf("got %d spans; want 2 (one per assistant message)", n)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 JSON lines, got %d", len(lines))
	}
	var span Span
	if err := json.Unmarshal([]byte(lines[0]), &span); err != nil {
		t.Fatalf("first line not valid JSON: %v\n%s", err, lines[0])
	}
	if span.Name != "gen_ai.completion" {
		t.Errorf("name = %q", span.Name)
	}
	attr := span.Attributes
	if got, _ := attr["gen_ai.request.model"].(string); got != "claude-sonnet-4-5" {
		t.Errorf("model attribute = %v", attr["gen_ai.request.model"])
	}
	if got, _ := attr["klyne.session_id"].(string); got != "sess-1" {
		t.Errorf("session_id attribute = %v", attr["klyne.session_id"])
	}
	// Resource block should carry service.name.
	if got, _ := span.Resource["service.name"].(string); got != "klyne" {
		t.Errorf("service.name = %v", span.Resource["service.name"])
	}
}

func TestEmit_RedactPaths(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	engine, _ := cost.New(config.Defaults())

	const full = "/Users/alice/work/secret-internal-project"
	s := &connectors.Session{
		ID: "s-redact", CLI: connectors.CLIClaude, ProjectPath: full,
		StartedAt: 1, LastMsgAt: 1, Status: connectors.SessionStatusActive,
		Model: "claude-sonnet-4-5", RawPath: "/tmp/r",
	}
	if err := store.UpsertSession(ctx, db, s); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := store.InsertMessage(ctx, db, &connectors.Message{
		ID: "a", SessionID: s.ID, CLI: connectors.CLIClaude,
		Role: connectors.RoleAssistant, TokensIn: 100, Model: "claude-sonnet-4-5", Ts: 100,
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Redacted (default callers should use): basename only, no full path,
	// plus a stable project_hash.
	var buf bytes.Buffer
	if _, err := Emit(ctx, db, engine, &buf, Filter{RedactPaths: true}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if strings.Contains(buf.String(), full) {
		t.Errorf("redacted output leaked full path:\n%s", buf.String())
	}
	var span Span
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &span); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got, _ := span.Attributes["klyne.project_path"].(string); got != "secret-internal-project" {
		t.Errorf("project_path = %q; want basename only", got)
	}
	if got, _ := span.Attributes["klyne.project_hash"].(string); got != shortHashPath(full) {
		t.Errorf("project_hash = %q; want %q", got, shortHashPath(full))
	}

	// Not redacted: full path retained, no hash.
	var buf2 bytes.Buffer
	if _, err := Emit(ctx, db, engine, &buf2, Filter{RedactPaths: false}); err != nil {
		t.Fatalf("Emit raw: %v", err)
	}
	if !strings.Contains(buf2.String(), full) {
		t.Errorf("raw output should contain full path:\n%s", buf2.String())
	}
	var span2 Span
	_ = json.Unmarshal([]byte(strings.TrimSpace(buf2.String())), &span2)
	if _, ok := span2.Attributes["klyne.project_hash"]; ok {
		t.Error("raw output should not carry klyne.project_hash")
	}
}

func TestDeriveIDs_Deterministic(t *testing.T) {
	a := deriveTraceID("sess-A")
	b := deriveTraceID("sess-A")
	if a != b {
		t.Errorf("trace id non-deterministic: %s vs %s", a, b)
	}
	if len(a) != 32 {
		t.Errorf("trace id length = %d; want 32 hex chars", len(a))
	}
	if deriveTraceID("sess-A") == deriveTraceID("sess-B") {
		t.Error("trace id should differ for different sessions")
	}
	if len(deriveSpanID("m-1")) != 16 {
		t.Errorf("span id length wrong")
	}
}

func TestEmit_RespectsSinceMs(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	engine, _ := cost.New(config.Defaults())

	s := &connectors.Session{
		ID: "s1", CLI: connectors.CLIClaude, ProjectPath: "/p",
		StartedAt: 1, LastMsgAt: 1, Status: connectors.SessionStatusActive,
		Model: "claude-sonnet-4-5", RawPath: "/tmp/y",
	}
	_ = store.UpsertSession(ctx, db, s)
	_ = store.InsertMessage(ctx, db, &connectors.Message{
		ID: "old", SessionID: s.ID, CLI: connectors.CLIClaude,
		Role: connectors.RoleAssistant, TokensIn: 100, Model: "claude-sonnet-4-5", Ts: 100,
	})
	_ = store.InsertMessage(ctx, db, &connectors.Message{
		ID: "new", SessionID: s.ID, CLI: connectors.CLIClaude,
		Role: connectors.RoleAssistant, TokensIn: 200, Model: "claude-sonnet-4-5", Ts: 500,
	})

	var buf bytes.Buffer
	n, err := Emit(ctx, db, engine, &buf, Filter{SinceMs: 200})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if n != 1 {
		t.Errorf("got %d spans; want 1 (since=200 should skip ts=100)", n)
	}
}

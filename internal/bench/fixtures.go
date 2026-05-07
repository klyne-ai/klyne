// Package bench provides performance benchmarks for all spec §12 budgets.
//
// Bench harness helpers live in fixtures.go and runner.go; the actual
// Go benchmark functions live in *_test.go files so they integrate with
// `go test -bench`. Every benchmark is also callable as a plain test
// (without -bench) so `go test ./internal/bench/...` passes in CI.
//
// # Generating fixtures
//
// SeedStore creates an in-memory SQLite database (via a tempdir), applies
// all migrations, upserts a synthetic session, and inserts N synthetic
// messages. It exercises the exact production code path (store.InsertMessage)
// so the bench measures real write throughput.
//
// # Running benches interactively
//
//	go test -bench=. -benchmem -benchtime=10s -count=1 ./internal/bench/...
//
// In CI the benchtime is shorter (2x — 2 iterations) to keep the job under
// the 300s timeout:
//
//	go test -bench=. -benchmem -benchtime=2x -count=1 -timeout 300s ./internal/bench/...
package bench

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/mohitpatell/agentdeck/internal/connectors"
	"github.com/mohitpatell/agentdeck/internal/store"
)

// wordBuckets are disjoint vocabulary groups used to generate realistic message
// content. Each message picks words from ONE bucket, ensuring that search terms
// from one bucket do NOT appear in messages from other buckets. This prevents
// every word from appearing in every message (which would defeat FTS5 BM25
// ranking and produce unrealistic latencies for high-frequency terms).
//
// In production, a Claude session has distinct topics per turn: e.g. one turn
// discusses "authentication", another "database schema". Our buckets model this.
var wordBuckets = [][]string{
	{"hello", "world", "greet", "welcome", "salutation", "morning", "evening"},
	{"agentdeck", "dashboard", "session", "monitor", "viewer", "panel"},
	{"benchmark", "performance", "latency", "throughput", "measure", "profile"},
	{"sqlite", "database", "schema", "migration", "index", "query", "table"},
	{"fts5", "fulltext", "search", "rank", "bm25", "snippet", "match"},
	{"message", "content", "payload", "body", "text", "response", "request"},
	{"model", "tokens", "completion", "prompt", "inference", "generation"},
	{"cost", "pricing", "billing", "usage", "budget", "spend", "credit"},
	{"assistant", "claude", "codex", "openai", "gemini", "anthropic"},
	{"user", "system", "tool", "result", "output", "input", "command"},
}

// GenerateMessages creates n synthetic *connectors.Message values for the
// given sessionID. Messages have alternating user/assistant roles, realistic
// content lengths, and monotonically increasing timestamps starting from a
// fixed epoch so seeds are deterministic.
//
// Each message draws its vocabulary from a single word bucket (selected by
// index modulo len(wordBuckets)) so search terms from one bucket do not
// appear in messages from other buckets. This produces realistic FTS5
// selectivity and BM25 rankings.
//
// Each message has ~200 bytes of content on average, matching a typical
// Claude Code JSONL line size used for the JSONL-vs-DB disk ratio test.
func GenerateMessages(n int, sessionID string) []*connectors.Message {
	msgs := make([]*connectors.Message, n)
	baseTS := int64(1700000000000) // arbitrary fixed epoch-ms

	for i := 0; i < n; i++ {
		role := connectors.RoleUser
		if i%2 == 1 {
			role = connectors.RoleAssistant
		}

		// Select a vocabulary bucket by message index so adjacent messages
		// use different vocabulary (mimicking real conversational turns).
		bucket := wordBuckets[i%len(wordBuckets)]
		content := buildContent(bucket, i, 200)

		msgs[i] = &connectors.Message{
			ID:          fmt.Sprintf("msg-%s-%07d", sessionID[:8], i),
			SessionID:   sessionID,
			CLI:         connectors.CLIClaude,
			ProjectPath: "/home/user/project",
			Role:        role,
			Content:     content,
			TokensIn:    int64(50 + (i % 200)),
			TokensOut:   int64(100 + (i % 500)),
			CostUSD:     float64(i%10) * 0.0001,
			Model:       "claude-sonnet-4-5",
			Ts:          baseTS + int64(i)*1000, // 1 second apart
			ParentUUID:  "",
		}
		if i > 0 {
			msgs[i].ParentUUID = msgs[i-1].ID
		}
	}
	return msgs
}

// buildContent constructs a string of approximately targetBytes length by
// cycling over words starting at the given offset index.
func buildContent(words []string, offset, targetBytes int) string {
	buf := make([]byte, 0, targetBytes+20)
	for len(buf) < targetBytes {
		word := words[(offset+len(buf))%len(words)]
		buf = append(buf, word...)
		buf = append(buf, ' ')
	}
	return string(buf[:targetBytes])
}

// SeedStore creates a real SQLite database in dir, applies all migrations,
// upserts a single synthetic session, and inserts n messages. Returns the
// opened *store.DB ready for use.
//
// The caller must call db.Close() when done (typically via b.Cleanup or
// defer). Benchmarks pass b.TempDir() as dir so the database is cleaned up
// automatically.
//
// SeedStore calls b.Fatal on any error so the benchmark aborts cleanly
// rather than producing misleading results.
func SeedStore(b testing.TB, dir string, n int) *store.DB {
	b.Helper()

	dbPath := dir + "/bench.db"
	db, err := store.Open(dbPath)
	if err != nil {
		b.Fatalf("SeedStore: open store: %v", err)
	}

	ctx := context.Background()
	sessionID := "bench-session-0001"

	// Upsert the parent session first (FK constraint).
	sess := &connectors.Session{
		ID:          sessionID,
		CLI:         connectors.CLIClaude,
		ProjectPath: "/bench/project",
		StartedAt:   time.Now().UnixMilli(),
		LastMsgAt:   time.Now().UnixMilli(),
		Status:      connectors.SessionStatusActive,
		RawPath:     "/bench/project/.claude/sessions/bench.jsonl",
	}
	if err := store.UpsertSession(ctx, db, sess); err != nil {
		_ = db.Close()
		b.Fatalf("SeedStore: upsert session: %v", err)
	}

	msgs := GenerateMessages(n, sessionID)
	for _, m := range msgs {
		if err := store.InsertMessage(ctx, db, m); err != nil {
			_ = db.Close()
			b.Fatalf("SeedStore: insert message %q: %v", m.ID, err)
		}
	}

	return db
}

// SeedStoreInTx inserts n messages in a single transaction for maximum
// throughput. Used by BenchmarkIngestThroughput to measure raw write speed
// without per-message transaction overhead.
//
// The session must already exist in db before calling.
func SeedStoreInTx(b testing.TB, db *store.DB, sessionID string, msgs []*connectors.Message) {
	b.Helper()
	ctx := context.Background()

	tx, err := db.Write().BeginTx(ctx, nil)
	if err != nil {
		b.Fatalf("SeedStoreInTx: begin tx: %v", err)
	}

	const insertMsg = `
INSERT OR IGNORE INTO messages
    (id, session_id, parent_uuid, role, content,
     tool_calls_json, tool_results_json,
     tokens_in, tokens_out, cost_usd, model, ts)
VALUES
    (?, ?, ?, ?, ?,
     ?, ?,
     ?, ?, ?, ?, ?)`

	for _, m := range msgs {
		if _, err := tx.ExecContext(ctx, insertMsg,
			m.ID, m.SessionID, nullableStr(m.ParentUUID),
			string(m.Role), m.Content,
			"[]", "[]",
			m.TokensIn, m.TokensOut, m.CostUSD,
			nullableStr(m.Model), m.Ts,
		); err != nil {
			_ = tx.Rollback()
			b.Fatalf("SeedStoreInTx: insert msg: %v", err)
		}
	}

	// Update session counters once in bulk (simulating a backfill).
	if _, err := tx.ExecContext(ctx,
		`UPDATE sessions SET msg_count = msg_count + ? WHERE id = ?`,
		len(msgs), sessionID,
	); err != nil {
		_ = tx.Rollback()
		b.Fatalf("SeedStoreInTx: update session: %v", err)
	}

	if err := tx.Commit(); err != nil {
		b.Fatalf("SeedStoreInTx: commit: %v", err)
	}
}

// nullableStr mirrors the store-internal helper so fixtures.go compiles
// without importing the unexported symbol.
func nullableStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

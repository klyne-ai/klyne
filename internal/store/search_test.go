package store_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mohitpatell/agentdeck/internal/store"
)

// openTestDB is a helper that opens a fresh in-memory-like database using a
// temp file. It also inserts a dummy session row so that the messages foreign
// key constraint is satisfied.
func openSearchTestDB(t *testing.T) (*store.DB, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "search_test.db")

	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Insert a reusable session row to satisfy FK constraints.
	sessID := "sess-search-001"
	ctx := context.Background()
	_, err = db.Write().ExecContext(ctx, `
		INSERT INTO sessions (id, cli, project_path, started_at, last_msg_at, raw_path)
		VALUES (?, 'claude', '/tmp/proj', ?, ?, '/tmp/proj/sess.jsonl')`,
		sessID, time.Now().UnixMilli(), time.Now().UnixMilli(),
	)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}
	return db, sessID
}

// seedMessage inserts a message row directly via raw SQL; the FTS trigger in
// 002_fts.sql will automatically mirror it into messages_fts.
func seedMessage(t *testing.T, db *store.DB, id, sessID, role, content string) {
	t.Helper()
	ctx := context.Background()
	_, err := db.Write().ExecContext(ctx,
		`INSERT INTO messages (id, session_id, role, content, ts) VALUES (?, ?, ?, ?, ?)`,
		id, sessID, role, content, time.Now().UnixMilli(),
	)
	if err != nil {
		t.Fatalf("seedMessage(%s): %v", id, err)
	}
}

// TestSearch_RanksNeedle seeds 100 messages with one containing a rare phrase
// and asserts it ranks #1.
func TestSearch_RanksNeedle(t *testing.T) {
	db, sessID := openSearchTestDB(t)
	ctx := context.Background()

	const needle = "xyloquartz frobnicate zyzzyva"

	// Seed 100 generic messages.
	for i := 0; i < 99; i++ {
		seedMessage(t, db, fmt.Sprintf("msg-gen-%03d", i), sessID, "user",
			fmt.Sprintf("This is a generic message number %d about everyday topics.", i))
	}
	// Seed the needle message.
	seedMessage(t, db, "msg-needle", sessID, "user", needle)

	hits, err := store.Search(ctx, db, "xyloquartz frobnicate zyzzyva", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("expected at least one hit, got none")
	}
	if hits[0].MessageID != "msg-needle" {
		t.Errorf("top hit = %q; want %q", hits[0].MessageID, "msg-needle")
	}
}

// TestSearch_EmptyQuery verifies that an empty query returns an empty slice
// without error.
func TestSearch_EmptyQuery(t *testing.T) {
	db, _ := openSearchTestDB(t)
	ctx := context.Background()

	hits, err := store.Search(ctx, db, "", 10)
	if err != nil {
		t.Fatalf("Search with empty query returned error: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("expected 0 hits for empty query, got %d", len(hits))
	}
}

// TestSearch_WhitespaceQuery verifies that a whitespace-only query behaves the
// same as an empty query.
func TestSearch_WhitespaceQuery(t *testing.T) {
	db, _ := openSearchTestDB(t)
	ctx := context.Background()

	hits, err := store.Search(ctx, db, "   \t  ", 10)
	if err != nil {
		t.Fatalf("Search with whitespace query returned error: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("expected 0 hits for whitespace query, got %d", len(hits))
	}
}

// TestSearch_LimitRespected verifies that Search honours the limit parameter.
func TestSearch_LimitRespected(t *testing.T) {
	db, sessID := openSearchTestDB(t)
	ctx := context.Background()

	// Seed 50 messages all containing the search term.
	for i := 0; i < 50; i++ {
		seedMessage(t, db, fmt.Sprintf("msg-limit-%03d", i), sessID, "user",
			fmt.Sprintf("common word apple banana cherry %d", i))
	}

	const limit = 10
	hits, err := store.Search(ctx, db, "apple banana cherry", limit)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) > limit {
		t.Errorf("got %d hits; want <= %d", len(hits), limit)
	}
}

// TestSearch_SpecialChars verifies that queries containing FTS5 operators and
// special characters (', ", *, OR, AND, NOT, NEAR) do not cause errors.
func TestSearch_SpecialChars(t *testing.T) {
	db, sessID := openSearchTestDB(t)
	ctx := context.Background()

	// Seed a message with content that includes some of the special chars.
	seedMessage(t, db, "msg-special-1", sessID, "user",
		`It's a "wonderful" life with operators like OR and NOT`)
	seedMessage(t, db, "msg-special-2", sessID, "user",
		"Another message about NEAR operations and wildcards")

	queries := []string{
		`it's`,
		`"wonderful"`,
		`OR`,
		`AND NOT`,
		`NEAR`,
		`*`,
		`word* OR phrase`,
		`single'quote`,
		`double"quote`,
		`"phrase with OR"`,
	}

	for _, q := range queries {
		t.Run(fmt.Sprintf("q=%s", q), func(t *testing.T) {
			_, err := store.Search(ctx, db, q, 10)
			if err != nil {
				t.Errorf("Search(%q) returned unexpected error: %v", q, err)
			}
		})
	}
}

// TestSearch_Snippet verifies that matched messages have a non-empty snippet.
func TestSearch_Snippet(t *testing.T) {
	db, sessID := openSearchTestDB(t)
	ctx := context.Background()

	seedMessage(t, db, "msg-snip-1", sessID, "assistant",
		"The magnificent elephant roams the savanna gracefully at dawn")

	hits, err := store.Search(ctx, db, "magnificent elephant", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("expected at least one hit")
	}
	if strings.TrimSpace(hits[0].Snippet) == "" {
		t.Error("expected non-empty snippet for matched message")
	}
}

// TestSearch_ReturnsFields verifies that all SearchHit fields are populated.
func TestSearch_ReturnsFields(t *testing.T) {
	db, sessID := openSearchTestDB(t)
	ctx := context.Background()

	seedMessage(t, db, "msg-fields-1", sessID, "user",
		"unique phrase for field verification quasar")

	hits, err := store.Search(ctx, db, "quasar", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("expected at least one hit")
	}
	h := hits[0]
	if h.MessageID == "" {
		t.Error("MessageID is empty")
	}
	if h.SessionID != sessID {
		t.Errorf("SessionID = %q; want %q", h.SessionID, sessID)
	}
	if h.TS == 0 {
		t.Error("TS is zero")
	}
	if h.Role == "" {
		t.Error("Role is empty")
	}
}

// TestSearch_RankOrdering verifies that results are ordered rank ASC (best
// match first — lower bm25 score is better in SQLite FTS5).
func TestSearch_RankOrdering(t *testing.T) {
	db, sessID := openSearchTestDB(t)
	ctx := context.Background()

	// Best match: contains the term 3 times.
	seedMessage(t, db, "msg-rank-best", sessID, "user",
		"canary canary canary is a special test bird used for mining safety")
	// Weaker match: contains it once.
	seedMessage(t, db, "msg-rank-weak", sessID, "user",
		"canary is also a songbird")

	hits, err := store.Search(ctx, db, "canary", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) < 2 {
		t.Fatalf("expected >= 2 hits, got %d", len(hits))
	}
	// Ranks should be non-decreasing (ascending).
	for i := 1; i < len(hits); i++ {
		if hits[i].Rank < hits[i-1].Rank {
			t.Errorf("hits not ordered by rank ASC: hit[%d].Rank=%f < hit[%d].Rank=%f",
				i, hits[i].Rank, i-1, hits[i-1].Rank)
		}
	}
}

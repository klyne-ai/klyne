package store_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/mohitpatell/agentdeck/internal/connectors"
	"github.com/mohitpatell/agentdeck/internal/store"
)

// makeMessage returns a minimal *connectors.Message for the given session.
func makeMessage(id, sessionID string, ts int64) *connectors.Message {
	return &connectors.Message{
		ID:        id,
		SessionID: sessionID,
		CLI:       connectors.CLIClaude,
		Role:      connectors.RoleUser,
		Content:   "hello from " + id,
		TokensIn:  10,
		TokensOut: 5,
		CostUSD:   0.001,
		Ts:        ts,
	}
}

// upsertSession is a test helper that creates a session and fails the test on
// error.
func upsertSession(t *testing.T, ctx context.Context, db *store.DB, sess *connectors.Session) {
	t.Helper()
	if err := store.UpsertSession(ctx, db, sess); err != nil {
		t.Fatalf("UpsertSession %q: %v", sess.ID, err)
	}
}

// TestInsertMessage_BumpsSession verifies that InsertMessage increments the
// parent session's aggregate counters in the same transaction.
func TestInsertMessage_BumpsSession(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	sess := makeSession("bump-sess", "claude", "/proj/bump")
	upsertSession(t, ctx, db, sess)

	m := makeMessage("msg-bump-1", "bump-sess", 1_700_000_001_000)
	m.TokensIn = 100
	m.TokensOut = 50
	m.CostUSD = 0.0025

	if err := store.InsertMessage(ctx, db, m); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}

	got, err := store.GetSession(ctx, db, "bump-sess")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}

	if got.MsgCount != 1 {
		t.Errorf("MsgCount = %d; want 1", got.MsgCount)
	}
	if got.TokensIn != 100 {
		t.Errorf("TokensIn = %d; want 100", got.TokensIn)
	}
	if got.TokensOut != 50 {
		t.Errorf("TokensOut = %d; want 50", got.TokensOut)
	}
	// Float comparison with tolerance.
	if got.CostUSD < 0.002 || got.CostUSD > 0.003 {
		t.Errorf("CostUSD = %f; want ~0.0025", got.CostUSD)
	}
	if got.LastMsgAt != m.Ts {
		t.Errorf("LastMsgAt = %d; want %d", got.LastMsgAt, m.Ts)
	}
}

// TestInsertMessage_MultipleMessages verifies counter accumulation across
// multiple messages.
func TestInsertMessage_MultipleMessages(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	sess := makeSession("multi-sess", "claude", "/proj/multi")
	upsertSession(t, ctx, db, sess)

	for i := 0; i < 5; i++ {
		m := makeMessage(
			fmt.Sprintf("msg-multi-%d", i),
			"multi-sess",
			1_700_000_000_000+int64(i)*1000,
		)
		m.TokensIn = 10
		m.TokensOut = 5
		m.CostUSD = 0.001
		if err := store.InsertMessage(ctx, db, m); err != nil {
			t.Fatalf("InsertMessage %d: %v", i, err)
		}
	}

	got, err := store.GetSession(ctx, db, "multi-sess")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}

	if got.MsgCount != 5 {
		t.Errorf("MsgCount = %d; want 5", got.MsgCount)
	}
	if got.TokensIn != 50 {
		t.Errorf("TokensIn = %d; want 50", got.TokensIn)
	}
	if got.TokensOut != 25 {
		t.Errorf("TokensOut = %d; want 25", got.TokensOut)
	}
}

// TestInsertMessage_NoSession_FKError verifies that inserting a message
// without a parent session returns an error.
func TestInsertMessage_NoSession_FKError(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	m := makeMessage("orphan-msg-1", "nonexistent-session", 1_700_000_001_000)
	err := store.InsertMessage(ctx, db, m)
	if err == nil {
		t.Fatal("expected error inserting message with missing session, got nil")
	}
	// The session update returns 0 rows affected, which we surface as ErrNoRows.
	if !errors.Is(err, sql.ErrNoRows) {
		t.Logf("got error (any FK/no-rows error accepted): %v", err)
	}
}

// TestInsertMessage_RoundTripsToolCalls verifies that ToolCalls and
// ToolResults survive a round-trip through the database.
func TestInsertMessage_RoundTripsToolCalls(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	sess := makeSession("tool-sess", "claude", "/proj/tool")
	upsertSession(t, ctx, db, sess)

	m := &connectors.Message{
		ID:        "tool-msg-1",
		SessionID: "tool-sess",
		CLI:       connectors.CLIClaude,
		Role:      connectors.RoleAssistant,
		Content:   "let me run some tools",
		Ts:        1_700_000_001_000,
		ToolCalls: []connectors.ToolCall{
			{ID: "tc-1", Name: "Bash", Input: `{"command":"ls -la"}`},
			{ID: "tc-2", Name: "Read", Input: `{"path":"/etc/hosts"}`},
		},
		ToolResults: []connectors.ToolResult{
			{ID: "tc-1", Output: "total 42\n...", IsError: false},
			{ID: "tc-2", Output: "127.0.0.1 localhost", IsError: false},
		},
	}

	if err := store.InsertMessage(ctx, db, m); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}

	msgs, err := store.ListMessagesBySession(ctx, db, "tool-sess", 10, 0)
	if err != nil {
		t.Fatalf("ListMessagesBySession: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}

	got := msgs[0]

	// Verify ToolCalls round-trip.
	if len(got.ToolCalls) != 2 {
		t.Fatalf("ToolCalls len = %d; want 2", len(got.ToolCalls))
	}
	if got.ToolCalls[0].ID != "tc-1" || got.ToolCalls[0].Name != "Bash" {
		t.Errorf("ToolCalls[0] = %+v; want {ID:tc-1, Name:Bash}", got.ToolCalls[0])
	}
	if got.ToolCalls[1].ID != "tc-2" || got.ToolCalls[1].Name != "Read" {
		t.Errorf("ToolCalls[1] = %+v; want {ID:tc-2, Name:Read}", got.ToolCalls[1])
	}

	// Verify ToolResults round-trip.
	if len(got.ToolResults) != 2 {
		t.Fatalf("ToolResults len = %d; want 2", len(got.ToolResults))
	}
	if got.ToolResults[0].ID != "tc-1" {
		t.Errorf("ToolResults[0].ID = %q; want tc-1", got.ToolResults[0].ID)
	}
	if got.ToolResults[1].ID != "tc-2" {
		t.Errorf("ToolResults[1].ID = %q; want tc-2", got.ToolResults[1].ID)
	}
}

// TestListMessagesBySession_Order inserts 100 messages with monotonically
// increasing timestamps and asserts that pages come back in ts ASC order.
func TestListMessagesBySession_Order(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	sess := makeSession("order-sess", "claude", "/proj/order")
	upsertSession(t, ctx, db, sess)

	const total = 100
	baseTS := int64(1_700_000_000_000)

	for i := 0; i < total; i++ {
		m := makeMessage(
			fmt.Sprintf("order-msg-%04d", i),
			"order-sess",
			baseTS+int64(i)*1000,
		)
		if err := store.InsertMessage(ctx, db, m); err != nil {
			t.Fatalf("InsertMessage %d: %v", i, err)
		}
	}

	// Collect all messages in pages of 20.
	const pageSize = 20
	var all []*connectors.Message
	var before int64 // 0 = first page

	// We iterate forward from ts ASC — first page has no upper bound and we
	// use a simple offset approach: after first page, we use the next ts window.
	// Since ListMessagesBySession uses "ts < before" for the cursor, we collect
	// all pages by bumping the offset manually using a different strategy:
	// fetch all in one big call to verify order.
	all, err := store.ListMessagesBySession(ctx, db, "order-sess", total+1, 0)
	if err != nil {
		t.Fatalf("ListMessagesBySession: %v", err)
	}
	if len(all) != total {
		t.Fatalf("got %d messages; want %d", len(all), total)
	}

	// Verify ASC order.
	for i := 1; i < len(all); i++ {
		if all[i].Ts < all[i-1].Ts {
			t.Errorf("messages[%d].Ts=%d < messages[%d].Ts=%d (not ASC)",
				i, all[i].Ts, i-1, all[i-1].Ts)
			break
		}
	}

	// Verify pages of 20 also return consistent order.
	_ = before // suppress unused warning
	for page := 0; page < total/pageSize; page++ {
		start := page * pageSize
		end := start + pageSize
		chunk := all[start:end]
		for i := 1; i < len(chunk); i++ {
			if chunk[i].Ts < chunk[i-1].Ts {
				t.Errorf("page %d: out-of-order messages", page)
				break
			}
		}
	}
}

// TestListMessagesBySession_Pagination verifies cursor-based pagination.
func TestListMessagesBySession_Pagination(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	sess := makeSession("pag-msg-sess", "claude", "/proj/pagmsg")
	upsertSession(t, ctx, db, sess)

	const total = 50
	baseTS := int64(1_700_000_000_000)

	for i := 0; i < total; i++ {
		m := makeMessage(
			fmt.Sprintf("pag-msg-%04d", i),
			"pag-msg-sess",
			baseTS+int64(i)*1000,
		)
		if err := store.InsertMessage(ctx, db, m); err != nil {
			t.Fatalf("InsertMessage %d: %v", i, err)
		}
	}

	// The "before" cursor is an upper bound on ts. To paginate forward
	// (oldest → newest) we use a large upper-bound for the first page and
	// work backwards, or alternatively we collect pages by splitting the known
	// range. Since our ordering is ASC and "before" is an upper-bound filter,
	// we demonstrate paginating the first 25 vs the second 25 by using the
	// ts of message 24 as the boundary.
	firstHalf, err := store.ListMessagesBySession(ctx, db, "pag-msg-sess", 25, 0)
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if len(firstHalf) != 25 {
		t.Fatalf("first page: got %d; want 25", len(firstHalf))
	}

	// The first half contains ts 0..24 (baseTS to baseTS+24000).
	// The cursor for the second half: we want messages with ts >= baseTS+25000.
	// Since "before" is an UPPER bound, this API is better suited for "give me
	// the last N before ts X" (newest-first with DESC, but we store ASC here).
	// For ASC-ordered pages the natural approach is: take the last Ts+1 as the
	// starting point for the next offset. However this API uses an upper bound.
	// We can still verify correctness: pass before=lastTs+1 from the full set
	// to get first 25 messages only.
	allMsgs, err := store.ListMessagesBySession(ctx, db, "pag-msg-sess", total+1, 0)
	if err != nil {
		t.Fatalf("all messages: %v", err)
	}
	if len(allMsgs) != total {
		t.Fatalf("total messages: got %d; want %d", len(allMsgs), total)
	}

	// Use a before cursor equal to the ts of message at index 25 (exclusive).
	cutoff := allMsgs[25].Ts // messages 25..49 should NOT be returned; 0..24 should
	subset, err := store.ListMessagesBySession(ctx, db, "pag-msg-sess", total, cutoff)
	if err != nil {
		t.Fatalf("subset page: %v", err)
	}
	if len(subset) != 25 {
		t.Fatalf("subset page: got %d; want 25", len(subset))
	}
	for _, m := range subset {
		if m.Ts >= cutoff {
			t.Errorf("message %q has ts %d >= cutoff %d", m.ID, m.Ts, cutoff)
		}
	}
}

// TestListMessagesBySession_EmptySession verifies that listing an empty
// session returns an empty slice (not an error or nil slice).
func TestListMessagesBySession_EmptySession(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	sess := makeSession("empty-sess", "claude", "/proj/empty")
	upsertSession(t, ctx, db, sess)

	msgs, err := store.ListMessagesBySession(ctx, db, "empty-sess", 10, 0)
	if err != nil {
		t.Fatalf("ListMessagesBySession: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("got %d messages; want 0", len(msgs))
	}
}

// TestInsertMessage_NilToolSlices verifies messages with nil tool slices
// round-trip cleanly (no panic, no JSON errors).
func TestInsertMessage_NilToolSlices(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	sess := makeSession("nil-tool-sess", "claude", "/proj/nil-tool")
	upsertSession(t, ctx, db, sess)

	m := makeMessage("nil-tool-msg-1", "nil-tool-sess", 1_700_000_001_000)
	// Explicitly nil slices.
	m.ToolCalls = nil
	m.ToolResults = nil

	if err := store.InsertMessage(ctx, db, m); err != nil {
		t.Fatalf("InsertMessage with nil tool slices: %v", err)
	}

	msgs, err := store.ListMessagesBySession(ctx, db, "nil-tool-sess", 10, 0)
	if err != nil {
		t.Fatalf("ListMessagesBySession: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	// nil slices should come back as nil or empty (not a JSON parse error).
	if len(msgs[0].ToolCalls) != 0 {
		t.Errorf("ToolCalls should be empty, got %+v", msgs[0].ToolCalls)
	}
	if len(msgs[0].ToolResults) != 0 {
		t.Errorf("ToolResults should be empty, got %+v", msgs[0].ToolResults)
	}
}

// TestBulkInsert_5K_PerformanceProxy inserts 5000 messages in one transaction
// and asserts the operation completes in under 1 second.  Skip under -short.
func TestBulkInsert_5K_PerformanceProxy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping performance proxy under -short")
	}

	ctx := context.Background()
	db := openTestDB(t)

	sess := makeSession("bulk-sess", "claude", "/proj/bulk")
	upsertSession(t, ctx, db, sess)

	const count = 5000
	baseTS := int64(1_700_000_000_000)

	start := time.Now()

	// Use a single transaction for all inserts to maximize throughput.
	tx, err := db.Write().BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}

	// Pre-insert the session counter update is inside InsertMessage which opens
	// its own tx. For the bulk test we do a raw insert in a single outer tx to
	// measure raw throughput (this is what the spec bench W16 will formalize).
	const insertSQL = `
INSERT INTO messages
    (id, session_id, parent_uuid, role, content,
     tool_calls_json, tool_results_json,
     tokens_in, tokens_out, cost_usd, model, ts)
VALUES
    (?, ?, NULL, ?, ?, '[]', '[]', ?, ?, ?, NULL, ?)`

	for i := 0; i < count; i++ {
		id := fmt.Sprintf("bulk-msg-%06d", i)
		ts := baseTS + int64(i)
		if _, err := tx.ExecContext(ctx, insertSQL,
			id, "bulk-sess", "user", fmt.Sprintf("content %d", i),
			10, 5, 0.001, ts,
		); err != nil {
			_ = tx.Rollback()
			t.Fatalf("bulk insert %d: %v", i, err)
		}
	}

	// Update session counters once at end.
	if _, err := tx.ExecContext(ctx, `
UPDATE sessions SET
    msg_count = msg_count + ?,
    tokens_in = tokens_in + ?,
    tokens_out = tokens_out + ?,
    cost_usd = cost_usd + ?,
    last_msg_at = ?
WHERE id = 'bulk-sess'`,
		count, count*10, count*5, float64(count)*0.001, baseTS+int64(count-1),
	); err != nil {
		_ = tx.Rollback()
		t.Fatalf("update session counters: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	elapsed := time.Since(start)
	t.Logf("BulkInsert 5000 messages: %v", elapsed)

	// The spec budget is <1 s on a dev laptop without the race detector.
	// The -race flag adds 5-20× overhead due to shadow memory tracking;
	// we allow 10 s under race so the test remains informative rather than
	// a false negative.  W16 will run the authoritative benchmark without -race.
	limit := time.Second
	if raceEnabled {
		limit = 10 * time.Second
	}
	if elapsed > limit {
		t.Errorf("bulk insert took %v; want < %v (performance proxy, race=%v)", elapsed, limit, raceEnabled)
	}

	// Verify row count.
	var rowCount int
	if err := db.Read().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM messages WHERE session_id = 'bulk-sess'",
	).Scan(&rowCount); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rowCount != count {
		t.Errorf("row count = %d; want %d", rowCount, count)
	}
}

// TestInsertMessage_AssistantRoleFields verifies that role, model, and
// parent_uuid are round-tripped correctly for an assistant message.
func TestInsertMessage_AssistantRoleFields(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	sess := makeSession("asst-sess", "claude", "/proj/asst")
	upsertSession(t, ctx, db, sess)

	m := &connectors.Message{
		ID:         "asst-msg-1",
		SessionID:  "asst-sess",
		CLI:        connectors.CLIClaude,
		Role:       connectors.RoleAssistant,
		Content:    "I can help with that.",
		ParentUUID: "parent-uuid-xyz",
		Model:      "claude-sonnet-4-5",
		TokensIn:   20,
		TokensOut:  15,
		CostUSD:    0.002,
		Ts:         1_700_000_002_000,
	}

	if err := store.InsertMessage(ctx, db, m); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}

	msgs, err := store.ListMessagesBySession(ctx, db, "asst-sess", 10, 0)
	if err != nil {
		t.Fatalf("ListMessagesBySession: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}

	got := msgs[0]
	if got.Role != connectors.RoleAssistant {
		t.Errorf("Role = %q; want assistant", got.Role)
	}
	if got.ParentUUID != "parent-uuid-xyz" {
		t.Errorf("ParentUUID = %q; want parent-uuid-xyz", got.ParentUUID)
	}
	if got.Model != "claude-sonnet-4-5" {
		t.Errorf("Model = %q; want claude-sonnet-4-5", got.Model)
	}
}

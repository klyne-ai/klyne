package store_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/klyne-ai/klyne/internal/store"
)

func openShieldSnapshotsDB(t *testing.T) *store.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "shield.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestInsertAndGetShieldSnapshot(t *testing.T) {
	ctx := context.Background()
	db := openShieldSnapshotsDB(t)

	s := &store.ShieldSnapshot{
		SessionID:     "sess-xyz",
		DecisionsJSON: `["use postgres","avoid ORM"]`,
		OpenFilesJSON: `["main.go","store.go"]`,
		TurnsJSON:     `["turn1","turn2"]`,
		ToolChainID:   "chain-001",
		PreTokens:     42000,
		FillPct:       75.5,
		Blocked:       true,
		BlockReason:   "shield",
	}
	if err := store.InsertShieldSnapshot(ctx, db, s); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if s.ID == 0 {
		t.Fatal("expected non-zero ID after insert")
	}

	got, err := store.GetShieldSnapshot(ctx, db, s.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.SessionID != "sess-xyz" {
		t.Errorf("session_id mismatch: got %q", got.SessionID)
	}
	if got.DecisionsJSON != `["use postgres","avoid ORM"]` {
		t.Errorf("decisions_json mismatch: got %q", got.DecisionsJSON)
	}
	if got.FillPct != 75.5 {
		t.Errorf("fill_pct mismatch: got %f", got.FillPct)
	}
	if !got.Blocked {
		t.Error("expected Blocked=true")
	}
	if got.BlockReason != "shield" {
		t.Errorf("block_reason mismatch: got %q", got.BlockReason)
	}
}

func TestGetShieldSnapshot_NotFound(t *testing.T) {
	ctx := context.Background()
	db := openShieldSnapshotsDB(t)

	_, err := store.GetShieldSnapshot(ctx, db, 9999)
	if err == nil {
		t.Fatal("expected error for missing row")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestLatestShieldSnapshot_ReturnsNewest(t *testing.T) {
	ctx := context.Background()
	db := openShieldSnapshotsDB(t)

	for _, ts := range []int64{1000, 3000, 2000} {
		s := &store.ShieldSnapshot{
			SessionID: "sess-A",
			FillPct:   70.0,
			Ts:        ts,
		}
		if err := store.InsertShieldSnapshot(ctx, db, s); err != nil {
			t.Fatalf("insert ts=%d: %v", ts, err)
		}
	}

	got, err := store.LatestShieldSnapshot(ctx, db, "sess-A")
	if err != nil {
		t.Fatalf("LatestShieldSnapshot: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil snapshot")
	}
	// ts DESC → highest ts is 3000
	if got.Ts != 3000 {
		t.Errorf("expected ts=3000 (latest), got %d", got.Ts)
	}
}

func TestLatestShieldSnapshot_NoneReturnsNoRows(t *testing.T) {
	ctx := context.Background()
	db := openShieldSnapshotsDB(t)

	_, err := store.LatestShieldSnapshot(ctx, db, "no-such-session")
	if err == nil {
		t.Fatal("expected error for missing session")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestListShieldSnapshots_OrderedByTsDesc(t *testing.T) {
	ctx := context.Background()
	db := openShieldSnapshotsDB(t)

	for _, ts := range []int64{1000, 3000, 2000} {
		s := &store.ShieldSnapshot{FillPct: 80.0, Ts: ts}
		if err := store.InsertShieldSnapshot(ctx, db, s); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	got, err := store.ListShieldSnapshots(ctx, db, store.ShieldSnapshotFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 rows, got %d", len(got))
	}
	if got[0].Ts != 3000 {
		t.Errorf("first row ts = %d, want 3000 (ts DESC)", got[0].Ts)
	}
	if got[2].Ts != 1000 {
		t.Errorf("last row ts = %d, want 1000", got[2].Ts)
	}
}

func TestListShieldSnapshots_FilterBySession(t *testing.T) {
	ctx := context.Background()
	db := openShieldSnapshotsDB(t)

	for _, sess := range []string{"sess-A", "sess-B", "sess-A"} {
		s := &store.ShieldSnapshot{SessionID: sess, FillPct: 71.0}
		if err := store.InsertShieldSnapshot(ctx, db, s); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	got, err := store.ListShieldSnapshots(ctx, db, store.ShieldSnapshotFilter{SessionID: "sess-A"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 rows for sess-A, got %d", len(got))
	}
}

func TestListShieldSnapshots_OnlyBlocked(t *testing.T) {
	ctx := context.Background()
	db := openShieldSnapshotsDB(t)

	snapshots := []store.ShieldSnapshot{
		{FillPct: 75.0, Blocked: true, BlockReason: "shield"},
		{FillPct: 93.0, Blocked: false, BlockReason: "fold"},
		{FillPct: 78.0, Blocked: true, BlockReason: "shield"},
	}
	for i := range snapshots {
		if err := store.InsertShieldSnapshot(ctx, db, &snapshots[i]); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	got, err := store.ListShieldSnapshots(ctx, db, store.ShieldSnapshotFilter{OnlyBlocked: true})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 blocked rows, got %d", len(got))
	}
}

func TestCountShieldDecisions(t *testing.T) {
	ctx := context.Background()
	db := openShieldSnapshotsDB(t)

	rows := []store.ShieldSnapshot{
		{SessionID: "s1", FillPct: 75.0, Blocked: true, BlockReason: "shield"},
		{SessionID: "s1", FillPct: 93.0, Blocked: false, BlockReason: "fold"},
		{SessionID: "s2", FillPct: 80.0, Blocked: true, BlockReason: "shield"},
	}
	for i := range rows {
		if err := store.InsertShieldSnapshot(ctx, db, &rows[i]); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	total, blocked, folded, err := store.CountShieldDecisions(ctx, db, "")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != 3 {
		t.Errorf("total=%d, want 3", total)
	}
	if blocked != 2 {
		t.Errorf("blocked=%d, want 2", blocked)
	}
	if folded != 1 {
		t.Errorf("folded=%d, want 1", folded)
	}

	// Scoped to session s1.
	total1, blocked1, folded1, err := store.CountShieldDecisions(ctx, db, "s1")
	if err != nil {
		t.Fatalf("count s1: %v", err)
	}
	if total1 != 2 {
		t.Errorf("s1 total=%d, want 2", total1)
	}
	if blocked1 != 1 {
		t.Errorf("s1 blocked=%d, want 1", blocked1)
	}
	if folded1 != 1 {
		t.Errorf("s1 folded=%d, want 1", folded1)
	}
}

func TestInsertShieldSnapshot_DefaultsNilJSON(t *testing.T) {
	ctx := context.Background()
	db := openShieldSnapshotsDB(t)

	// Zero-value struct: JSON fields default to "[]".
	s := &store.ShieldSnapshot{FillPct: 70.0}
	if err := store.InsertShieldSnapshot(ctx, db, s); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := store.GetShieldSnapshot(ctx, db, s.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.DecisionsJSON != "[]" {
		t.Errorf("decisions_json = %q, want []", got.DecisionsJSON)
	}
	if got.OpenFilesJSON != "[]" {
		t.Errorf("open_files_json = %q, want []", got.OpenFilesJSON)
	}
	if got.TurnsJSON != "[]" {
		t.Errorf("turns_json = %q, want []", got.TurnsJSON)
	}
}

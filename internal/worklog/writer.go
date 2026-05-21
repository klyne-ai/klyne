package worklog

import (
	"context"
	"fmt"
	"time"

	"github.com/klyne-ai/klyne/internal/projectpath"
	"github.com/klyne-ai/klyne/internal/store"
)

// WriteResult captures the outcome of a WriteEntry call. SuppressedBy is the
// name of the first suppression rule that fired (empty if the entry was kept
// visible).
type WriteResult struct {
	Signature    string
	SuppressedBy string
	Importance   int
	RecapVisible int
}

// UpsertFunc is the storage seam both detectors share. It is injected rather
// than referenced directly so the writer can be unit-tested against a fake
// upsert (and so detector code paths cannot accidentally diverge on which
// columns get written).
type UpsertFunc func(ctx context.Context, db *store.DB, row store.StopSummary, w store.WorklogColumns) error

// WriteEntry is the single entry point for both the Claude Stop hook and the
// Codex episode-boundary detector. It:
//
//   - filters irrelevant files and detects dependency-lock churn,
//   - computes a stable dedup signature,
//   - scores deterministic importance (1-10),
//   - applies the suppression rule pipeline,
//   - writes one stop_summaries row via the injected upsert with the
//     migration-015 worklog columns populated.
//
// dismissed is the per-project set of signatures the user has explicitly
// dismissed (nil-safe).
func WriteEntry(ctx context.Context, db *store.DB, e Entry, dismissed map[string]bool, upsert UpsertFunc) (WriteResult, error) {
	// Roll worktrees up to the canonical main-repo path so the worklog
	// shows one project per repo, not one per worktree. Safe no-op for
	// non-git or already-canonical paths.
	e.ProjectPath = projectpath.Canonical(e.ProjectPath)
	cleaned, depLock := FilterFiles(e.Files)
	e.Files = cleaned
	if depLock && !e.Has(TagDependencyChange) {
		e.EventTags = append(e.EventTags, TagDependencyChange)
	}

	sig := Signature(closeReason(e), e.CommitSHA, e.Files)
	imp := Score(e)
	drop, reason := ShouldSuppress(e, sig, dismissed)
	visible := 1
	if drop {
		visible = 0
	}

	row := store.StopSummary{
		SessionID:   e.SessionID,
		Ts:          e.TS.UnixMilli(),
		ProjectPath: e.ProjectPath,
		CLI:         e.CLI,
		// Memory-layer rows leave summary empty; ai_drafted_summary is the
		// canonical prose home (migration 015).
		Summary:  "",
		LastUser: e.LastUser,
		LastBash: e.LastBash,
		Files:    e.Files,
	}
	cols := store.WorklogColumns{
		RecapVisible:     visible,
		DraftState:       "proposed",
		Signature:        sig,
		Importance:       imp,
		LastAccessedAt:   time.Now().UnixMilli(),
		AIDraftedSummary: e.AIDraftedSummary,
	}
	if err := upsert(ctx, db, row, cols); err != nil {
		return WriteResult{}, fmt.Errorf("worklog: upsert: %w", err)
	}
	return WriteResult{
		Signature:    sig,
		Importance:   imp,
		RecapVisible: visible,
		SuppressedBy: reason,
	}, nil
}

// closeReason returns the categorical reason the entry was closed. It feeds
// the Signature so two entries that close for different reasons over the
// same file set still get distinct signatures (e.g. explicit user log vs
// commit-driven close).
func closeReason(e Entry) string {
	switch {
	case e.Has(TagExplicitUserLog):
		return "explicit"
	case e.Has(TagPROpened):
		return "pr"
	case e.CommitSHA != "":
		return "commit"
	default:
		return "stop"
	}
}

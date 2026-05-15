package mcpserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/klyne-ai/klyne/internal/store"
)

// preCompactHookSuffix is the argv klyne is invoked with from the PreCompact
// hook, mirroring hookCommandSuffix and preToolHookSuffix.
const preCompactHookSuffix = "precompact"

// fillBandLow is the lower fill threshold: below this the shield does not arm.
const fillBandLow = 0.70

// fillCeiling is the upper fill threshold: at or above this klyne defers to
// native compact rather than blocking (graceful fold).
const fillCeiling = 0.92

// PreCompactHookInput is the JSON payload Claude Code delivers on stdin for
// every PreCompact hook invocation.
type PreCompactHookInput struct {
	// SessionID is the Claude Code session id.
	SessionID string `json:"session_id,omitempty"`
	// CWD is the working directory of the Claude Code process.
	CWD string `json:"cwd,omitempty"`
	// HookEventName identifies the hook type; expected "PreCompact".
	HookEventName string `json:"hook_event_name,omitempty"`
	// ContextWindow holds token usage information.
	ContextWindow *PreCompactContextWindow `json:"context_window,omitempty"`
	// Trigger is "auto" or "manual".
	Trigger string `json:"trigger,omitempty"`
}

// PreCompactContextWindow mirrors the context_window object in the PreCompact
// hook payload delivered by Claude Code.
type PreCompactContextWindow struct {
	// CurrentTokens is the token count at hook-fire time.
	CurrentTokens int64 `json:"current_tokens,omitempty"`
	// MaxTokens is the model's context-window ceiling.
	MaxTokens int64 `json:"max_tokens,omitempty"`
	// FillPct is pre-computed fill percent 0..100 (when Claude Code provides it).
	FillPct float64 `json:"fill_pct,omitempty"`
}

// preCompactDecision captures klyne's PreCompact verdict.
type preCompactDecision struct {
	// Block is true when klyne is blocking native compact.
	Block bool
	// Reason is one of "shield" | "fold" | "no-snapshot" | "below-band".
	Reason string
	// SnapshotID is the shield_snapshot row id used for the block (0 when not blocking).
	SnapshotID int64
}

// PreCompactHookResult is the structured outcome returned by HandlePreCompact
// for use in tests and the CLI entry point.
type PreCompactHookResult struct {
	// Decision is "block" | "allow".
	Decision string
	// Reason is the block reason, or "" when allowing.
	Reason string
	// SnapshotID is the row id of the block snapshot (0 when not blocking).
	SnapshotID int64
	// Output is the JSON to emit on stdout (empty means allow / no-op).
	Output string
}

// HandlePreCompact reads a PreCompact hook payload from r, decides whether
// klyne should block the native compact, optionally records the decision,
// and returns the hook output JSON to emit on stdout.
//
// v0 algorithm:
//   - fill ≥ 92%  → fold (don't block; graceful hand-off to native)
//   - fill in [70%, 92%) AND snapshot exists → block
//   - fill < 70% OR no snapshot → allow
//
// db may be nil; snapshot recording and lookup are skipped when nil.
func HandlePreCompact(ctx context.Context, r io.Reader, db *store.DB) (*PreCompactHookResult, error) {
	body, err := io.ReadAll(io.LimitReader(r, 256*1024))
	if err != nil {
		return emptyCompactResult(), nil // transparent on read errors
	}

	var input PreCompactHookInput
	if len(body) > 0 {
		if err := json.Unmarshal(body, &input); err != nil {
			return emptyCompactResult(), nil // malformed payload → pass-through
		}
	}

	fill := computeFill(input.ContextWindow)
	preTokens := int64(0)
	if input.ContextWindow != nil {
		preTokens = input.ContextWindow.CurrentTokens
	}

	decision := shieldDecide(fill)

	// When shield wants to block, verify we actually have a snapshot to
	// re-inject from. Without one, blocking would leave the user with no
	// context — not a useful trade.
	if decision.Block && db != nil && input.SessionID != "" {
		snap, err := store.LatestShieldSnapshot(ctx, db, input.SessionID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				// No snapshot armed → can't block meaningfully.
				decision = preCompactDecision{Block: false, Reason: "no-snapshot"}
			} else {
				fmt.Fprintf(os.Stderr, "klyne precompact: load snapshot: %v\n", err)
				decision = preCompactDecision{Block: false, Reason: "no-snapshot"}
			}
		} else {
			decision.SnapshotID = snap.ID
		}
	} else if decision.Block && (db == nil || input.SessionID == "") {
		// Can't look up a snapshot without db+session; degrade gracefully.
		decision = preCompactDecision{Block: false, Reason: "no-snapshot"}
	}

	// Record the final decision so `klyne precompact --status` can surface it.
	if db != nil {
		recordDecision(ctx, db, input.SessionID, fill, float64(preTokens), decision)
	}

	if !decision.Block {
		// Empty output = native compact proceeds.
		return emptyCompactResult(), nil
	}

	out, err := buildCompactBlockOutput(decision.SnapshotID)
	if err != nil {
		return emptyCompactResult(), nil
	}

	return &PreCompactHookResult{
		Decision:   "block",
		Reason:     decision.Reason,
		SnapshotID: decision.SnapshotID,
		Output:     out,
	}, nil
}

// shieldDecide implements the v0 algorithm (fold ceiling + band block).
// Per spec: skip stale/incomplete/lossy checks in v0; those are v1.
func shieldDecide(fill float64) preCompactDecision {
	if fill >= fillCeiling {
		// Graceful fold: context is so full that re-injecting would cost
		// more tokens than native compact saves.
		return preCompactDecision{Block: false, Reason: "fold"}
	}
	if fill >= fillBandLow {
		return preCompactDecision{Block: true, Reason: "shield"}
	}
	// Below arming threshold; native compact can proceed.
	return preCompactDecision{Block: false, Reason: "below-band"}
}

// computeFill derives a 0..1 fill fraction from the context window payload.
// Prefers the explicit FillPct field (divided by 100) when present; otherwise
// derives from CurrentTokens/MaxTokens; falls back to 0.
func computeFill(cw *PreCompactContextWindow) float64 {
	if cw == nil {
		return 0
	}
	if cw.FillPct > 0 {
		return cw.FillPct / 100.0
	}
	if cw.MaxTokens > 0 {
		return float64(cw.CurrentTokens) / float64(cw.MaxTokens)
	}
	return 0
}

// buildCompactBlockOutput constructs the JSON object Claude Code expects when
// a PreCompact hook blocks native compact.
func buildCompactBlockOutput(snapshotID int64) (string, error) {
	msg := "klyne shielded compact"
	if snapshotID > 0 {
		msg = fmt.Sprintf("klyne shielded compact (snapshot=%d) — context preserved, scoped re-inject on next prompt", snapshotID)
	}
	payload := map[string]any{
		"decision":      "block",
		"reason":        msg,
		"systemMessage": msg,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// recordDecision persists the block/allow outcome as a shield_snapshot row
// so decision counters stay accurate even for non-block events.
func recordDecision(ctx context.Context, db *store.DB, sessionID string, fill, preTokens float64, d preCompactDecision) {
	blocked := d.Block
	reason := d.Reason
	if reason == "below-band" || reason == "no-snapshot" {
		// Don't write a row for these pass-through cases — they weren't armed.
		return
	}
	s := &store.ShieldSnapshot{
		Ts:          time.Now().UnixMilli(),
		SessionID:   sessionID,
		FillPct:     fill * 100.0,
		PreTokens:   int64(preTokens),
		Blocked:     blocked,
		BlockReason: reason,
	}
	if err := store.InsertShieldSnapshot(ctx, db, s); err != nil {
		fmt.Fprintf(os.Stderr, "klyne precompact: record decision: %v\n", err)
	}
}

// emptyCompactResult returns an allow decision with no output.
func emptyCompactResult() *PreCompactHookResult {
	return &PreCompactHookResult{}
}

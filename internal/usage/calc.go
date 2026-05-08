// Package usage computes rolling-window token aggregates from the messages
// table. The dashboard surfaces these as the "Claude Code Usage" popover —
// 5-hour rolling window, 7-day rolling window, and the 7-day Sonnet-only
// subset — mirroring how the official CLI reports consumption against the
// undocumented per-plan rate limits.
//
// The package deliberately does NOT bake in vendor caps. Anthropic and
// OpenAI don't publish exact rate-limit numbers, they change without
// notice, and they depend on the user's plan tier. The HTTP handler
// returns raw token sums and the UI converts to percentage against a
// tier table the user can edit. See contracts.UsageWindow.
package usage

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mohitpatell/agentdeck/internal/api"
	"github.com/mohitpatell/agentdeck/internal/connectors"
	"github.com/mohitpatell/agentdeck/internal/store"
)

// Window sizes, in seconds. Spec §usage: rolling windows mirror the
// official CLI's reported buckets.
const (
	Window5h = int64(5 * 60 * 60)            // 5 hours in seconds
	Window7d = int64(7 * 24 * 60 * 60)       // 7 days in seconds
)

// SonnetModelInfix matches the model column for any Claude Sonnet variant
// (e.g. "claude-sonnet-4-6", "claude-3-5-sonnet-20241022"). Anthropic's
// 7-day Sonnet sub-limit applies only to these models.
const SonnetModelInfix = "sonnet"

// Compute returns the rolling-window aggregates for both connectors at
// time t (epoch-ms).
//
// The function runs five small, indexed queries against the messages table
// (one per (cli, window) pair plus the Sonnet sub-window). On a 200k-row
// database these complete in low single-digit milliseconds.
//
// OAuth augmentation: when the optional fetcher is supplied and the user
// has Claude Code credentials, the response carries vendor-canonical
// utilization percentages from Anthropic's /api/oauth/usage. Pass nil
// to compute estimates only (used by tests and the noop deployment
// path).
//
// Codex augmentation: when codexSnap is non-nil, the latest rate-limit
// snapshot (which the Codex CLI writes into its session JSONL on every
// turn) populates the codex.oauth field — same shape, same semantics as
// the Claude OAuth path even though the data source is different.
func Compute(ctx context.Context, db *store.DB, now time.Time, oauth *OAuthFetcher, codexSnap *CodexSnapshotReader) (api.UsageResponse, error) {
	if db == nil {
		return api.UsageResponse{}, fmt.Errorf("usage: nil db")
	}
	nowMs := now.UnixMilli()

	resp := api.UsageResponse{Now: nowMs}

	// Claude — 5h, 7d, 7d Sonnet
	claude5h, err := windowFor(ctx, db, connectors.CLIClaude, nowMs, Window5h, false)
	if err != nil {
		return api.UsageResponse{}, fmt.Errorf("usage: claude 5h: %w", err)
	}
	claude7d, err := windowFor(ctx, db, connectors.CLIClaude, nowMs, Window7d, false)
	if err != nil {
		return api.UsageResponse{}, fmt.Errorf("usage: claude 7d: %w", err)
	}
	claude7dSonnet, err := windowFor(ctx, db, connectors.CLIClaude, nowMs, Window7d, true)
	if err != nil {
		return api.UsageResponse{}, fmt.Errorf("usage: claude 7d sonnet: %w", err)
	}
	resp.Claude = api.UsageCLI{
		Window5h:       claude5h,
		Window7d:       claude7d,
		Window7dSonnet: claude7dSonnet,
	}

	// Best-effort OAuth augmentation. fetchFresh handles errors quietly
	// — anything we get back here is either real data or nil (degraded).
	if oauth != nil {
		if oauthData, oerr := oauth.Fetch(ctx); oerr == nil {
			resp.Claude.OAuth = oauthData
		}
		// Genuine fetch errors (e.g. JSON parse) are deliberately
		// swallowed. Degrading to estimates is always preferred over
		// failing the whole /usage call.
	}

	// Codex — 5h and 7d only. The Sonnet sub-window is Anthropic-specific
	// and zero-valued for non-Claude CLIs.
	codex5h, err := windowFor(ctx, db, connectors.CLICodex, nowMs, Window5h, false)
	if err != nil {
		return api.UsageResponse{}, fmt.Errorf("usage: codex 5h: %w", err)
	}
	codex7d, err := windowFor(ctx, db, connectors.CLICodex, nowMs, Window7d, false)
	if err != nil {
		return api.UsageResponse{}, fmt.Errorf("usage: codex 7d: %w", err)
	}
	resp.Codex = api.UsageCLI{
		Window5h: codex5h,
		Window7d: codex7d,
	}

	// Codex live snapshot from the JSONL stream. Errors degrade silently
	// to estimate-only, just like the Claude OAuth path.
	if codexSnap != nil {
		if codexData, cerr := codexSnap.Latest(); cerr == nil {
			resp.Codex.OAuth = codexData
		}
	}

	return resp, nil
}

// windowFor aggregates messages for one (cli, window) pair. When
// sonnetOnly is true the result is filtered to model strings containing
// "sonnet" (case-insensitive).
//
// The query joins sessions to filter by cli — messages does not carry
// the cli column. Both join keys (sessions.id, messages.session_id) are
// indexed, so the join is a covered lookup.
func windowFor(
	ctx context.Context,
	db *store.DB,
	cli connectors.CLI,
	nowMs int64,
	windowSeconds int64,
	sonnetOnly bool,
) (api.UsageWindow, error) {
	since := nowMs - windowSeconds*1000

	// Aggregate query — single row, three sums + min(ts).
	const baseQuery = `
SELECT
    COALESCE(SUM(m.tokens_in), 0)  AS tokens_in,
    COALESCE(SUM(m.tokens_out), 0) AS tokens_out,
    COUNT(*)                       AS messages,
    COALESCE(MIN(m.ts), 0)         AS first_ts
FROM messages m
INNER JOIN sessions s ON s.id = m.session_id
WHERE s.cli = ?
  AND m.ts >= ?`

	q := baseQuery
	args := []any{string(cli), since}
	if sonnetOnly {
		q += "\n  AND LOWER(COALESCE(m.model, '')) LIKE ?"
		args = append(args, "%"+strings.ToLower(SonnetModelInfix)+"%")
	}

	row := db.Read().QueryRowContext(ctx, q, args...)

	var w api.UsageWindow
	w.WindowSeconds = windowSeconds
	if err := row.Scan(&w.TokensIn, &w.TokensOut, &w.Messages, &w.FirstMsgTs); err != nil {
		return api.UsageWindow{}, fmt.Errorf("scan window cli=%q sonnet=%v: %w", cli, sonnetOnly, err)
	}
	w.Tokens = w.TokensIn + w.TokensOut

	// MIN(ts) is the start of the rolling block — we only return it when
	// at least one message landed inside the window. Zero is the "empty
	// window" sentinel for the frontend.
	if w.Messages == 0 {
		w.FirstMsgTs = 0
	}
	return w, nil
}

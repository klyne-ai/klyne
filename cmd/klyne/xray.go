package main

// xray.go — `klyne xray` sub-command: Context X-ray v0.
//
// Prints the scorecard from the spec (composite verdict + bloat attribution +
// MCP/skill/hook source attribution + cache-hit-rate trajectory) for the
// active session or an explicit --session-id.
//
// Named "xray" rather than "audit" because "audit" (audit-sessions) was
// already taken by the session-integrity checker. The scorecard header still
// reads "klyne audit" per the spec rendering.
//
// I/O lives here; all analysis lives in internal/contexthealth (pure).

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/contexthealth"
	"github.com/klyne-ai/klyne/internal/mcpserver"
)

func newXrayCmd() *cobra.Command {
	var sessionID string

	c := &cobra.Command{
		Use:   "xray",
		Short: "Context X-ray: scorecard of context fill, bloat, and cache health",
		Long: `Print the Context X-ray scorecard for the active session.

Shows:
  - Composite context-health verdict (Healthy / Drifting / Risky / RescueNow)
  - Cache-hit-rate trajectory over the last 5 turns
  - Pre-prompt token attribution by source (MCP servers, skills, hooks)
  - Top bloat sources (file reads, commands, tool results)

Output exactly mirrors the spec rendering in
docs/superpowers/specs/2026-05-15-context-xray-design.md.

Examples:
  klyne xray
  klyne xray --session-id abc123`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runXray(cmd, sessionID)
		},
		SilenceUsage: true,
	}

	c.Flags().StringVar(&sessionID, "session-id", "",
		"explicit session id to analyse (default: latest active session in cwd)")
	return c
}

func runXray(cmd *cobra.Command, sessionID string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	// Resolve the JSONL path — delegate to the same resolver the MCP tool uses.
	path, err := resolveXrayPath(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("resolve session: %w", err)
	}
	if path == "" {
		fmt.Fprintln(cmd.OutOrStdout(), "No Claude Code session found for this working directory.")
		return nil
	}

	// Load the snapshot (parses JSONL, computes ContextFillPct).
	snap, err := mcpserver.LoadSnapshot(ctx, path)
	if err != nil {
		return fmt.Errorf("load snapshot: %w", err)
	}

	// Run the deterministic classifier.
	res := contexthealth.Classify(contexthealth.Input{
		SessionID:      snap.SessionID,
		CLI:            connectors.CLIClaude,
		Model:          snap.Model,
		ContextFillPct: snap.ContextFillPct,
		MsgCount:       snap.MsgCount,
		Messages:       snap.Messages,
	})

	// Compute the new v0 signals: cache trajectory + source attribution.
	traj := contexthealth.ComputeCacheTrajectory(snap.Messages)
	sources := contexthealth.AttributeSources(snap.Messages)

	scorecard := renderXrayScorecard(snap, res, traj, sources)
	fmt.Fprint(cmd.OutOrStdout(), scorecard)
	return nil
}

// resolveXrayPath resolves an explicit session id or the latest active session
// for the current working directory. Returns "" when nothing matches.
func resolveXrayPath(ctx context.Context, sessionID string) (string, error) {
	if sessionID != "" {
		return mcpserver.FindSessionByID(sessionID)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve cwd: %w", err)
	}
	cands, err := mcpserver.ListSessionsForCWDCtx(ctx, cwd)
	if err != nil {
		return "", err
	}
	if pick, ok := mcpserver.PickActiveSession(cands); ok {
		return pick.Path, nil
	}
	// No unambiguous active session — fall back to the most-recently-modified
	// one so xray is still useful after the session has gone idle.
	if len(cands) > 0 {
		return cands[0].Path, nil
	}
	return "", nil
}

// renderXrayScorecard produces the exact scorecard format from the spec.
// Example output:
//
//	klyne audit — session 7c9e (Claude, 2.4h)
//
//	Composite:    Risky — context fill 71%, hidden ratio 4.2
//	Cache:        84% → 47% over last 5 turns ↓ (churning)
//
//	Pre-prompt context: 47K tokens
//	  serena MCP        18K  (last useful call: 3d ago)
//	  ...
//
//	Top repetition: src/auth.ts read 6× (likely lost prior content)
func renderXrayScorecard(
	snap *mcpserver.SessionSnapshot,
	res contexthealth.Result,
	traj contexthealth.CacheTrajectoryResult,
	sources []contexthealth.SourceRow,
) string {
	var b strings.Builder

	// Header.
	age := sessionAgeStr(snap.Messages)
	shortID := shortSessionID(snap.SessionID)
	fmt.Fprintf(&b, "klyne audit — session %s (Claude, %s)\n\n", shortID, age)

	// Composite verdict line.
	fmt.Fprintf(&b, "Composite:    %s — context fill %.0f%%, hidden ratio %.1f\n",
		titleCase(string(res.State)),
		res.Signals.ContextFillPct,
		res.Signals.HiddenRatio,
	)

	// Cache trajectory line.
	if len(traj.Rates) >= 2 {
		arrow := trajectoryArrow(traj.Direction)
		fmt.Fprintf(&b, "Cache:        %.0f%% → %.0f%% over last %d turns %s (%s)\n",
			traj.First, traj.Last, len(traj.Rates), arrow, string(traj.Direction),
		)
	} else if len(traj.Rates) == 1 {
		fmt.Fprintf(&b, "Cache:        %.0f%% (single turn sampled)\n", traj.Rates[0])
	} else {
		fmt.Fprintln(&b, "Cache:        n/a (no cached-token data)")
	}

	b.WriteByte('\n')

	// Pre-prompt source attribution section.
	totalSourceTokens := 0
	for _, s := range sources {
		totalSourceTokens += s.Tokens
	}
	if len(sources) > 0 {
		fmt.Fprintf(&b, "Pre-prompt context: %s tokens\n", formatKTokens(totalSourceTokens))
		nameWidth := 18
		for _, s := range sources {
			label := padRight(s.Name, nameWidth)
			tokenStr := padRight(formatKTokens(s.Tokens), 5)
			if s.LastUsefulCallAgo != "" {
				fmt.Fprintf(&b, "  %s %s  (last useful call: %s)\n", label, tokenStr, s.LastUsefulCallAgo)
			} else {
				fmt.Fprintf(&b, "  %s %s\n", label, tokenStr)
			}
		}
	} else {
		fmt.Fprintln(&b, "Pre-prompt context: (no attributed sources)")
	}

	b.WriteByte('\n')

	// Top repetition line (from the bloat scorecard).
	if res.Signals.TopRepeatedFile != "" && res.Signals.TopRepeatedFileCount >= 2 {
		base := baseName(res.Signals.TopRepeatedFile)
		fmt.Fprintf(&b, "Top repetition: %s read %d× (likely lost prior content)\n",
			base, res.Signals.TopRepeatedFileCount)
	} else if len(res.Bloat) > 0 {
		top := res.Bloat[0]
		fmt.Fprintf(&b, "Top bloat: %s (%.0f%% of tool output bytes)\n",
			top.Label, top.SharePct)
	} else {
		fmt.Fprintln(&b, "Top bloat: none detected")
	}

	return b.String()
}

// trajectoryArrow returns a Unicode arrow indicating the direction.
func trajectoryArrow(d contexthealth.CacheDirection) string {
	switch d {
	case contexthealth.CacheDirectionRising:
		return "↑"
	case contexthealth.CacheDirectionFalling:
		return "↓"
	case contexthealth.CacheDirectionChurning:
		return "↓"
	default:
		return "→"
	}
}

// formatKTokens formats a token count as "47K" or "2.4K" or "400" depending on size.
func formatKTokens(n int) string {
	if n >= 1000 {
		k := float64(n) / 1000.0
		if k == float64(int(k)) {
			return fmt.Sprintf("%dK", int(k))
		}
		return fmt.Sprintf("%.1fK", k)
	}
	return fmt.Sprintf("%d", n)
}

// sessionAgeStr returns a human-readable age string like "2.4h" or "45m"
// for the session, computed from the first to last message timestamps.
func sessionAgeStr(msgs []*connectors.Message) string {
	if len(msgs) < 2 {
		return "0m"
	}
	first := msgs[0].Ts
	last := msgs[len(msgs)-1].Ts
	dur := time.Duration(last-first) * time.Millisecond
	if dur < time.Minute {
		return "0m"
	}
	if dur < time.Hour {
		return fmt.Sprintf("%dm", int(dur.Minutes()))
	}
	hours := dur.Hours()
	if hours == float64(int(hours)) {
		return fmt.Sprintf("%dh", int(hours))
	}
	return fmt.Sprintf("%.1fh", hours)
}

// shortSessionID returns the first 8 characters of a session ID, or the full
// ID when it is shorter.
func shortSessionID(id string) string {
	if utf8.RuneCountInString(id) <= 8 {
		return id
	}
	return id[:8]
}

// titleCase capitalises the first letter of a string and replaces underscores
// with spaces — converts "rescue_now" → "Rescue now".
func titleCase(s string) string {
	s = strings.ReplaceAll(s, "_", " ")
	if len(s) == 0 {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// padRight pads a string to width with trailing spaces.
func padRight(s string, width int) string {
	n := utf8.RuneCountInString(s)
	if n >= width {
		return s
	}
	return s + strings.Repeat(" ", width-n)
}

// baseName returns the last path component of a slash-separated path.
func baseName(p string) string {
	idx := strings.LastIndex(p, "/")
	if idx < 0 {
		return p
	}
	return p[idx+1:]
}

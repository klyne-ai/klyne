package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/klyne-ai/klyne/internal/mcpserver"
)

// newStatuslineCmd registers `klyne statusline`.
//
// Emits a single short line suitable for Claude Code's `statusLine`
// settings hook. Inspired by ccusage's statusline integration but
// computed locally from klyne's existing token-timeline engine — no
// AI calls, no network.
//
// Recommended hook wiring (~/.claude/settings.json):
//
//	"statusLine": {
//	  "type": "command",
//	  "command": "klyne statusline"
//	}
//
// Output is intentionally compact so it doesn't crowd the prompt.
// Three modes:
//
//   - --format=short  (default) e.g. "klyne ▸ 38% ctx · 62k/160k · 5h 18%"
//   - --format=mini   e.g. "38% · 18%"   — for tight statuslines
//   - --format=plain  identical to short but without the leading ▸ icon
//
// When no session is found for the current cwd, prints "klyne ▸ idle"
// and exits 0 — never blocks the user's prompt with an error.
func newStatuslineCmd() *cobra.Command {
	var (
		sessionID string
		format    string
	)
	c := &cobra.Command{
		Use:   "statusline",
		Short: "One-line statusline output for Claude Code's statusLine hook",
		Long: `Emit a single short line summarising the active klyne session.
Designed for Claude Code's statusLine hook — exits 0 even on missing
state so it never breaks the user's prompt.

Format options:
  --format=short   (default) klyne ▸ NN% ctx · TOK/CTX · 5h NN%
  --format=mini    NN% · NN%
  --format=plain   short, without the leading icon

Examples:
  klyne statusline
  klyne statusline --format=mini
  klyne statusline --session=<id> --format=short`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runStatusline(cmd, sessionID, format)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	c.Flags().StringVar(&sessionID, "session", "", "explicit session id (default: latest in cwd)")
	c.Flags().StringVar(&format, "format", "short", "output format: short | mini | plain")
	return c
}

func runStatusline(cmd *cobra.Command, sessionID, format string) error {
	cwd, _ := os.Getwd()
	in := mcpserver.TokenTimelineInput{
		SessionID: sessionID,
		CWD:       cwd,
	}
	_, out, err := mcpserver.HandleGetTokenTimeline(context.Background(), nil, in)
	if err != nil || out.SessionID == "" || len(out.Points) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), idleStatusline(format))
		return nil
	}
	fmt.Fprintln(cmd.OutOrStdout(), renderStatusline(out, format))
	return nil
}

func renderStatusline(out mcpserver.TokenTimelineOutput, format string) string {
	ctxPct := out.PctOfContext
	burnPct := out.PctUsed
	tokens := humanTokensShort(out.LatestInput)
	ctxSize := humanTokensShort(out.ContextWindow)

	switch strings.ToLower(format) {
	case "mini":
		return fmt.Sprintf("%s%% · %s%%", fmtPct(ctxPct), fmtPct(burnPct))
	case "plain":
		return fmt.Sprintf("klyne %s%% ctx · %s/%s · 5h %s%%",
			fmtPct(ctxPct), tokens, ctxSize, fmtPct(burnPct))
	default: // short
		return fmt.Sprintf("klyne ▸ %s%% ctx · %s/%s · 5h %s%%",
			fmtPct(ctxPct), tokens, ctxSize, fmtPct(burnPct))
	}
}

func idleStatusline(format string) string {
	switch strings.ToLower(format) {
	case "mini":
		return "—"
	case "plain":
		return "klyne idle"
	default:
		return "klyne ▸ idle"
	}
}

// fmtPct rounds a 0-100 percentage to a whole number. Caps at 999
// so a runaway computation never blows up the statusline width.
func fmtPct(p float64) string {
	if p < 0 {
		p = 0
	}
	if p > 999 {
		p = 999
	}
	return fmt.Sprintf("%.0f", p)
}

// humanTokensShort renders a token count using k / M suffixes with
// no decimals. Optimised for terminal status lines (width-sensitive).
//
//	0..999       → "123"
//	1000..999_999 → "12k", "120k"
//	>= 1_000_000 → "1M", "12M"
func humanTokensShort(n int64) string {
	if n < 0 {
		n = -n
	}
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%dM", n/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%dk", n/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

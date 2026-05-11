package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/klyne-ai/klyne/internal/mcpserver"
)

// tokens.go — `klyne tokens` subcommand. A reliable terminal
// surface for the same token-timeline data the MCP slash prompt
// (/klyne:tokens) renders. Exists because Claude Code's slash UI
// occasionally fails to fire MCP prompts on bare press-enter; the
// CLI is the always-available escape hatch.
//
// Output is the same Markdown the slash prompt produces, written
// verbatim to stdout so users can pipe it through `glow` or pager
// of choice. No JSON-RPC framing, no hook side-effects.

func newTokensCmd() *cobra.Command {
	var (
		sessionID string
		window    string
		hours     int
	)
	c := &cobra.Command{
		Use:   "tokens",
		Short: "Show this session's per-turn token usage and context-window fill",
		Long: `Render the per-turn token usage timeline for the active session in this directory.

The output mirrors what /klyne:tokens shows in Claude Code's chat:
a single-session view of cumulative input tokens, % of the model's
context window, an ASCII sparkline, and a per-turn table.

Defaults to a 5-hour lookback. Pass --window=30m for a 30-minute
view, --window=1h for one hour, etc. (Go duration syntax; min 1m,
max 24h.)

This is the CLI escape hatch when /klyne:tokens does not fire
reliably from Claude Code's slash menu.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("resolve cwd: %w", err)
			}
			in := mcpserver.TokenTimelineInput{
				SessionID:   sessionID,
				CWD:         cwd,
				Window:      window,
				WindowHours: hours,
			}
			_, out, err := mcpserver.HandleGetTokenTimeline(context.Background(), nil, in)
			if err != nil {
				return fmt.Errorf("token timeline: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), mcpserver.FormatTokenTimelineAsMarkdown(out))
			return nil
		},
		SilenceUsage: true,
	}
	c.Flags().StringVar(&sessionID, "session", "", "explicit session id (default: latest in cwd)")
	c.Flags().StringVar(&window, "window", "", "lookback as a Go duration string (\"30m\", \"5h\", \"2h30m\"); default 5h")
	c.Flags().IntVar(&hours, "hours", 0, "convenience integer hours; ignored when --window is set")
	return c
}

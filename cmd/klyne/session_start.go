package main

import (
	"context"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/klyne-ai/klyne/internal/hooks"
)

// session_start.go — `klyne session-start` SessionStart-hook entry point.
//
// Claude Code fires this hook at the beginning of every session,
// including the one-shot `claude --print` mode. It is the ONLY hook
// that fires before model generation in --print mode (UserPromptSubmit
// does not), so we use it to inject the KLYNE_SUMMARY instruction.
//
// Hidden from the user-facing help; runs from the daemon fast path,
// or as a fallback exec when the daemon socket isn't reachable.
func newSessionStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "session-start",
		Short:  "SessionStart hook entry point (internal)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Bounded ceiling: SessionStart payloads are tiny JSON
			// objects, the handler does no I/O. 2s matches advise.
			ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Second)
			defer cancel()
			res := hooks.SessionStart(ctx, cmd.InOrStdin())
			if len(res.Stdout) > 0 {
				_, _ = cmd.OutOrStdout().Write(res.Stdout)
			}
			if len(res.Stderr) > 0 {
				_, _ = cmd.ErrOrStderr().Write(res.Stderr)
			}
			// SessionStart, like all hooks, must never block the
			// session — always exit 0. The hook output is best-effort.
			os.Exit(0)
			return nil
		},
	}
}

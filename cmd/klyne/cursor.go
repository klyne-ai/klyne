package main

import (
	"github.com/spf13/cobra"

	"github.com/klyne-ai/klyne/internal/hooks"
)

// cursor.go — `klyne cursor` Cursor-CLI hook entry point.
//
// Cursor's hook configuration (~/.cursor/hooks.json, schema version 1)
// wires a single command per event. We register `klyne cursor` as
// that command for SessionStart / BeforeSubmitPrompt /
// AfterAgentResponse / Stop — the handler in internal/hooks
// dispatches by the payload's `hook_event_name` field.
//
// Like `klyne session-end`, this subcommand is hidden from --help:
// it's a hook entry point, not a human-facing command.

// newCursorCmd registers `klyne cursor`. Reads the Cursor hook
// payload from stdin, dispatches in-process via hooks.CursorHook,
// writes hooks' stdout payload to this process's stdout (Cursor
// reads it back). Hard rule (same as every other hook entry): never
// block the agent on a klyne error — always exits 0.
func newCursorCmd() *cobra.Command {
	c := &cobra.Command{
		Use:    "cursor",
		Short:  "Cursor hook entry point (sessionStart/afterAgentResponse/stop/…)",
		Hidden: true,
		Long: `Read a Cursor hook event JSON payload from stdin and dispatch
internally by the payload's hook_event_name field. Designed as the
single command wired into ~/.cursor/hooks.json for every event
klyne cares about (installed automatically by 'klyne mcp install').

Stdin: Cursor hook payload (event-specific JSON).
Stdout: the JSON Cursor expects on that event (often {} when klyne
  has nothing to add — fail-open).
Stderr: debugging lines only.
Exit code: always 0. Cursor hooks fail open by default; klyne never
  blocks the agent on its own error.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			res := hooks.CursorHook(cmd.Context(), cmd.InOrStdin(), nil)
			if len(res.Stdout) > 0 {
				_, _ = cmd.OutOrStdout().Write(res.Stdout)
			}
			if len(res.Stderr) > 0 {
				_, _ = cmd.ErrOrStderr().Write(res.Stderr)
			}
			return nil
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	return c
}

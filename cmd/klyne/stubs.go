package main

// stubs.go — forward declarations for commands wired in main.go that
// are not yet implemented in this worktree. These satisfy the compiler
// without providing real functionality.
//
// TODO(resume): remove once the runbooks/status/session-end commands land.

import "github.com/spf13/cobra"

func newRunbooksCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "runbooks",
		Short: "Runbook proposer (not yet implemented in this worktree)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return stub(cmd, "runbooks", "TBD")
		},
		SilenceUsage: true,
	}
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Installation snapshot (not yet implemented in this worktree)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return stub(cmd, "status", "TBD")
		},
		SilenceUsage: true,
	}
}

func newSessionEndCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "session-end",
		Short: "Session-end summary writer (not yet implemented in this worktree)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return stub(cmd, "session-end", "TBD")
		},
		SilenceUsage: true,
		Hidden:       true,
	}
}

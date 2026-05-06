package main

import "github.com/spf13/cobra"

// newStartCmd registers `agentdeck start`. Body is W12.
func newStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the agentdeck daemon (watches CLI logs, serves UI)",
		RunE:  runStart,
	}
}

func runStart(cmd *cobra.Command, _ []string) error {
	return stub(cmd, "start", "W12")
}

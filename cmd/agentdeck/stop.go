package main

import "github.com/spf13/cobra"

// newStopCmd registers `agentdeck stop`. Body is W12.
func newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the running agentdeck daemon",
		RunE:  runStop,
	}
}

func runStop(cmd *cobra.Command, _ []string) error {
	return stub(cmd, "stop", "W12")
}

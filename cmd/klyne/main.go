// Command agentdeck is the W0 stub of the daemon CLI. Subcommands are
// wired here; their bodies are filled in by W12 (app wiring).
//
// Running `agentdeck` with no subcommand defaults to `agentdeck start`,
// matching the v1 spec ("agentdeck (no args). Daemon starts, opens
// http://127.0.0.1:7878 in default browser." — spec Flow A).
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// version is hardcoded for the W0 bootstrap. W17 (release) wires this
// to a build-time -ldflags value.
const version = "v0.0.0-bootstrap"

func main() {
	if err := newRootCmd().Execute(); err != nil {
		// Cobra already prints the error; propagate the exit code.
		os.Exit(1)
	}
}

// newRootCmd builds the cobra command tree. Factored into a function so
// tests can construct fresh trees without state leakage.
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:     "agentdeck",
		Short:   "Local-first dashboard for Claude Code and Codex CLI sessions",
		Version: version,
		// With no subcommand, behave as `agentdeck start` (spec Flow A).
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStart(cmd, args)
		},
		SilenceUsage: true,
	}

	// Honor -v as a short for --version (Cobra wires --version by default).
	root.Flags().BoolP("version", "v", false, "print version and exit")

	root.AddCommand(newStartCmd())
	root.AddCommand(newStopCmd())
	root.AddCommand(newDoctorCmd())
	root.AddCommand(newAuditCmd())
	root.AddCommand(newMcpCmd())
	return root
}

// stub is the standard "not implemented" message printed by every W0
// subcommand body. The phrasing is asserted by W12's tests when the
// real implementation lands.
func stub(cmd *cobra.Command, name, owner string) error {
	_, err := fmt.Fprintf(cmd.OutOrStdout(),
		"agentdeck %s: not implemented (%s)\n", name, owner)
	return err
}

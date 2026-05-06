package main

import (
	"encoding/json"

	"github.com/spf13/cobra"
)

// newDoctorCmd registers `agentdeck doctor`. The W0 stub emits a
// well-formed JSON document so smoke-test scripts can already parse it;
// W12 fills in real diagnostic fields.
func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Print a diagnostic report (paths, providers, schema version)",
		RunE:  runDoctor,
	}
}

// doctorReport is the W0 stub envelope. W12 will extend this struct;
// fields here are intentionally minimal to avoid pre-committing to a
// shape the real diagnostic doesn't want.
type doctorReport struct {
	Status  string `json:"status"`
	Version string `json:"version"`
	Note    string `json:"note"`
}

func runDoctor(cmd *cobra.Command, _ []string) error {
	report := doctorReport{
		Status:  "stub",
		Version: version,
		Note:    "not implemented (W12)",
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

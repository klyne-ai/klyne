package main

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/klyne-ai/klyne/internal/app"
)

// stopWaitInterval is the polling interval used while waiting for the
// daemon to exit after SIGTERM. The grand total wait is bounded by
// stopMaxWait (≤ 2s, matching App's shutdown grace).
const (
	stopWaitInterval = 50 * time.Millisecond
	stopMaxWait      = 3 * time.Second
)

// newStopCmd registers `klyne stop`.
//
// Idempotent:
//   - missing pidfile  → exit 0 with an informational message.
//   - dead pid in file → exit 0 after removing the stale pidfile.
//   - live daemon      → SIGTERM, wait up to stopMaxWait, remove pidfile.
func newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the running klyne daemon",
		RunE:  runStop,
	}
}

func runStop(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()

	pid, err := app.ReadPidfile()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			_, _ = fmt.Fprintln(out, "klyne stop: no daemon running (pidfile absent)")
			return nil
		}
		// Bad contents — clean up and report.
		_ = app.RemovePidfile()
		return fmt.Errorf("klyne stop: read pidfile: %w", err)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		_ = app.RemovePidfile()
		return fmt.Errorf("klyne stop: find process %d: %w", pid, err)
	}

	// Liveness probe via signal 0.
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		// Process already gone — clean up and exit 0.
		_ = app.RemovePidfile()
		_, _ = fmt.Fprintf(out, "klyne stop: pid %d not running; removed stale pidfile\n", pid)
		return nil
	}

	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("klyne stop: signal pid %d: %w", pid, err)
	}

	// Wait for the daemon to exit (or time out).
	deadline := time.Now().Add(stopMaxWait)
	for time.Now().Before(deadline) {
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			// gone
			_ = app.RemovePidfile()
			_, _ = fmt.Fprintf(out, "klyne stop: pid %d stopped\n", pid)
			return nil
		}
		time.Sleep(stopWaitInterval)
	}

	// Daemon did not exit in time — leave pidfile in place so an operator
	// can inspect, but report the timeout so scripts can decide what to do.
	return fmt.Errorf("klyne stop: pid %d did not exit within %s", pid, stopMaxWait)
}

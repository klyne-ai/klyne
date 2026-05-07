package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/mohitpatell/agentdeck/internal/app"
	"github.com/mohitpatell/agentdeck/internal/config"
)

// newStartCmd registers `agentdeck start`.
//
// Behavior (spec Flow A):
//   - Loads config from ~/.agentdeck/config.toml (or defaults).
//   - Builds App via app.New.
//   - Writes ~/.agentdeck/daemon.pid.
//   - Installs SIGINT/SIGTERM handlers via signal.NotifyContext.
//   - Calls App.Start; on return removes the pidfile.
//
// Flags:
//   --no-open   suppress the auto-launch of the system browser (CI / headless).
func newStartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the agentdeck daemon (watches CLI logs, serves UI)",
		RunE:  runStart,
	}
	cmd.Flags().Bool("no-open", false, "do not open the dashboard in a browser")
	return cmd
}

func runStart(cmd *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("agentdeck start: load config: %w", err)
	}

	a, err := app.New(cfg)
	if err != nil {
		return fmt.Errorf("agentdeck start: build app: %w", err)
	}

	noOpen, _ := cmd.Flags().GetBool("no-open")
	if noOpen {
		a.SuppressBrowser = true
	}

	// Refuse to start if a stale pidfile points at a live process.
	if existing, err := app.ReadPidfile(); err == nil {
		if processAlive(existing) {
			return fmt.Errorf("agentdeck start: another daemon appears to be running (pid %d)", existing)
		}
		// Stale pidfile — remove it and continue.
		_ = app.RemovePidfile()
	}

	if err := app.WritePidfile(os.Getpid()); err != nil {
		return fmt.Errorf("agentdeck start: write pidfile: %w", err)
	}
	defer func() { _ = app.RemovePidfile() }()

	ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	// signal.NotifyContext takes nil → context.Background; cmd.Context() is
	// also background by default, so this is safe.

	if err := a.Start(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("agentdeck start: %w", err)
	}
	return nil
}

// processAlive reports whether a process with the given PID exists. On
// POSIX, sending signal 0 is the standard liveness probe — it never
// actually delivers a signal.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0: probe-only.
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return false
	}
	return true
}

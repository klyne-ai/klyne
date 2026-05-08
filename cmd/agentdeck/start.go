package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/klyne-ai/klyne/internal/app"
	"github.com/klyne-ai/klyne/internal/config"
)

// defaultDemoFixturesDir is the in-repo path to the canonical sample
// JSONL fixtures used by `agentdeck start --demo`. It's resolved
// relative to the current working directory at runtime so users can
// override it in tests by chdir'ing into a different fixture set.
const defaultDemoFixturesDir = "examples/sample-jsonl"

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
//
//	--no-open   suppress the auto-launch of the system browser (CI / headless).
func newStartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the agentdeck daemon (watches CLI logs, serves UI)",
		RunE:  runStart,
	}
	cmd.Flags().Bool("no-open", false, "do not open the dashboard in a browser")
	cmd.Flags().Bool("demo", false,
		"run in offline demo mode: skip live Claude/Codex watchers and seed a temporary DB from examples/sample-jsonl")
	cmd.Flags().String("demo-fixtures", defaultDemoFixturesDir,
		"directory of *.jsonl fixtures to seed when --demo is set (relative to cwd)")
	return cmd
}

func runStart(cmd *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("agentdeck start: load config: %w", err)
	}

	demo, _ := cmd.Flags().GetBool("demo")
	fixturesDir, _ := cmd.Flags().GetString("demo-fixtures")

	if demo {
		// Reroute the DB to a throwaway tempdir path and disable the
		// live connectors. The temp DB is recreated on every start so
		// demo state never leaks between runs and never collides with
		// the user's real ~/.agentdeck/agentdeck.db.
		demoDB := filepath.Join(os.TempDir(), "agentdeck-demo.db")
		if err := os.Remove(demoDB); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("agentdeck start: cleanup demo db: %w", err)
		}
		cfg.Paths.DB = demoDB
		cfg.Connectors.Claude.Enabled = false
		cfg.Connectors.Codex.Enabled = false
	}

	a, err := app.New(cfg)
	if err != nil {
		return fmt.Errorf("agentdeck start: build app: %w", err)
	}

	if demo {
		a.Demo = true
		// Resolve fixtures dir to an absolute path so log lines and
		// the banner show what the user can paste into ls.
		absFixtures, err := filepath.Abs(fixturesDir)
		if err != nil {
			return fmt.Errorf("agentdeck start: resolve fixtures dir: %w", err)
		}
		a.DemoFixturesDir = absFixtures
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

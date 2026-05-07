// Package app — cross-platform browser opener.
//
// On a fresh-machine spec Flow A, the daemon must auto-open the dashboard at
// http://127.0.0.1:7878 in the user's default browser. The choice of command
// depends on GOOS:
//
//	macOS    → "open"
//	Windows  → "rundll32 url.dll,FileProtocolHandler"
//	*nix     → "xdg-open"
//
// OpenBrowser is best-effort — failure to launch a browser does NOT fail the
// daemon (the URL is still printed to stdout so the user can copy it).
//
// Tests (and CI) inject the OpenBrowserFunc field on App so the real exec.Cmd
// is never spawned in unit / integration tests.
package app

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
)

// OpenBrowserFunc opens url in the user's default browser. Implementations
// should return promptly and never block on the spawned process.
type OpenBrowserFunc func(url string) error

// DefaultOpenBrowser is the production implementation: it shells out to the
// platform-native opener and returns immediately after Start (does not Wait
// for the spawned process to exit).
//
// Best-effort: any spawn error is returned, but callers in production should
// treat it as advisory and continue running the daemon.
func DefaultOpenBrowser(url string) error {
	if url == "" {
		return errors.New("app: open browser: empty url")
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		// Linux + BSDs.
		cmd = exec.Command("xdg-open", url)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("app: open browser via %q: %w", cmd.Path, err)
	}
	// Detach: we do not wait for the browser process; it may run for hours.
	go func() { _ = cmd.Wait() }()
	return nil
}

// NoopOpenBrowser is a sentinel browser opener that does nothing. Used in
// CI/tests to assert the URL would have been launched without spawning a
// real process.
func NoopOpenBrowser(_ string) error { return nil }

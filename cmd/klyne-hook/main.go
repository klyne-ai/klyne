// Command klyne-hook is the lightweight stub binary that Claude Code
// invokes for every PreToolUse / UserPromptSubmit / PreCompact / Stop
// hook event. It forwards the event to the long-running klyne daemon
// over a Unix-domain socket and exits with the daemon's reply.
//
// # Why a separate binary
//
// The full klyne binary is ~22 MB on disk and loads ~100 MB resident
// at launch (Go runtime, sqlite3, every linked subcommand). On
// memory-pressured macOS, the kernel's jetsam killer terminates
// freshly-spawned subprocesses before they can run — Claude Code
// reports them as "Failed with non-blocking status code: No stderr
// output." See internal/hookrpc/protocol.go for the full diagnosis.
//
// klyne-hook is a few hundred lines of stdlib Go: net, os, io,
// encoding/json. The resulting binary is small enough to dodge jetsam
// even under heavy pressure. It does no work itself — every hook
// dispatch happens inside the long-running daemon, which already has
// the DB open and is treated as a protected process by the kernel.
//
// # Fallback path
//
// When the daemon socket is missing, unresponsive, or returns a
// transport error, klyne-hook execs the equivalent full `klyne`
// subcommand (e.g. `klyne pretool` for the pretool event). This
// preserves today's behavior for users who haven't started `klyne
// start` and for events that the daemon doesn't yet route (currently
// session-end, see hookrpc.DaemonRoutedEvents).
//
// # Usage
//
//	klyne-hook <event>
//
// Where <event> is one of: pretool, advise, precompact, session-end.
// stdin is the hook payload Claude Code passes; stdout is the
// daemon's reply; stderr is forwarded verbatim. Exit code mirrors
// the daemon's reply (or the fallback subprocess's exit code).
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/hookrpc"
)

// fallbackBinaryEnv is the env var users / installers can set to
// point at the full klyne binary explicitly. When unset, klyne-hook
// looks for `klyne` next to its own executable (matching the layout
// `klyne mcp install` produces) before falling back to $PATH.
const fallbackBinaryEnv = "KLYNE_BINARY"

// readPayloadLimit caps how much stdin we pull before giving up.
// Hook payloads from Claude Code are JSON objects measured in KB;
// anything larger is a malformed input we don't want to ship over
// the socket.
const readPayloadLimit = 256 * 1024

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: klyne-hook <event>")
		os.Exit(2)
	}
	event := hookrpc.Event(os.Args[1])

	// Read the hook payload from stdin. Capped to keep a misbehaving
	// caller from feeding us GB of data.
	payload, err := io.ReadAll(io.LimitReader(os.Stdin, readPayloadLimit))
	if err != nil {
		// Even read errors degrade to the fallback path — better to
		// pay the full binary's launch cost than to drop the hook.
		exitViaFallback(event, nil, fmt.Errorf("read stdin: %w", err))
	}

	// Capture cwd for the daemon's session resolution. Errors here
	// shouldn't be fatal; daemon falls back to its own cwd.
	cwd, _ := os.Getwd()

	// Only events the daemon actually routes go through the fast
	// path. For everything else (today: session-end), skip the dial
	// attempt entirely and exec the fallback — saves ~200ms of
	// connect timeout per event.
	if !hookrpc.DaemonRoutedEvents[event] {
		exitViaFallback(event, payload, nil)
	}

	req := hookrpc.Request{
		Event:   event,
		Payload: payload,
		Cwd:     cwd,
	}

	resp, callErr := hookrpc.Call(req)
	if callErr != nil {
		// Any call error (dial failure, write/read timeout, wedged
		// handler) means the daemon's fast path is unusable. Emit the
		// throttled, user-visible warning so the user knows what to
		// do, then fall back to exec'ing the full binary. The warning
		// covers every failure mode because from the user's
		// perspective they all look the same: hooks not working.
		warnDaemonDownIfNeeded()
		exitViaFallback(event, payload, nil)
	}

	if len(resp.Stdout) > 0 {
		_, _ = os.Stdout.Write([]byte(resp.Stdout))
	}
	if len(resp.Stderr) > 0 {
		_, _ = os.Stderr.Write([]byte(resp.Stderr))
	}
	os.Exit(resp.ExitCode)
}

// exitViaFallback execs the full klyne binary at the equivalent
// subcommand. Does not return. The payload (if non-nil) is piped
// into the child's stdin; reason (if non-nil) is logged to stderr.
// On exec failure, exits 0 — hooks must NEVER block the agent on
// klyne errors.
func exitViaFallback(event hookrpc.Event, payload []byte, reason error) {
	bin := findFallbackBinary()
	if bin == "" {
		// No klyne binary anywhere — neither the daemon's fast path
		// nor the full-binary exec is available. Tell the user
		// explicitly that klyne isn't installed correctly so they
		// don't keep hitting silent hook failures. Throttled the same
		// way the daemon-down warning is.
		warnNoBinaryIfNeeded(reason)
		os.Exit(0)
	}

	if reason != nil {
		fmt.Fprintf(os.Stderr, "klyne-hook: daemon unavailable, falling back: %v\n", reason)
	}

	// If we already drained stdin, restart the fallback with the
	// captured payload piped in. Otherwise let the child consume
	// the real stdin (cheaper when daemon-routed-events filter sent
	// us straight here).
	if payload != nil {
		cmd := exec.Command(bin, string(event))
		cmd.Stdin = &bytesReader{b: payload}
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			// Preserve the child's exit code when it's a subprocess
			// exit; otherwise treat any spawn error as silent 0.
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				os.Exit(exitErr.ExitCode())
			}
			fmt.Fprintf(os.Stderr, "klyne-hook: fallback spawn failed: %v\n", err)
			os.Exit(0)
		}
		os.Exit(0)
	}

	// syscall.Exec replaces this process with the full binary; on
	// success it never returns. Stdin/stdout/stderr inherit.
	err := syscall.Exec(bin, []string{bin, string(event)}, os.Environ())
	fmt.Fprintf(os.Stderr, "klyne-hook: exec %s: %v\n", bin, err)
	os.Exit(0)
}

// findFallbackBinary locates the full klyne binary, preferring:
//  1. $KLYNE_BINARY when set (explicit override).
//  2. A `klyne` next to klyne-hook's own executable.
//  3. `klyne` discoverable on $PATH.
func findFallbackBinary() string {
	if env := os.Getenv(fallbackBinaryEnv); env != "" {
		if _, err := os.Stat(env); err == nil {
			return env
		}
	}
	if exe, err := os.Executable(); err == nil {
		dir := dirName(exe)
		candidate := dir + "klyne"
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	if path, err := exec.LookPath("klyne"); err == nil {
		return path
	}
	return ""
}

// dirName returns p's directory portion including the trailing
// separator.
func dirName(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[:i+1]
		}
	}
	return ""
}

// daemonDownWarningInterval is the minimum gap between consecutive
// "daemon not running" warnings written to stderr. Hooks fire on every
// tool use / prompt submit / compact / stop; without throttling the
// terminal would flood. 15 minutes is short enough that the user sees
// the warning soon after the daemon goes down, long enough that the
// signal stays useful.
const daemonDownWarningInterval = 15 * time.Minute

// daemonDownSentinelName is the basename of the file we touch to track
// the last warning's timestamp. Lives next to the hook socket in
// config.ConfigDir() so it's tied to the daemon's lifecycle.
const daemonDownSentinelName = "daemon-down-warned-at"

// warnDaemonDownIfNeeded writes a one-shot, throttled stderr line
// telling the user the klyne daemon isn't running and how to start it.
// Throttled via a sentinel file in config.ConfigDir(); if the sentinel
// is missing or older than daemonDownWarningInterval, write the
// warning and touch the file. Best-effort — any I/O failure is
// swallowed because hooks must never block.
func warnDaemonDownIfNeeded() {
	if throttled(daemonDownSentinelName) {
		return
	}
	fmt.Fprintln(os.Stderr,
		"klyne: daemon not reachable. Start it with `klyne start` "+
			"to enable jetsam-safe hook handling. Without the daemon, "+
			"hook events spawn the full ~22MB klyne binary which the "+
			"kernel may kill under memory pressure (you'll see "+
			"\"Failed with non-blocking status code\" errors). Run "+
			"`klyne start` once in any terminal — it backgrounds itself.")
	touchSentinel(daemonDownSentinelName)
}

// noBinarySentinelName tracks throttle state for the "no klyne binary
// anywhere" warning — distinct from the daemon-down sentinel because
// the user's fix is different (install klyne, not start it).
const noBinarySentinelName = "no-binary-warned-at"

// warnNoBinaryIfNeeded fires when klyne-hook can find neither the
// daemon socket nor a full klyne binary on disk. That state means the
// install is broken: only the stub got laid down. Throttled the same
// way as the daemon-down warning so we don't flood, but the message
// is distinct so the user knows the fix is `make install`, not
// `klyne start`. reason (if non-nil) is included for diagnostic
// context — e.g. when stdin read failed.
func warnNoBinaryIfNeeded(reason error) {
	if throttled(noBinarySentinelName) {
		return
	}
	if reason != nil {
		fmt.Fprintf(os.Stderr,
			"klyne: hook fired but neither the daemon nor a fallback "+
				"klyne binary is available (and: %v). Reinstall with "+
				"`make install` from the klyne repo, or set the "+
				"KLYNE_BINARY env var to point at your klyne binary.\n",
			reason)
	} else {
		fmt.Fprintln(os.Stderr,
			"klyne: hook fired but neither the daemon nor a fallback "+
				"klyne binary is available. Reinstall with `make install` "+
				"from the klyne repo, or set the KLYNE_BINARY env var to "+
				"point at your klyne binary.")
	}
	touchSentinel(noBinarySentinelName)
}

// throttled returns true when the named sentinel file was touched
// within daemonDownWarningInterval. Used to suppress repeated warnings
// inside the same window.
func throttled(name string) bool {
	sentinel := filepath.Join(config.ConfigDir(), name)
	if info, err := os.Stat(sentinel); err == nil {
		if time.Since(info.ModTime()) < daemonDownWarningInterval {
			return true
		}
	}
	return false
}

// touchSentinel writes (or refreshes the mtime of) the named sentinel
// file in config.ConfigDir(). Best-effort — any I/O failure is
// swallowed.
func touchSentinel(name string) {
	dir := config.ConfigDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	sentinel := filepath.Join(dir, name)
	f, err := os.Create(sentinel)
	if err == nil {
		_ = f.Close()
	}
}

// bytesReader is a one-shot io.Reader over a []byte. exec.Cmd needs
// an io.Reader for Stdin; bytes.NewReader would work but pulls the
// entire bytes package in. This avoids the dep.
type bytesReader struct {
	b   []byte
	off int
}

func (r *bytesReader) Read(p []byte) (int, error) {
	if r.off >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.off:])
	r.off += n
	return n, nil
}

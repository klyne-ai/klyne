// Package hookrpc defines the wire protocol used by the lightweight
// klyne-hook stub binary to forward Claude Code hook events to the
// long-running klyne daemon.
//
// # Why this exists
//
// Each Claude Code hook event (PreToolUse, UserPromptSubmit, PreCompact,
// Stop) spawns a subprocess. When that subprocess is the full klyne
// binary, it loads ~100 MB of resident memory (Go runtime + sqlite3 +
// every linked subcommand) just to do a few KB of work. On a memory-
// pressured macOS, the kernel's jetsam killer terminates the subprocess
// at launch before it can write anything to stderr — the user sees
// "PreToolUse hook error: Failed with non-blocking status code".
//
// klyne-hook is a ~3 MB stub that forwards the hook payload to the
// already-loaded daemon over a Unix-domain socket. The daemon does the
// real work using its already-open SQLite connection. The stub is
// small enough that jetsam never kills it.
//
// # Wire format
//
// Newline-delimited JSON, one Request line and one Response line per
// connection. Connection closes after the response. Hook payloads from
// Claude Code are JSON, so embedded newlines are escaped — no framing
// edge cases with JSON-in-JSON.
//
// # Fallback path
//
// When the daemon socket is missing or unresponsive (connect within
// HookConnectTimeout, complete the roundtrip within HookCallTimeout),
// klyne-hook execs the full klyne binary at the equivalent subcommand
// so behavior degrades to today's "always-spawn" model rather than
// failing the hook. Users running without `klyne start` keep working.
package hookrpc

import (
	"path/filepath"
	"time"

	"github.com/klyne-ai/klyne/internal/config"
)

// SocketName is the Unix-socket filename inside ConfigDir() that the
// daemon binds and the stub dials. Kept short because some platforms
// cap socket paths at 104 bytes.
const SocketName = "hook.sock"

// SocketPath returns the absolute path to the hook RPC socket. Lives
// next to the pidfile so socket lifecycle tracks the daemon lifecycle.
func SocketPath() string {
	return filepath.Join(config.ConfigDir(), SocketName)
}

// HookConnectTimeout bounds how long the stub waits for the initial
// Dial to succeed. Short on purpose: a missing or hung daemon should
// trigger the exec fallback quickly so the hook stays responsive even
// if the daemon is stuck.
const HookConnectTimeout = 200 * time.Millisecond

// HookCallTimeout bounds the total roundtrip (write request + read
// response) once a connection is established. 2 seconds covers every
// real hook handler with margin; longer responses indicate a wedged
// daemon and should fall back to exec.
const HookCallTimeout = 2 * time.Second

// Event identifies which Claude Code hook event the stub is forwarding.
// The daemon's dispatch table maps each value to the matching handler
// (formerly invoked as `klyne pretool` / `klyne advise` / etc.).
type Event string

const (
	EventPreTool      Event = "pretool"
	EventAdvise       Event = "advise"
	EventPreCompact   Event = "precompact"
	EventSessionEnd   Event = "session-end"
)

// AllEvents enumerates every supported event. Used by the dispatcher
// to validate incoming requests and by the install command to know
// which entries to write into Claude Code's settings.json.
var AllEvents = []Event{
	EventPreTool,
	EventAdvise,
	EventPreCompact,
	EventSessionEnd,
}

// DaemonRoutedEvents enumerates the events the v1 daemon actually
// handles in-process. session-end is intentionally excluded for now:
// its compute path includes ~380 LOC of summary + worklog logic that
// hasn't been hoisted out of cmd/klyne yet, so klyne-hook continues
// to exec the full binary for that event. The stub falls back
// transparently; users see no behavior change beyond the SIGKILL
// elimination for the three routed events.
var DaemonRoutedEvents = map[Event]bool{
	EventPreTool:    true,
	EventAdvise:     true,
	EventPreCompact: true,
}

// Request is the single JSON object the stub sends per connection.
//
// The stub forwards the hook event's stdin payload verbatim in Payload
// (base64-encoded to stay safe across the JSON line boundary even
// though Claude Code only sends JSON today). Env and Cwd are captured
// from the stub's process at invocation time so the daemon can resolve
// project paths the same way a freshly-spawned subprocess would.
//
// Protocol version (Version field) lets future stubs and daemons
// negotiate. Both sides reject unknown major versions and continue on
// known versions, so a newer stub talking to an older daemon (or vice
// versa) degrades to the exec fallback rather than misbehaving.
type Request struct {
	Version int               `json:"version"`
	Event   Event             `json:"event"`
	Payload []byte            `json:"payload"`
	Env     map[string]string `json:"env,omitempty"`
	Cwd     string            `json:"cwd"`
}

// Response is the single JSON object the daemon writes back. Stdout
// and Stderr are forwarded verbatim to the stub's own stdio so the
// hook output is indistinguishable from a direct subprocess
// invocation. ExitCode becomes the stub's exit code.
//
// Error is set only when the daemon could not even attempt to dispatch
// (e.g. unknown event, internal panic). The stub treats a non-empty
// Error like a dial failure and falls back to exec.
type Response struct {
	Version  int    `json:"version"`
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`
}

// ProtocolVersion is the wire-format major version. Bump on any
// breaking change to Request/Response shape. Both stub and daemon
// stamp this on every message and refuse mismatched majors.
const ProtocolVersion = 1

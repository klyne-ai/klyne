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
// response) once a connection is established. 6 seconds covers the
// session-end handler's own 5s ceiling with margin; lighter handlers
// (pretool/advise/precompact) finish in well under 100ms either way.
// Longer responses indicate a wedged daemon and should fall back to
// exec.
const HookCallTimeout = 6 * time.Second

// Event identifies which Claude Code hook event the stub is forwarding.
// The daemon's dispatch table maps each value to the matching handler
// (formerly invoked as `klyne pretool` / `klyne advise` / etc.).
type Event string

const (
	EventPreTool      Event = "pretool"
	EventAdvise       Event = "advise"
	EventPreCompact   Event = "precompact"
	EventSessionEnd   Event = "session-end"
	// EventSessionStart fires when Claude Code starts a new session
	// (interactive launch or `claude --print` invocation). It is the
	// ONLY hook that fires in `--print` mode before the assistant
	// generates, so it carries the KLYNE_SUMMARY instruction —
	// without it, scripted/test/CI flows can't reach the assistant
	// with our additionalContext.
	EventSessionStart Event = "session-start"
	// EventCursor is the single event name klyne wires into Cursor
	// CLI's ~/.cursor/hooks.json across every hook (sessionStart,
	// afterAgentResponse, stop, …). The actual Cursor event is
	// discriminated by the `hook_event_name` field inside the JSON
	// payload, dispatched inside `klyne cursor`. Keeping ONE event
	// here simplifies the install table and means cursor doesn't
	// need a per-event entry in DaemonRoutedEvents.
	EventCursor Event = "cursor"
)

// AllEvents enumerates every supported event. Used by the dispatcher
// to validate incoming requests and by the install command to know
// which entries to write into Claude Code's settings.json.
var AllEvents = []Event{
	EventPreTool,
	EventAdvise,
	EventPreCompact,
	EventSessionEnd,
	EventSessionStart,
	EventCursor,
}

// DaemonRoutedEvents enumerates the events the daemon handles
// in-process. All Claude Code hook events route through the daemon
// when it is reachable; the stub still falls back to exec'ing the
// full binary when the daemon socket is missing or unresponsive.
var DaemonRoutedEvents = map[Event]bool{
	EventPreTool:      true,
	EventAdvise:       true,
	EventPreCompact:   true,
	EventSessionEnd:   true,
	EventSessionStart: true,
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

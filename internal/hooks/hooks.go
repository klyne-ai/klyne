// Package hooks contains the daemon-callable implementations of the
// four Claude Code hook entry points. Each function in this package
// matches the contract of the corresponding `klyne <hook>` cobra
// subcommand in cmd/klyne/, but takes its inputs as plain values
// (stdin, env, cwd) instead of via cobra and never touches global
// process state — so the same code runs both as a one-shot subprocess
// (today's path) and as an in-daemon dispatch (the hookrpc fast path).
//
// # Why this package exists
//
// Every hook event Claude Code fires spawns the full `klyne` binary.
// On a memory-pressured machine the kernel's jetsam killer terminates
// that subprocess at launch before it can do anything — users see
// silent "PreToolUse hook error" lines on every tool call. The fix is
// to keep the heavy work inside the long-lived `klyne` daemon (which
// jetsam treats as a protected process) and have a tiny stub forward
// hook events over a Unix socket. This package is the daemon-side
// landing pad for those forwarded events.
//
// # Code duplication note
//
// The compute logic here intentionally mirrors what lives in
// cmd/klyne/advise.go and cmd/klyne/session_end.go. We don't share
// the original code because those functions are in `package main` and
// can't be imported. A v2 refactor would move both copies behind a
// single internal package; for now the duplication is bounded
// (~250 lines) and the test suite for the cmd/klyne path stays
// untouched.
package hooks

import (
	"bytes"
	"context"
	"io"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/mcpserver"
	"github.com/klyne-ai/klyne/internal/policy"
	"github.com/klyne-ai/klyne/internal/store"
)

// Result is the value every hook handler returns to its caller (the
// daemon-side dispatcher in internal/app, or a test). The fields map
// directly onto hookrpc.Response: Stdout becomes the hook's printed
// output (consumed by Claude Code), Stderr is for debugging only,
// ExitCode is the process exit code the stub will mimic.
//
// Convention: ExitCode is 0 in every documented success-or-degraded
// path. Hooks must never block Claude Code on klyne errors — any
// internal failure is logged to Stderr and exits 0. Non-zero exit
// codes are reserved for "stub couldn't reach daemon AND couldn't exec
// fallback" hard failures handled outside this package.
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// PreTool is the daemon-side implementation of `klyne pretool`. The
// Claude Code PreToolUse hook fires this on every tool call; the
// handler matches the proposed command against the risky-command
// policy, snapshots the working tree on match, and emits a
// systemMessage when relevant.
//
// db may be nil — the policy match still runs but the snapshot row
// won't be persisted. Same degradation pattern as the cobra
// subcommand: prefer a partial result to a blocking error.
//
// matcher is loaded once at daemon startup (see internal/app wiring).
// Passing it in keeps this function pure and easy to test.
func PreTool(ctx context.Context, stdin io.Reader, db *store.DB, matcher *policy.Matcher) Result {
	var out, errb bytes.Buffer
	if matcher == nil {
		// No policy means nothing to match. Exit silently, matching
		// the cobra path's graceful degrade when the file is missing.
		return Result{}
	}
	res, err := mcpserver.HandlePreToolUse(ctx, stdin, db, matcher)
	if err != nil {
		_, _ = errb.WriteString("klyne pretool: " + err.Error() + "\n")
		return Result{Stderr: errb.Bytes()}
	}
	if res != nil && res.Output != "" {
		out.WriteString(res.Output)
		out.WriteByte('\n')
	}
	return Result{Stdout: out.Bytes(), Stderr: errb.Bytes()}
}

// PreCompact is the daemon-side implementation of `klyne precompact`.
// The Claude Code PreCompact hook fires this before a /compact event;
// the handler either records the boundary or, when armed, returns a
// blocking decision payload that aborts the compact.
//
// db may be nil; in that case the handler degrades to "always allow,"
// matching the cobra path.
func PreCompact(ctx context.Context, stdin io.Reader, db *store.DB) Result {
	var out, errb bytes.Buffer
	res, err := mcpserver.HandlePreCompact(ctx, stdin, db)
	if err != nil {
		_, _ = errb.WriteString("klyne precompact: " + err.Error() + "\n")
		return Result{Stderr: errb.Bytes()}
	}
	if res != nil && res.Output != "" {
		out.WriteString(res.Output)
		out.WriteByte('\n')
	}
	return Result{Stdout: out.Bytes(), Stderr: errb.Bytes()}
}

// resolveDB hands out a *store.DB the handlers can use. The daemon
// passes in its own already-open DB so handlers reuse the connection
// pool (the big perf win of running in-process). Tests pass nil to
// exercise the no-DB degradation path.
//
// Defined as a hook so internal/app can override it after the daemon
// boots without making every handler take an extra parameter.
var resolveDB = func(ctx context.Context) (*store.DB, error) {
	return store.Open(ctx, config.DBPath())
}

// SetDB lets the daemon inject its own *store.DB at startup so all
// subsequent hook dispatches reuse the daemon's connection pool. Pass
// nil to revert to opening a fresh DB per call (test-only mode).
func SetDB(db *store.DB) {
	if db == nil {
		resolveDB = func(ctx context.Context) (*store.DB, error) {
			return store.Open(ctx, config.DBPath())
		}
		return
	}
	resolveDB = func(_ context.Context) (*store.DB, error) {
		return db, nil
	}
}

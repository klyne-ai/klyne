package app

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/klyne-ai/klyne/internal/hookrpc"
	"github.com/klyne-ai/klyne/internal/hooks"
	"github.com/klyne-ai/klyne/internal/policy"
)

// hookserver.go wires the klyne-hook RPC listener into the daemon's
// lifecycle. The daemon binds a Unix socket at ConfigDir()/hook.sock;
// the lightweight klyne-hook stub binary forwards each Claude Code
// hook event to that socket, the daemon dispatches to the matching
// internal/hooks handler (using its already-open DB connection), and
// the response is streamed back.
//
// See internal/hookrpc/protocol.go for the full design rationale; the
// short version is "stop spawning a 100MB klyne subprocess per hook
// event, because macOS jetsam kills it under memory pressure."

// startHookServer brings up the hookrpc listener. Returns nil if the
// daemon's HookRPCDisabled config flag is set — useful for tests and
// for users who want to opt out of the fast path entirely. Errors
// from socket binding are surfaced; the caller decides whether to
// abort daemon startup (we choose to log + continue, since the stub
// has a fallback to exec the full binary anyway).
func (a *App) startHookServer(ctx context.Context) error {
	if a == nil || a.db == nil {
		return nil
	}

	// Load the risky-command policy once at startup so every PreTool
	// dispatch reuses the parsed matcher. Falls back to a permissive
	// empty matcher when the policy file is missing — same graceful
	// degradation as the cobra path.
	matcher, err := loadDaemonPolicy()
	if err != nil {
		a.logger.Warn("hookserver: policy load failed (continuing without snapshots)",
			slog.Any("error", err))
		matcher = nil
	}

	dispatch := hookrpc.Dispatcher{
		hookrpc.EventPreTool: func(ctx context.Context, req hookrpc.Request) ([]byte, []byte, int, error) {
			res := hooks.PreTool(ctx, bytes.NewReader(req.Payload), a.db, matcher)
			return res.Stdout, res.Stderr, res.ExitCode, nil
		},
		hookrpc.EventPreCompact: func(ctx context.Context, req hookrpc.Request) ([]byte, []byte, int, error) {
			res := hooks.PreCompact(ctx, bytes.NewReader(req.Payload), a.db)
			return res.Stdout, res.Stderr, res.ExitCode, nil
		},
		hookrpc.EventAdvise: func(ctx context.Context, req hookrpc.Request) ([]byte, []byte, int, error) {
			res := hooks.Advise(ctx, bytes.NewReader(req.Payload), req.Cwd)
			return res.Stdout, res.Stderr, res.ExitCode, nil
		},
		hookrpc.EventSessionEnd: func(ctx context.Context, req hookrpc.Request) ([]byte, []byte, int, error) {
			res := hooks.SessionEnd(ctx, bytes.NewReader(req.Payload), a.db)
			return res.Stdout, res.Stderr, res.ExitCode, nil
		},
	}

	// Inject the daemon's open DB so hook handlers reuse the
	// connection pool instead of opening their own per-call (the
	// whole reason this fast path exists is to skip the heavy
	// per-call setup).
	hooks.SetDB(a.db)

	srv, err := hookrpc.NewServer(dispatch)
	if err != nil {
		return fmt.Errorf("hookserver: %w", err)
	}
	a.hookSrv = srv

	a.hookWG.Add(1)
	go func() {
		defer a.hookWG.Done()
		if err := srv.Serve(ctx); err != nil {
			a.logger.Error("hookserver: serve ended", slog.Any("error", err))
		}
	}()

	a.logger.Info("hookserver: listening", slog.String("socket", hookrpc.SocketPath()))
	return nil
}

// hookSrvIface is the minimal subset of *hookrpc.Server the app
// package depends on. Keeping it as an interface in this file lets
// app.go declare the App.hookSrv field without importing internal/
// hookrpc, which keeps the central composition root's import list
// tidy and prevents future hook-server changes from forcing app.go
// recompilation when only this file changed.
type hookSrvIface interface {
	Stop() error
}

// stopHookServer is called by App.Stop. Idempotent — guards on nil so
// daemons that disabled the hookserver via config don't panic.
func (a *App) stopHookServer() error {
	if a == nil || a.hookSrv == nil {
		return nil
	}
	err := a.hookSrv.Stop()
	a.hookWG.Wait()
	a.hookSrv = nil
	hooks.SetDB(nil) // reset to per-call DB open for any later in-process callers (tests).
	return err
}

// loadDaemonPolicy resolves the risky-command policy file the same
// way the cobra `klyne pretool` subcommand does: prefer the file
// next to the running binary, fall back to the working directory,
// final fall back is the bundled default if present.
//
// Returns (nil, nil) when no file is found anywhere — a nil matcher
// is the documented "policy disabled" signal that the handlers
// already handle.
func loadDaemonPolicy() (*policy.Matcher, error) {
	if env := os.Getenv("KLYNE_POLICY_PATH"); env != "" {
		return policy.LoadFile(env)
	}
	exe, err := os.Executable()
	if err == nil {
		// Look next to the binary first — `make install` lays the
		// policy dir alongside the binary.
		dir := exe
		for len(dir) > 0 && dir[len(dir)-1] != '/' && dir[len(dir)-1] != '\\' {
			dir = dir[:len(dir)-1]
		}
		candidate := dir + "policy/risky_commands.json"
		if _, statErr := os.Stat(candidate); statErr == nil {
			return policy.LoadFile(candidate)
		}
	}
	// Last resort: relative to cwd (dev mode, `go run` from repo root).
	const dev = "policy/risky_commands.json"
	if _, err := os.Stat(dev); err == nil {
		return policy.LoadFile(dev)
	}
	return nil, nil
}

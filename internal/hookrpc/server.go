package hookrpc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Handler runs the daemon-side work for one hook event. Returned
// stdout/stderr are sent back to the stub verbatim; exitCode becomes
// the stub's exit code. err is mapped to Response.Error and aborts
// dispatch (the stub falls back to exec).
//
// The context carries the connection's read/write deadline so handlers
// that hang for any reason don't pin a goroutine forever.
type Handler func(ctx context.Context, req Request) (stdout, stderr []byte, exitCode int, err error)

// Dispatcher maps each Event to its Handler. The daemon constructs one
// of these at startup from its real handlers and passes it to Serve.
type Dispatcher map[Event]Handler

// Server owns the Unix-socket listener and serves hookrpc requests
// until Stop is called. Safe to construct once per daemon process.
type Server struct {
	dispatch Dispatcher
	listener net.Listener
	wg       sync.WaitGroup
	stopped  chan struct{}
}

// NewServer prepares a Server bound to SocketPath(). It removes any
// stale socket file left over from a previous daemon crash before
// binding. The returned Server is not yet listening — call Serve.
//
// The stale-socket cleanup is safe because we hold the daemon pidfile
// elsewhere: a fresh daemon means the previous one is gone, so any
// socket on disk is by definition stale. If two daemons are racing,
// the pidfile check (in cmd/klyne/start.go) catches it before we get
// here.
func NewServer(dispatch Dispatcher) (*Server, error) {
	path := SocketPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("hookrpc: mkdir %s: %w", filepath.Dir(path), err)
	}
	// Best-effort remove of stale socket — ignore "not exist" errors.
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("hookrpc: clear stale socket %s: %w", path, err)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("hookrpc: listen on %s: %w", path, err)
	}
	// Restrict socket to the current user. Other users on the host
	// must not be able to invoke klyne hook handlers.
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("hookrpc: chmod socket: %w", err)
	}
	return &Server{
		dispatch: dispatch,
		listener: ln,
		stopped:  make(chan struct{}),
	}, nil
}

// Serve runs the accept loop until the underlying listener is closed
// (typically via Stop). Each connection is handled in its own
// goroutine. Returns nil on clean shutdown, the underlying error
// otherwise.
func (s *Server) Serve(ctx context.Context) error {
	// Watcher goroutine: when ctx cancels, close the listener which
	// breaks Accept with net.ErrClosed.
	go func() {
		<-ctx.Done()
		_ = s.listener.Close()
	}()

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				s.wg.Wait()
				close(s.stopped)
				return nil
			}
			// Transient accept errors (rare on Unix sockets) shouldn't
			// kill the listener; log via the dispatcher? Caller doesn't
			// have a logger here, so swallow and continue.
			continue
		}
		s.wg.Add(1)
		go func(c net.Conn) {
			defer s.wg.Done()
			defer c.Close() //nolint:errcheck
			s.handleConn(ctx, c)
		}(conn)
	}
}

// Stop closes the listener, waits for in-flight handlers to drain,
// and removes the socket file. Safe to call once. Subsequent calls
// are no-ops.
func (s *Server) Stop() error {
	_ = s.listener.Close()
	<-s.stopped
	if err := os.Remove(SocketPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("hookrpc: remove socket: %w", err)
	}
	return nil
}

func (s *Server) handleConn(ctx context.Context, c net.Conn) {
	// Per-connection deadline mirrors the client's HookCallTimeout so
	// a hung handler doesn't tie up the goroutine forever. Handlers
	// that need longer should be redesigned (return immediately,
	// background the work).
	deadline := time.Now().Add(HookCallTimeout)
	if err := c.SetDeadline(deadline); err != nil {
		writeError(c, "set deadline: "+err.Error())
		return
	}

	br := bufio.NewReader(c)
	line, err := br.ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		writeError(c, "read request: "+err.Error())
		return
	}

	var req Request
	if err := json.Unmarshal(bytes.TrimSpace(line), &req); err != nil {
		writeError(c, "decode request: "+err.Error())
		return
	}
	if req.Version != 0 && req.Version != ProtocolVersion {
		writeError(c, fmt.Sprintf("protocol version %d unsupported (server %d)",
			req.Version, ProtocolVersion))
		return
	}

	handler, ok := s.dispatch[req.Event]
	if !ok {
		writeError(c, fmt.Sprintf("unknown event %q", req.Event))
		return
	}

	stdout, stderr, exitCode, herr := safeCall(ctx, handler, req)
	resp := Response{
		Version:  ProtocolVersion,
		Stdout:   string(stdout),
		Stderr:   string(stderr),
		ExitCode: exitCode,
	}
	if herr != nil {
		resp.Error = herr.Error()
	}
	enc := json.NewEncoder(c)
	_ = enc.Encode(&resp)
}

// safeCall wraps the user-supplied Handler in a panic recovery so one
// buggy handler can't crash the entire daemon.
func safeCall(ctx context.Context, h Handler, req Request) (stdout, stderr []byte, exitCode int, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("handler panic: %v", r)
			exitCode = 0 // hooks never block; treat panic as silent failure
		}
	}()
	return h(ctx, req)
}

func writeError(c net.Conn, msg string) {
	resp := Response{
		Version: ProtocolVersion,
		Error:   msg,
	}
	enc := json.NewEncoder(c)
	_ = enc.Encode(&resp)
}

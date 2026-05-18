package hookrpc

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"
)

// ErrDaemonUnavailable signals the stub should fall back to exec'ing
// the full klyne binary. Returned on dial failure, on read/write
// timeout, on protocol-version mismatch, and on any response that
// carries a non-empty Error field. Distinct sentinel so callers can
// branch cleanly without string-matching error messages.
var ErrDaemonUnavailable = errors.New("hookrpc: daemon unavailable")

// Call performs one stub→daemon roundtrip and returns the daemon's
// Response. On any transport or protocol failure it returns a non-nil
// error wrapping ErrDaemonUnavailable; the stub interprets this as
// "fall back to exec" rather than failing the hook outright.
//
// Concurrent calls open separate connections — the daemon accepts
// each in its own goroutine. The socket itself is fine under
// concurrent dialers; Unix-domain sockets do not impose accept
// serialization.
func Call(req Request) (*Response, error) {
	req.Version = ProtocolVersion

	conn, err := net.DialTimeout("unix", SocketPath(), HookConnectTimeout)
	if err != nil {
		return nil, fmt.Errorf("%w: dial: %v", ErrDaemonUnavailable, err)
	}
	defer conn.Close() //nolint:errcheck

	deadline := time.Now().Add(HookCallTimeout)
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, fmt.Errorf("%w: set deadline: %v", ErrDaemonUnavailable, err)
	}

	enc := json.NewEncoder(conn)
	if err := enc.Encode(&req); err != nil {
		return nil, fmt.Errorf("%w: write request: %v", ErrDaemonUnavailable, err)
	}

	br := bufio.NewReader(conn)
	line, err := br.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("%w: read response: %v", ErrDaemonUnavailable, err)
	}
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("%w: decode response: %v", ErrDaemonUnavailable, err)
	}
	if resp.Version != 0 && resp.Version != ProtocolVersion {
		return nil, fmt.Errorf("%w: protocol mismatch: daemon=%d stub=%d",
			ErrDaemonUnavailable, resp.Version, ProtocolVersion)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("%w: daemon error: %s", ErrDaemonUnavailable, resp.Error)
	}
	return &resp, nil
}

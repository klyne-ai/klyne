//go:build darwin

package claudeauth

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// readKeychainEntry shells out to the system `security` tool to fetch a
// generic-password entry by service name. Returns the raw bytes (the JSON
// blob Claude Code stored).
//
// The `-w` flag instructs `security` to print the password value alone.
// Any non-zero exit usually means the entry is missing — surface that as
// ErrCredentialsNotFound so callers can fall back without leaking shell
// noise.
func readKeychainEntry(service string) ([]byte, error) {
	cmd := exec.Command("security", "find-generic-password", "-s", service, "-w")
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			// `security` writes "could not be found" diagnostic to stderr
			// when the entry is missing. Treat any non-zero exit as
			// "not found" — there's no useful distinction for our caller.
			return nil, ErrCredentialsNotFound
		}
		return nil, fmt.Errorf("claudeauth: invoke security: %w", err)
	}
	out = []byte(strings.TrimRight(string(out), "\r\n"))
	if len(out) == 0 {
		return nil, ErrCredentialsNotFound
	}
	return out, nil
}

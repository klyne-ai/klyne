//go:build !darwin

package claudeauth

// readKeychainEntry returns ErrUnsupportedPlatform on non-darwin systems.
// The macOS Keychain is the only credential store Claude Code v1 writes
// to, so without it we cannot retrieve a user's OAuth token.
func readKeychainEntry(_ string) ([]byte, error) {
	return nil, ErrUnsupportedPlatform
}

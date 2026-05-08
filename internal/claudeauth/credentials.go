// Package claudeauth reads the OAuth credentials Claude Code stores in the
// macOS Keychain so agentdeck can call Anthropic's first-party
// /api/oauth/usage endpoint with the same auth the official CLI uses.
//
// Provenance: Claude Code on macOS persists credentials as a generic
// password under the keychain service "Claude Code-credentials". The blob
// is JSON shaped like:
//
//	{
//	  "claudeAiOauth": {
//	    "accessToken":   "...",
//	    "refreshToken":  "...",
//	    "expiresAt":     <epoch-ms>,
//	    "subscriptionType": "max"
//	  }
//	}
//
// We read accessToken only — refresh is delegated to Claude Code itself.
// If the token has expired, calls to Anthropic will 401 and we fall back
// to local token-based estimation; the user can re-login through Claude
// Code to refresh.
//
// Platform: this package is functional on darwin only. On other systems
// LoadClaudeCredentials returns ErrUnsupportedPlatform — the caller is
// expected to fall back gracefully.
package claudeauth

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrUnsupportedPlatform is returned by LoadClaudeCredentials on systems
// that don't have the Claude Code keychain entry (every non-darwin OS in
// v1).
var ErrUnsupportedPlatform = errors.New("claudeauth: unsupported platform")

// ErrCredentialsNotFound is returned when the Keychain entry is missing.
// Most often this means Claude Code is not installed or the user has not
// completed first-run login.
var ErrCredentialsNotFound = errors.New("claudeauth: credentials not found in keychain")

// Credentials is the subset of the Keychain blob we need.
type Credentials struct {
	AccessToken      string
	RefreshToken     string
	ExpiresAt        int64 // epoch-ms; 0 if absent
	SubscriptionType string
}

// keychainBlob mirrors the JSON shape Claude Code stores. Only fields we
// consume are listed.
type keychainBlob struct {
	ClaudeAiOAuth struct {
		AccessToken      string `json:"accessToken"`
		RefreshToken     string `json:"refreshToken"`
		ExpiresAt        int64  `json:"expiresAt"`
		SubscriptionType string `json:"subscriptionType"`
	} `json:"claudeAiOauth"`
}

// LoadClaudeCredentials returns the OAuth credentials Claude Code stored
// in the system keychain. On darwin it shells out to the system
// `security` tool; on other platforms it returns ErrUnsupportedPlatform.
//
// Callers MUST handle errors gracefully — missing creds is a normal
// (degraded) state, not a daemon-fatal condition.
func LoadClaudeCredentials() (*Credentials, error) {
	raw, err := readKeychainEntry("Claude Code-credentials")
	if err != nil {
		return nil, err
	}
	return parseCredentials(raw)
}

// parseCredentials decodes the Keychain JSON blob.
func parseCredentials(raw []byte) (*Credentials, error) {
	var blob keychainBlob
	if err := json.Unmarshal(raw, &blob); err != nil {
		return nil, fmt.Errorf("claudeauth: parse keychain blob: %w", err)
	}
	if blob.ClaudeAiOAuth.AccessToken == "" {
		return nil, ErrCredentialsNotFound
	}
	return &Credentials{
		AccessToken:      blob.ClaudeAiOAuth.AccessToken,
		RefreshToken:     blob.ClaudeAiOAuth.RefreshToken,
		ExpiresAt:        blob.ClaudeAiOAuth.ExpiresAt,
		SubscriptionType: blob.ClaudeAiOAuth.SubscriptionType,
	}, nil
}

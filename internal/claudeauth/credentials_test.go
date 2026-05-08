package claudeauth

import (
	"errors"
	"testing"
)

func TestParseCredentials_HappyPath(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"claudeAiOauth":{"accessToken":"abc","refreshToken":"def","expiresAt":1700000000000,"subscriptionType":"max"}}`)
	got, err := parseCredentials(raw)
	if err != nil {
		t.Fatalf("parseCredentials: %v", err)
	}
	if got.AccessToken != "abc" {
		t.Errorf("AccessToken = %q, want %q", got.AccessToken, "abc")
	}
	if got.RefreshToken != "def" {
		t.Errorf("RefreshToken = %q, want %q", got.RefreshToken, "def")
	}
	if got.ExpiresAt != 1700000000000 {
		t.Errorf("ExpiresAt = %d, want 1700000000000", got.ExpiresAt)
	}
	if got.SubscriptionType != "max" {
		t.Errorf("SubscriptionType = %q, want %q", got.SubscriptionType, "max")
	}
}

func TestParseCredentials_MissingAccessToken(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"claudeAiOauth":{"refreshToken":"r","expiresAt":1}}`)
	_, err := parseCredentials(raw)
	if !errors.Is(err, ErrCredentialsNotFound) {
		t.Errorf("expected ErrCredentialsNotFound, got %v", err)
	}
}

func TestParseCredentials_MalformedJSON(t *testing.T) {
	t.Parallel()
	raw := []byte(`{not json`)
	if _, err := parseCredentials(raw); err == nil {
		t.Errorf("expected parse error, got nil")
	}
}

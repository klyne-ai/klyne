package usage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/mohitpatell/agentdeck/internal/api"
	"github.com/mohitpatell/agentdeck/internal/claudeauth"
)

// OAuth endpoint and headers Claude Code uses on macOS. Confirmed by
// reading the official ccusage / Claude-Code-Usage-Monitor scripts and
// the user's own SwiftBar plugin.
//
// Cache TTL is 5 minutes — long enough that page reloads, SSE-driven
// refreshes, and the UI's own poll interval all coalesce into a single
// upstream call per window, well below Anthropic's rate limit. The
// SwiftBar plugin polls at the same cadence for the same reason.
const (
	oauthUsageURL = "https://api.anthropic.com/api/oauth/usage"
	oauthBeta     = "oauth-2025-04-20"
	oauthCacheTTL = 5 * time.Minute
	oauthTimeout  = 5 * time.Second
)

// CredentialLoader returns the user's Claude OAuth token. Defaulted to
// claudeauth.LoadClaudeCredentials in production; tests inject a stub.
type CredentialLoader func() (*claudeauth.Credentials, error)

// OAuthFetcher fetches /api/oauth/usage on demand with a small TTL cache
// so repeated /usage calls don't hammer Anthropic.
//
// Failures (no token, expired token, network blip, non-200 response) are
// cached as nil for half the TTL — this keeps the badge feeling live
// without pegging the daemon on a flapping network.
type OAuthFetcher struct {
	loadCreds CredentialLoader
	client    *http.Client

	mu       sync.Mutex
	cached   *api.OAuthUsage
	cachedAt time.Time
}

// NewOAuthFetcher constructs an OAuthFetcher using the production
// credential loader and a 5s-timeout HTTP client.
func NewOAuthFetcher() *OAuthFetcher {
	return &OAuthFetcher{
		loadCreds: claudeauth.LoadClaudeCredentials,
		client:    &http.Client{Timeout: oauthTimeout},
	}
}

// SetCredentialLoader swaps the credential loader. Used by tests.
func (f *OAuthFetcher) SetCredentialLoader(l CredentialLoader) {
	f.loadCreds = l
}

// SetHTTPClient swaps the HTTP client. Used by tests.
func (f *OAuthFetcher) SetHTTPClient(c *http.Client) {
	f.client = c
}

// Fetch returns the latest /api/oauth/usage payload, served from cache
// when fresh. Returns (nil, nil) when no credentials are available or
// the upstream call failed — callers should treat that as "OAuth data
// unavailable" and fall back to the local token estimate, NOT as an
// error.
//
// A genuine internal error (malformed response we couldn't parse) is
// returned only for diagnostic visibility; the handler should still
// return a usable response.
func (f *OAuthFetcher) Fetch(ctx context.Context) (*api.OAuthUsage, error) {
	f.mu.Lock()
	if !f.cachedAt.IsZero() && time.Since(f.cachedAt) < oauthCacheTTL {
		out := f.cached
		f.mu.Unlock()
		return out, nil
	}
	f.mu.Unlock()

	usage, err := f.fetchFresh(ctx)
	// Whatever the outcome, freeze the result for at least half the TTL
	// so a flap doesn't get retried on every request.
	f.mu.Lock()
	f.cached = usage
	if err != nil {
		f.cachedAt = time.Now().Add(-oauthCacheTTL / 2)
	} else {
		f.cachedAt = time.Now()
	}
	f.mu.Unlock()
	return usage, err
}

// rawWindow mirrors the Anthropic JSON shape for one window slice.
type rawWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    string  `json:"resets_at"`
}

// rawUsage mirrors the relevant subset of the Anthropic JSON. There are
// many sibling windows we don't surface (seven_day_opus,
// seven_day_omelette, etc.); they are ignored.
type rawUsage struct {
	FiveHour       *rawWindow `json:"five_hour"`
	SevenDay       *rawWindow `json:"seven_day"`
	SevenDaySonnet *rawWindow `json:"seven_day_sonnet"`
}

// fetchFresh executes the OAuth request and decodes the response into
// the contract DTO. Returns (nil, nil) for known degraded states
// (missing creds, auth failure) so callers fall back transparently.
func (f *OAuthFetcher) fetchFresh(ctx context.Context) (*api.OAuthUsage, error) {
	creds, err := f.loadCreds()
	if err != nil {
		// No credentials = degraded but normal. Don't return the error
		// to the caller — they'd just log noise.
		if errors.Is(err, claudeauth.ErrCredentialsNotFound) ||
			errors.Is(err, claudeauth.ErrUnsupportedPlatform) {
			return nil, nil
		}
		return nil, fmt.Errorf("oauth: load credentials: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, oauthTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, oauthUsageURL, nil)
	if err != nil {
		return nil, fmt.Errorf("oauth: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+creds.AccessToken)
	req.Header.Set("anthropic-beta", oauthBeta)
	req.Header.Set("Accept", "application/json")
	// Identify ourselves to Anthropic so noisy queries from agentdeck
	// are distinguishable from the official CLI.
	req.Header.Set("User-Agent", "agentdeck/0.1 (+https://github.com/mohitpatell/agentdeck)")

	resp, err := f.client.Do(req)
	if err != nil {
		// Network/timeout errors are normal in the field; degrade silently.
		return nil, nil //nolint:nilerr
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		// Drain a bit so we can include the message in the error. Cap
		// at 1 KiB — we don't want to log a megabyte of HTML if Anthropic
		// returns a load-balancer error page.
		_, _ = io.CopyN(io.Discard, resp.Body, 1024)
		// 401 = expired token. 429 = rate limited. Both are "degraded
		// but expected"; the caller will fall back to the local estimate
		// without surfacing a scary error.
		return nil, nil
	}

	var raw rawUsage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("oauth: decode response: %w", err)
	}

	out := &api.OAuthUsage{
		SubscriptionType: creds.SubscriptionType,
		FiveHour:         convertWindow(raw.FiveHour),
		SevenDay:         convertWindow(raw.SevenDay),
		SevenDaySonnet:   convertWindow(raw.SevenDaySonnet),
	}
	return out, nil
}

// convertWindow translates the Anthropic JSON shape into our DTO. Returns
// nil when the API itself returned null.
func convertWindow(w *rawWindow) *api.OAuthWindow {
	if w == nil {
		return nil
	}
	return &api.OAuthWindow{
		UtilizationPct: w.Utilization,
		ResetsAt:       parseISOToMillis(w.ResetsAt),
	}
}

// parseISOToMillis parses an RFC 3339 timestamp into epoch-ms. Returns 0
// for empty/malformed input — the frontend treats 0 as "no reset known".
func parseISOToMillis(s string) int64 {
	if s == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t, err = time.Parse(time.RFC3339, s)
		if err != nil {
			return 0
		}
	}
	return t.UnixMilli()
}

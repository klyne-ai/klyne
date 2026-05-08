package usage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/claudeauth"
)

// roundTripFn lets a test inject a synthetic HTTP response without a real
// server — keeps these tests offline, fast, and hermetic.
type roundTripFn func(*http.Request) (*http.Response, error)

func (f roundTripFn) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func newJSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

func TestOAuthFetcher_HappyPath(t *testing.T) {
	t.Parallel()

	const sample = `{
		"five_hour":      {"utilization": 12.5, "resets_at": "2026-05-07T18:40:01.359132+00:00"},
		"seven_day":      {"utilization": 34.0, "resets_at": "2026-05-09T10:00:01.359159+00:00"},
		"seven_day_sonnet": {"utilization": 15.0, "resets_at": "2026-05-09T10:00:00.359167+00:00"},
		"seven_day_opus": null
	}`

	var seenAuth, seenBeta string
	f := NewOAuthFetcher()
	f.SetCredentialLoader(func() (*claudeauth.Credentials, error) {
		return &claudeauth.Credentials{AccessToken: "tok-abc", SubscriptionType: "max"}, nil
	})
	f.SetHTTPClient(&http.Client{Transport: roundTripFn(func(r *http.Request) (*http.Response, error) {
		seenAuth = r.Header.Get("Authorization")
		seenBeta = r.Header.Get("anthropic-beta")
		if r.URL.String() != oauthUsageURL {
			t.Errorf("URL = %q, want %q", r.URL.String(), oauthUsageURL)
		}
		return newJSONResponse(200, sample), nil
	})})

	got, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got == nil {
		t.Fatal("Fetch: got nil OAuthUsage")
	}
	if seenAuth != "Bearer tok-abc" {
		t.Errorf("Authorization header = %q, want %q", seenAuth, "Bearer tok-abc")
	}
	if seenBeta != oauthBeta {
		t.Errorf("anthropic-beta header = %q, want %q", seenBeta, oauthBeta)
	}
	if got.SubscriptionType != "max" {
		t.Errorf("SubscriptionType = %q, want %q", got.SubscriptionType, "max")
	}
	if got.FiveHour == nil || got.FiveHour.UtilizationPct != 12.5 {
		t.Errorf("FiveHour utilization = %+v, want 12.5", got.FiveHour)
	}
	if got.SevenDay == nil || got.SevenDay.UtilizationPct != 34.0 {
		t.Errorf("SevenDay utilization = %+v, want 34.0", got.SevenDay)
	}
	if got.SevenDaySonnet == nil || got.SevenDaySonnet.UtilizationPct != 15.0 {
		t.Errorf("SevenDaySonnet utilization = %+v, want 15.0", got.SevenDaySonnet)
	}
	// Verify the ResetsAt parses to a sensible epoch-ms.
	wantMs := time.Date(2026, 5, 7, 18, 40, 1, 359132000, time.UTC).UnixMilli()
	if got.FiveHour.ResetsAt != wantMs {
		t.Errorf("FiveHour.ResetsAt = %d, want %d", got.FiveHour.ResetsAt, wantMs)
	}
}

func TestOAuthFetcher_NoCredentialsIsNotAnError(t *testing.T) {
	t.Parallel()
	f := NewOAuthFetcher()
	f.SetCredentialLoader(func() (*claudeauth.Credentials, error) {
		return nil, claudeauth.ErrCredentialsNotFound
	})
	got, err := f.Fetch(context.Background())
	if err != nil {
		t.Errorf("expected nil error on missing creds, got %v", err)
	}
	if got != nil {
		t.Errorf("expected nil OAuthUsage on missing creds, got %+v", got)
	}
}

func TestOAuthFetcher_UnsupportedPlatformIsNotAnError(t *testing.T) {
	t.Parallel()
	f := NewOAuthFetcher()
	f.SetCredentialLoader(func() (*claudeauth.Credentials, error) {
		return nil, claudeauth.ErrUnsupportedPlatform
	})
	got, err := f.Fetch(context.Background())
	if err != nil || got != nil {
		t.Errorf("expected (nil, nil), got (%+v, %v)", got, err)
	}
}

func TestOAuthFetcher_HTTPErrorDegrades(t *testing.T) {
	t.Parallel()
	f := NewOAuthFetcher()
	f.SetCredentialLoader(func() (*claudeauth.Credentials, error) {
		return &claudeauth.Credentials{AccessToken: "tok"}, nil
	})
	f.SetHTTPClient(&http.Client{Transport: roundTripFn(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("network unreachable")
	})})
	got, err := f.Fetch(context.Background())
	if err != nil || got != nil {
		t.Errorf("expected (nil, nil) on network error, got (%+v, %v)", got, err)
	}
}

func TestOAuthFetcher_401Degrades(t *testing.T) {
	t.Parallel()
	f := NewOAuthFetcher()
	f.SetCredentialLoader(func() (*claudeauth.Credentials, error) {
		return &claudeauth.Credentials{AccessToken: "expired"}, nil
	})
	f.SetHTTPClient(&http.Client{Transport: roundTripFn(func(*http.Request) (*http.Response, error) {
		return newJSONResponse(401, `{"error":"unauthorized"}`), nil
	})})
	got, err := f.Fetch(context.Background())
	if err != nil || got != nil {
		t.Errorf("expected (nil, nil) on 401, got (%+v, %v)", got, err)
	}
}

func TestOAuthFetcher_CachesResults(t *testing.T) {
	t.Parallel()
	calls := 0
	f := NewOAuthFetcher()
	f.SetCredentialLoader(func() (*claudeauth.Credentials, error) {
		return &claudeauth.Credentials{AccessToken: "tok"}, nil
	})
	f.SetHTTPClient(&http.Client{Transport: roundTripFn(func(*http.Request) (*http.Response, error) {
		calls++
		return newJSONResponse(200, `{"five_hour":{"utilization":1.0,"resets_at":""}}`), nil
	})})

	for i := 0; i < 5; i++ {
		_, err := f.Fetch(context.Background())
		if err != nil {
			t.Fatalf("Fetch #%d: %v", i, err)
		}
	}
	if calls != 1 {
		t.Errorf("expected 1 upstream call, got %d (cache not honoured)", calls)
	}
}

func TestParseISOToMillis(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want int64
	}{
		{"empty", "", 0},
		{"rfc3339-z", "2026-05-09T10:00:00Z", time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC).UnixMilli()},
		{"rfc3339-offset", "2026-05-09T10:00:00.359167+00:00", time.Date(2026, 5, 9, 10, 0, 0, 359167000, time.UTC).UnixMilli()},
		{"garbage", "not a date", 0},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := parseISOToMillis(tc.in); got != tc.want {
				t.Errorf("parseISOToMillis(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

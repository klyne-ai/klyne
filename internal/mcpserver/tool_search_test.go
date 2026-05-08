package mcpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// fakeDaemon stands in for the klyne daemon's HTTP API. It returns
// the supplied JSON body to every GET /search call and records the
// query string so tests can assert the right params were forwarded.
type fakeDaemon struct {
	srv         *httptest.Server
	lastQuery   url.Values
	respStatus  int
	respBody    string
}

func newFakeDaemon(t *testing.T, status int, body string) *fakeDaemon {
	t.Helper()
	d := &fakeDaemon{respStatus: status, respBody: body}
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.lastQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(d.respStatus)
		_, _ = w.Write([]byte(d.respBody))
	}))
	t.Cleanup(d.srv.Close)
	return d
}

func TestHandleSearchMessages_HappyPath(t *testing.T) {
	d := newFakeDaemon(t, http.StatusOK, `{
		"query":"bank sms",
		"hits":[
			{"message_id":"m1","session_id":"sess-bank","cli":"claude","project_path":"/tmp/trackit","role":"user","snippet":"the <b>bank sms</b> isn't being parsed","score":-1.5,"ts":1746690000000},
			{"message_id":"m2","session_id":"sess-other","cli":"codex","project_path":"/tmp/other","role":"assistant","snippet":"the <b>bank sms</b> regex","score":-2.1,"ts":1746680000000}
		],
		"took_ms":12
	}`)
	t.Setenv("KLYNE_BASE_URL", d.srv.URL)

	_, out, err := HandleSearchMessages(context.Background(), nil, SearchInput{Query: "bank sms"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(out.Hits) != 2 {
		t.Fatalf("got %d hits, want 2", len(out.Hits))
	}
	if out.Hits[0].SessionID != "sess-bank" {
		t.Errorf("Hits[0].SessionID = %q, want sess-bank", out.Hits[0].SessionID)
	}
	if out.Hits[0].CLI != "claude" {
		t.Errorf("Hits[0].CLI = %q, want claude", out.Hits[0].CLI)
	}
	if d.lastQuery.Get("q") != "bank sms" {
		t.Errorf("daemon q param = %q, want %q", d.lastQuery.Get("q"), "bank sms")
	}
}

func TestHandleSearchMessages_EmptyQuery(t *testing.T) {
	t.Setenv("KLYNE_BASE_URL", "http://127.0.0.1:1") // never reached
	_, out, err := HandleSearchMessages(context.Background(), nil, SearchInput{Query: "  "})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(out.Hits) != 0 {
		t.Errorf("expected zero hits for empty query, got %d", len(out.Hits))
	}
	if !strings.Contains(strings.ToLower(out.Reason), "query") {
		t.Errorf("expected reason to mention query, got %q", out.Reason)
	}
}

func TestHandleSearchMessages_DaemonDown(t *testing.T) {
	// Closed port → connection refused. The handler must surface a
	// friendly error, NOT hang for the OS-default 75-second timeout.
	t.Setenv("KLYNE_BASE_URL", "http://127.0.0.1:1")
	_, out, err := HandleSearchMessages(context.Background(), nil, SearchInput{Query: "anything"})
	if err != nil {
		t.Fatalf("handler should not return go-level error; got %v", err)
	}
	if len(out.Hits) != 0 {
		t.Errorf("expected zero hits when daemon is down, got %d", len(out.Hits))
	}
	if !strings.Contains(strings.ToLower(out.Reason), "daemon") {
		t.Errorf("expected reason to mention daemon, got %q", out.Reason)
	}
	if !out.DaemonDown {
		t.Errorf("DaemonDown = false, want true")
	}
}

func TestHandleSearchMessages_AppliesProjectPathFilter(t *testing.T) {
	// project_path filter is applied CLIENT-side because the daemon's
	// /search endpoint doesn't expose it. This test pins that
	// behaviour: the daemon returns 3 hits across 2 projects, and the
	// tool drops the one outside the requested project_path.
	d := newFakeDaemon(t, http.StatusOK, `{
		"query":"x",
		"hits":[
			{"message_id":"m1","session_id":"s1","cli":"claude","project_path":"/keep","role":"user","snippet":"one","score":-1,"ts":1},
			{"message_id":"m2","session_id":"s2","cli":"claude","project_path":"/drop","role":"user","snippet":"two","score":-1,"ts":2},
			{"message_id":"m3","session_id":"s3","cli":"claude","project_path":"/keep","role":"user","snippet":"three","score":-1,"ts":3}
		],
		"took_ms":1
	}`)
	t.Setenv("KLYNE_BASE_URL", d.srv.URL)

	_, out, err := HandleSearchMessages(context.Background(), nil, SearchInput{Query: "x", ProjectPath: "/keep"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(out.Hits) != 2 {
		t.Fatalf("got %d hits, want 2 after /keep filter", len(out.Hits))
	}
	for _, h := range out.Hits {
		if h.ProjectPath != "/keep" {
			t.Errorf("hit %s leaked through filter, project=%q", h.MessageID, h.ProjectPath)
		}
	}
}

func TestHandleSearchMessages_ForwardsLimitAndSort(t *testing.T) {
	d := newFakeDaemon(t, http.StatusOK, `{"query":"x","hits":[],"took_ms":0}`)
	t.Setenv("KLYNE_BASE_URL", d.srv.URL)

	_, _, err := HandleSearchMessages(context.Background(), nil, SearchInput{
		Query: "x", Limit: 50, Sort: "relevance",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if d.lastQuery.Get("limit") != "50" {
		t.Errorf("limit param = %q, want 50", d.lastQuery.Get("limit"))
	}
	if d.lastQuery.Get("sort") != "relevance" {
		t.Errorf("sort param = %q, want relevance", d.lastQuery.Get("sort"))
	}
}

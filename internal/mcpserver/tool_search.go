package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// search_messages tool
// ====================
// The only tool in the MCP surface that depends on the klyne
// daemon. Full-text search lives in SQLite (FTS5 + BM25); the JSONL
// transcripts on disk are not indexed, so direct-read isn't viable
// for cross-session search at scale.
//
// Behaviour when the daemon is unreachable: return DaemonDown=true
// with a friendly Reason instead of letting the AI wait for the OS
// 75-second TCP timeout. The host (Claude Code) surfaces the Reason
// to the user, who restarts the daemon and retries.

// defaultDaemonURL is the daemon's loopback address per
// internal/config/schema.go. Override via KLYNE_BASE_URL when
// the user runs the daemon on a non-default port.
const defaultDaemonURL = "http://127.0.0.1:7878"

// daemonHTTPTimeout caps the daemon round-trip. Search is normally
// sub-50ms; 5 seconds is generous enough for cold-DB warm-ups but
// well under any user-perceived "is this hung?" threshold.
const daemonHTTPTimeout = 5 * time.Second

// searchDefaultLimit / searchMaxLimit mirror the daemon-side caps.
// Echoing them here lets us validate input before paying the HTTP
// roundtrip and surface a clear error to the AI when it asks for
// nonsense.
const (
	searchDefaultLimit = 10
	searchMaxLimit     = 200
)

// SearchInput is the input schema for search_messages.
type SearchInput struct {
	// Query is the FTS5 query string. Required. Empty / whitespace
	// returns an empty result set with a Reason explaining why.
	Query string `json:"query" jsonschema:"full-text query (FTS5 syntax — quote phrases that contain spaces); required"`
	// Limit caps how many hits to return. Default 10; max 200.
	Limit int `json:"limit,omitempty" jsonschema:"max hits to return; default 10, max 200"`
	// Sort selects the result ordering. "recent" (default) returns
	// newest hits first — best when the user wants "what did I just
	// discuss?". "relevance" returns BM25-best first — best when the
	// user wants to find a forgotten thread by keyword density.
	Sort string `json:"sort,omitempty" jsonschema:"recent (default) | relevance"`
	// ProjectPath optionally restricts hits to messages whose
	// project_path equals this value. Applied client-side because the
	// daemon's /search endpoint does not yet expose this filter.
	// Useful for "find anything we discussed about X in this repo."
	ProjectPath string `json:"project_path,omitempty" jsonschema:"optional client-side filter; only return hits whose project_path matches"`
}

// SearchHitRow is one row in SearchOutput.Hits. Mirrors
// api.SearchHit verbatim so the AI sees the same shape the web UI
// shows.
type SearchHitRow struct {
	MessageID   string  `json:"message_id"`
	SessionID   string  `json:"session_id"`
	CLI         string  `json:"cli"`
	ProjectPath string  `json:"project_path"`
	Role        string  `json:"role"`
	Snippet     string  `json:"snippet"`
	Score       float64 `json:"score"`
	TS          int64   `json:"ts"`
}

// SearchOutput is the structured result of search_messages.
type SearchOutput struct {
	Query  string         `json:"query" jsonschema:"the query that was executed"`
	Hits   []SearchHitRow `json:"hits" jsonschema:"matching messages, sorted per the requested sort"`
	Total  int            `json:"total" jsonschema:"number of hits returned (after client-side filtering)"`
	TookMs int64          `json:"took_ms,omitempty" jsonschema:"daemon-side latency in milliseconds"`
	// Reason carries a human-readable note when Hits is empty: the
	// query was empty, the daemon was unreachable, etc. The AI reads
	// this to know whether to retry, ask the user to start the
	// daemon, or report no matches.
	Reason string `json:"reason,omitempty" jsonschema:"empty-result explanation; populated when Hits is empty"`
	// DaemonDown is true when the search call failed because the
	// daemon was unreachable. The AI uses this to suggest restarting
	// klyne rather than reformulating the query.
	DaemonDown bool `json:"daemon_down,omitempty" jsonschema:"true when the daemon could not be reached"`
}

// daemonBaseURL returns the URL the search tool will hit. Honours the
// KLYNE_BASE_URL env override; defaults to defaultDaemonURL.
func daemonBaseURL() string {
	if v := strings.TrimSpace(os.Getenv("KLYNE_BASE_URL")); v != "" {
		return v
	}
	return defaultDaemonURL
}

// HandleSearchMessages calls the daemon's GET /search endpoint and
// returns the hits. Pure function over the input + the daemon's
// response — no JSONL access, no SQLite open.
func HandleSearchMessages(ctx context.Context, _ *mcp.CallToolRequest, in SearchInput) (*mcp.CallToolResult, SearchOutput, error) {
	q := strings.TrimSpace(in.Query)
	if q == "" {
		const reason = "Empty query — pass a non-empty `query` to search messages."
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: reason}},
		}, SearchOutput{Reason: reason}, nil
	}

	limit := in.Limit
	if limit <= 0 {
		limit = searchDefaultLimit
	}
	if limit > searchMaxLimit {
		limit = searchMaxLimit
	}

	sort := strings.TrimSpace(in.Sort)
	if sort == "" {
		sort = "recent"
	}

	base := daemonBaseURL()
	endpoint, err := url.Parse(base)
	if err != nil {
		return nil, SearchOutput{}, fmt.Errorf("parse daemon url %q: %w", base, err)
	}
	endpoint.Path = "/search"
	params := url.Values{}
	params.Set("q", q)
	params.Set("limit", strconv.Itoa(limit))
	params.Set("sort", sort)
	endpoint.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, SearchOutput{}, fmt.Errorf("build request: %w", err)
	}

	client := &http.Client{Timeout: daemonHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		// Distinguish "daemon down" from other HTTP errors so the AI
		// can give a targeted recovery instruction.
		if isConnectionRefused(err) || isTimeout(err) {
			reason := fmt.Sprintf("Could not reach the klyne daemon at %s — start it with `klyne` and try again.", base)
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: reason}},
			}, SearchOutput{Query: q, Reason: reason, DaemonDown: true}, nil
		}
		return nil, SearchOutput{Query: q}, fmt.Errorf("call daemon: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, SearchOutput{Query: q}, fmt.Errorf("daemon returned %s", resp.Status)
	}

	// Decode into the local hit type to keep the import surface small
	// (avoid pulling internal/api into the MCP package).
	var raw struct {
		Query  string         `json:"query"`
		Hits   []SearchHitRow `json:"hits"`
		TookMs int64          `json:"took_ms"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, SearchOutput{Query: q}, fmt.Errorf("decode daemon response: %w", err)
	}

	hits := raw.Hits
	if in.ProjectPath != "" {
		filtered := make([]SearchHitRow, 0, len(hits))
		for _, h := range hits {
			if h.ProjectPath == in.ProjectPath {
				filtered = append(filtered, h)
			}
		}
		hits = filtered
	}

	out := SearchOutput{
		Query:  q,
		Hits:   hits,
		Total:  len(hits),
		TookMs: raw.TookMs,
	}
	if len(hits) == 0 {
		out.Reason = fmt.Sprintf("No matches for %q.", q)
	}

	summary := fmt.Sprintf("Found %d hit(s) for %q (%dms).", out.Total, q, out.TookMs)
	if len(hits) == 0 {
		summary = out.Reason
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: summary}},
	}, out, nil
}

// isConnectionRefused detects "daemon not running" by unwrapping the
// net.OpError → syscall.Errno chain. Cheaper and more portable than
// string-matching the error message.
func isConnectionRefused(err error) bool {
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return strings.Contains(strings.ToLower(opErr.Error()), "connection refused")
	}
	return false
}

// isTimeout reports whether err is a net.Error timeout. Distinct from
// connection-refused because a timeout might mean the daemon is up
// but stuck, which surfaces a different recovery hint.
func isTimeout(err error) bool {
	var ne net.Error
	if errors.As(err, &ne) {
		return ne.Timeout()
	}
	return false
}

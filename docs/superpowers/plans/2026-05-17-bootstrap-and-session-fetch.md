# Bootstrap Integration Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the klyne bootstrap → per-session-fetch integration loop so a fresh Claude session can ask "what was I working on in session X?" without falling back to raw `jq`/`ls`/`wc` over the JSONL transcripts.

**Architecture:** Two independent fixes registered on the existing klyne MCP server.
1. Bug 1 — two new MCP tools (`get_session`, `summarize_session`) that fetch one session's content directly from klyne's SQLite store. New `StopSummariesBySession` DAO helper. No schema changes.
2. Bug 2 — extend `tool_bootstrap.go` to also read Claude's on-disk auto-memory at `~/.claude/projects/<encoded-cwd>/memory/`. New `claude_memory.go` file with a frontmatter parser + Markdown renderer that surfaces files under a distinct "Claude auto-memory" section, leaving the existing "klyne memory" (SQLite) section labeled and untouched.

**Tech Stack:** Go 1.25, `modelcontextprotocol/go-sdk`, `modernc.org/sqlite`, klyne's `internal/store` DAO layer. Tests use `testing` + existing `withFakeHome` / `writeJSONL` / `withBootstrapDB` helpers in `internal/mcpserver/`.

---

## File Structure

**Create:**
- `internal/mcpserver/tool_get_session.go` — `get_session` MCP tool handler. Loads session metadata + ordered messages via existing store DAOs.
- `internal/mcpserver/tool_get_session_test.go` — happy-path + edge-case tests.
- `internal/mcpserver/tool_summarize_session.go` — `summarize_session` MCP tool handler. Synthesizes timeline from stop_summaries + session_summaries + decisions + files.
- `internal/mcpserver/tool_summarize_session_test.go` — tests covering all populated/empty source combinations.
- `internal/mcpserver/claude_memory.go` — `ReadClaudeAutoMemory(home, cwd)` + frontmatter parser + Markdown renderer.
- `internal/mcpserver/claude_memory_test.go` — frontmatter parsing + missing-dir tolerance.

**Modify:**
- `internal/store/stop_summaries.go` — add `ListStopSummariesForSession(ctx, db, sessionID, limit)` (mirrors existing `ListStopSummariesForProject` shape).
- `internal/store/stop_summaries_test.go` — test the new helper.
- `internal/mcpserver/server.go::New()` — register the two new tools in the existing `mcp.AddTool` chain.
- `internal/mcpserver/tool_bootstrap.go` — add `ClaudeAutoMemories` field on `BootstrapOutput`, populate it from `ReadClaudeAutoMemory`, render it under a new section.
- `internal/mcpserver/tool_bootstrap_test.go` — add a test that seeds `~/.claude/projects/<encoded-cwd>/memory/*.md` and asserts the new section.
- `internal/mcpserver/skills/klyne-bootstrap/SKILL.md` — mention both memory sources.

---

## Task 1: Add `ListStopSummariesForSession` DAO helper

The existing `tool_status_snapshot.go` references stop_summaries by project; for `summarize_session` we need a per-session helper. Lives in `internal/store/stop_summaries.go` so it sits beside the existing list helper.

**Files:**
- Modify: `internal/store/stop_summaries.go`
- Test: `internal/store/stop_summaries_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/store/stop_summaries_test.go`:

```go
func TestListStopSummariesForSession(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	// Two summaries for session A (different ts), one for session B.
	for _, s := range []*StopSummary{
		{SessionID: "sess-A", Ts: 1000, ProjectPath: "/p/a", Summary: "a-first"},
		{SessionID: "sess-A", Ts: 2000, ProjectPath: "/p/a", Summary: "a-second"},
		{SessionID: "sess-B", Ts: 1500, ProjectPath: "/p/b", Summary: "b-only"},
	} {
		if err := InsertStopSummary(ctx, db, s); err != nil {
			t.Fatalf("insert %s/%d: %v", s.SessionID, s.Ts, err)
		}
	}

	got, err := ListStopSummariesForSession(ctx, db, "sess-A", 10)
	if err != nil {
		t.Fatalf("ListStopSummariesForSession: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2", len(got))
	}
	// Newest first.
	if got[0].Ts != 2000 || got[1].Ts != 1000 {
		t.Errorf("ts order = %d,%d, want 2000,1000", got[0].Ts, got[1].Ts)
	}
	if got[0].Summary != "a-second" {
		t.Errorf("got[0].Summary = %q, want a-second", got[0].Summary)
	}

	// Empty session id → empty result, not all rows.
	none, err := ListStopSummariesForSession(ctx, db, "sess-missing", 10)
	if err != nil {
		t.Fatalf("missing session: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("missing session returned %d rows, want 0", len(none))
	}
}
```

If `openTestDB` is not the helper used in this file, grep `func TestList` in `stop_summaries_test.go` for the existing setup helper and call that instead.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestListStopSummariesForSession -v`
Expected: FAIL — `ListStopSummariesForSession` undefined.

- [ ] **Step 3: Implement the helper**

Append to `internal/store/stop_summaries.go`:

```go
// ListStopSummariesForSession returns stop-hook summaries for one
// session_id, newest first. limit caps the result (default 10).
// Returns an empty slice when no rows exist.
func ListStopSummariesForSession(ctx context.Context, db *DB, sessionID string, limit int) ([]StopSummary, error) {
	if limit <= 0 {
		limit = 10
	}
	const q = `SELECT session_id, ts, project_path, cli, summary, last_user, last_bash, files_json
	             FROM stop_summaries
	            WHERE session_id = ?
	            ORDER BY ts DESC
	            LIMIT ?`
	rows, err := db.Read().QueryContext(ctx, q, sessionID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list stop summaries by session: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	out := make([]StopSummary, 0)
	for rows.Next() {
		var s StopSummary
		var filesJSON string
		if err := rows.Scan(&s.SessionID, &s.Ts, &s.ProjectPath, &s.CLI, &s.Summary,
			&s.LastUser, &s.LastBash, &filesJSON); err != nil {
			return nil, fmt.Errorf("store: scan stop summary: %w", err)
		}
		if filesJSON != "" {
			_ = json.Unmarshal([]byte(filesJSON), &s.Files)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: stop summary rows: %w", err)
	}
	return out, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestListStopSummariesForSession -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/stop_summaries.go internal/store/stop_summaries_test.go
git commit -m "feat(store): add ListStopSummariesForSession"
```

---

## Task 2: Add `get_session` MCP tool

Fetch ordered messages of one session from SQLite. The store already exposes `GetSession` + `ListMessagesBySessionOrdered`; this tool wraps both behind one MCP call.

**Files:**
- Create: `internal/mcpserver/tool_get_session.go`
- Test: `internal/mcpserver/tool_get_session_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/mcpserver/tool_get_session_test.go`:

```go
package mcpserver

import (
	"context"
	"testing"

	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/store"
)

func TestHandleGetSession_NotFound(t *testing.T) {
	withFakeHome(t)
	_ = withBootstrapDB(t)

	_, out, err := HandleGetSession(context.Background(), nil, GetSessionInput{SessionID: "does-not-exist"})
	if err != nil {
		t.Fatalf("HandleGetSession: %v", err)
	}
	if out.Found {
		t.Errorf("Found = true, want false for missing session")
	}
	if out.Reason == "" {
		t.Errorf("Reason should explain why the session was not found")
	}
}

func TestHandleGetSession_ReturnsMessagesNewestFirst(t *testing.T) {
	withFakeHome(t)
	db := withBootstrapDB(t)
	ctx := context.Background()

	// Seed a session + 3 messages.
	sess := &connectors.Session{
		ID:          "sess-fetch",
		CLI:         connectors.CLIClaude,
		ProjectPath: "/tmp/proj",
		StartedAt:   1000,
		LastMsgAt:   3000,
		Status:      connectors.SessionStatusActive,
	}
	if err := store.UpsertSession(ctx, db, sess); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	for i, ts := range []int64{1000, 2000, 3000} {
		m := &connectors.Message{
			ID:        string(rune('a' + i)),
			SessionID: "sess-fetch",
			Role:      connectors.Role("user"),
			Content:   string(rune('a' + i)),
			Ts:        ts,
		}
		if err := store.InsertMessage(ctx, db, m); err != nil {
			t.Fatalf("insert msg %d: %v", i, err)
		}
	}

	_, out, err := HandleGetSession(context.Background(), nil, GetSessionInput{SessionID: "sess-fetch", Limit: 10, Order: "desc"})
	if err != nil {
		t.Fatalf("HandleGetSession: %v", err)
	}
	if !out.Found {
		t.Fatalf("Found = false, want true")
	}
	if out.SessionID != "sess-fetch" {
		t.Errorf("SessionID = %q, want sess-fetch", out.SessionID)
	}
	if len(out.Messages) != 3 {
		t.Fatalf("Messages len = %d, want 3", len(out.Messages))
	}
	if out.Messages[0].Ts != 3000 {
		t.Errorf("desc order — Messages[0].Ts = %d, want 3000", out.Messages[0].Ts)
	}
}

func TestHandleGetSession_LimitClampedToMax(t *testing.T) {
	withFakeHome(t)
	_ = withBootstrapDB(t)
	_, out, err := HandleGetSession(context.Background(), nil, GetSessionInput{SessionID: "anything", Limit: 999_999})
	if err != nil {
		t.Fatalf("HandleGetSession: %v", err)
	}
	if out.Limit != getSessionMaxLimit {
		t.Errorf("Limit = %d, want clamp to %d", out.Limit, getSessionMaxLimit)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcpserver/ -run TestHandleGetSession -v`
Expected: FAIL — `HandleGetSession` / `GetSessionInput` / `getSessionMaxLimit` undefined.

- [ ] **Step 3: Implement the handler**

Create `internal/mcpserver/tool_get_session.go`:

```go
package mcpserver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/store"
)

// get_session tool
// ================
// Fetches one session's metadata + ordered messages directly from
// klyne's SQLite store. Distinct from search_messages (cross-session
// FTS) and from generate_handoff (current-session JSONL re-scan +
// renderer): this is the "I already know the session_id, give me its
// content" surface that bootstrap recipients need.

const (
	getSessionDefaultLimit = 100
	getSessionMaxLimit     = 1000
)

// GetSessionInput is the JSON-Schema input for the get_session tool.
type GetSessionInput struct {
	SessionID string `json:"session_id" jsonschema:"the session id to fetch; required"`
	// Limit caps how many messages to return. Default 100, max 1000.
	Limit int `json:"limit,omitempty" jsonschema:"max messages to return; default 100, max 1000"`
	// Before is an epoch-ms cursor; only messages with ts < before are
	// returned. 0 means "no upper bound". Use the Ts of the last
	// message in a prior page to paginate backwards.
	Before int64 `json:"before,omitempty" jsonschema:"epoch-ms cursor; only messages with ts < before are returned"`
	// Since is an epoch-ms lower bound; only messages with ts >= since
	// are returned. 0 means "no lower bound".
	Since int64 `json:"since,omitempty" jsonschema:"epoch-ms lower bound; only messages with ts >= since are returned"`
	// Order is "asc" (default — oldest first) or "desc" (newest first).
	Order string `json:"order,omitempty" jsonschema:"asc (default) | desc"`
}

// SessionMetaRow is the trimmed session metadata returned alongside
// the messages — same shape the cockpit uses, minus a few columns the
// AI doesn't need (encoded_cwd, raw_path).
type SessionMetaRow struct {
	ID          string  `json:"id"`
	CLI         string  `json:"cli"`
	ProjectPath string  `json:"project_path,omitempty"`
	StartedAt   int64   `json:"started_at"`
	LastMsgAt   int64   `json:"last_msg_at"`
	MsgCount    int     `json:"msg_count"`
	TokensIn    int64   `json:"tokens_in"`
	TokensOut   int64   `json:"tokens_out"`
	CostUSD     float64 `json:"cost_usd"`
	Model       string  `json:"model,omitempty"`
	Status      string  `json:"status,omitempty"`
}

// GetSessionOutput is the structured payload returned by HandleGetSession.
type GetSessionOutput struct {
	SessionID string                `json:"session_id"`
	Found     bool                  `json:"found"`
	Reason    string                `json:"reason,omitempty" jsonschema:"populated when Found is false"`
	Session   *SessionMetaRow       `json:"session,omitempty"`
	Messages  []*connectors.Message `json:"messages,omitempty"`
	Total     int                   `json:"total"`
	Limit     int                   `json:"limit"`
	Order     string                `json:"order"`
}

// HandleGetSession returns metadata + ordered messages for one
// session_id. Pure SQLite reads — no JSONL access.
func HandleGetSession(ctx context.Context, _ *mcp.CallToolRequest, in GetSessionInput) (*mcp.CallToolResult, GetSessionOutput, error) {
	if in.SessionID == "" {
		const reason = "session_id is required"
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: reason}},
		}, GetSessionOutput{Reason: reason}, nil
	}

	limit := in.Limit
	if limit <= 0 {
		limit = getSessionDefaultLimit
	}
	if limit > getSessionMaxLimit {
		limit = getSessionMaxLimit
	}
	order := in.Order
	if order != "desc" {
		order = "asc"
	}

	db, err := store.Open(config.DBPath())
	if err != nil {
		return nil, GetSessionOutput{}, fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	sess, err := store.GetSession(ctx, db, in.SessionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			reason := fmt.Sprintf("session %q not found in klyne store", in.SessionID)
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: reason}},
			}, GetSessionOutput{
				SessionID: in.SessionID,
				Reason:    reason,
				Limit:     limit,
				Order:     order,
			}, nil
		}
		return nil, GetSessionOutput{}, fmt.Errorf("get session: %w", err)
	}

	msgs, err := store.ListMessagesBySessionFiltered(ctx, db, in.SessionID, limit, in.Before, order, store.MessageFilter{})
	if err != nil {
		return nil, GetSessionOutput{}, fmt.Errorf("list messages: %w", err)
	}

	// Client-side `since` filter — the store DAO does not expose a
	// lower bound directly, and folding it in at this layer keeps the
	// helper signature stable.
	if in.Since > 0 {
		filtered := make([]*connectors.Message, 0, len(msgs))
		for _, m := range msgs {
			if m.Ts >= in.Since {
				filtered = append(filtered, m)
			}
		}
		msgs = filtered
	}

	out := GetSessionOutput{
		SessionID: in.SessionID,
		Found:     true,
		Session: &SessionMetaRow{
			ID:          sess.ID,
			CLI:         string(sess.CLI),
			ProjectPath: sess.ProjectPath,
			StartedAt:   sess.StartedAt,
			LastMsgAt:   sess.LastMsgAt,
			MsgCount:    sess.MsgCount,
			TokensIn:    sess.TokensIn,
			TokensOut:   sess.TokensOut,
			CostUSD:     sess.CostUSD,
			Model:       sess.Model,
			Status:      string(sess.Status),
		},
		Messages: msgs,
		Total:    len(msgs),
		Limit:    limit,
		Order:    order,
	}
	summary := fmt.Sprintf("get_session %s: returned %d/%d messages", in.SessionID, len(msgs), sess.MsgCount)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: summary}},
	}, out, nil
}
```

- [ ] **Step 4: Register the tool in server.go**

In `internal/mcpserver/server.go::New()`, add an `mcp.AddTool` block after the existing `search_messages` registration (the per-session fetch is the natural pair to cross-session search):

```go
	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_session",
		Description: `Fetch one session's metadata and ordered messages directly from klyne's SQLite store.

Use when you already know the session_id (typically from bootstrap, list_sessions, or search_messages) and need the actual content of the conversation. Pure SQLite read — no JSONL access, no daemon required.

Inputs: session_id (required), optional limit (default 100, max 1000), optional before (epoch-ms cursor for backward pagination), optional since (epoch-ms lower bound), optional order ("asc" default | "desc").

Returns: session metadata + the messages slice. Use this instead of falling back to bash + jq over the raw JSONL — klyne is the source of truth.`,
	}, HandleGetSession)
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/mcpserver/ -run TestHandleGetSession -v`
Expected: PASS (all three subtests).

- [ ] **Step 6: Commit**

```bash
git add internal/mcpserver/tool_get_session.go internal/mcpserver/tool_get_session_test.go internal/mcpserver/server.go
git commit -m "feat(mcp): add get_session tool"
```

---

## Task 3: Add `summarize_session` MCP tool

Synthesises a session timeline from stop_summaries (recurring per-stop snapshots) + the latest session_summaries row (rolling AI summary) + decisions linked to this session + the dedup'd files-touched union. No AI call.

**Files:**
- Create: `internal/mcpserver/tool_summarize_session.go`
- Test: `internal/mcpserver/tool_summarize_session_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/mcpserver/tool_summarize_session_test.go`:

```go
package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/store"
)

func TestHandleSummarizeSession_AllSourcesPopulated(t *testing.T) {
	withFakeHome(t)
	db := withBootstrapDB(t)
	ctx := context.Background()

	// Session row so decisions can FK against it.
	sess := &connectors.Session{
		ID:          "sess-sum",
		CLI:         connectors.CLIClaude,
		ProjectPath: "/tmp/proj",
		StartedAt:   1000,
		LastMsgAt:   3000,
		Status:      connectors.SessionStatusEnded,
	}
	if err := store.UpsertSession(ctx, db, sess); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Two stop_summaries (newest first after sort).
	for _, s := range []*store.StopSummary{
		{SessionID: "sess-sum", Ts: 2000, ProjectPath: "/tmp/proj", Summary: "first stop", Files: []string{"a.go", "b.go"}, LastUser: "do thing"},
		{SessionID: "sess-sum", Ts: 3000, ProjectPath: "/tmp/proj", Summary: "second stop", Files: []string{"b.go", "c.go"}, LastUser: "do other thing"},
	} {
		if err := store.InsertStopSummary(ctx, db, s); err != nil {
			t.Fatalf("insert stop summary: %v", err)
		}
	}

	// A rolling AI summary.
	if err := store.InsertSummary(ctx, db, &store.Summary{
		SessionID: "sess-sum", Text: "we built X then Y", Model: "test-model", TS: 3100,
	}); err != nil {
		t.Fatalf("insert summary: %v", err)
	}

	// Linked decisions.
	for _, d := range []*store.Decision{
		{ID: "dec-1", Ts: 2500, ProjectPath: "/tmp/proj", SessionID: "sess-sum", Text: "picked sqlite"},
		{ID: "dec-2", Ts: 2700, ProjectPath: "/tmp/proj", SessionID: "sess-sum", Text: "dropped v1"},
	} {
		if err := store.InsertDecision(ctx, db, d); err != nil {
			t.Fatalf("insert decision: %v", err)
		}
	}

	_, out, err := HandleSummarizeSession(context.Background(), nil, SummarizeSessionInput{SessionID: "sess-sum"})
	if err != nil {
		t.Fatalf("HandleSummarizeSession: %v", err)
	}
	if !out.Found {
		t.Fatalf("Found = false, want true")
	}
	if len(out.StopSummaries) != 2 {
		t.Fatalf("StopSummaries len = %d, want 2", len(out.StopSummaries))
	}
	// Newest first.
	if out.StopSummaries[0].Ts != 3000 {
		t.Errorf("StopSummaries[0].Ts = %d, want 3000", out.StopSummaries[0].Ts)
	}
	if out.LatestSummary == nil || out.LatestSummary.Text != "we built X then Y" {
		t.Errorf("LatestSummary missing or wrong: %+v", out.LatestSummary)
	}
	if len(out.Decisions) != 2 {
		t.Errorf("Decisions len = %d, want 2", len(out.Decisions))
	}
	// Files de-duplicated across all stop_summaries — expect a, b, c.
	wantFiles := map[string]bool{"a.go": true, "b.go": true, "c.go": true}
	if len(out.FilesTouched) != 3 {
		t.Errorf("FilesTouched len = %d, want 3 (de-duped union)", len(out.FilesTouched))
	}
	for _, f := range out.FilesTouched {
		if !wantFiles[f] {
			t.Errorf("unexpected file in FilesTouched: %q", f)
		}
	}
	// Markdown must surface each section header.
	for _, want := range []string{"# Session summary", "## Stop-summary timeline", "## Files touched", "## Decisions", "## Latest rolling summary"} {
		if !strings.Contains(out.Markdown, want) {
			t.Errorf("Markdown missing %q\n%s", want, out.Markdown)
		}
	}
}

func TestHandleSummarizeSession_NotFound(t *testing.T) {
	withFakeHome(t)
	_ = withBootstrapDB(t)

	_, out, err := HandleSummarizeSession(context.Background(), nil, SummarizeSessionInput{SessionID: "nope"})
	if err != nil {
		t.Fatalf("HandleSummarizeSession: %v", err)
	}
	if out.Found {
		t.Errorf("Found = true, want false")
	}
	if !strings.Contains(out.Markdown, "not found") {
		t.Errorf("Markdown should mention not-found:\n%s", out.Markdown)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcpserver/ -run TestHandleSummarizeSession -v`
Expected: FAIL — undefined symbols.

- [ ] **Step 3: Implement the handler**

Create `internal/mcpserver/tool_summarize_session.go`:

```go
package mcpserver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/store"
)

// summarize_session tool
// ======================
// Deterministic per-session timeline. Stitches together every data
// source klyne already keeps about one session — stop_summaries (the
// recurring "what just happened" snapshots written by the Stop hook),
// session_summaries (the rolling AI-generated summary), decisions
// linked to the session, and the de-duplicated union of files touched
// across all stop_summaries — into one Markdown block the agent can
// echo verbatim.
//
// No AI call. No JSONL read. Pure synthesis from klyne.db.

const summarizeStopSummaryLimit = 50

// SummarizeSessionInput is the JSON-Schema input for the tool.
type SummarizeSessionInput struct {
	SessionID string `json:"session_id" jsonschema:"the session id to summarize; required"`
}

// SummarizeSessionOutput is the structured payload returned to the
// agent plus a verbatim-renderable markdown body.
type SummarizeSessionOutput struct {
	SessionID     string              `json:"session_id"`
	Found         bool                `json:"found"`
	Reason        string              `json:"reason,omitempty"`
	Session       *SessionMetaRow     `json:"session,omitempty"`
	StopSummaries []store.StopSummary `json:"stop_summaries,omitempty"`
	LatestSummary *store.Summary      `json:"latest_summary,omitempty"`
	Decisions     []store.Decision    `json:"decisions,omitempty"`
	FilesTouched  []string            `json:"files_touched,omitempty"`
	Markdown      string              `json:"markdown"`
}

// HandleSummarizeSession assembles the per-session timeline.
func HandleSummarizeSession(ctx context.Context, _ *mcp.CallToolRequest, in SummarizeSessionInput) (*mcp.CallToolResult, SummarizeSessionOutput, error) {
	if strings.TrimSpace(in.SessionID) == "" {
		const reason = "session_id is required"
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: reason}},
		}, SummarizeSessionOutput{Reason: reason, Markdown: reason}, nil
	}

	db, err := store.Open(config.DBPath())
	if err != nil {
		return nil, SummarizeSessionOutput{}, fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	sess, err := store.GetSession(ctx, db, in.SessionID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, SummarizeSessionOutput{}, fmt.Errorf("get session: %w", err)
	}
	// Even when the session row is absent the timeline can still be
	// useful — stop_summaries do NOT FK against sessions, so the agent
	// may have a hook-emitted summary for a session the ingestor never
	// saw. Continue gathering the rest.

	stops, err := store.ListStopSummariesForSession(ctx, db, in.SessionID, summarizeStopSummaryLimit)
	if err != nil {
		return nil, SummarizeSessionOutput{}, fmt.Errorf("stop summaries: %w", err)
	}

	latest, err := store.LatestSummary(ctx, db, in.SessionID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, SummarizeSessionOutput{}, fmt.Errorf("latest summary: %w", err)
	}

	decisions, err := store.ListDecisions(ctx, db, store.DecisionFilter{
		SessionID: in.SessionID,
		Limit:     100,
	})
	if err != nil {
		return nil, SummarizeSessionOutput{}, fmt.Errorf("decisions: %w", err)
	}

	files := dedupFilesFromStops(stops)

	out := SummarizeSessionOutput{
		SessionID:     in.SessionID,
		Found:         sess != nil || len(stops) > 0 || latest != nil || len(decisions) > 0,
		StopSummaries: stops,
		LatestSummary: latest,
		Decisions:     decisions,
		FilesTouched:  files,
	}
	if sess != nil {
		out.Session = &SessionMetaRow{
			ID:          sess.ID,
			CLI:         string(sess.CLI),
			ProjectPath: sess.ProjectPath,
			StartedAt:   sess.StartedAt,
			LastMsgAt:   sess.LastMsgAt,
			MsgCount:    sess.MsgCount,
			TokensIn:    sess.TokensIn,
			TokensOut:   sess.TokensOut,
			CostUSD:     sess.CostUSD,
			Model:       sess.Model,
			Status:      string(sess.Status),
		}
	}
	if !out.Found {
		out.Reason = fmt.Sprintf("session %q not found in klyne store and no derived data exists for it", in.SessionID)
	}
	out.Markdown = formatSessionSummaryAsMarkdown(out)

	summary := fmt.Sprintf("summarize_session %s: %d stop-summaries, %d decisions, %d files",
		in.SessionID, len(stops), len(decisions), len(files))
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: summary}},
	}, out, nil
}

// dedupFilesFromStops takes the union of files mentioned across every
// stop_summary's Files slice, sorted for stable output.
func dedupFilesFromStops(stops []store.StopSummary) []string {
	seen := make(map[string]bool, 16)
	for _, s := range stops {
		for _, f := range s.Files {
			seen[f] = true
		}
	}
	out := make([]string, 0, len(seen))
	for f := range seen {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// formatSessionSummaryAsMarkdown renders the structured timeline as a
// stable Markdown block. Empty sections are omitted so the rendering
// does not include `_(none)_` clutter — the agent can echo it directly
// without filtering.
func formatSessionSummaryAsMarkdown(out SummarizeSessionOutput) string {
	if !out.Found {
		return out.Reason
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Session summary: `%s`\n\n", short(out.SessionID))

	if out.Session != nil {
		fmt.Fprintf(&b, "- Project: `%s`\n", out.Session.ProjectPath)
		fmt.Fprintf(&b, "- CLI: `%s`  ·  Model: `%s`  ·  Status: `%s`\n",
			out.Session.CLI, out.Session.Model, out.Session.Status)
		fmt.Fprintf(&b, "- Started: %s  ·  Last msg: %s\n",
			time.UnixMilli(out.Session.StartedAt).UTC().Format(time.RFC3339),
			time.UnixMilli(out.Session.LastMsgAt).UTC().Format(time.RFC3339))
		fmt.Fprintf(&b, "- Messages: %d  ·  Tokens in/out: %d / %d  ·  Cost: $%.2f\n\n",
			out.Session.MsgCount, out.Session.TokensIn, out.Session.TokensOut, out.Session.CostUSD)
	}

	if len(out.StopSummaries) > 0 {
		b.WriteString("## Stop-summary timeline\n\n")
		for _, s := range out.StopSummaries {
			when := time.UnixMilli(s.Ts).UTC().Format(time.RFC3339)
			fmt.Fprintf(&b, "- **%s** — %s\n", when, oneLine(s.Summary))
			if s.LastUser != "" {
				fmt.Fprintf(&b, "  - last user: %s\n", oneLine(truncatePreview(s.LastUser)))
			}
			if s.LastBash != "" {
				fmt.Fprintf(&b, "  - last bash: `%s`\n", oneLine(s.LastBash))
			}
		}
		b.WriteString("\n")
	}

	if len(out.FilesTouched) > 0 {
		b.WriteString("## Files touched\n\n")
		for _, f := range out.FilesTouched {
			fmt.Fprintf(&b, "- `%s`\n", f)
		}
		b.WriteString("\n")
	}

	if len(out.Decisions) > 0 {
		b.WriteString("## Decisions\n\n")
		for _, d := range out.Decisions {
			fmt.Fprintf(&b, "- `%s` — %s\n", d.ID, oneLine(d.Text))
		}
		b.WriteString("\n")
	}

	if out.LatestSummary != nil {
		fmt.Fprintf(&b, "## Latest rolling summary (v%d, model=%s)\n\n", out.LatestSummary.Version, out.LatestSummary.Model)
		b.WriteString(out.LatestSummary.Text)
		b.WriteString("\n")
	}

	return b.String()
}
```

- [ ] **Step 4: Register the tool in server.go**

Add after the `get_session` block:

```go
	mcp.AddTool(srv, &mcp.Tool{
		Name: "summarize_session",
		Description: `Synthesise one session's timeline from klyne's own data — stop-hook summaries, the rolling session summary, linked decisions, and files touched — into one Markdown block.

Use after bootstrap when the user asks "what was I working on in session X?" / "summarize session Y for me". Pure SQLite synthesis — no AI call, no JSONL re-scan. Prefer this over generate_handoff for known-session-id digestion (generate_handoff is for the CURRENT session and re-scans the JSONL).

Inputs: session_id (required). Returns: structured fields plus a verbatim-renderable markdown body covering session metadata, stop-summary timeline, files touched, linked decisions, and the latest rolling summary.`,
	}, HandleSummarizeSession)
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/mcpserver/ -run TestHandleSummarizeSession -v`
Expected: PASS (both subtests).

- [ ] **Step 6: Commit**

```bash
git add internal/mcpserver/tool_summarize_session.go internal/mcpserver/tool_summarize_session_test.go internal/mcpserver/server.go
git commit -m "feat(mcp): add summarize_session tool"
```

---

## Task 4: Claude auto-memory reader + frontmatter parser

Standalone read-side helper that scans `~/.claude/projects/<encoded-cwd>/memory/` for `.md` files, parses simple YAML-like frontmatter (`name`, `description`, `type`), and returns a typed list. The frontmatter parser is intentionally minimal — Claude's own writer produces a fixed shape (`key: value` lines between `---` delimiters with no nesting), so a full YAML dependency is overkill.

**Files:**
- Create: `internal/mcpserver/claude_memory.go`
- Test: `internal/mcpserver/claude_memory_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/mcpserver/claude_memory_test.go`:

```go
package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadClaudeAutoMemory_MissingDirReturnsEmpty(t *testing.T) {
	home := withFakeHome(t)
	got, err := ReadClaudeAutoMemory(home, "/no/such/project")
	if err != nil {
		t.Fatalf("ReadClaudeAutoMemory: %v", err)
	}
	if got.Dir == "" {
		t.Errorf("Dir should be populated even when missing, for the agent to know where it looked")
	}
	if len(got.Entries) != 0 {
		t.Errorf("Entries len = %d, want 0", len(got.Entries))
	}
	if got.Index != "" {
		t.Errorf("Index should be empty for missing dir, got %q", got.Index)
	}
}

func TestReadClaudeAutoMemory_ParsesFrontmatter(t *testing.T) {
	home := withFakeHome(t)
	cwd := "/tmp/proj-auto-memory"
	dir := filepath.Join(home, ".claude", "projects", EncodeCWD(cwd), "memory")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// MEMORY.md (index).
	if err := os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte("- [foo](foo.md) — short hook\n"), 0o644); err != nil {
		t.Fatalf("write MEMORY.md: %v", err)
	}
	// A project memory.
	body := "---\nname: foo memory\ndescription: notes about foo\ntype: project\n---\nfoo body line 1\nfoo body line 2\n"
	if err := os.WriteFile(filepath.Join(dir, "project_foo.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write project_foo: %v", err)
	}
	// A file without frontmatter — must still be surfaced, with empty fields.
	if err := os.WriteFile(filepath.Join(dir, "raw_note.md"), []byte("just a body\n"), 0o644); err != nil {
		t.Fatalf("write raw_note: %v", err)
	}

	got, err := ReadClaudeAutoMemory(home, cwd)
	if err != nil {
		t.Fatalf("ReadClaudeAutoMemory: %v", err)
	}
	if !strings.Contains(got.Index, "[foo](foo.md)") {
		t.Errorf("Index missing pointer line: %q", got.Index)
	}
	if len(got.Entries) != 2 {
		t.Fatalf("Entries len = %d, want 2 (project_foo + raw_note)", len(got.Entries))
	}

	byFile := map[string]ClaudeAutoMemoryEntry{}
	for _, e := range got.Entries {
		byFile[e.File] = e
	}
	pf := byFile["project_foo.md"]
	if pf.Name != "foo memory" {
		t.Errorf("project_foo.Name = %q, want %q", pf.Name, "foo memory")
	}
	if pf.Description != "notes about foo" {
		t.Errorf("project_foo.Description = %q", pf.Description)
	}
	if pf.Type != "project" {
		t.Errorf("project_foo.Type = %q, want project", pf.Type)
	}
	if !strings.Contains(pf.Body, "foo body line 1") {
		t.Errorf("project_foo.Body missing line 1: %q", pf.Body)
	}

	rn := byFile["raw_note.md"]
	if rn.Name != "" || rn.Type != "" {
		t.Errorf("raw_note frontmatter should be empty, got %+v", rn)
	}
	if !strings.Contains(rn.Body, "just a body") {
		t.Errorf("raw_note.Body missing body: %q", rn.Body)
	}
}

func TestRenderClaudeAutoMemoryAsMarkdown_EmptyShape(t *testing.T) {
	out := RenderClaudeAutoMemoryAsMarkdown(ClaudeAutoMemory{Dir: "/x"})
	if !strings.Contains(out, "_(none)_") {
		t.Errorf("empty render must contain _(none)_, got: %s", out)
	}
}

func TestRenderClaudeAutoMemoryAsMarkdown_WithEntries(t *testing.T) {
	mem := ClaudeAutoMemory{
		Dir:   "/x/memory",
		Index: "- [foo](foo.md) — hook\n",
		Entries: []ClaudeAutoMemoryEntry{
			{File: "project_foo.md", Name: "foo memory", Description: "notes", Type: "project", Body: "body text"},
		},
	}
	out := RenderClaudeAutoMemoryAsMarkdown(mem)
	for _, want := range []string{"foo memory", "(project)", "project_foo.md", "notes"} {
		if !strings.Contains(out, want) {
			t.Errorf("Markdown missing %q\n%s", want, out)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcpserver/ -run ReadClaudeAutoMemory -v`
Expected: FAIL — undefined symbols.

- [ ] **Step 3: Implement the reader + renderer**

Create `internal/mcpserver/claude_memory.go`:

```go
package mcpserver

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Claude auto-memory reader
// =========================
// Claude Code's own "auto memory" system (per the system prompt's
// `# auto memory` section) writes per-project Markdown files under
// ~/.claude/projects/<encoded-cwd>/memory/. Each file carries a small
// YAML frontmatter block (name / description / type / etc) and a body.
//
// These files live OUTSIDE klyne's SQLite store but are the densest
// per-project context the agent has — bootstrap was previously
// ignoring them and labelling its own (often empty) SQLite output as
// "Project memories", which made bootstrap useless for projects
// where the user had relied on Claude's auto-memory.
//
// This file is a pure reader. No writes. The parser tolerates files
// without frontmatter (returns them with empty metadata + the full
// content as Body) so any .md the user dropped into the directory is
// still surfaced.

// ClaudeAutoMemoryEntry is one .md file read from the auto-memory dir.
// The optional fields are populated from the frontmatter when present.
type ClaudeAutoMemoryEntry struct {
	File        string `json:"file"`                  // basename, e.g. "project_foo.md"
	Name        string `json:"name,omitempty"`        // frontmatter `name`
	Description string `json:"description,omitempty"` // frontmatter `description`
	Type        string `json:"type,omitempty"`        // frontmatter `type` (user / feedback / project / reference)
	Body        string `json:"body,omitempty"`        // everything after the frontmatter; empty when file had no body
}

// ClaudeAutoMemory is the full read of the auto-memory dir for one
// project. Dir is always populated (even when missing) so the agent
// can tell the user where it looked.
type ClaudeAutoMemory struct {
	Dir     string                  `json:"dir"`               // resolved absolute path
	Index   string                  `json:"index,omitempty"`   // contents of MEMORY.md (the index), verbatim
	Entries []ClaudeAutoMemoryEntry `json:"entries,omitempty"` // sibling .md files, sorted by File
}

// claudeAutoMemoryDir resolves <home>/.claude/projects/<encoded-cwd>/memory.
// Returns "" when cwd is not absolute (EncodeCWD's contract).
func claudeAutoMemoryDir(home, cwd string) string {
	enc := EncodeCWD(cwd)
	if enc == "" {
		return ""
	}
	return filepath.Join(home, ".claude", "projects", enc, "memory")
}

// ReadClaudeAutoMemory scans the auto-memory dir for projectCwd and
// returns the parsed entries + the verbatim MEMORY.md index.
//
// Tolerates a missing directory — returns a populated Dir field with
// empty Entries / Index so the caller can render "no auto-memory
// found at <path>" if it wants. Returns an error only on unexpected
// I/O failures (permission denied, etc).
func ReadClaudeAutoMemory(home, projectCwd string) (ClaudeAutoMemory, error) {
	dir := claudeAutoMemoryDir(home, projectCwd)
	out := ClaudeAutoMemory{Dir: dir}
	if dir == "" {
		return out, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return out, fmt.Errorf("read auto-memory dir %s: %w", dir, err)
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		body, err := os.ReadFile(path) //nolint:gosec
		if err != nil {
			// Skip unreadable files rather than failing the whole call.
			continue
		}
		// MEMORY.md is the index — surface it raw under .Index, not
		// as a regular entry.
		if e.Name() == "MEMORY.md" {
			out.Index = string(body)
			continue
		}
		entry := parseAutoMemoryFile(e.Name(), string(body))
		out.Entries = append(out.Entries, entry)
	}
	sort.Slice(out.Entries, func(i, j int) bool {
		return out.Entries[i].File < out.Entries[j].File
	})
	return out, nil
}

// parseAutoMemoryFile splits a Markdown file into (frontmatter, body).
// The frontmatter is a `---`-delimited block at the very top of the
// file. Inside it, only top-level `key: value` lines are recognised —
// Claude's writer does not produce nested YAML so this matches its
// output exactly and avoids pulling in a yaml dependency.
func parseAutoMemoryFile(name, raw string) ClaudeAutoMemoryEntry {
	out := ClaudeAutoMemoryEntry{File: name}
	sc := bufio.NewScanner(strings.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	lines := make([]string, 0, 32)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}

	// No frontmatter — entire file is the body.
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r") != "---" {
		out.Body = strings.TrimRight(raw, "\n")
		return out
	}

	// Find the closing `---`.
	closeIdx := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r") == "---" {
			closeIdx = i
			break
		}
	}
	if closeIdx == -1 {
		// Open frontmatter never closed — treat entire file as body.
		out.Body = strings.TrimRight(raw, "\n")
		return out
	}

	for _, kv := range lines[1:closeIdx] {
		colon := strings.IndexByte(kv, ':')
		if colon <= 0 {
			continue
		}
		key := strings.TrimSpace(kv[:colon])
		val := strings.TrimSpace(kv[colon+1:])
		switch strings.ToLower(key) {
		case "name":
			out.Name = val
		case "description":
			out.Description = val
		case "type":
			out.Type = val
		}
	}
	out.Body = strings.TrimSpace(strings.Join(lines[closeIdx+1:], "\n"))
	return out
}

// RenderClaudeAutoMemoryAsMarkdown turns a ClaudeAutoMemory into a
// Markdown section suitable for embedding in the bootstrap output.
// Always emits a section header so the agent's output shape is stable
// whether the dir exists or not.
func RenderClaudeAutoMemoryAsMarkdown(m ClaudeAutoMemory) string {
	var b strings.Builder
	if len(m.Entries) == 0 && strings.TrimSpace(m.Index) == "" {
		b.WriteString("_(none)_\n\n")
		return b.String()
	}
	for _, e := range m.Entries {
		title := e.Name
		if title == "" {
			title = e.File
		}
		typ := e.Type
		if typ == "" {
			typ = "untyped"
		}
		fmt.Fprintf(&b, "- **%s** (%s) — `%s`\n", title, typ, e.File)
		if e.Description != "" {
			fmt.Fprintf(&b, "  - %s\n", oneLine(e.Description))
		}
	}
	b.WriteString("\n")
	return b.String()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/mcpserver/ -run "ReadClaudeAutoMemory|RenderClaudeAutoMemoryAsMarkdown" -v`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/mcpserver/claude_memory.go internal/mcpserver/claude_memory_test.go
git commit -m "feat(mcp): add Claude auto-memory reader + renderer"
```

---

## Task 5: Wire Claude auto-memory into bootstrap

Extend the existing bootstrap handler to populate the new section and update the Markdown renderer. Re-label the existing memory section so the two sources are unambiguous.

**Files:**
- Modify: `internal/mcpserver/tool_bootstrap.go`
- Test: `internal/mcpserver/tool_bootstrap_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/mcpserver/tool_bootstrap_test.go`:

```go
// TestHandleBootstrap_SurfacesClaudeAutoMemory seeds the on-disk
// auto-memory directory and asserts the bootstrap response carries
// the parsed entries under a distinct "Claude auto-memory" section.
func TestHandleBootstrap_SurfacesClaudeAutoMemory(t *testing.T) {
	home := withFakeHome(t)
	_ = withBootstrapDB(t)

	cwd := "/tmp/proj-with-auto-memory"
	memDir := filepath.Join(home, ".claude", "projects", EncodeCWD(cwd), "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatalf("mkdir memdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(memDir, "MEMORY.md"), []byte("- [pos](project_positioning.md) — hook\n"), 0o644); err != nil {
		t.Fatalf("write MEMORY.md: %v", err)
	}
	body := "---\nname: positioning\ndescription: three modes\ntype: project\n---\nbody text\n"
	if err := os.WriteFile(filepath.Join(memDir, "project_positioning.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write project_positioning.md: %v", err)
	}

	out := mustBootstrap(t, BootstrapInput{CWD: cwd})

	if len(out.ClaudeAutoMemory.Entries) != 1 {
		t.Fatalf("ClaudeAutoMemory.Entries len = %d, want 1", len(out.ClaudeAutoMemory.Entries))
	}
	if out.ClaudeAutoMemory.Entries[0].Name != "positioning" {
		t.Errorf("first entry Name = %q, want positioning", out.ClaudeAutoMemory.Entries[0].Name)
	}
	for _, want := range []string{
		"## klyne memory (SQLite store)",
		"## Claude auto-memory",
		"positioning",
		"(project)",
	} {
		if !strings.Contains(out.Markdown, want) {
			t.Errorf("Markdown missing %q\n%s", want, out.Markdown)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcpserver/ -run TestHandleBootstrap_SurfacesClaudeAutoMemory -v`
Expected: FAIL — `ClaudeAutoMemory` field undefined on `BootstrapOutput`.

- [ ] **Step 3: Extend `BootstrapOutput`**

Edit `internal/mcpserver/tool_bootstrap.go`:

```go
// BootstrapOutput is the structured payload returned by HandleBootstrap.
type BootstrapOutput struct {
	CWD                    string                  `json:"cwd" jsonschema:"the working directory that was searched"`
	Sessions               []CandidateRow          `json:"sessions" jsonschema:"up to 3 most-recent sessions in this project, newest first"`
	ProjectMemories        []store.Decision        `json:"project_memories" jsonschema:"up to 5 most-recent project-scoped klyne (SQLite) memories"`
	GlobalMemoryCount      int                     `json:"global_memory_count" jsonschema:"total global klyne memory count (project_path = \"\")"`
	GlobalMemoriesPreview  []store.Decision        `json:"global_memories_preview" jsonschema:"up to 3 most-recent global klyne (SQLite) memories"`
	ClaudeAutoMemory       ClaudeAutoMemory        `json:"claude_auto_memory" jsonschema:"on-disk Claude auto-memory for this project (~/.claude/projects/<encoded-cwd>/memory/) — separate store, separate writer"`
	LatestHealth           *BootstrapHealthSummary `json:"latest_health,omitempty" jsonschema:"context-health verdict for the most-recently modified session, when one exists"`
	Markdown               string                  `json:"markdown" jsonschema:"slash-prompt-ready markdown rendering (verbatim-echo target)"`
}
```

- [ ] **Step 4: Populate it inside `HandleBootstrap`**

Inside `HandleBootstrap`, after the global-memory partition block and BEFORE the `latest_health` block, add:

```go
	// --- Claude auto-memory (on-disk, written by Claude itself) ----
	// This is a SECOND memory system distinct from klyne's SQLite
	// store: Claude Code maintains it via its own "auto memory" system
	// prompt and writes .md files with YAML frontmatter under
	// ~/.claude/projects/<encoded-cwd>/memory/. Bootstrap shows both
	// so the agent has a complete picture without conflating sources.
	home, herr := os.UserHomeDir()
	if herr == nil {
		auto, aerr := ReadClaudeAutoMemory(home, cwd)
		if aerr == nil {
			out.ClaudeAutoMemory = auto
		}
		// Silent on aerr: auto-memory is optional context and an
		// unreadable file should not fail the bootstrap call.
	}
```

- [ ] **Step 5: Update the Markdown renderer to label both sources and emit the new section**

Replace the `// --- project memories` and `// --- global memories` blocks inside `formatBootstrapAsMarkdown` with a single re-labelled section, then append the auto-memory section. Final structure:

```go
	// --- klyne memory (SQLite store) -------------------------------
	b.WriteString("## klyne memory (SQLite store)\n\n")
	b.WriteString("### Project-scoped\n\n")
	if len(out.ProjectMemories) == 0 {
		b.WriteString("_(none)_\n\n")
	} else {
		for _, d := range out.ProjectMemories {
			fmt.Fprintf(&b, "- %s\n", oneLine(d.Text))
		}
		b.WriteString("\n")
	}
	b.WriteString("### Global\n\n")
	if out.GlobalMemoryCount == 0 {
		b.WriteString("_(none)_\n\n")
	} else {
		for _, d := range out.GlobalMemoriesPreview {
			fmt.Fprintf(&b, "- %s\n", oneLine(d.Text))
		}
		remaining := out.GlobalMemoryCount - len(out.GlobalMemoriesPreview)
		if remaining > 0 {
			fmt.Fprintf(&b, "- _(+%d more — call `recall` to see them)_\n", remaining)
		}
		b.WriteString("\n")
	}

	// --- Claude auto-memory (files on disk) ------------------------
	b.WriteString("## Claude auto-memory (files on disk)\n\n")
	if out.ClaudeAutoMemory.Dir != "" {
		fmt.Fprintf(&b, "_Source: `%s`_\n\n", out.ClaudeAutoMemory.Dir)
	}
	b.WriteString(RenderClaudeAutoMemoryAsMarkdown(out.ClaudeAutoMemory))
```

The existing `## Project memories` and `## Global memories` headers are removed in favour of the nested `### Project-scoped` / `### Global` headers under the single `## klyne memory (SQLite store)` umbrella. This is the only place those exact strings appear, so the existing test `TestHandleBootstrap_EmptyProject` will need its expected-section list updated in the next step.

- [ ] **Step 6: Update the existing empty-shape test for the relabel**

In `internal/mcpserver/tool_bootstrap_test.go::TestHandleBootstrap_EmptyProject`, change the expected sections slice:

```go
	for _, section := range []string{"Recent sessions", "klyne memory (SQLite store)", "Claude auto-memory"} {
		if !strings.Contains(out.Markdown, "## "+section) {
			t.Errorf("Markdown missing section %q\n%s", section, out.Markdown)
		}
	}
```

And in `TestHandleBootstrap_MemoriesProjectAndGlobals`, update the expected substrings from `## Project memories` / `## Global memories` to `### Project-scoped` / `### Global`.

- [ ] **Step 7: Run all bootstrap tests to verify they pass**

Run: `go test ./internal/mcpserver/ -run TestHandleBootstrap -v`
Expected: PASS for all 4 bootstrap tests.

- [ ] **Step 8: Commit**

```bash
git add internal/mcpserver/tool_bootstrap.go internal/mcpserver/tool_bootstrap_test.go
git commit -m "feat(mcp): bootstrap surfaces Claude auto-memory"
```

---

## Task 6: Update the klyne-bootstrap skill doc

The skill description and "How to report" sections currently say "four sections" — update to reflect the new structure (recent sessions, klyne memory, Claude auto-memory, current session health).

**Files:**
- Modify: `internal/mcpserver/skills/klyne-bootstrap/SKILL.md`

- [ ] **Step 1: Edit the skill doc**

Replace the "How to report" paragraph and the structured-fields paragraph:

```markdown
## How to invoke

Call `mcp__klyne__bootstrap` with no arguments. It auto-resolves the project from the current working directory.

The response is structured (`sessions`, `project_memories`, `global_memories_preview`, `claude_auto_memory`, `latest_health`) plus a pre-rendered `markdown` field for verbatim display.

## How to report

Render the `markdown` field VERBATIM. The server has already produced the Markdown with all sections — recent sessions, **klyne memory (SQLite store)** (with project + global subsections), **Claude auto-memory (files on disk)**, and (when populated) current session health — under 80 chars per line, with `_(none)_` placeholders for empty sections so the shape is stable.

The two memory sections describe **different systems**: `klyne memory` is rows in klyne's SQLite store written via the `remember` / `recall` / `record_decision` MCP tools; `Claude auto-memory` is `.md` files Claude Code writes for itself under `~/.claude/projects/<encoded-cwd>/memory/`. Surface both verbatim — do not merge them, and do not "correct" either label.
```

- [ ] **Step 2: Reinstall the skill bundle so the on-disk skill matches**

Run: `go run ./cmd/klyne mcp install --skills-only` (if that flag exists) OR `go run ./cmd/klyne mcp install` from the repo root to refresh `~/.claude/skills/klyne-bootstrap/SKILL.md`.

If you are unsure of the right subcommand, check `internal/mcpserver/install.go` for the install entry point, or skip this step — `InstallSkills` runs on every `klyne mcp install` invocation, and the user will pick up the new content the next time they reinstall klyne.

- [ ] **Step 3: Commit**

```bash
git add internal/mcpserver/skills/klyne-bootstrap/SKILL.md
git commit -m "docs(skills): bootstrap mentions both memory sources"
```

---

## Task 7: End-to-end verification

Confirm the integration loop actually closes on real data. The user's reported case is session `a0506355-3914-470c-9152-1c6c535db50f` in the klyne project.

- [ ] **Step 1: Build the binary**

Run: `go build -o /tmp/klyne-test ./cmd/klyne`
Expected: clean build.

- [ ] **Step 2: Run the full test suite**

Run: `go test ./...`
Expected: PASS across all packages.

- [ ] **Step 3: Smoke-test `summarize_session` against the real DB**

The MCP tool is intended to be called from a Claude session, but the underlying handler can be exercised from a tiny throwaway Go script or — simpler — by relying on the unit tests already added in Task 3 (which seed an in-memory schema and exercise the same path).

If you want to verify against the production `~/.klyne/klyne.db`, run:

```bash
go run ./cmd/klyne sessions list 2>/dev/null | head -5
```

to confirm the database is intact (no schema breakage from adding the new helper).

- [ ] **Step 4: Manual MCP verification (optional but recommended)**

Restart Claude Code so it re-handshakes with the rebuilt klyne MCP server (the user can do this from their session), then:

1. Run `/klyne:bootstrap`.
2. Expect: a "Claude auto-memory (files on disk)" section listing the 5 files at `~/.claude/projects/-Users-user-Desktop-Project-klyne/memory/`.
3. Ask Claude: "what was I working on in session a0506355-3914-470c-9152-1c6c535db50f?"
4. Expect: Claude invokes `mcp__klyne__summarize_session` (NOT bash + jq) and synthesises an answer from the structured response.

This is a behavioural check on the host (Claude Code) — the unit tests guarantee the tools exist and work; this step confirms the host actually reaches for them.

- [ ] **Step 5: Final commit if any leftover changes**

```bash
git status
# If anything is untracked or modified, commit it with a descriptive message.
```

---

## Self-Review

Spec coverage:
- Bug 1 acceptance #1 (both tools registered): Tasks 2.4 and 3.4 register them in `server.go`.
- Bug 1 acceptance #2 (`get_session(..., limit=10)` returns 10 newest messages): Task 2.1's `TestHandleGetSession_ReturnsMessagesNewestFirst` exercises this.
- Bug 1 acceptance #3 (`summarize_session` returns markdown-renderable summary): Task 3.1's `TestHandleSummarizeSession_AllSourcesPopulated` asserts the section headers and structured fields.
- Bug 1 acceptance #4 (no bash/jq fallback): Task 7 step 4 is the behavioural check; the tool's existence guarantees it can be reached.
- Bug 2 acceptance #1 (5 auto-memory files shown): Task 5.1 seeds + asserts the section is populated and named entries appear.
- Bug 2 acceptance #2 (klyne memory section continues to show empty): Task 5.5 re-labels but preserves the `_(none)_` placeholder.
- Bug 2 acceptance #3 (sections clearly labeled): Task 5.5's new headers (`## klyne memory (SQLite store)` and `## Claude auto-memory (files on disk)`) make this explicit.

Type consistency:
- `GetSessionInput` uses `Before` / `Since` / `Order` consistently with the `store.MessageFilter` shape used in the implementation.
- `SummarizeSessionInput` only takes `session_id` — no other args are referenced anywhere else in the plan.
- `ClaudeAutoMemory` / `ClaudeAutoMemoryEntry` field names match between `claude_memory.go`, the tests, and the bootstrap renderer.
- `StopSummary.Files` is `[]string` everywhere it's referenced (matches the existing type in `internal/store/stop_summaries.go`).

No placeholders: every step includes the actual code or command.

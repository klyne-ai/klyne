# `/klyne:status` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `/klyne:tokens` and `/klyne:health` with a single `/klyne:status` slashcommand backed by a new `get_session_status` MCP tool, and strip the duplicated "Current session health" tail from `/klyne:bootstrap`.

**Architecture:** New MCP tool resolves the session once, loads the JSONL snapshot once, runs `contexthealth.Classify` and `contexthealth.ComputeTimeline` against that single snapshot, and composes one Markdown blob (verdict → reason → trajectory headline → sparkline → table → bloat sources). The slashcommand stays a byte-for-byte echo of the `Markdown` field, matching all existing klyne slashcommands. Underlying `get_context_health` and `get_token_timeline` MCP tools remain registered — the web cockpit and other internal callers depend on them.

**Tech Stack:** Go 1.22, `github.com/modelcontextprotocol/go-sdk/mcp`, Go embed for slashcommand files, standard `testing` package.

**Spec:** [`docs/superpowers/specs/2026-05-19-status-command-design.md`](../specs/2026-05-19-status-command-design.md)

---

## File Structure

| File | Action | Responsibility |
|---|---|---|
| `internal/mcpserver/tool_session_status.go` | **create** | `GetSessionStatusInput`/`Output` types, `HandleGetSessionStatus`, the composition renderer. |
| `internal/mcpserver/tool_session_status_test.go` | **create** | Unit tests for the renderer + happy path, ambiguous, no-session, tokens-empty, unknown-context-window edge cases. |
| `internal/mcpserver/slashcommands/status.md` | **create** | Dumb echo shim with frontmatter description. |
| `internal/mcpserver/slashcommands/tokens.md` | **delete** | Replaced by status. |
| `internal/mcpserver/slashcommands/health.md` | **delete** | Replaced by status. |
| `internal/mcpserver/server.go` | **edit** | Register `get_session_status`; update `bootstrap` tool description. |
| `internal/mcpserver/slashcommands.go` | **edit** | `InstallSlashCommands` removes destination `.md` files no longer in the embed FS. |
| `internal/mcpserver/slashcommands_test.go` | **create** | Test for the new cleanup behaviour. |
| `internal/mcpserver/tool_bootstrap.go` | **edit** | Delete `BootstrapHealthSummary`, the `LatestHealth` field on `BootstrapOutput`, the `HandleGetContextHealth`-delegating block, and the `## Current session health` markdown tail. |
| `internal/mcpserver/tool_bootstrap_test.go` | **edit** | Drop `LatestHealth` assertions and `"Current session health"` markdown-omission assertion. |
| `internal/mcpserver/slashcommands/bootstrap.md` | **edit** | Frontmatter + body drop "latest health verdict" / "current session health". |

---

## Task 1: Slashcommand installer removes stale files

**Files:**
- Modify: `internal/mcpserver/slashcommands.go:58-119`
- Create: `internal/mcpserver/slashcommands_test.go`

Background: `InstallSlashCommands` currently only writes/overwrites. When we delete `tokens.md` and `health.md` from the embed FS in Task 7, users running `klyne mcp install` will still have those files lingering in `~/.claude/commands/klyne/`. This task makes the installer treat the embed FS as the source of truth — anything in the destination dir that's not in the embed FS gets removed.

- [ ] **Step 1: Write the failing test**

Create `internal/mcpserver/slashcommands_test.go`:

```go
package mcpserver

import (
	"os"
	"path/filepath"
	"testing"
)

// TestInstallSlashCommands_RemovesStaleFiles seeds the destination
// directory with an .md file that does NOT exist in the embed FS and
// verifies that re-running InstallSlashCommands removes it. This is
// the behaviour that lets us retire `/klyne:tokens` and `/klyne:health`
// cleanly when their files are dropped from slashcommands/.
func TestInstallSlashCommands_RemovesStaleFiles(t *testing.T) {
	home := withFakeHome(t)
	dest := filepath.Join(home, ".claude", "commands", "klyne")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatalf("mkdir dest: %v", err)
	}
	stale := filepath.Join(dest, "no-longer-in-embed.md")
	if err := os.WriteFile(stale, []byte("legacy"), 0o644); err != nil {
		t.Fatalf("seed stale file: %v", err)
	}

	if _, err := InstallSlashCommands(); err != nil {
		t.Fatalf("InstallSlashCommands: %v", err)
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale file should have been removed; stat err = %v", err)
	}
}

// TestInstallSlashCommands_KeepsUnrelatedSubdirsAndNonMdFiles confirms
// the cleanup pass is conservative: it only removes top-level .md files
// whose basename is not in the embed FS. Sub-directories and non-.md
// files are left alone — the user might have parked unrelated commands
// there.
func TestInstallSlashCommands_KeepsUnrelatedSubdirsAndNonMdFiles(t *testing.T) {
	home := withFakeHome(t)
	dest := filepath.Join(home, ".claude", "commands", "klyne")
	if err := os.MkdirAll(filepath.Join(dest, "user-subdir"), 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	nonMd := filepath.Join(dest, "user-notes.txt")
	if err := os.WriteFile(nonMd, []byte("notes"), 0o644); err != nil {
		t.Fatalf("seed non-md file: %v", err)
	}

	if _, err := InstallSlashCommands(); err != nil {
		t.Fatalf("InstallSlashCommands: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dest, "user-subdir")); err != nil {
		t.Errorf("user subdir should be preserved: %v", err)
	}
	if _, err := os.Stat(nonMd); err != nil {
		t.Errorf("non-md file should be preserved: %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/mcpserver/ -run TestInstallSlashCommands -v`

Expected: FAIL — `TestInstallSlashCommands_RemovesStaleFiles` fails because the stale file is still present after install.

- [ ] **Step 3: Implement the cleanup pass in `InstallSlashCommands`**

In `internal/mcpserver/slashcommands.go`, after the existing for-loop that writes embedded files (currently ends at line 109, just before the `action := ...` block), add the cleanup pass. The full updated function body around that region:

```go
	rewroteAny := false
	written := 0
	embedded := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		embedded[name] = struct{}{}
		content, err := fs.ReadFile(slashCommandsFS, "slashcommands/"+name)
		if err != nil {
			return nil, fmt.Errorf("read embedded %s: %w", name, err)
		}
		target := filepath.Join(dest, name)
		existing, readErr := os.ReadFile(target)
		switch {
		case os.IsNotExist(readErr):
			// New file
		case readErr != nil:
			return nil, fmt.Errorf("read %s: %w", target, readErr)
		case bytes.Equal(existing, content):
			written++
			continue
		}
		if err := os.WriteFile(target, content, 0o644); err != nil {
			return nil, fmt.Errorf("write %s: %w", target, err)
		}
		rewroteAny = true
		written++
	}

	// Cleanup pass: remove any .md file in the destination that is no
	// longer in the embed FS. This is how retiring a slashcommand
	// (deleting its file under slashcommands/) actually propagates to
	// the user's ~/.claude/commands/klyne/ directory on the next
	// `klyne mcp install`. Conservative — only top-level .md files are
	// considered; subdirectories and other extensions are left alone.
	destEntries, err := os.ReadDir(dest)
	if err != nil {
		return nil, fmt.Errorf("read dest dir %s: %w", dest, err)
	}
	for _, de := range destEntries {
		if de.IsDir() {
			continue
		}
		name := de.Name()
		if filepath.Ext(name) != ".md" {
			continue
		}
		if _, ok := embedded[name]; ok {
			continue
		}
		if err := os.Remove(filepath.Join(dest, name)); err != nil {
			return nil, fmt.Errorf("remove stale %s: %w", name, err)
		}
		rewroteAny = true
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/mcpserver/ -run TestInstallSlashCommands -v`

Expected: PASS — both new tests green.

- [ ] **Step 5: Run the full mcpserver test suite to confirm no regression**

Run: `go test ./internal/mcpserver/...`

Expected: PASS — all existing tests still green.

- [ ] **Step 6: Commit**

```bash
git add internal/mcpserver/slashcommands.go internal/mcpserver/slashcommands_test.go
git commit -m "$(cat <<'EOF'
slashcommands: remove stale .md files on install

InstallSlashCommands previously only wrote/overwrote, leaving behind
.md files whose source was removed from the embed FS. The upcoming
/klyne:status work retires tokens.md and health.md; without this
cleanup pass those files would linger in ~/.claude/commands/klyne/
after the next `klyne mcp install`.

Cleanup is conservative: only top-level .md files are removed,
subdirectories and other extensions are preserved.
EOF
)"
```

---

## Task 2: Drop `LatestHealth` from bootstrap

**Files:**
- Modify: `internal/mcpserver/tool_bootstrap.go:69-93,247-266,387-394`
- Modify: `internal/mcpserver/tool_bootstrap_test.go:72-88`
- Modify: `internal/mcpserver/slashcommands/bootstrap.md`
- Modify: `internal/mcpserver/server.go:118-123`

Background: Bootstrap's tail section "Current session health" duplicates the surface `/klyne:status` will own. Since the only consumer of `BootstrapOutput.LatestHealth` is the test we're modifying, we delete the field + type cleanly rather than leaving dead structured output.

- [ ] **Step 1: Update the bootstrap test first**

Open `internal/mcpserver/tool_bootstrap_test.go`. In `TestHandleBootstrap_EmptyProject` (around lines 50-96), make these changes:

Delete the block at lines 72-74:
```go
	if out.LatestHealth != nil {
		t.Errorf("LatestHealth = %+v, want nil for empty project", out.LatestHealth)
	}
```

Delete the block at lines 85-88:
```go
	// LatestHealth absent → no "Current session health" section.
	if strings.Contains(out.Markdown, "Current session health") {
		t.Errorf("Markdown should omit 'Current session health' when LatestHealth is nil\n%s", out.Markdown)
	}
```

Add a positive assertion that the markdown does NOT have a "Current session health" section, period (regardless of LatestHealth). Insert after the existing `_(none)_` assertion (around line 84):

```go
	// /klyne:status now owns the health verdict. Bootstrap must not
	// re-render it here.
	if strings.Contains(out.Markdown, "Current session health") {
		t.Errorf("Markdown must not include 'Current session health' — that surface moved to /klyne:status\n%s", out.Markdown)
	}
```

- [ ] **Step 2: Run the bootstrap test to verify it still passes for the unchanged behaviour, then check the failing pieces**

Run: `go test ./internal/mcpserver/ -run TestHandleBootstrap_EmptyProject -v`

Expected: FAIL — the test references `out.LatestHealth` which still exists, so the strikethrough wasn't enough. Actually it should PASS at this stage because we only deleted assertions, didn't add any failing ones. Re-read: the new assertion just checks the markdown doesn't contain "Current session health" — that's *currently* satisfied for an empty project because `LatestHealth` is nil. So the test will PASS even before the implementation. That's fine; the real failure comes when we delete the field.

Continue.

- [ ] **Step 3: Delete `BootstrapHealthSummary` and the `LatestHealth` field**

In `internal/mcpserver/tool_bootstrap.go`, delete lines 69-78 entirely:

```go
// BootstrapHealthSummary is the trimmed-down view of the latest
// session's context-health verdict surfaced in the brief. Keeps the
// payload small — the agent can call `get_context_health` directly for
// the full bloat report.
type BootstrapHealthSummary struct {
	SessionID      string `json:"session_id" jsonschema:"the session id whose health is reported"`
	State          string `json:"state" jsonschema:"context-health state classification (healthy / drifting / risky / rescue_now)"`
	Action         string `json:"action" jsonschema:"recommended next action (continue / consider-handoff / handoff-now / compact-pending)"`
	ContextFillPct int    `json:"context_fill_pct" jsonschema:"percent of context window consumed by the next turn (0..100)"`
}
```

And delete the `LatestHealth` field from `BootstrapOutput` (the line currently at 88):

```go
	LatestHealth          *BootstrapHealthSummary `json:"latest_health,omitempty" jsonschema:"context-health verdict for the most-recently modified session, when one exists"`
```

- [ ] **Step 4: Remove the `HandleGetContextHealth`-delegating block**

In `HandleBootstrap`, delete lines 247-266 (the entire `// --- latest health: delegate ...` block):

```go
	// --- latest health: delegate to the existing handler ------------
	// We pick the newest-modified session (cands[0]) so the verdict
	// reflects whichever session the agent is most likely resuming.
	// If the delegated call errors or returns an ambiguous /
	// no-session result, we leave LatestHealth nil — the agent will
	// see the absence and can decide whether to fetch directly.
	if len(cands) > 0 {
		_, hOut, hErr := HandleGetContextHealth(ctx, nil, GetContextHealthInput{
			CWD:       cwd,
			SessionID: cands[0].SessionID,
		})
		if hErr == nil && !hOut.Ambiguous && hOut.State != "" {
			out.LatestHealth = &BootstrapHealthSummary{
				SessionID:      hOut.SessionID,
				State:          hOut.State,
				Action:         hOut.Action,
				ContextFillPct: int(hOut.ContextFillPct + 0.5),
			}
		}
	}
```

- [ ] **Step 5: Remove the markdown tail**

In `formatBootstrapAsMarkdown`, delete lines 387-394 (the entire `// --- latest health (only when populated) ---` block):

```go
	// --- latest health (only when populated) -----------------------
	if out.LatestHealth != nil {
		b.WriteString("## Current session health\n\n")
		fmt.Fprintf(&b, "- Session: `%s`\n", short(out.LatestHealth.SessionID))
		fmt.Fprintf(&b, "- State: `%s`\n", out.LatestHealth.State)
		fmt.Fprintf(&b, "- Action: `%s`\n", out.LatestHealth.Action)
		fmt.Fprintf(&b, "- Context fill: %d%%\n", out.LatestHealth.ContextFillPct)
	}
```

- [ ] **Step 6: Update the bootstrap slashcommand frontmatter and body**

Replace `internal/mcpserver/slashcommands/bootstrap.md` with:

```markdown
---
description: Day-1 session brief — recent sessions, runbooks, Claude auto-memory, reflections, worklog entries
---

Call the `mcp__klyne__bootstrap` MCP tool with no arguments — let it auto-resolve the project from the current working directory.

Display the `markdown` field of the response VERBATIM. The server has already rendered every section (recent sessions, klyne runbooks, Claude auto-memory, recent reflections, cross-AI worklog entries) with `_(none)_` placeholders for empty ones so the shape is stable.

Do not summarise, paraphrase, or editorialize — output the markdown field byte-for-byte and stop.

For the live context verdict + token timeline, the user runs `/klyne:status`.
```

- [ ] **Step 7: Update the `bootstrap` MCP tool description in server.go**

In `internal/mcpserver/server.go`, find the `mcp.AddTool(srv, &mcp.Tool{ Name: "bootstrap", ...` block (around line 118-123). Replace the `Description` field with:

```go
		Description: `Day-1 session briefing: recent sessions, klyne runbooks (project + global), Claude auto-memory, recent reflections, and cross-AI worklog entries — all in one call.

Call at session start when you have no prior context for this project, or when the user asks "what was I working on?" / "where did I leave off?". Pure JSONL + SQLite reads — no AI calls. Render the response's markdown field verbatim.

For the live context-health verdict + token timeline of the current session, the user runs /klyne:status (see get_session_status).`,
```

- [ ] **Step 8: Run the bootstrap tests**

Run: `go test ./internal/mcpserver/ -run TestHandleBootstrap -v`

Expected: PASS — all existing bootstrap tests green (the empty-project test now confirms the "Current session health" section is absent unconditionally).

- [ ] **Step 9: Run the full mcpserver test suite to catch any other reference to `LatestHealth`**

Run: `go test ./internal/mcpserver/...`

Expected: PASS — no other file references `LatestHealth` or `BootstrapHealthSummary` (confirmed by grep before drafting this plan).

- [ ] **Step 10: Run a top-level build to confirm no Go consumer breaks**

Run: `go build ./...`

Expected: clean build.

- [ ] **Step 11: Commit**

```bash
git add internal/mcpserver/tool_bootstrap.go internal/mcpserver/tool_bootstrap_test.go internal/mcpserver/slashcommands/bootstrap.md internal/mcpserver/server.go
git commit -m "$(cat <<'EOF'
bootstrap: drop 'Current session health' tail (moves to /klyne:status)

The health-verdict tail duplicates the surface a forthcoming
/klyne:status command will own. Deleting it from bootstrap keeps
the briefing focused on cross-session orientation (recent sessions,
runbooks, reflections, worklog).

BootstrapHealthSummary and LatestHealth are deleted entirely — the
local test was the only consumer, no external caller depends on the
JSON field. Updates the bootstrap MCP tool description and the
/klyne:bootstrap slashcommand frontmatter accordingly, pointing users
at /klyne:status for live verdict + tokens.
EOF
)"
```

---

## Task 3: Scaffold `GetSessionStatus` types

**Files:**
- Create: `internal/mcpserver/tool_session_status.go`

Background: Define the JSON-Schema input/output shape first so subsequent tasks can compose against it.

- [ ] **Step 1: Write the new file with the type scaffold and a stub handler**

Create `internal/mcpserver/tool_session_status.go`:

```go
package mcpserver

import (
	"context"
	"errors"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/klyne-ai/klyne/internal/contexthealth"
)

// tool_session_status.go — get_session_status MCP tool.
//
// Composes the verdict from contexthealth.Classify with the
// trajectory + table from contexthealth.ComputeTimeline into a
// single Markdown blob. Backs the /klyne:status slashcommand.
//
// Resolves the session and loads the JSONL snapshot once, then
// runs both classifiers against that single snapshot — avoids the
// duplicate disk read that would happen if the slashcommand called
// get_context_health + get_token_timeline separately.

// GetSessionStatusInput mirrors the GetContextHealthInput / TokenTimelineInput
// shape so the AI can call this tool with the same disambiguation /
// override pattern it already uses for the underlying tools.
type GetSessionStatusInput struct {
	SessionID string `json:"session_id,omitempty" jsonschema:"explicit Claude Code session id; defaults to latest session in current working directory"`
	CWD       string `json:"cwd,omitempty" jsonschema:"override the working directory used to resolve the latest session"`
}

// GetSessionStatusOutput exposes both the verdict fields (so machine
// consumers can read State/Action without re-parsing the Markdown)
// and the markdown rendering that the slashcommand echoes verbatim.
type GetSessionStatusOutput struct {
	SessionID      string                        `json:"session_id,omitempty" jsonschema:"the session id whose status is reported"`
	Path           string                        `json:"path,omitempty" jsonschema:"absolute path of the analysed transcript"`
	Model          string                        `json:"model,omitempty" jsonschema:"model id of the most recent assistant turn"`
	State          string                        `json:"state,omitempty" jsonschema:"context-health state classification (healthy / drifting / risky / rescue_now)"`
	Action         string                        `json:"action,omitempty" jsonschema:"recommended next action"`
	Reason         string                        `json:"reason,omitempty" jsonschema:"one-sentence rationale for the verdict"`
	ContextFillPct float64                       `json:"context_fill_pct,omitempty" jsonschema:"percent of context window consumed by the next turn"`
	MsgCount       int                           `json:"msg_count,omitempty" jsonschema:"total message count in the transcript"`
	Bloat          []contexthealth.BloatRow      `json:"bloat,omitempty" jsonschema:"top tool-output sources contributing to bloat"`
	Points         []contexthealth.TimelinePoint `json:"points,omitempty" jsonschema:"per-assistant-turn token rows in chronological order"`
	LatestInput    int64                         `json:"latest_input,omitempty" jsonschema:"most recent qualifying turn's TokensIn — current prefix size"`
	PeakInput      int64                         `json:"peak_input,omitempty" jsonschema:"largest single-turn TokensIn in the window"`
	FirstInput     int64                         `json:"first_input,omitempty" jsonschema:"oldest qualifying turn's TokensIn"`
	ContextWindow  int64                         `json:"context_window,omitempty" jsonschema:"model's context window in tokens; 0 when unknown"`
	WindowStartMs  int64                         `json:"window_start_ms,omitempty" jsonschema:"left edge of the displayed window (epoch ms)"`
	WindowEndMs    int64                         `json:"window_end_ms,omitempty" jsonschema:"right edge (epoch ms)"`
	Ambiguous      bool                          `json:"ambiguous,omitempty" jsonschema:"true when multiple sessions in this cwd require explicit session_id"`
	Candidates     []CandidateRow                `json:"candidates,omitempty" jsonschema:"sessions to choose from when ambiguous"`
	Markdown       string                        `json:"markdown" jsonschema:"slash-prompt-ready markdown rendering (verbatim-echo target)"`
}

// HandleGetSessionStatus is the MCP entry point. Stub — Task 5 wires
// the real composition. Returns "not implemented" so the package
// compiles before the renderer lands.
func HandleGetSessionStatus(_ context.Context, _ *mcp.CallToolRequest, _ GetSessionStatusInput) (*mcp.CallToolResult, GetSessionStatusOutput, error) {
	return nil, GetSessionStatusOutput{}, errors.New("not implemented")
}

// formatSessionStatusAsMarkdown — stub for Task 4. Returns empty
// string so the package compiles before the renderer lands.
func formatSessionStatusAsMarkdown(_ GetSessionStatusOutput, _ *time.Location, _ string) string {
	return ""
}
```

- [ ] **Step 2: Verify the package compiles**

Run: `go build ./internal/mcpserver/...`

Expected: clean build.

- [ ] **Step 3: Run the package tests to confirm no regression**

Run: `go test ./internal/mcpserver/...`

Expected: PASS — new file is harmless scaffolding.

- [ ] **Step 4: Commit**

```bash
git add internal/mcpserver/tool_session_status.go
git commit -m "$(cat <<'EOF'
session_status: scaffold types for get_session_status tool

Defines GetSessionStatusInput/Output and stub handler so subsequent
tasks can wire the renderer and composition logic against a stable
signature. Handler returns "not implemented" until Task 5.
EOF
)"
```

---

## Task 4: Implement the composition renderer

**Files:**
- Modify: `internal/mcpserver/tool_session_status.go`
- Modify: `internal/mcpserver/tool_session_status_test.go` (created in this task)

Background: The renderer is pure — it takes a populated `GetSessionStatusOutput`, the timezone, and renders the verdict header, the trajectory headline + sparkline, the per-turn table, and the bloat sources. Reuses existing helpers (`renderTrajectorySentence`, `renderSparkline`, `renderTimeAxis`, `humanTokens`, `formatPct`, `evenlySamplePoints`, `short`) from `tool_token_timeline.go` and the bloat-row pattern from `formatHealthAsMarkdown`.

- [ ] **Step 1: Write the failing renderer test**

Create `internal/mcpserver/tool_session_status_test.go`:

```go
package mcpserver

import (
	"strings"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/contexthealth"
)

// TestFormatSessionStatus_HappyPath_AllSectionsPresent confirms the
// renderer emits the verdict header, the reason, the trajectory
// headline, the sparkline, the per-turn table (5-column variant when
// context window is known), and the bloat-sources list.
func TestFormatSessionStatus_HappyPath_AllSectionsPresent(t *testing.T) {
	now := time.Date(2026, 5, 19, 10, 14, 0, 0, time.UTC).UnixMilli()
	out := GetSessionStatusOutput{
		SessionID:      "abcd1234-very-long-id",
		Path:           "/tmp/session.jsonl",
		Model:          "claude-opus-4-7",
		State:          "drifting",
		Action:         "continue",
		Reason:         "Context fill is climbing but tool outputs remain bounded.",
		ContextFillPct: 14.0,
		MsgCount:       120,
		ContextWindow:  200_000,
		FirstInput:     20_000,
		LatestInput:    28_450,
		PeakInput:      31_200,
		WindowStartMs:  now - 60*60*1000,
		WindowEndMs:    now,
		Points: []contexthealth.TimelinePoint{
			{TsMs: now - 60*60*1000, TotalInput: 20_000, CachedReadTokens: 12_000, EffectiveInput: 8_000},
			{TsMs: now - 45*60*1000, TotalInput: 25_000, CachedReadTokens: 17_000, EffectiveInput: 8_000},
			{TsMs: now - 30*60*1000, TotalInput: 31_200, CachedReadTokens: 22_000, EffectiveInput: 9_200},
			{TsMs: now, TotalInput: 28_450, CachedReadTokens: 20_100, EffectiveInput: 8_350},
		},
		Bloat: []contexthealth.BloatRow{
			{Label: "Read(/big/file.json)", SharePct: 35.0},
			{Label: "Bash(npm test)", SharePct: 18.0},
			{Label: "Read(/another/file.go)", SharePct: 9.0},
		},
	}

	md := formatSessionStatusAsMarkdown(out, time.UTC, "UTC")

	for _, want := range []string{
		"# Session status",
		"`abcd1234`",                 // short session id
		"drifting",                   // state
		"continue",                   // action
		"Context fill:",              // fill label
		"Context fill is climbing",   // reason text
		"## Tokens",
		"## Top context-bloat sources",
		"Read(/big/file.json)",
		"| time",                     // table header start
		"| input tokens",             // table header column
		"| % of context",             // table header column (5-col variant)
		"| cached",
		"| uncached",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("rendered markdown missing %q\n----\n%s\n----", want, md)
		}
	}
}

// TestFormatSessionStatus_NoSession returns a single line when the
// resolver could not find a session under the cwd. No headers, no
// empty sections — keeps the slash-prompt output clean.
func TestFormatSessionStatus_NoSession(t *testing.T) {
	out := GetSessionStatusOutput{
		Reason:   "No Claude Code session found for this working directory.",
		Markdown: "", // renderer fills this
	}
	md := formatSessionStatusAsMarkdown(out, time.UTC, "UTC")
	if !strings.Contains(md, "No Claude Code session") {
		t.Errorf("no-session output should surface the reason verbatim; got:\n%s", md)
	}
	if strings.Contains(md, "## Tokens") || strings.Contains(md, "## Top context-bloat") {
		t.Errorf("no-session output must not include token / bloat headers; got:\n%s", md)
	}
}

// TestFormatSessionStatus_Ambiguous renders the candidate list and
// short-circuits the verdict + token sections.
func TestFormatSessionStatus_Ambiguous(t *testing.T) {
	out := GetSessionStatusOutput{
		Ambiguous: true,
		Candidates: []CandidateRow{
			{SessionID: "s1", Preview: "hello", IsActive: true, ModTime: "2026-05-19T10:00:00Z", MsgCount: 12},
			{SessionID: "s2", Preview: "world", IsActive: false, ModTime: "2026-05-19T09:00:00Z", MsgCount: 30},
		},
	}
	md := formatSessionStatusAsMarkdown(out, time.UTC, "UTC")
	if !strings.Contains(md, "get_session_status") {
		t.Errorf("ambiguous markdown must name the tool to retry: %s", md)
	}
	if !strings.Contains(md, "s1") || !strings.Contains(md, "s2") {
		t.Errorf("ambiguous markdown must list every candidate: %s", md)
	}
	if strings.Contains(md, "## Tokens") || strings.Contains(md, "## Top context-bloat") {
		t.Errorf("ambiguous markdown must not include analysis sections: %s", md)
	}
}

// TestFormatSessionStatus_HealthOnly_TimelineEmpty covers the short-
// session edge case: the verdict is computable but the timeline has
// no qualifying assistant turns yet. The renderer must show the
// verdict and a "no token-timeline rows yet" subline instead of
// failing or printing an empty table.
func TestFormatSessionStatus_HealthOnly_TimelineEmpty(t *testing.T) {
	out := GetSessionStatusOutput{
		SessionID:      "tiny",
		State:          "healthy",
		Action:         "continue",
		Reason:         "Session just started.",
		ContextFillPct: 2.0,
		ContextWindow:  200_000,
		Points:         nil,
	}
	md := formatSessionStatusAsMarkdown(out, time.UTC, "UTC")
	if !strings.Contains(md, "healthy") {
		t.Errorf("verdict must still render when timeline is empty: %s", md)
	}
	if !strings.Contains(md, "## Tokens") {
		t.Errorf("Tokens section header must still render: %s", md)
	}
	if !strings.Contains(md, "no token-timeline rows") {
		t.Errorf("Tokens section must explain the empty timeline: %s", md)
	}
}

// TestFormatSessionStatus_UnknownContextWindow renders the 4-column
// table variant — no '% of context' column when ContextWindow is 0.
func TestFormatSessionStatus_UnknownContextWindow(t *testing.T) {
	now := time.Date(2026, 5, 19, 10, 0, 0, 0, time.UTC).UnixMilli()
	out := GetSessionStatusOutput{
		SessionID:     "xyz",
		State:         "healthy",
		Action:        "continue",
		Reason:        "ok",
		ContextWindow: 0, // unknown
		Points: []contexthealth.TimelinePoint{
			{TsMs: now, TotalInput: 1234, CachedReadTokens: 800, EffectiveInput: 434},
		},
		LatestInput: 1234,
		FirstInput:  1234,
		PeakInput:   1234,
	}
	md := formatSessionStatusAsMarkdown(out, time.UTC, "UTC")
	if strings.Contains(md, "% of context") {
		t.Errorf("4-col table variant must not include '%% of context' header when ContextWindow=0: %s", md)
	}
	if !strings.Contains(md, "input tokens") {
		t.Errorf("4-col table variant must still include 'input tokens' header: %s", md)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/mcpserver/ -run TestFormatSessionStatus -v`

Expected: FAIL — `formatSessionStatusAsMarkdown` returns empty string, every assertion fails.

- [ ] **Step 3: Implement the renderer**

In `internal/mcpserver/tool_session_status.go`, replace the `formatSessionStatusAsMarkdown` stub with the real implementation. Also delete the `touchDeps` helper.

```go
// formatSessionStatusAsMarkdown renders the unified status output.
// Verdict on top, reason next, then the token trajectory + table,
// then the bloat-sources list. Returns a single string the slash
// command echoes byte-for-byte.
func formatSessionStatusAsMarkdown(out GetSessionStatusOutput, loc *time.Location, tzName string) string {
	if out.Ambiguous {
		return formatAmbiguousAsMarkdown("get_session_status", out.Candidates)
	}
	// No-session path: the resolver populated Reason and left State empty.
	if out.State == "" && len(out.Points) == 0 {
		if out.Reason != "" {
			return out.Reason
		}
		return "No Claude Code session found for this working directory."
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Session status — `%s`\n\n", short(out.SessionID))
	fmt.Fprintf(&b, "**State:** `%s` · **Action:** `%s` · **Context fill:** %.1f%%\n\n",
		out.State, out.Action, out.ContextFillPct)
	if out.Reason != "" {
		fmt.Fprintf(&b, "> %s\n\n", out.Reason)
	}

	// --- Tokens section ----------------------------------------------
	b.WriteString("## Tokens\n\n")
	if len(out.Points) == 0 {
		b.WriteString("_No token-timeline rows yet — session is too short for a trajectory._\n\n")
	} else {
		// Reuse the same trajectory + sparkline shape as /klyne:tokens.
		b.WriteString(renderTrajectorySentence(TokenTimelineOutput{
			ContextWindow: out.ContextWindow,
			FirstInput:    out.FirstInput,
			PeakInput:     out.PeakInput,
			LatestInput:   out.LatestInput,
			PctOfContext:  pctOfContext(out.LatestInput, out.ContextWindow),
		}))
		b.WriteString("\n\n```\n")
		values := totalInputSeries(out.Points)
		b.WriteString(renderSparkline(values))
		b.WriteString("\n")
		b.WriteString(renderTimeAxis(out.WindowStartMs, out.WindowEndMs, sparklineCols))
		b.WriteString("\n")
		fmt.Fprintf(&b, "first ~%s · peak ~%s · now ~%s\n",
			humanTokens(out.FirstInput), humanTokens(out.PeakInput), humanTokens(out.LatestInput))
		b.WriteString("```\n\n")

		// Per-turn table — same column shape as /klyne:tokens. 10 rows
		// evenly sampled across the timeline.
		const tableRows = 10
		samples := evenlySamplePoints(out.Points, tableRows)
		if out.ContextWindow > 0 {
			b.WriteString("| time     | input tokens | % of context | cached | uncached |\n")
			b.WriteString("|----------|-------------:|-------------:|-------:|---------:|\n")
			for _, p := range samples {
				when := time.UnixMilli(p.TsMs).In(loc).Format("15:04:05")
				pct := float64(p.TotalInput) / float64(out.ContextWindow) * 100
				if pct > 100 {
					pct = 100
				}
				fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n",
					when, humanTokens(p.TotalInput), formatPct(pct),
					humanTokens(p.CachedReadTokens), humanTokens(p.EffectiveInput))
			}
		} else {
			b.WriteString("| time     | input tokens | cached | uncached |\n")
			b.WriteString("|----------|-------------:|-------:|---------:|\n")
			for _, p := range samples {
				when := time.UnixMilli(p.TsMs).In(loc).Format("15:04:05")
				fmt.Fprintf(&b, "| %s | %s | %s | %s |\n",
					when, humanTokens(p.TotalInput),
					humanTokens(p.CachedReadTokens), humanTokens(p.EffectiveInput))
			}
		}
		b.WriteString("\n")
	}

	// --- Bloat sources (top 3) ---------------------------------------
	if len(out.Bloat) > 0 {
		b.WriteString("## Top context-bloat sources\n\n")
		max := 3
		if len(out.Bloat) < max {
			max = len(out.Bloat)
		}
		for i := 0; i < max; i++ {
			row := out.Bloat[i]
			fmt.Fprintf(&b, "%d. **%s** — %.1f%% of tool output\n", i+1, row.Label, row.SharePct)
		}
	}

	return b.String()
}

// pctOfContext returns latest/window * 100 capped at 100. Returns 0
// when window is 0 (model unknown).
func pctOfContext(latest, window int64) float64 {
	if window <= 0 {
		return 0
	}
	pct := float64(latest) / float64(window) * 100
	if pct > 100 {
		pct = 100
	}
	return pct
}
```

Update the imports to add `fmt` and `strings` (used by the renderer). Final import block for this task:

```go
import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/klyne-ai/klyne/internal/contexthealth"
)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/mcpserver/ -run TestFormatSessionStatus -v`

Expected: PASS — all five renderer tests green.

- [ ] **Step 5: Commit**

```bash
git add internal/mcpserver/tool_session_status.go internal/mcpserver/tool_session_status_test.go
git commit -m "$(cat <<'EOF'
session_status: render verdict + trajectory + table + bloat sources

Pure renderer with five-case test coverage: happy path,
no-session, ambiguous candidates, health-only short session
(timeline empty), and unknown-context-window 4-column table variant.
Reuses renderTrajectorySentence / renderSparkline / evenlySamplePoints
from tool_token_timeline.go and the bloat-row shape from
formatHealthAsMarkdown so the merged output matches today's
/klyne:tokens and /klyne:health byte-for-byte where they overlap.
EOF
)"
```

---

## Task 5: Wire `HandleGetSessionStatus` end-to-end

**Files:**
- Modify: `internal/mcpserver/tool_session_status.go`
- Modify: `internal/mcpserver/tool_session_status_test.go`

Background: The handler resolves the session via `resolveSession` (shared with `get_context_health` / `get_token_timeline`), loads the JSONL snapshot once, then runs both `contexthealth.Classify` and `contexthealth.ComputeTimeline` against that single snapshot. Avoids the double session-resolution and double snapshot load that the AI-glue approach would have caused.

- [ ] **Step 1: Add the handler integration tests**

Append to `internal/mcpserver/tool_session_status_test.go`:

```go
// TestHandleGetSessionStatus_NoSession returns the no-session
// markdown when the cwd has no candidates.
func TestHandleGetSessionStatus_NoSession(t *testing.T) {
	withFakeHome(t)
	_, out, err := HandleGetSessionStatus(context.Background(), nil, GetSessionStatusInput{
		CWD: "/tmp/proj-no-sessions",
	})
	if err != nil {
		t.Fatalf("HandleGetSessionStatus: %v", err)
	}
	if !strings.Contains(out.Markdown, "No Claude Code session") {
		t.Errorf("expected no-session markdown, got: %s", out.Markdown)
	}
	if out.State != "" {
		t.Errorf("State must be empty when no session: %q", out.State)
	}
	if len(out.Points) != 0 {
		t.Errorf("Points must be empty when no session")
	}
}

// TestHandleGetSessionStatus_HappyPath seeds one session under the
// cwd's project tree, calls the handler, and verifies the Markdown
// contains both the verdict section and the Tokens section.
func TestHandleGetSessionStatus_HappyPath(t *testing.T) {
	home := withFakeHome(t)
	cwd := "/tmp/proj-status-happy"
	dir := filepath.Join(home, ".claude", "projects", EncodeCWD(cwd))
	// Two assistant turns is the minimum the timeline needs to draw
	// first/peak/latest distinctly. Pick token counts that produce a
	// visible trajectory.
	now := time.Now()
	lines := []string{
		fmt.Sprintf(`{"type":"user","sessionId":"sess-happy","timestamp":%q,"message":{"role":"user","content":[{"type":"text","text":"start"}]}}`, now.Add(-10*time.Minute).UTC().Format(time.RFC3339Nano)),
		fmt.Sprintf(`{"type":"assistant","sessionId":"sess-happy","timestamp":%q,"message":{"role":"assistant","model":"claude-opus-4-7","usage":{"input_tokens":5000,"cache_read_input_tokens":2000,"output_tokens":100}}}`, now.Add(-9*time.Minute).UTC().Format(time.RFC3339Nano)),
		fmt.Sprintf(`{"type":"user","sessionId":"sess-happy","timestamp":%q,"message":{"role":"user","content":[{"type":"text","text":"more"}]}}`, now.Add(-5*time.Minute).UTC().Format(time.RFC3339Nano)),
		fmt.Sprintf(`{"type":"assistant","sessionId":"sess-happy","timestamp":%q,"message":{"role":"assistant","model":"claude-opus-4-7","usage":{"input_tokens":12000,"cache_read_input_tokens":8000,"output_tokens":200}}}`, now.Add(-4*time.Minute).UTC().Format(time.RFC3339Nano)),
	}
	writeJSONL(t, dir, "session-happy.jsonl", lines...)

	_, out, err := HandleGetSessionStatus(context.Background(), nil, GetSessionStatusInput{CWD: cwd})
	if err != nil {
		t.Fatalf("HandleGetSessionStatus: %v", err)
	}
	if out.State == "" {
		t.Errorf("State should be populated for a seeded session, got empty; markdown:\n%s", out.Markdown)
	}
	if !strings.Contains(out.Markdown, "# Session status") {
		t.Errorf("happy-path markdown missing verdict header:\n%s", out.Markdown)
	}
	if !strings.Contains(out.Markdown, "## Tokens") {
		t.Errorf("happy-path markdown missing Tokens section:\n%s", out.Markdown)
	}
}

// TestHandleGetSessionStatus_AmbiguousCWD seeds two sessions in the
// same project tree that are both within the active window so the
// resolver cannot pick one — handler must surface candidates without
// guessing.
func TestHandleGetSessionStatus_AmbiguousCWD(t *testing.T) {
	home := withFakeHome(t)
	cwd := "/tmp/proj-status-ambiguous"
	dir := filepath.Join(home, ".claude", "projects", EncodeCWD(cwd))
	now := time.Now()
	// Both sessions sit OUTSIDE the 30s active window (so neither
	// triggers PickActiveSession's preference), forcing the resolver
	// into the ambiguous branch.
	base := now.Add(-2 * time.Hour)
	for i := 1; i <= 2; i++ {
		name := fmt.Sprintf("session-%d.jsonl", i)
		line := fmt.Sprintf(
			`{"type":"user","sessionId":"sess-%d","timestamp":%q,"message":{"role":"user","content":[{"type":"text","text":"a"}]}}`,
			i, base.Add(time.Duration(i)*time.Minute).UTC().Format(time.RFC3339Nano),
		)
		p := writeJSONL(t, dir, name, line)
		mtime := base.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatalf("chtimes %s: %v", name, err)
		}
	}

	_, out, err := HandleGetSessionStatus(context.Background(), nil, GetSessionStatusInput{CWD: cwd})
	if err != nil {
		t.Fatalf("HandleGetSessionStatus: %v", err)
	}
	if !out.Ambiguous {
		t.Errorf("expected Ambiguous=true, got %+v", out)
	}
	if len(out.Candidates) != 2 {
		t.Errorf("expected 2 candidates, got %d", len(out.Candidates))
	}
	if !strings.Contains(out.Markdown, "get_session_status") {
		t.Errorf("ambiguous markdown must reference the tool name; got:\n%s", out.Markdown)
	}
}
```

Add these imports at the top of the test file:

```go
import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/contexthealth"
)
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/mcpserver/ -run TestHandleGetSessionStatus -v`

Expected: FAIL — the stub returns `"not implemented"`; every assertion fails.

- [ ] **Step 3: Replace the stub with the real handler**

In `internal/mcpserver/tool_session_status.go`:

Update imports:

```go
import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/klyne-ai/klyne/internal/contexthealth"
	"github.com/klyne-ai/klyne/internal/usage"
)
```

(Drop `errors` — no longer used. Drop `connectors`, `config` — not needed for this composition.)

Replace the stub handler with:

```go
// HandleGetSessionStatus resolves the session, loads the JSONL
// snapshot, runs Classify + ComputeTimeline against it, and composes
// the unified Markdown. Single source of truth for /klyne:status.
func HandleGetSessionStatus(ctx context.Context, _ *mcp.CallToolRequest, in GetSessionStatusInput) (*mcp.CallToolResult, GetSessionStatusOutput, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, contextHealthDefaultDeadline)
		defer cancel()
	}

	path, ambiguous, cands, err := resolveSession(ctx, GetContextHealthInput{
		SessionID: in.SessionID,
		CWD:       in.CWD,
	})
	if err != nil {
		return nil, GetSessionStatusOutput{}, err
	}
	if ambiguous {
		rows := make([]CandidateRow, 0, len(cands))
		for _, c := range cands {
			rows = append(rows, CandidateRow{
				SessionID: c.SessionID,
				Preview:   c.Preview,
				IsActive:  c.IsActive,
				ModTime:   c.ModTime.UTC().Format(timeRFC3339),
				MsgCount:  c.MsgCount,
			})
		}
		const reason = "Multiple Claude Code sessions in this project. Pick one and call get_session_status again with session_id."
		ambOut := GetSessionStatusOutput{Ambiguous: true, Candidates: rows}
		ambOut.Markdown = formatSessionStatusAsMarkdown(ambOut, time.Local, "")
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: reason}},
		}, ambOut, nil
	}
	if path == "" {
		const msg = "No Claude Code session found for this working directory."
		out := GetSessionStatusOutput{Reason: msg, Markdown: msg}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: msg}},
		}, out, nil
	}

	snap, err := LoadSnapshot(ctx, path)
	if err != nil {
		return nil, GetSessionStatusOutput{}, fmt.Errorf("load snapshot: %w", err)
	}

	// Verdict.
	res := contexthealth.Classify(contexthealth.Input{
		SessionID:      snap.SessionID,
		CLI:            "claude",
		Model:          snap.Model,
		ContextFillPct: snap.ContextFillPct,
		MsgCount:       snap.MsgCount,
		Messages:       snap.Messages,
	})

	// Timeline — same default window as /klyne:tokens (full session
	// when no window passed, capped at 24h in resolveWindowMs).
	now := time.Now().UnixMilli()
	tl := contexthealth.ComputeTimeline(snap.Messages, now, resolveWindowMs("", 0), 0)
	if tl.SessionID == "" {
		tl.SessionID = snap.SessionID
	}
	if tl.Model == "" {
		tl.Model = snap.Model
	}
	tl.ContextWindow = usage.ContextWindowForModel(tl.Model)

	out := GetSessionStatusOutput{
		SessionID:      snap.SessionID,
		Path:           snap.Path,
		Model:          snap.Model,
		State:          string(res.State),
		Action:         string(res.Action),
		Reason:         res.Reason,
		ContextFillPct: snap.ContextFillPct,
		MsgCount:       snap.MsgCount,
		Bloat:          res.Bloat,
		Points:         tl.Points,
		LatestInput:    tl.LatestInput,
		PeakInput:      tl.PeakInput,
		FirstInput:     tl.FirstInput,
		ContextWindow:  tl.ContextWindow,
		WindowStartMs:  tl.WindowStartMs,
		WindowEndMs:    tl.WindowEndMs,
	}

	loc := time.Local
	tzName, _ := time.Now().In(loc).Zone()
	if tzName == "" {
		tzName = "local"
	}
	out.Markdown = formatSessionStatusAsMarkdown(out, loc, tzName)

	// AI-facing summary stays terse — one sentence. The Markdown is
	// the verbatim-echo target.
	summary := fmt.Sprintf("Session `%s`: %s · %s", short(out.SessionID), out.State, out.Action)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: summary}},
	}, out, nil
}
```

(`strings` stays imported because `formatSessionStatusAsMarkdown` uses `strings.Builder`.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/mcpserver/ -run TestHandleGetSessionStatus -v`

Expected: PASS — all three handler tests green.

- [ ] **Step 5: Run the full mcpserver suite + a top-level build**

Run: `go test ./internal/mcpserver/... && go build ./...`

Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add internal/mcpserver/tool_session_status.go internal/mcpserver/tool_session_status_test.go
git commit -m "$(cat <<'EOF'
session_status: implement HandleGetSessionStatus end-to-end

Resolves the session via the shared resolveSession helper (same
disambiguation behaviour as get_context_health / get_token_timeline),
loads the JSONL snapshot once, then runs Classify + ComputeTimeline
against that single snapshot. Avoids the duplicate disk-read +
duplicate session-resolution that AI-glue composition would have
caused.

Tested: no-session path, happy path with seeded JSONL, ambiguous-CWD
candidate surfacing.
EOF
)"
```

---

## Task 6: Register the MCP tool

**Files:**
- Modify: `internal/mcpserver/server.go` (insert near other context-related tools, e.g. just after the `get_token_timeline` registration around line 116)

- [ ] **Step 1: Add the `AddTool` call**

In `internal/mcpserver/server.go`, find the block ending `}, HandleGetTokenTimeline)` (around line 116). Insert immediately after it:

```go
	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_session_status",
		Description: `Unified context check for a Claude Code session — verdict + recommended action + reason + context fill % + per-turn token trajectory (sparkline + table) + top bloat sources, in one Markdown payload.

Backs the /klyne:status slashcommand. Resolves the session and loads the JSONL transcript once; runs both contexthealth.Classify and contexthealth.ComputeTimeline against that single snapshot so the verdict and the timeline are guaranteed to describe the same data.

Same disambiguation behaviour as get_context_health / get_token_timeline — when session_id is omitted and the cwd's project tree has multiple sessions with none uniquely active, the response is marked ambiguous and the candidate list is returned. Render the response's markdown field verbatim.`,
	}, HandleGetSessionStatus)
```

- [ ] **Step 2: Verify the build is clean**

Run: `go build ./...`

Expected: clean build.

- [ ] **Step 3: Smoke-test the tool registration**

Run: `go test ./internal/mcpserver/...`

Expected: PASS — adding a tool registration shouldn't break anything; this just confirms.

- [ ] **Step 4: Commit**

```bash
git add internal/mcpserver/server.go
git commit -m "$(cat <<'EOF'
mcpserver: register get_session_status tool

Wires HandleGetSessionStatus into the MCP server registration so the
host can discover and invoke it. Description names the
/klyne:status slashcommand and explains the verdict + tokens +
bloat composition so the AI knows when to fire this vs the
underlying get_context_health / get_token_timeline tools (which
remain available for narrower needs).
EOF
)"
```

---

## Task 7: Ship the `/klyne:status` slashcommand; retire tokens/health

**Files:**
- Create: `internal/mcpserver/slashcommands/status.md`
- Delete: `internal/mcpserver/slashcommands/tokens.md`
- Delete: `internal/mcpserver/slashcommands/health.md`

Background: Because Task 1 added the cleanup pass to `InstallSlashCommands`, deleting `tokens.md` and `health.md` from the embed FS is enough — the next `klyne mcp install` will remove them from every user's `~/.claude/commands/klyne/`.

- [ ] **Step 1: Create the new slashcommand**

Create `internal/mcpserver/slashcommands/status.md`:

```markdown
---
description: Unified context check — health verdict + recommended action + token timeline + top bloat sources for the active session
---

Call the `mcp__klyne__get_session_status` MCP tool with no arguments — let it auto-resolve the session from the current working directory.

Display the `markdown` field of the response VERBATIM. The server has already rendered the verdict header, the reason, the trajectory headline, the ASCII sparkline, the per-turn table, and the top bloat sources.

Do not summarise, paraphrase, reformat, or add commentary — output the markdown field byte-for-byte and stop.
```

- [ ] **Step 2: Delete the retired slashcommand files**

```bash
git rm internal/mcpserver/slashcommands/tokens.md internal/mcpserver/slashcommands/health.md
```

- [ ] **Step 3: Verify the embed FS still compiles**

Run: `go build ./...`

Expected: clean — the embed directive uses a glob, so removing files is fine.

- [ ] **Step 4: Run the full mcpserver test suite to confirm nothing references the retired files**

Run: `go test ./internal/mcpserver/...`

Expected: PASS.

- [ ] **Step 5: Manually verify the embed FS contents**

Run: `go test ./internal/mcpserver/ -run TestInstallSlashCommands -v`

Expected: PASS — the install + cleanup test from Task 1 still green; bundled file count is now 6 (bootstrap, handoff, precompact, reflect, sessions, status).

- [ ] **Step 6: Commit**

```bash
git add internal/mcpserver/slashcommands/status.md
git commit -m "$(cat <<'EOF'
slashcommands: add /klyne:status; retire /klyne:tokens and /klyne:health

The unified /klyne:status command echoes the get_session_status
MCP tool's pre-rendered markdown verbatim — same dumb-echo pattern
as every other klyne slashcommand. tokens.md and health.md are
deleted from the embed FS; Task 1's InstallSlashCommands cleanup
pass will remove the stale files from ~/.claude/commands/klyne/
on the next `klyne mcp install`.
EOF
)"
```

---

## Task 8: Manual smoke test + reinstall

**Files:** none (build + run).

Background: Verify the new command works end-to-end in a real Claude Code session: build the binary, reinstall slashcommands, fire `/klyne:status` and confirm it returns the unified output.

- [ ] **Step 1: Build the klyne binary fresh**

Run: `go build -o /tmp/klyne-status-test ./cmd/klyne`

Expected: clean build. Verify the binary exists.

- [ ] **Step 2: Reinstall slashcommands using the new binary**

Run: `/tmp/klyne-status-test mcp install`

Expected: stdout reports `Files: 6` (was 7), and the action is `updated` (because `tokens.md` and `health.md` get removed in the cleanup pass).

- [ ] **Step 3: Verify the destination directory is clean**

Run: `ls ~/.claude/commands/klyne/`

Expected: six `.md` files: `bootstrap.md`, `handoff.md`, `precompact.md`, `reflect.md`, `sessions.md`, `status.md`. No `tokens.md`, no `health.md`.

- [ ] **Step 4: Optional — confirm the daemon picks up the new tool**

If the user has the klyne daemon running, restart it:

```bash
/tmp/klyne-status-test daemon restart  # only if the user runs the daemon
```

Then in a Claude Code session in this repo's working tree, invoke `/klyne:status` and confirm the output renders a verdict header + Tokens section + bloat sources.

- [ ] **Step 5: Final commit (only if the smoke test surfaced doc fixes)**

If the smoke test surfaces nothing, this task closes without a commit. If small adjustments are needed (e.g. typo in the rendered Markdown), edit + commit them with a `session_status: post-smoke fix …` message.

---

## Self-Review Checklist

After implementing every task, verify:

- [ ] `go build ./...` is clean.
- [ ] `go test ./internal/mcpserver/...` passes.
- [ ] `grep -rn 'LatestHealth\|BootstrapHealthSummary' .` returns no hits outside this plan / spec.
- [ ] `grep -rn 'slashcommands/tokens\|slashcommands/health' .` returns no hits in code (only docs / commits).
- [ ] `ls ~/.claude/commands/klyne/` after reinstall shows exactly six `.md` files.

If any check fails, the corresponding task is incomplete — go back and finish it.

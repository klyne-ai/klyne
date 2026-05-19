# Runbook auto-recall via MCP server instructions

**Date:** 2026-05-19
**Status:** Design — awaiting user review
**Predecessor:** [[project_klyne_memory_feature_repositioning]] (memory feature reframed as "runbooks" with pre-execution-recall hero positioning, 2026-05-19)

## Problem

The runbook feature ships with a clear promise: *"add a runbook once, the model auto-picks it on every relevant ask — you never have to reference it again."* The implementation only delivers half of that. `remember` stores runbooks correctly. `recall` returns them correctly. But the model has to *choose* to call `recall`, and it routinely doesn't.

Observed failure mode (2026-05-19, separate session): user saved a project runbook to four services — *"for labstack changes we have four working dirs with labstack branch"* — then 30 seconds later asked a debugging question about labstack. The model never called `recall`, grepped the wrong branches on three of the four services (`master`/`main` instead of the labstack worktrees), and confidently reported the route didn't exist. The runbook contained the answer to "which branch should I check," and it sat unused.

The pre-execution-recall intent lives only in:
- A code comment above the tool registrations (`internal/mcpserver/server.go:213-220`).
- The `recall` tool's own description, which the model only reads when inspecting the tool.

Neither surface is part of the model's system context. The model never sees "this project has runbooks" unless it goes looking.

## Goals

1. The model sees, on every message of every session, that runbooks exist for the current project and what they cover (titles only).
2. The model has an explicit directive about when to call `recall` — covering both operational shell commands AND topical matches against the inventory (debugging-style asks like the labstack case).
3. Both Claude Code and Codex are covered by the same mechanism — no host-specific code.
4. Zero context cost for users who have no runbooks (the new-user path stays clean).

## Non-goals

- No new MCP tool. `recall`, `remember`, `list_memories` stay as-is.
- No changes to the runbook data model (`store.Decision` table).
- No PreToolUse hook to *force* `recall` (rejected — Claude-only, doubles the design surface for marginal compliance gain once instructions are in place).
- No live mid-session refresh of the instructions string (the MCP spec ties `Instructions` to the `initialize` response; it is not refreshable). Mid-session `remember` calls update `recall` results immediately; the static inventory just doesn't reflect them until next session, which is acceptable because the runbook was just discussed and is already in context.

## Decisions made during brainstorming

| Decision | Value | Rationale |
|---|---|---|
| Payload shape | Directive + titled inventory | Listing titles lets the model judge relevance without a tool call — far higher recall hit rate than a bare directive. |
| Trigger rule | Operational shell commands OR topical match against listed titles | A directive-only rule (operational shell only) would have missed the labstack debugging case. Topical-match against the visible inventory catches non-shell asks. |
| Inventory cap | 20 most-recent per scope (project + global), with `+N more` footer | Bounds worst-case context cost (~600–1200 tokens) without hiding scale. |
| Mechanism | `mcp.ServerOptions{Instructions: ...}` at server init | Standards-based, one place to change, covers Claude Code AND Codex through MCP's own `initialize` contract. |
| Stale runbooks | Out of scope for this design | User controls deletion via the cockpit UI ([[project_klyne_memory_feature_repositioning]]). No TTL or expiry logic. |
| Empty case | Return `""` so MCP omits `instructions` from `initialize` | Zero context cost for users with no runbooks. |

## Architecture

```
mcp.Server boot (subprocess spawned by Claude Code OR Codex)
   │
   ▼
mcpserver.New()
   │
   ├─ cwd        ← os.Getwd()
   ├─ canonical  ← projectpath.Canonical(cwd)   ← collapses worktree to main repo
   ├─ db         ← store.Open(config.DBPath())
   │
   ├─ instructions.Build(ctx, canonical, db)
   │       │
   │       ├─ project ← store.ListDecisions(canonical, limit=21)
   │       ├─ global  ← store.ListDecisions("",        limit=21)
   │       │
   │       ├─ if len(project)+len(global) == 0 → return ""
   │       │
   │       └─ render: directive + project inventory + global inventory + footer
   │
   └─ mcp.NewServer(impl, &ServerOptions{Instructions: text})
        │
        ▼
   MCP initialize response → host injects into model's system context
```

Limit is `21` (cap + 1) so a single query detects overflow without a second `COUNT`. Render uses the first 20; footer reports `len-20` as the hidden count.

The CWD is canonicalised via `projectpath.Canonical()` — the same function `remember` and `recall` already use — preserving the [[project_klyne_worktree_unification]] invariant. A session opened in a worktree sees the same instructions as one opened in the main repo, because both resolve to the same `project_path`.

## Components

Three files, ~150 LOC total, no new dependencies.

### `internal/mcpserver/instructions/build.go`

```go
package instructions

// Build returns the MCP server Instructions string for the current
// project. Returns "" when there are no runbooks (project ∪ global)
// or when the store is unreachable — instructions are best-effort
// enrichment and MUST NOT block server startup.
func Build(ctx context.Context, cwd string, db *store.DB) string
```

Single exported function. Pure: same inputs → same output. Testable without spinning up an MCP server.

### `internal/mcpserver/instructions/render.go`

Pure formatting helpers, no DB access:

- `renderDirective() string` — the static behavioural prompt (see "Instructions text" below).
- `renderInventory(items []store.Decision, scope string) string` — bulleted list of `id` + first-line title (≤ 60 chars, `…` truncation — reuses the same logic as `tool_memory_crud.go`'s `name` derivation for `list_memories`).
- `renderFooter(hiddenCount int) string` — `"+N more, call mcp__klyne__recall to see all"` when `hiddenCount > 0`, else `""`.

### `internal/mcpserver/server.go`

One-line wiring change. Replace:

```go
srv := mcp.NewServer(&mcp.Implementation{
    Name:    "klyne",
    Version: version,
}, nil)
```

with:

```go
cwd, _ := os.Getwd()
canonical := projectpath.Canonical(cwd)
db, dbErr := store.Open(context.Background(), config.DBPath())
defer func() { if dbErr == nil { db.Close() } }()

instr := ""
if dbErr == nil {
    instr = instructions.Build(context.Background(), canonical, db)
}

srv := mcp.NewServer(&mcp.Implementation{
    Name:    "klyne",
    Version: version,
}, &mcp.ServerOptions{Instructions: instr})
```

The version constant bumps to `v0.7.1` per existing convention — wire-visible behaviour changed (instructions field now present).

## Instructions text

Computed at server init. Empty when no runbooks exist. Otherwise:

```
klyne tracks runbooks for this project. Before acting on a user request,
check whether one applies:

  - Operational asks (deploys, secrets, migrations, scripts under
    ./scripts/, "add X for service Y", etc.) → ALWAYS call
    mcp__klyne__recall first, then follow any matching runbook verbatim
    with variables substituted from the user's request.

  - Topical match against the inventory below → call mcp__klyne__recall
    to fetch the full runbook body before investigating.

If a runbook matches, echo the substituted commands in a fenced block
and confirm BEFORE executing.

# Project runbooks (path: <canonical>)

  - d-abc123  for labstack changes we have four working dirs…
  - d-def456  postgres migrations run via ./scripts/migrate.sh —…
  - …
  +5 more, call mcp__klyne__recall to see all

# Global runbooks

  - d-ghi789  RUNBOOK: rotate vendor API key — use ops vault…
  - …
```

Sections are omitted when their list is empty (e.g. global-only users see no project section; project-only users see no global section).

## Data flow

```
User starts session
   │
   ▼
Claude Code / Codex spawns klyne MCP subprocess
   │
   ▼
mcpserver.New() runs                              [once per session]
   │
   ▼
instructions.Build → text                         [≤ 2 DB queries]
   │
   ▼
mcp initialize response includes instructions     [standard MCP]
   │
   ▼
Host injects instructions into model's system     [Claude Code AND Codex]
   │
   ▼
EVERY user message: model sees inventory + directive
   │
   ▼
Relevant ask → model calls mcp__klyne__recall     [tool already exists]
   │
   ▼
recall returns full bodies → model follows verbatim
```

## Error handling

Instructions are best-effort enrichment. They MUST NOT block server startup.

| Failure | Behaviour | Why |
|---|---|---|
| `os.Getwd()` fails | Log to stderr, build with empty cwd → global-only inventory | Still useful for users with global runbooks. |
| `store.Open()` fails | Log to stderr, return `""` from `Build` | Server starts. `remember`/`recall` will surface the same DB error on next call, where it's actionable. |
| `store.ListDecisions()` fails | Log to stderr, return `""` from `Build` | Same reasoning. |
| Empty inventory (zero project + zero global) | Return `""` | MCP omits the `instructions` field. Zero context cost. |

stderr-only logging (never stdout) preserves the JSON-RPC channel — same constraint already documented at `server.go:303-305`.

## Testing

### Unit tests — `internal/mcpserver/instructions/build_test.go`

Three table-driven cases against an in-memory `store.DB` (the existing pattern in `tool_memory_crud_test.go`):

1. **`TestBuild_EmptyDB`** — fresh DB, expect `""`.
2. **`TestBuild_ProjectAndGlobal`** — seed 3 project + 2 global runbooks. Assert: directive header present, both `# Project runbooks` and `# Global runbooks` sections present, titles match seeded first lines (truncated at 60 chars), no footer.
3. **`TestBuild_OverflowFooter`** — seed 25 project + 5 global. Assert: exactly 20 project titles, footer `"+5 more, call mcp__klyne__recall to see all"`, global section intact (no footer there, since under cap).

### Integration test — `internal/mcpserver/tool_memory_crud_test.go`

One new case: open a real `store.DB`, insert a runbook via `HandleRememberMemory`, call `instructions.Build`, assert the new runbook's first line appears in the returned string. Protects the wiring (canonical-path resolution, store query, render) without re-testing the renderer.

### Renderer tests — `internal/mcpserver/instructions/render_test.go`

Pure-function tests, no DB:

- `TestRenderInventory_TitleTruncation` — input with a 200-char first line → output title is ≤ 60 chars with `…` suffix.
- `TestRenderInventory_MultilineFirstLine` — input with `"line one\nline two"` → title is `"line one"`.
- `TestRenderFooter_Zero` — `renderFooter(0)` returns `""`.
- `TestRenderFooter_Positive` — `renderFooter(5)` returns the formatted footer string.

### Server test

`server.go` itself stays simple enough that no expansion is needed — we are passing a non-`nil` options struct, which the SDK handles in its own tests. If a smoke test of `New()` exists, it continues to pass (returns a `*mcp.Server`).

## Open questions

None blocking. The two latent risks are documented and accepted:

1. **Stale inventory** if user adds a runbook mid-session — accepted. `recall` returns the new runbook live; only the static inventory in instructions misses it until next session, which is acceptable because the new runbook was just discussed in-context.
2. **Codex MCP host behaviour** — the MCP spec mandates `Instructions` injection but real-world host fidelity varies. We'll verify Codex respects it by inspecting its MCP transport on a manual test session before declaring Codex parity in release notes.

## Out of scope (explicitly)

- Hook-based forced recall (Claude-only mechanism — rejected).
- Live instructions refresh via `notifications/list_changed` (not supported by MCP for the `Instructions` field).
- Per-runbook tag filtering in the inventory (the directive tells the model when to recall; tags are for `recall`'s own filter).
- Runbook TTL or expiry (user controls deletion via cockpit UI).
- Per-tool description tweaks to other klyne tools (single place — instructions — is the contract).

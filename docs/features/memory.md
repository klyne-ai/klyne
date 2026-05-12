# klyne memory — "remember this" / "refer klyne …"

> Status: shipped 2026-05-12. Same storage as `klyne decisions` (no migration), exposed under a friendlier verb pair (`remember` / `recall`) plus a dashboard route.

## The flow

Two trigger phrases the user types in Claude Code:

| User says | What klyne does |
|---|---|
| **"klyne remember this …"** | Stores a project-scoped memory under the current cwd's project. Multi-line text and runbooks supported. |
| **"klyne remember this globally …"** / **"klyne remember … everywhere"** | Stores a global memory (applies to every project). |
| **"refer klyne …"** / **"check klyne …"** / **"what does klyne remember about …"** | Recalls BOTH project-scoped AND global memories in one MCP call. Claude follows any matching runbook with variables substituted from the request. |

## Architecture

```
                                +-------------------+
   "klyne remember this …" ---> | mcp__klyne__       |
                                |   remember         |
                                |   (scope=project)  |
                                +---------+----------+
                                          |
                                          v
                                +---------+----------+
                                | decisions table    |
                                | (~/.klyne/klyne.db)|
                                | one row per memory |
                                +---------+----------+
                                          |
                                          v
                                +---------+----------+
   "refer klyne …" ---------->  | mcp__klyne__       |
                                |   recall           |  ---> { project_memories: [...],
                                |   (project ∪ global)|       global_memories: [...] }
                                +---------+----------+
                                          |
                                          v
                                +---------+----------+
   browser → /memory (SPA) -->  | GET /memory/items  |
                                | grouped by project |  ---> dashboard view
                                +--------------------+
```

**Same underlying table as `klyne decisions`.** The `remember`/`recall` verbs are user-facing; the existing `record_decision`/`list_decisions`/`search_decisions` MCP tools remain (back-compat, used by the original "we picked X over Y" flow).

## Scoping rules

| Scope | `project_path` stored | Surfaces in… |
|---|---|---|
| `project` (default) | absolute path of the current project | recall calls that resolve to this project |
| `global` | `""` (empty string) | recall calls in **every** project — useful for runbooks that work the same way for `auth-service`, `consultation-service`, etc. |

## MCP tools (new)

### `remember`

```jsonc
{
  "text": "RUNBOOK: add-secret-to-openbao-bucket … (multi-line OK)",
  "scope": "project",                          // "project" (default) | "global"
  "project_path": "/Users/.../consultation-service",   // optional — defaults to cwd
  "cwd": "/Users/.../consultation-service",             // optional — used to fill project_path
  "tags": ["runbook", "secrets", "openbao"],
  "session_id": "optional — pin to one session"
}
```

Returns `{ id, ts, scope, project_path }`.

### `recall`

```jsonc
{
  "cwd": "/Users/.../consultation-service",  // optional — used to default project_path
  "project_path": "...",                      // optional explicit override
  "query": "secret",                          // optional substring filter, case-insensitive
  "tag": "runbook",                           // optional single-tag filter
  "limit": 50                                 // per scope; total can be 2× limit
}
```

Returns:

```jsonc
{
  "project_path": "/Users/.../consultation-service",
  "project_memories": [/* decisions where project_path matches */],
  "global_memories":  [/* decisions where project_path == "" */],
  "project_count": 3,
  "global_count":  1,
  "total":         4
}
```

The two lists are returned separately so Claude can reason about scope. Project memories beat global memories when they conflict.

## Dashboard

Live at **`http://127.0.0.1:7878/memory`** after `klyne start`. Layout:

- **Global** section at the top — every memory with `project_path == ""`.
- **One section per project** below, sorted by most-recent-activity DESC, so the service you're actively working in sits at the top.
- Each row shows: short id, relative timestamp, tags (colour-keyed deterministically by tag string), full text (multi-line OK), Delete button.
- Filter bar: `?q=` for substring text filter, `?tag=` for single-tag filter. Debounced on input.

API: `GET /memory/items[?q=&tag=]` and `DELETE /memory/items/{id}`. (The SvelteKit SPA owns the bare `/memory` URL — the API uses the `/memory/items` subpath so they don't collide on direct page loads.) Writes go through MCP (`remember`) or the CLI (`klyne decisions add`) — the dashboard is read-with-delete-only so the workflow stays chat-driven.

## CLAUDE.md rule (paste this)

Drop this into `~/.claude/CLAUDE.md` (for every project) or into a per-project `CLAUDE.md`:

```markdown
## klyne memory

Three trigger phrases. Match them literally (case-insensitive prefix is enough).

### When the user says "klyne remember this …" or "klyne remember … for this project"

Call `mcp__klyne__remember` with:
- `text` = the user's instruction body, verbatim (paraphrase only if it's obviously verbose — preserve runbook structure exactly)
- `scope` = `"project"`
- `tags` = pick from `["runbook", "decision", "secrets", "infra", "deploy", "db", "auth"]` as appropriate

Confirm the new id and scope back to the user in one short line.

### When the user says "klyne remember this globally …" or "klyne remember … everywhere"

Same as above, but `scope` = `"global"`. Confirm scope explicitly so the user knows it will apply to every project.

### Before running operational shell commands

Before running shell commands for any of the following intents, **first call
`mcp__klyne__recall`** (no args needed — it auto-resolves cwd):

- Adding / rotating / inspecting secrets (`bao`, `vault`, `openbao`)
- Deploying or restarting a service
- Database migrations
- Any infra task that has a script under `./scripts/`

Look at BOTH `project_memories` AND `global_memories` in the response.

If a memory looks like a runbook (multi-line, numbered steps, or starts with `RUNBOOK:`):
1. Match the user's request against the runbook's intent.
2. Extract the variables the runbook declares (look for `$VAR` or `Inputs:` lines).
3. Substitute. Echo the substituted commands back to the user in a fenced block.
4. Ask for confirmation before executing.

If no runbook matches, proceed normally — but mention which memories you found so the user knows what's in scope.

### When the user says "refer klyne …" / "check klyne …" / "what does klyne remember about …"

Call `mcp__klyne__recall` (optionally with `query=` filled from the user's question). Display the results — project memories first, then globals. Do not try to act on them unless the user follows up.
```

## Real walkthrough

```
You: klyne remember this for our auth-service project:
     RUNBOOK: add-secret-to-bucket
     1. See keys: ./scripts/openbao/bao-secret.sh get $SVC $BUCKET
     2. Add key:  ./scripts/openbao/bao-secret.sh set $SVC $BUCKET $KEY=$VAL
     (uses `bao kv patch` — does NOT clobber other keys)

AI: Recorded memory d-71e776ba02137f79 with scope=project under auth-service.
    Tags: runbook, secrets, openbao.

[2 weeks later, in a fresh Claude Code session in a different worktree of the same project]

You: refer klyne and add NEW_API_KEY=abc123 to auth-service main bucket

AI: Found a project-scoped runbook (d-71e776ba…). Substituting:

    ```
    # Step 1 — verify the bucket
    ./scripts/openbao/bao-secret.sh get auth-service main
    # Step 2 — add the key without clobbering
    ./scripts/openbao/bao-secret.sh set auth-service main NEW_API_KEY=abc123
    ```

    Confirm before I run step 2?

You: yes

AI: Running… [executes both steps, captures stdout]
```

The runbook lives in `~/.klyne/klyne.db`. It survives `klyne stop`, daemon restarts, Claude Code restarts, even moving to a different worktree of the same project. It applies regardless of which session you happen to be in.

## What's stored where

| Thing | Where | Schema |
|---|---|---|
| Memory rows | `~/.klyne/klyne.db`, `decisions` table | `id, ts, project_path, session_id, text, tags_json` (migration 009 — no new migration needed) |
| Tags | `tags_json` column, JSON array of strings | filtered client-side by `Tag` or via the SQL LIKE in `ListDecisions` |
| Global vs project | `project_path` column — `""` means global | enforced by the `remember` tool (refuses scope=project without a project_path) |

## Limitations + future work

- **Substring search only**, not FTS. For runbook recall this is fine (single-word tag-aligned queries like `secret`, `openbao`, `migration` hit reliably). Multi-word queries are treated as one literal string. Use `tag` filter for precision.
- **Immutable** — amending a memory is delete-then-add. Aligned with the underlying schema header: "One row per decision; immutable once written." When/if you want versioned runbooks, that's a v0.2 feature: new `runbooks` table with a `version` column.
- **No CLI alias yet** — the chat flow (`"klyne remember this …"`) is the primary path. Use `klyne decisions add/list/search/delete` from the terminal if you need it. A `klyne memory` CLI alias is a small follow-up.

## Files touched in this slice

- `internal/mcpserver/tool_memory.go` — the two new tool handlers
- `internal/mcpserver/server.go` — register `remember` + `recall`
- `internal/api/contracts.go` — `MemoryResponse`, `MemoryProjectGroup` DTOs + route constants
- `internal/api/handlers/memory.go` — `GET /memory/items`, `DELETE /memory/items/{id}`
- `internal/api/handlers/memory_test.go` — 6 handler tests covering grouping, filters, delete, empty
- `internal/api/handlers/mounter.go` — mount the new routes
- `ui/src/routes/memory/+page.svelte` — dashboard page
- `ui/src/lib/ui/TopNav.svelte` — add "Memory" link to top nav
- `ui/src/lib/types.ts` — `Decision`, `MemoryProjectGroup`, `MemoryResponse`
- `ui/src/lib/api.ts` — `fetchMemory`, `deleteMemory`
- `docs/features/memory.md` — this file

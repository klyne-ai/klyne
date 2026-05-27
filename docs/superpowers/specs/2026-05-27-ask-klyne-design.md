# Ask Klyne — Chat drawer on the Productivity page

_Authored 2026-05-27 · branch `init`_

## 1. Why

The Productivity page renders synthesized "What was done" cards for the current
project + date range, but the user often has follow-up questions the cards don't
answer:

- "What pending tasks are still picked up but not merged yet?"
- "How many bugs got solved this week?"
- "Any pending bug still not fixed?"

These questions can be answered from data Klyne already captures —
`stop_summaries.ai_drafted_summary` (per-session narrative) plus the structured
`stop_summaries.worklog_entry_json` categories (`pending`, `bugs_fixed`,
`bugs_found`, `features_picked`, `features_worked_on`, `decisions`, `blockers`,
…). Today the user has to leave the page and grep the raw data.

Ask Klyne adds a slide-in chat drawer to the Productivity page that takes the
question + the page's already-active (project, date-range) filter, feeds the
matching stop_summaries into the local `claude` CLI, and returns the answer.
Ephemeral — closing the drawer wipes the conversation.

## 2. Scope

In scope (this spec):

- A right-side slide-in drawer on `/productivity` triggered by a new "Ask Klyne"
  button.
- A new HTTP endpoint `POST /api/ask` that fetches stop_summaries in scope and
  shells out to `claude -p` to produce an answer.
- A new store function that returns the per-session AI summary + worklog
  categories for a (project, range) tuple.
- Multi-turn chat (history is sent back with each request) within a single open
  of the drawer.

Out of scope:

- Persisting chat history — closing the drawer or reloading the page is a
  fresh conversation. No DB tables, no migrations.
- Streaming token-by-token rendering. The first version uses `--output-format
  json` and renders the full answer when the subprocess returns. Streaming can
  follow if latency is an issue.
- Cross-project / global "Ask Klyne". The drawer is scoped to the current
  productivity page.
- An MCP tool. The handler is plain HTTP; an MCP wrapper can be layered later
  but is not in this spec.
- Authentication. Klyne UI is local-only today; the handler runs the same way
  any other UI endpoint does.

## 3. Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│  /productivity  (SvelteKit page)                                 │
│  ┌──────────────────────────────────┐  ┌───────────────────┐    │
│  │  Productivity content            │  │  AskKlyneDrawer   │    │
│  │  [Ask Klyne ▸] button            │→ │  ─ message list   │    │
│  │  range: { from, to, project }    │  │  ─ input box      │    │
│  └──────────────────────────────────┘  └────────┬──────────┘    │
└───────────────────────────────────────────────────│──────────────┘
                                                    │ POST /api/ask
                                                    ▼
                            ┌────────────────────────────────┐
                            │ internal/api/handlers/ask.go   │
                            │  1. validate request           │
                            │  2. store.LoadAskContext       │
                            │  3. build prompt               │
                            │  4. spawn `claude -p`          │
                            │  5. return AskResponse         │
                            └────────────┬───────────────────┘
                                         ▼
                            ┌────────────────────────────────┐
                            │ claude CLI subprocess          │
                            │ model: claude-sonnet-4-6        │
                            │ --output-format json            │
                            └────────────────────────────────┘
```

Five new code units, each with one responsibility:

| Unit | Path | Responsibility |
|---|---|---|
| Drawer component | `ui/src/lib/components/AskKlyneDrawer.svelte` | Drawer chrome, message list, input. Pure display + local state. |
| API client | `ui/src/lib/api/ask.ts` | POST `/api/ask`, return parsed `AskResponse`. |
| Page integration | `ui/src/routes/productivity/+page.svelte` | Add "Ask Klyne" button, mount drawer, pass active range. |
| HTTP handler | `internal/api/handlers/ask.go` | Validate, fetch, build prompt, subprocess, respond. |
| Store fetch | `internal/store/ask_context.go` | One SQL query → `[]AskRow`. |
| Contract types | `internal/api/contracts.go` (edit) | `RouteAsk`, `AskRequest`, `AskResponse`, `AskMessage`. |

The LLM call mirrors `internal/api/handlers/productivity_compile.go` lines
173-232 (`spawnClaudeProductivitySync`): same argv-only invocation, same
`--output-format json` parsing via `parseClaudeRunResult`, same security
posture (no shell interpolation; subprocess args are positional).

## 4. Contracts

### 4.1 HTTP

```go
// internal/api/contracts.go (additions)

const RouteAsk = "/api/ask"

type AskRequest struct {
    // Projects: optional filter. Empty/missing = no project filter (all
    // projects in range). The productivity page passes the list of
    // projects it is currently rendering so Ask Klyne sees the same
    // scope the user is looking at.
    Projects []string     `json:"projects,omitempty"`
    FromMs   int64        `json:"from_ms"` // inclusive
    ToMs     int64        `json:"to_ms"`   // inclusive
    Question string       `json:"question"`
    History  []AskMessage `json:"history,omitempty"` // prior turns this open of the drawer
}

type AskMessage struct {
    Role    string `json:"role"`    // "user" | "assistant"
    Content string `json:"content"`
}

type AskResponse struct {
    Answer        string `json:"answer"`
    SessionsUsed  int    `json:"sessions_used"`         // rows fed to the prompt
    TruncatedToN  int    `json:"truncated_to_n,omitempty"` // set if cap was hit
    Model         string `json:"model"`
    DurationMs    int64  `json:"duration_ms"`
}
```

### 4.2 Store

```go
// internal/store/ask_context.go

type AskRow struct {
    SessionID         string
    TsMs              int64
    AIDraftedSummary  string
    WorklogEntryJSON  string // raw JSON; the prompt builder substrings what it needs
}

func LoadAskContext(ctx context.Context, db *DB, projects []string, fromMs, toMs int64) ([]AskRow, error)
```

The query (no project filter when `projects` is empty):

```sql
-- projects empty
SELECT session_id, ts, project_path, ai_drafted_summary, worklog_entry_json
FROM stop_summaries
WHERE ts BETWEEN ? AND ?
ORDER BY ts ASC

-- projects = [p1, p2, ...]
SELECT session_id, ts, project_path, ai_drafted_summary, worklog_entry_json
FROM stop_summaries
WHERE ts BETWEEN ? AND ?
  AND project_path IN (?, ?, ...)
ORDER BY ts ASC
```

(Note: the column is `ts` in milliseconds, per migration 011 — the API
contract uses `from_ms`/`to_ms` to be unambiguous on the wire, but the
SQL column name is `ts`.)

### 4.3 Frontend props

```ts
// AskKlyneDrawer.svelte
export let open: boolean;
export let projects: string[]; // project_paths the page is rendering
export let fromMs: number;
export let toMs: number;
// emits: dispatch('close')
```

Drawer internal state (reset every time `open` flips false → true so the chat
is ephemeral):

```ts
let messages: AskMessage[] = [];
let pending: boolean = false;
let input: string = '';
let error: string | null = null;
```

## 5. Data flow

1. User clicks **Ask Klyne** on `/productivity` → `open = true`. Drawer mounts
   with empty `messages`, focuses the textarea.
2. User types a question, presses Enter (or Send) → drawer appends
   `{role: "user", content}` to `messages` locally, sets `pending = true`,
   calls `askKlyne({projects, from_ms, to_ms, question, history})`.
3. Handler runs `store.LoadAskContext(ctx, db, projects, fromMs, toMs)`.
   If zero rows → return 200 with a hard-coded "no sessions in range" answer,
   skip the LLM.
4. Handler builds the prompt. Template:

   ```
   You are Ask Klyne. Answer the user's question using ONLY the sessions below.

   Projects: <comma-joined projects, or "all">
   Range:    <from ISO> .. <to ISO>
   Sessions: <N>

   ## Session 1 — <date>
   ai_summary: <ai_drafted_summary or "(empty)">
   worklog:    <compact worklog_entry_json categories>

   ## Session 2 — ...
   ...

   ---
   Prior conversation (most recent last):
   user: ...
   assistant: ...

   Current user question:
   <question>

   Rules:
   - Cite session date when listing items.
   - Say "no matching entries" if the data does not support an answer.
   - Do not invent. If a count is asked, count exactly from the data above.
   ```

   The worklog rendering keeps only the seven categories that matter for the
   example questions: `pending`, `features_picked`, `features_worked_on`,
   `bugs_fixed`, `bugs_found`, `blockers`, `decisions`. Empty arrays are
   omitted to keep the prompt tight.

5. Handler runs:

   ```
   claude -p \
     --model claude-sonnet-4-6 \
     --output-format json \
     --permission-mode bypassPermissions \
     -- <prompt>
   ```

   60-second context timeout. `cmd.Dir = projectPath`. Args passed as argv —
   no shell interpolation.

6. Handler parses the JSON envelope with the existing `parseClaudeRunResult`
   helper from `productivity_compile.go` (extract assistant text + model +
   duration).

7. Handler returns `AskResponse{Answer, SessionsUsed: len(rows), Model,
   DurationMs}`.

8. Drawer appends `{role: "assistant", content: answer}` to `messages`,
   clears `pending`, focuses the textarea for the next turn.

### 5.1 Context cap

If `len(rows) > 200`, sort by `importance` desc and keep the top 200. Set
`TruncatedToN = 200` in the response so the drawer can show a small note
("answer based on 200 of N sessions in range"). Most ranges (≤7 days for one
project) will not hit this.

## 6. Error handling

| Failure | Server response | Drawer behaviour |
|---|---|---|
| Empty `question` | 400 `{"error":"question required"}` | Send button is disabled when input is empty; this is a guard only |
| `question` > 2000 chars | 400 `{"error":"question too long"}` | Inline form error under the textarea |
| `to_ms < from_ms` | 400 `{"error":"invalid range"}` | Should not happen (page guarantees) — show generic error |
| No sessions in range | 200 with `answer: "No sessions recorded in the selected range."` | Render as a normal assistant message |
| `claude` not on PATH | 500 `{"error":"claude CLI not installed"}` | Red error bubble with Retry button |
| `claude -p` exits non-zero or times out | 500 `{"error":"<sanitized stderr tail>"}` | Red error bubble with Retry button |
| Anything unexpected | 500 generic | Red error bubble |

No server-side retries. Retry from the drawer simply re-POSTs the same body.
Failures do not consume a slot in `messages` — i.e. the user message stays
visible but the drawer can resend it.

## 7. Testing

- **`internal/store/ask_context_test.go`** — golden table test. Seed
  stop_summaries across multiple projects and timestamps; assert
  `LoadAskContext` returns exactly the matching rows ordered ascending. Cover
  the empty-result case.
- **`internal/api/handlers/ask_test.go`** — table-driven HTTP tests using the
  same `runCmd` injection pattern as `productivity_compile_test.go`:
  - Empty range short-circuits without calling `runCmd`.
  - Happy path: fake `runCmd` returns canned JSON; assert response shape,
    `SessionsUsed`, model passthrough.
  - 400 cases: empty question, question >2000 chars, range inversion, missing
    project_path.
  - 500 case: `runCmd` returns error → response sanitises stderr.
  - Truncation: seed 250 rows, assert prompt only contains 200 + response has
    `TruncatedToN = 200`.
- **`ui/src/lib/components/AskKlyneDrawer.test.ts`** — Vitest component test:
  - Opening focuses the textarea.
  - Submitting calls `fetch('/api/ask', ...)` with the correct body
    (project_path, range, question, history).
  - Assistant message renders after fetch resolves.
  - Closing the drawer resets `messages` so reopening starts fresh.
- **Manual smoke** — run dev server, open `/productivity` for a project with
  recorded sessions, ask one of the example questions; verify the count
  matches a hand-grep of `worklog_entry_json` for that range.

## 8. Out-of-scope follow-ups

These are deliberately deferred so the first ship is small:

- Streaming answers (switch to `--output-format stream-json`).
- Persisted chat history (new SQLite table keyed by `(project, day)`).
- An MCP wrapper exposing the same engine to Claude Code sessions.
- A scope toggle in the drawer to broaden beyond the page's current range.
- Suggested-question chips ("How many bugs?", "What's pending?") in an empty
  drawer.

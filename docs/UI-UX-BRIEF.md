# klyne — UI/UX Brief (v1)

> **Purpose:** Hand-off doc for design input. The backend is built and exposing real data. This doc inventories everything we have, proposes a screen set, and surfaces the design decisions only you can make.
>
> **Status of v1 UI:** Functional but layout-naive — flat session list, no project grouping, weak hierarchy. Confirmed in screenshot review on 2026-05-07.
>
> **Read this if you are:** the designer/owner deciding the v1.1 visual structure.

---

## 0. Reality check — what's wrong with the current UI

Your screenshot showed:

| Issue | Why it hurts |
|---|---|
| `partner-service` appears 3× as separate sidebar rows | Same project, different sessions. User has to mentally re-group. |
| `trackIt` 39 sessions, all flat in sidebar | Sidebar becomes scroll-sludge. Can't see at a glance what projects exist. |
| Dashboard is a static placeholder + cost tile | "Dashboard" implies overview; we're not showing one. |
| Sidebar is the navigation AND the content list AND the search results | Three jobs, one component → cluttered. |
| No project tile / card / hub view | No way to land on a project and see its slice of work. |
| Cost shows `$0.00` per session for `claude-opus-4-7` | Real bug — unpriced model. Easy fix to `pricing.json`. |
| Total cost says `$39.03` but per-session lines say `$0.0000` | Mismatch in displayed precision; total comes from old opus-4-6 / sonnet-4-6 sessions whose models ARE in pricing.json. |

The data layer is fine. **The issue is structure, not content.**

---

## 1. What data we have (right now, on your machine)

Pulled live from `http://127.0.0.1:7878` against your real `~/.claude/projects/` and `~/.codex/sessions/`:

```
169 sessions   28 unique projects   15,405 messages   35-day span (2026-04-02 → 2026-05-07)
7,038,326 output tokens   $39.03 paid cost (3 priced models)
```

Top 10 projects by recency:

| Project | Sessions | Messages | Out tokens | Cost | Last active |
|---|---:|---:|---:|---:|---|
| klyne | 1 | 504 | 672K | $0 (opus-4-7) | now |
| trinity | 1 | 68 | 33K | $0 (opus-4-7) | 1h ago |
| ai-for-bharat-hackathon | 7 | 1,149 | 611K | $0 (opus-4-7) | yesterday |
| trackIt | 39 | 4,361 | 2.3M | $0 (opus-4-7) | yesterday |
| mohitpatel | 6 | 960 | 459K | $0.28 | yesterday |
| partner-service | 6 | 359 | 82K | $0.34 | yesterday |
| consultation-service | 10 | 879 | 350K | $0.09 | yesterday |
| operations-app | 35 | 2,344 | 687K | $5.45 | yesterday |
| oms-service | 21 | 1,368 | 507K | $28.90 | 9 days ago |
| subscription-service | 8 | 526 | 155K | $0 | 3 days ago |

Models in use:
- `claude-opus-4-7` × 37 sessions ← **unpriced (the bug)**
- `claude-opus-4-6` × 9
- `claude-sonnet-4-6` × 6
- (rest tagged but model field not stored)

**This is the real shape of the data. Design against these numbers, not abstract ones.**

---

## 2. Data inventory (what the API gives the UI)

### 2.1 Per-session fields (`GET /sessions`, `GET /sessions/:id`)

```ts
interface Session {
  id:            string  // UUID from CLI
  cli:           'claude' | 'codex'
  project_path:  string  // e.g. /Users/mohitpatel/Desktop/Project/klyne
  encoded_cwd:   string  // path-encoded form (for Claude only)
  started_at:    number  // epoch-ms
  last_msg_at:   number  // epoch-ms
  msg_count:     number
  tokens_in:     number
  tokens_out:    number
  cost_usd:      number
  model:         string  // "claude-opus-4-7" etc.
  status:        'active' | 'idle' | 'compacted'
  raw_path:      string  // absolute path to JSONL on disk
}
```

Filter knobs already supported: `?cli=`, `?project=`, `?limit=` (1-500), `?before=` (cursor pagination by `last_msg_at`).

### 2.2 Per-message fields (`GET /sessions/:id/messages`)

```ts
interface Message {
  id:            string
  session_id:    string
  parent_uuid:   string  // links into parent message
  role:          'user' | 'assistant' | 'tool' | 'system'
  content:       string
  tool_calls:    ToolCall[]    // assistant emits tool calls inline
  tool_results:  ToolResult[]  // user-role messages with tool results
  tool_name:     string
  tokens_in:     number
  tokens_out:    number
  cost_usd:      number
  model:         string
  ts:            number  // epoch-ms
}

interface ToolCall    { id, name, input_json }
interface ToolResult  { tool_use_id, output, is_error }
```

### 2.3 Search (`GET /search?q=`)

```ts
interface SearchHit {
  message_id:   string
  session_id:   string
  ts:           number
  role:         string
  snippet:      string  // FTS5 snippet() ~64 tokens around match
  rank:         number  // bm25 — lower is better
}
```
Sub-50 ms p95 over 100K msgs (perf bench verified). Empty query → empty result.

### 2.4 Cost rollups (`GET /cost/summary?group=...`)

Group by: `session` | `project` | `day` | `model`. Returns `{group_key, cost_usd, tokens_in, tokens_out, msg_count}`. Date filter: `?since=&until=` (epoch-ms).

### 2.5 Summary (`GET /sessions/:id/summary`)

Latest auto-generated rolling summary: `{session_id, version, text, model, ts}`. Versioned — can list history of summaries per session.

### 2.6 Restore (`GET /sessions/:id/restore`)

Markdown bundle: `{session_id, markdown, resume_command, last_messages[]}`. The "killer demo" payload after `/compact`.

### 2.7 Settings (`GET /settings`, `PUT /settings`)

Returns current `Config` + `DetectedProviders` (booleans only — no key material).

### 2.8 Wizard (`GET /wizard/detect`, `POST /wizard/complete`)

Detect: providers available + selector recommendations per task with reason strings.

### 2.9 Real-time (`GET /events` SSE)

| Event | Payload | When fires |
|---|---|---|
| `MsgNew` | `{session_id, message_id, ts, role, model, tokens_in, tokens_out, cost_usd}` | every new line written to JSONL |
| `SessionUpdate` | `{session_id, last_msg_at, msg_count, cost_usd, status}` | session counters change |
| `SummaryReady` | `{session_id, version, ts, model}` | rolling summary just persisted |
| `CostTick` | `{ts, total_usd_today}` | periodic cost rollup |
| `CompactDetected` | `{session_id, ts}` | `/compact` heuristic fires |
| `ThreadRebuild` | `{thread_count, ts}` | v1.1 stub |

Heartbeat every 15s; reconnects supported via `Last-Event-ID`.

---

## 3. Derived / aggregable data (no new endpoints needed)

The UI can compute these client-side from `/sessions` + `/cost/summary`:

| Derived | How | Used in |
|---|---|---|
| **Project list** | Group sessions by `project_path`; basename → display name | "Projects" landing page |
| **Project totals** | Sum `msg_count`, `tokens_*`, `cost_usd`; max `last_msg_at`; cli set | Project tile |
| **Recent activity** | Sessions sorted by `last_msg_at` DESC, top N | Dashboard / sidebar |
| **Active vs idle vs compacted** | Bucket by `status` | Status badges |
| **CLI mix per project** | Count sessions where `cli == claude` vs `codex` | Tile badges |
| **Model mix** | `Counter(s.model for s in sessions)` | Settings / cost view |
| **Daily activity heatmap** | `/cost/summary?group=day` for last 35 days | Activity view |
| **Hot models / sessions of the day** | `/cost/summary?group=model` + `started_at` filter | Dashboard hero |

---

## 4. Data gaps (what's missing — small additions if needed)

| Gap | Effort to add | Justification |
|---|---|---|
| `GET /projects` rollup endpoint | XS — server-side aggregation of sessions | Saves UI from holding 169 sessions in memory; lets us paginate projects |
| `GET /projects/:project_path/sessions` | XS — already supported via `?project=` filter on /sessions | Just a different URL shape |
| `GET /tools/usage` (top tools used, per project) | S — new SQL aggregation over `tool_name` | "What tools do I use most" insight |
| `GET /compact_events` (history of compacts) | XS — table already populated | Show "Recovered from 4 compacts in this project" |
| `GET /sessions/:id/export` (markdown / json) | S | "Save this session" power-user feature |
| `GET /search/suggestions` (top phrases) | M — new FTS aggregation | Search empty-state suggestions |

**Recommendation:** add `/projects` and `/compact_events` for v1.1 — they unlock the project-tile UI cleanly. Everything else is v1.2+.

---

## 5. Proposed screen inventory

7 screens total. Numbered in the order a user encounters them.

### 5.1 First-run Wizard (already built — keep)
- 4 screens: Welcome → Detection → ModelPick → Done.
- Used once per install; navigate to `/wizard`.

### 5.2 Dashboard (`/`) — **redesign needed**
**Job:** "What was I working on, what's running now, what should I look at."

Currently: empty state placeholder + cost tile. **Proposed:**

```
┌────────────────────────────────────────────────────────────────────────┐
│  klyne  / search                              [⚙ settings] [user]  │
├────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│  Active now (1)                                                          │
│  ┌──────────────────────────────────────────────────────────────┐       │
│  │ 🟢 klyne · claude-opus-4-7 · 504 msgs · ↑in 3.9K ↓out 672K│       │
│  │    Started 14h ago · last msg 0s ago · open ›                 │       │
│  └──────────────────────────────────────────────────────────────┘       │
│                                                                          │
│  Recent projects (28)                              [view all projects ›]│
│  ┌────────────────┐  ┌────────────────┐  ┌────────────────┐             │
│  │  trackIt       │  │ operations-app │  │   oms-service  │             │
│  │  39 sessions   │  │  35 sessions   │  │  21 sessions   │             │
│  │  4,361 msgs    │  │  2,344 msgs    │  │  1,368 msgs    │             │
│  │  $0 (unpriced) │  │  $5.45         │  │  $28.90        │             │
│  │  yesterday     │  │  yesterday     │  │  9d ago        │             │
│  └────────────────┘  └────────────────┘  └────────────────┘             │
│  (...22 more projects in a single line scroll)                           │
│                                                                          │
│  Cost this week              Activity (last 7 days)                      │
│  ┌──────────────┐            ┌──────────────────────────────┐           │
│  │ $39.03 total │            │  ▂▅▇█▂▁▃ 7,038K out tokens  │           │
│  │ ───────────  │            │  M T W T F S S               │           │
│  │ opus-4-6 $X  │            └──────────────────────────────┘           │
│  │ sonnet $Y    │                                                        │
│  └──────────────┘                                                        │
│                                                                          │
│  Top searches (auto from FTS popularity)  [v1.2]                         │
└────────────────────────────────────────────────────────────────────────┘
```

**Decisions for you:**
1. Tile size — small (3 across, 6 across)? Card with hover state?
2. Should cost tiles always show $0 for unpriced models, or hide them?
3. Activity sparkline — overkill or keep?

### 5.3 Projects index (`/projects`) — **new**
**Job:** Browse all projects. Filter, sort, search.

```
┌────────────────────────────────────────────────────────────────────┐
│  Projects (28)                          [filter: all ▾] [sort: recent ▾]│
├────────────────────────────────────────────────────────────────────┤
│ ▢ trackIt                  39 sess  4,361 msg  yesterday  claude  │
│ ▢ operations-app           35 sess  2,344 msg  yesterday  claude  │
│ ▢ oms-service              21 sess  1,368 msg  9d ago     claude  │
│ ▢ consultation-service     10 sess    879 msg  yesterday  claude  │
│ ▢ ai-for-bharat-hackathon   7 sess  1,149 msg  yesterday  claude  │
│ ▢ partner-service           6 sess    359 msg  yesterday  claude  │
│ ▢ subscription-service      8 sess    526 msg  3d ago     claude  │
│ ...                                                                │
└────────────────────────────────────────────────────────────────────┘
```

Click row → project detail.

**Decisions for you:**
1. Tile grid OR table? Both work; table fits 28 rows on one screen.
2. Per-project filter chips: by CLI, by status (active/idle), by date range?

### 5.4 Project detail (`/projects/<encoded_path>`) — **new**
**Job:** All sessions in one project, grouped by day.

```
┌────────────────────────────────────────────────────────────────────┐
│  ‹ projects                                                          │
│                                                                      │
│  trackIt                                                             │
│  /Users/mohitpatel/Desktop/Project/trackIt                           │
│  39 sessions · 4,361 msgs · 2.3M out tokens · $0.00 (claude-opus-4-7)│
│                                                                      │
│  [search within this project...] [filter: all CLIs ▾]                │
├────────────────────────────────────────────────────────────────────┤
│  Today                                                               │
│    14:32  session-abc123  120 msgs  ↓45K  $0   🟢 active   open ›   │
│                                                                      │
│  Yesterday                                                           │
│    19:11  session-def456   89 msgs  ↓32K  $0   ⚫ idle      open ›   │
│    11:20  session-ghi789  204 msgs  ↓78K  $0   ♻ compacted open ›   │
│                                                                      │
│  Apr 30                                                              │
│    ...                                                                │
└────────────────────────────────────────────────────────────────────┘
```

**Decisions for you:**
1. Day-grouping format: "Today / Yesterday / explicit date" or relative ("3 days ago")?
2. Show summary preview inline under each session row?
3. Compact-event indicator inline ("♻ compacted at 14:23 → restore ›")?

### 5.5 Session detail (`/sessions/<id>`) — already built — **minor refinement**
Currently functional. Refinements:
- Add breadcrumb: `‹ trackIt / session-abc123`
- Add header card: model, started/last, tokens, cost, status badge.
- Add "↩ Restore context" button (W15 modal already wired) prominent at top if `status == compacted`.
- Group consecutive tool_use/tool_result pairs visually.

### 5.6 Search (`/search?q=`) — already built — **redesign needed**
Currently functional. Refinements:
- Result rows currently flat; should show: project name + session id + role badge + matched snippet + relative time.
- Add filters: `cli=` `project=` `role=` `since=` chips.
- Empty state: show "Top searches" / "Recently searched" if available; for v1, just `Try: payment, race condition, refactor`.
- Keyboard nav: ↑↓ select, Enter open, Esc close.

### 5.7 Settings (`/settings`) — already built — **keep, polish**
Already functional. Refinements:
- Tab structure: `[Providers] [Tasks] [Connectors] [About]`.
- Providers tab: show DetectedProviders booleans + reason strings; link to "where to get an API key" docs.
- Tasks tab: ModelPicker per task (Summarize, Title); show selector's recommendation as default.
- Connectors tab: enable/disable Claude, Codex; show JSONL root paths + counts.
- About tab: version, schema version, DB path, "View doctor JSON".

### 5.8 Wizard (`/wizard`) — already built — keep, polish copy.

---

## 6. Proposed information architecture

```
klyne (top nav: 4 items + search)
│
├─ Dashboard      (`/`)
│   ├─ Active now (live tile, SSE-driven)
│   ├─ Recent projects (top 6 tiles, scroll)
│   ├─ Cost this week
│   └─ Activity heatmap
│
├─ Projects       (`/projects`)
│   ├─ Project index (table of 28)
│   └─ Project detail (`/projects/<path>`)
│       └─ Session list grouped by day
│
├─ Search         (`/search?q=`)
│   ├─ Empty state with hints
│   ├─ Result rows: project · session · snippet · time
│   └─ Filters: cli, project, role, date
│
├─ Sessions       (deep-link only via dashboard / project / search)
│   └─ Session detail (`/sessions/<id>`)
│       └─ Restore-context modal (after /compact)
│
└─ Settings       (`/settings`, tabbed)
    ├─ Providers
    ├─ Tasks (model overrides)
    ├─ Connectors
    └─ About
```

Top nav (always visible):
```
[klyne]  Dashboard · Projects · Search    ⚙ Settings  [model picker shortcut]
```

---

## 7. Key user flows

### 7.1 "What was I working on?" (daily glance, ≤ 5 sec)
1. Click klyne icon (or open `localhost:7878`).
2. Dashboard loads → "Active now" tile shows current session.
3. Below, recent projects show top 6 with last-active timestamps.
4. Done. Mental model refreshed.

### 7.2 "Find that thing I worked on Thursday" (≤ 30 sec)
1. Hit `/` → search bar.
2. Type `payment webhook race condition`.
3. Results page: top hits are project + session + snippet, ranked.
4. Click → session detail with the matched message highlighted.
5. (Optional) Click "Open in CLI" → `claude --resume <id>` on clipboard.

### 7.3 "Recover from /compact" (the killer demo, ≤ 15 sec)
1. Working in `claude` terminal, /compact fires, context blown.
2. Switch to klyne tab — session detail shows ♻ compacted badge.
3. Click "Restore context" → modal with rolling summary + last 20 messages as Markdown.
4. Click "Copy as resume prompt".
5. Switch back to terminal, paste. Threading restored.

### 7.4 "How much have I burned this month?" (≤ 10 sec)
1. Dashboard → "Cost this week" tile.
2. Click into details → cost view: per-day, per-project, per-model.
3. (Optional) Identify which project ran up the bill.

### 7.5 "Configure for my BYOK setup" (first run, ≤ 60 sec)
1. Wizard auto-launches.
2. Detection screen: shows what's already set (env vars, Ollama).
3. ModelPick screen: pre-filled with selector's recommendations + "Recommended" badge.
4. Done. → Dashboard.

---

## 8. Visual / interaction principles

These are the constraints to design within (locked by spec §10 / §18):

- **Tailwind 3 defaults.** No DaisyUI / Skeleton / Tremor. Plain Tailwind utility classes.
- **Svelte 5 runes.** No state libraries; reactive primitives only.
- **Dark mode default** (matches your screenshot). Light mode v1.2.
- **No animations beyond simple fades / slides.** This is a tool, not a marketing site.
- **Keyboard shortcuts everywhere.** `/` focus search, `j`/`k` navigate, `Enter` open, `Esc` close, `g d` go dashboard, `g p` go projects, `g s` go settings.
- **Density over whitespace.** Power users have 169 sessions; we should fit ≥10 rows on screen without scroll.
- **Color sparingly.** Status badges (green active / grey idle / amber compacted), CLI badges (purple claude / blue codex), nothing else.

---

## 9. Open design questions for you

These are choices that don't have a single right answer — please weigh in.

1. **Tile vs table on Projects index.** With 28 projects, tile grid (4 across) takes 7 rows. Table takes 28 rows but more density. Personal preference?
2. **Sidebar — keep or kill?** The current always-visible session sidebar is the source of the "flat repetition" problem. Three options:
   - (a) Kill it; navigate via top nav + search only.
   - (b) Keep it but show **projects**, not sessions, with collapsible session children.
   - (c) Make it optional / collapsible (default-collapsed).
3. **Cost display for unpriced models.** When `model = claude-opus-4-7` and we don't have a rate yet — show "$0", "—", "(unpriced)", or hide?
4. **"Active now" semantic.** Right now status=active means "JSONL file was modified recently". Should we tighten to "modified in last 60s"?
5. **Day grouping vs flat list inside a project.** 39 trackIt sessions over 35 days — group by day or just show all sorted?
6. **Markdown rendering of message content?** Currently shown as plain text in `<pre>`. Power users would want syntax highlighting + collapsible code blocks. Worth it for v1.1 or defer?
7. **Activity heatmap on dashboard.** GitHub-style 7×N grid? Sparkline? Just a number? Or remove entirely?
8. **Project rename / alias.** `ai-for-bharat-hackathon` is awkward. Should the user be able to rename project display labels? Alias stored locally.
9. **Notifications / toasts.** When `CompactDetected` fires while you're on the dashboard — toast? Inline banner? Nothing?
10. **Inline summary preview.** Under each session row in the project detail, should we show the first ~80 chars of the latest summary? Adds load + complexity.

---

## 10. What to do with this brief

1. Read it once.
2. Leave inline comments / answers on the 10 design questions.
3. (Optional) Sketch your preferred dashboard layout — even on paper. Drop a photo in `docs/design/` and I'll implement against it.
4. When you're ready, dispatch a "redesign" workstream — I'll wire whatever you choose against the existing API. The data layer doesn't need to change for any of these proposals (except the optional `/projects` rollup, which is XS).

---

## 11. Appendix — what's already there vs missing

| Capability | Backend | UI |
|---|---|---|
| List sessions | ✅ `/sessions` | ✅ flat sidebar (needs grouping) |
| Session detail | ✅ `/sessions/:id/messages` | ✅ basic |
| Search | ✅ FTS5 | ✅ basic |
| Cost rollups | ✅ `/cost/summary` | ⚠ tile only, no drill-down |
| Live updates | ✅ SSE | ✅ wired |
| Restore context | ✅ `/sessions/:id/restore` | ✅ modal |
| Wizard | ✅ `/wizard/*` | ✅ 4 screens |
| Settings | ✅ `/settings` | ✅ basic |
| **Projects landing** | ⚠ derive client-side | ❌ |
| **Project detail** | ⚠ filter `?project=` works | ❌ |
| **Activity / analytics** | ⚠ `/cost/summary?group=day` works | ❌ |
| **Tools usage stats** | ❌ no endpoint | ❌ |
| **Compact event history** | ⚠ table populated, no endpoint | ❌ |
| **Session export** | ❌ | ❌ |

---

*Document version: 1.0 · 2026-05-07 · Author: klyne Wave 4 orchestrator session*

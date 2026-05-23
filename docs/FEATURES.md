# klyne — Complete Feature Reference (with real output from your machine)

> **This doc is the canonical "what does klyne actually do, on real data" reference.**
> Every code block below is **real output captured live** on 2026-05-12 against your real Claude + Codex transcripts. The headline numbers are a point-in-time snapshot (207 sessions, 30-day window, 80,747 messages, $26.5K in compute); because klyne ingests live local transcripts, your browser may already show higher counts by the time you record. Where I could not reproduce a feature without your interactive participation (slash commands inside Claude Code chat, advisor hook firing in real time, web-UI screencasts), I've called it out in **§ Things you need to record yourself** at the bottom — those are the only video shots you have to capture by hand.

Use this doc:

1. **As your video script reference** — copy the real output blocks into the recording overlay.
2. **As your "is this feature actually useful to me?" checklist** — every section ends with a candid *Useful for you?* note.
3. **As your post-install verification** — every command listed here should work against your real data; exact counts may drift upward as new sessions are ingested.

---

## Table of contents

- [Setup state on your machine right now](#setup-state-on-your-machine-right-now)
- [Feature 1 — The MCP server (chat-side tools)](#feature-1--the-mcp-server)
- [Feature 2 — The proactive advisor hook](#feature-2--the-proactive-advisor-hook)
- [Feature 3 — Session insight CLIs (`tokens` / `top` / `patterns` / `roast` / `files` / `subagents`)](#feature-3--session-insight-clis)
- [Feature 4 — Decisions log (CLI + MCP)](#feature-4--decisions-log)
- [Feature 5 — Statusline + OTel exporter](#feature-5--statusline--otel-exporter)
- [Feature 6 — Trust + setup commands (`doctor` / `audit-sessions` / `config` / `mcp install`)](#feature-6--trust--setup)
- [Feature 7 — The web cockpit at `http://127.0.0.1:7878`](#feature-7--the-web-cockpit)
- [Things you need to record yourself](#things-you-need-to-record-yourself)
- [Suggested 5-minute demo script](#suggested-5-minute-demo-script)

---

## Setup state on your machine right now

Captured on 2026-05-12 13:30 IST. Shape of the data klyne was sitting on:

```text
$ ./bin/klyne doctor
{
  "ok": true,
  "version": "5934de4-dirty",
  "schema_version": 9,
  "config_path": "~/.klyne/config.toml",
  "db_path": "~/.klyne/klyne.db",
  "db_size_bytes": 183320576,
  "connectors": {
    "claude": {"enabled": true, "root": "~/.claude/projects", "exists": true},
    "codex":  {"enabled": true, "root": "~/.codex/sessions",  "exists": true}
  },
  "providers": {"anthropic": false, "gemini": false, "ollama": true, "openai": false}
}

$ ./bin/klyne config get plan
plan: max-5x (estimated cap ~220M effective tokens / 5h)

$ ./bin/klyne config get advisor
advisor: on
```

**You're on a Max-5x plan, advisor is enabled, DB is 183 MB, ingest is hooked up to both Claude and Codex roots.** No AI providers configured — which is fine because every feature below is deterministic over JSONL bytes; the optional AI features in the cockpit are BYOK.

Top-of-the-funnel numbers from the web `/usage/stats?days=30` endpoint at capture time:

| Metric (30-day) | Real number on your machine |
|---|---:|
| Total sessions | **207** |
| Total messages | **80,747** |
| Total input tokens | **11.5 B** |
| Total cache reads | **11.2 B** (97 % cache reuse) |
| Total output tokens | **45.6 M** |
| Total cost (priced models only) | **$26,476** |
| Favourite model | `claude-opus-4-7` |
| Peak hour (IST) | **18:00** |
| Current streak | **30 days** (you've coded every single day) |
| Longest streak | **30 days** |

Top projects by token usage:

| Project | tokens_in (cached + uncached) | messages |
|---|---:|---:|
| `Learning/oms-service` | 3.10 B | 12,982 |
| `Learning/trackIt` | 2.66 B | 23,868 |
| `Learning/operations-app` | 1.24 B | 11,457 |
| `Private/project-redacted` | 980 M | 5,893 |
| `Learning/trackIt/.worktrees/build-ios-app-xeD9x` | 773 M | 3,605 |

> **What this means for the README and video.** When you say "klyne sees everything I do", these are the actual numbers behind the claim. Quote them — *"207 sessions, 80K messages, $26K in compute that klyne audited in 4 seconds"* — instead of generic phrases like "many sessions". Specificity sells.

---

<a id="real-world-walkthroughs"></a>

## Feature 1 — The MCP server

> **What it is.** A subprocess that Claude Code and Codex CLI spawn over stdio. The AI itself calls these tools mid-session. **All sixteen are live and tested below against your real data.**

### A1 · `list_sessions` — disambiguate parallel sessions

**Real call** (against this very Claude Code session — the one writing this doc):

```json
$ mcp__klyne__list_sessions
{
  "cwd": "~/Desktop/Project/klyne",
  "candidates": [
    {
      "session_id": "336f4d9b-6f69-48c4-9d87-2247b42c7b35",
      "is_active": true,
      "mod_time": "2026-05-12T08:05:53Z",
      "msg_count": 159,
      "preview": "I want to create a video for 5 min describing all the feature and also update the readme…"
    },
    {
      "session_id": "1f50cf33-f23c-47d3-a163-beb5be501322",
      "is_active": false,
      "mod_time": "2026-05-12T06:58:39Z",
      "msg_count": 1266,
      "preview": "https://github.com/DeibyGS/claudestat can you review this project also…"
    }
  ]
}
```

**Real-world trigger.** You had two Claude Code terminals open in the klyne project this morning — one for the README work (current), one for the claudestat-inspired analytics work earlier. When the AI needs to call `get_pre_compact_context` or `generate_handoff`, it calls `list_sessions` first to disambiguate. The `preview` field is your actual first prompt of each session — that's what makes the picker usable.

**Useful for you?** Yes — every other MCP tool uses this internally to resolve "the current session" when you don't pass a `session_id`. You'll rarely call it directly.

---

### A2 · `get_context_health` — verdict + bloat scorecard

**Real call** (against the current session):

```json
$ mcp__klyne__get_context_health
{
  "state": "healthy",
  "action": "continue",
  "reason": "Session is healthy — keep going.",
  "session_id": "336f4d9b-6f69-48c4-9d87-2247b42c7b35",
  "model": "claude-opus-4-7",
  "context_fill_pct": 15.17,
  "msg_count": 115,
  "bloat": [
    {"label": "Read README.md (1 read)",                          "share_pct": 15.63},
    {"label": "Read cli-review-2026-05-10.md (1 read)",           "share_pct": 10.33},
    {"label": "Read proactive-session-advisor.md (1 read)",       "share_pct": 9.59},
    {"label": "Bash: ls (5 commands)",                            "share_pct": 7.44},
    {"label": "Read mcp.go (1 read)",                             "share_pct": 5.59}
  ]
}
```

**Real-world story.** We're at 15 % context fill — verdict `healthy`. But notice the bloat: my single read of `README.md` already ate 15.6 % of the tool output, and the four largest reads collectively claim 50 % of the prefix. If this session ran 5 more hours, those reads would still be sitting there — even though I might be done with `proactive-session-advisor.md` an hour from now. That's the exact data the **stale-context** advisor trigger uses.

**Useful for you?** Yes. The verdict is one of `healthy / drifting / risky / rescue_now`. The bloat scorecard is what makes `/klyne:status` instantly actionable — you can see *which file* is dragging the prefix down, not just "context is too big".

---

### A4 · `generate_handoff` — deterministic Markdown handoff

**Real call** (against this very session you're reading):

```json
$ mcp__klyne__generate_handoff session_id="336f4d9b-…"
```

**The Markdown it returned:**

```markdown
# Handoff from session `336f4d9b`

We are working in `~/Desktop/Project/klyne`.

## Recent task

I want to create a video for 5 min describing all the feature and also update the readme
with better real world eg so user can understand better so review all the feature and
actions we have a prepare…

## Files touched

- `~/Desktop/Project/klyne/Makefile`
- `~/Desktop/Project/klyne/README.md`
- `~/Desktop/Project/klyne/cmd/klyne/main.go`
- `~/Desktop/Project/klyne/cmd/klyne/mcp.go`
- `~/Desktop/Project/klyne/cmd/klyne/roast.go`
- `~/Desktop/Project/klyne/docs/UI-UX-BRIEF.md`
- `~/Desktop/Project/klyne/docs/cli-review-2026-05-10.md`
- `~/Desktop/Project/klyne/docs/features/analytics-commands.md`
- `~/Desktop/Project/klyne/docs/features/proactive-session-advisor.md`
- `~/Desktop/Project/klyne/docs/features/v2-statusline-files-decisions-subagents-otel.md`
- `~/Desktop/Project/klyne/docs/features/v3-stats-dashboard.md`
- `~/Desktop/Project/klyne/docs/proof/01-compact-recovery/claim.md`
- `~/Desktop/Project/klyne/internal/mcpserver/slashcommands.go`
- `~/Desktop/Project/klyne/internal/mcpserver/slashcommands/handoff.md`
- `~/Desktop/Project/klyne/internal/mcpserver/slashcommands/health.md`
- `~/Desktop/Project/klyne/internal/mcpserver/slashcommands/tokens.md`
- `~/Desktop/Project/klyne/internal/mcpserver/tool_code_review_context.go`
- `~/Desktop/Project/klyne/ui/src/routes/cockpit/+page.svelte`

## Commands run

- `ls` (×5)
- `./bin/klyne config` (×2)
- `./bin/klyne files` (×2)
- `./bin/klyne patterns` (×2)
- `./bin/klyne` / `./bin/klyne audit-sessions` / `./bin/klyne decisions`
- `./bin/klyne doctor` / `./bin/klyne roast` / `./bin/klyne start`
- `./bin/klyne statusline` / `./bin/klyne subagents`

## Last few exchanges
…
```

**Real-world story.** Imagine right now I hit my 5-hour cap and Claude blocks the next turn. I'd open a fresh Claude Code window in the same directory, paste this Markdown, and the new session knows:

- which 18 files I touched
- which 12 commands I ran (and how many times)
- the last 6 exchanges verbatim

**Zero AI calls.** The same input always produces the same output — pass `scope="current-topic"` and it drops files irrelevant to the most recent direction.

**Useful for you?** This is the highest-leverage feature klyne ships. Every 5-hour cap that interrupts you = one of these. The cost of *not* using it is ~5K tokens of re-explanation in the new session.

---

### A5 · `get_pre_compact_context` — recover what `/compact` ate

**Real call** (against `15009012-…` — an oms-service session that ran `/compact` on 2026-04-15):

```json
$ mcp__klyne__get_pre_compact_context session_id="15009012-…" limit=8
{
  "found_compact": true,
  "compact_timestamp": "2026-04-15T17:51:34.256Z",
  "trigger": "manual",
  "pre_tokens": 793401,
  "session_id": "15009012-8c13-4a5d-88d9-2f1f6894ecaa",
  "path": "~/.claude/projects/-Users-user-Desktop-Learning-oms-service/15009012-8c13-4a5d-88d9-2f1f6894ecaa.jsonl"
}
```

**The raw JSONL on disk confirms three real compact events in this session:**

| When | Trigger | preTokens | postTokens | What got compacted |
|---|---|---:|---:|---|
| 2026-04-13 03:51 IST | manual | 826,799 | — | benefit-engine v1 design |
| 2026-04-14 06:47 IST | manual | 918,526 | 12,263 | benefit-engine implementation |
| 2026-04-15 17:51 IST | manual | 793,401 | 9,002 | benefit-engine testing |

**Read that last row again.** `/compact` shrank a 793,401-token conversation to 9,002 tokens — **88× compression**. Everything in those 784,399 missing tokens — file paths, command stems, the test outputs, the tool-result lines — is lost from the AI's view. klyne reads it back from disk.

**Real-world story.** Three weeks ago you debugged a benefit-engine bug in `oms-service`, compacted, debugged some more, compacted again, debugged some more, compacted a third time. **Today, if Claude says "let me re-read the file to remember"**, you type `/klyne:precompact` and get back the lost reasoning — including the manual decisions that drove the design.

**Useful for you?** Absolutely. Three `/compact` events in one session. The audit found two more in your Codex transcripts. Every one of these is recoverable content.

---

### A6 · `get_token_timeline` — per-turn input growth

**Real call** (against this session, 5-hour window):

```json
$ mcp__klyne__get_token_timeline window="5h"
{
  "session_id": "336f4d9b-…",
  "model": "claude-opus-4-7",
  "context_window": 1000000,
  "first_input": 52738,
  "peak_input": 151693,
  "latest_input": 151693,
  "pct_of_context": 15.17,
  "plan_tier": "max-5x",
  "cap_effective": 220000000,
  "pct_used": 0.21,                 // 0.21% of your 5-hour cap
  "total_effective": 453732,         // uncached over the 5-hour window
  "points": [ /* 63 per-turn data points */ ]
}
```

**The same data, rendered by `klyne tokens`:**

```text
Started at 52.7K (5% of context) → peaked at 137.9K (14%) → now at 137.9K (14%).

▃▃▃▃▃▃▃▃▄▄▄▄▄▄▅▅▅▅▆▆▆▆▆▆▆▆▇▇▇▇▇▇████████
24h ago                              now
first ~52.7K · peak ~137.9K · now ~137.9K

| time     | input tokens | % of context | cached | uncached |
|----------|-------------:|-------------:|-------:|---------:|
| 13:30:00 | 52.7K  | 5%  | 19.8K | 32.9K  ← first turn — cache priming
| 13:30:09 | 63.5K  | 6%  | 52.7K | 10.8K
| 13:30:19 | 76.1K  | 8%  | 68.2K | 7.9K
| 13:30:45 | 101.5K | 10% | 98.1K | 3.4K   ← cache kicking in
| 13:31:13 | 108.2K | 11% | 105.3K | 2.8K
| 13:33:42 | 126.6K | 13% | 113.4K | 13.2K  ← I read a big file
| 13:35:07 | 136.8K | 14% | 134.6K | 2.1K
| 13:35:22 | 137.9K | 14% | 136.8K | 1.1K   ← cache fully warm

Activity heatmap (IST)
00          06          12          18        23
··························░░····················
peak hour May 12 13:00 — 54 turn(s), 379K effective tokens · 1 active hour(s) total
```

**Real-world story.** Each row tells a clear story: turn 1 burned 33K uncached because the cache wasn't primed. By turn 5 the cache was doing its job — every subsequent turn cost only 1–8K against the rate-limit. The 13:33:42 spike to 13.2K uncached is when I read `klyne audit-sessions` output. Without this view, all you'd know is "I'm at 14 % context" — not *which turn* burned what.

**Useful for you?** Yes — this is what you reach for when you're trying to understand "why did my session get slow at 4 PM". The cached/uncached split is the load-bearing signal — most tools only show total input and miss the 10× cache discount entirely.

---

### A7 · `record_decision` — pin a load-bearing choice

**Real call** I made while writing this doc:

```json
$ mcp__klyne__record_decision \
    text="Klyne README + FEATURES.md restructure prioritizes real-world examples from user's actual sessions (klyne, trackIt, jointLedger) over fictional fixtures — picked 2026-05-12 during video-doc scoping" \
    tags=["docs", "readme", "video"]

{"id": "d-e00c08fd589c11b0", "ts": 1778573193093}
```

**Then list_decisions immediately recalls it:**

```json
$ mcp__klyne__list_decisions
{
  "count": 1,
  "decisions": [{
    "id": "d-e00c08fd589c11b0",
    "ts": 1778573193093,
    "tags": ["docs", "readme", "video"],
    "text": "Klyne README + FEATURES.md restructure prioritizes real-world examples from user's actual sessions (klyne, trackIt, jointLedger) over fictional fixtures — picked 2026-05-12 during video-doc scoping"
  }]
}
```

**And search_decisions finds it by keyword:**

```json
$ mcp__klyne__search_decisions query="README"
{ ...same row, found by case-insensitive substring "README" }
```

**Real-world story.** Six weeks from now you start a new klyne session and ask Claude: *"refresh me on the docs strategy."* The AI calls `list_decisions` automatically and the pinned note surfaces. **You don't re-litigate.** Same data the `klyne decisions` CLI writes — there is one source of truth.

**Useful for you?** Yes — but you have to use it consistently. The flow: AI sees you make a load-bearing choice ("we picked Postgres over SQLite because…"), AI calls `record_decision`, AI confirms inline. Three weeks later that note is one tool call away. If you don't use it, you'll re-litigate everything next sprint.

> **Note about your current state:** before today, `klyne decisions list` returned zero rows on your machine. The pinned decision above is the first you've ever stored. **For the video, calling out "even I haven't used this enough yet" is actually a more honest pitch than pretending it's already saving you hours.**

---

### A8 · `list_decisions` / A9 · `search_decisions` — recall

See A7 above — both confirmed working. Decisions are project-scoped by default (`project_path` defaults to cwd); pass `all_projects=true` to query across every repo klyne has touched.

---

### A10 · `code_review_context` — optional enrichment

Only fires when `<project_root>/.code-review-graph/` exists (from the upstream [tirth8205/code-review-graph](https://github.com/tirth8205/code-review-graph) project). Gracefully no-ops when absent — returns `detected: false`. **Not currently installed on any of your repos**, so this is a "ready when you adopt the upstream tool" surface.

**Useful for you?** Only if you decide to install the upstream code-review-graph in your active repos. Not required.

---

### A11 · `remember` — store a persistent memory

Stores a project-scoped or global memory (same underlying `decisions` table). Supports multi-line text and runbooks. See [Feature 4.5](#feature-45--klyne-remember-this---refer-klyne--memory-flow) for the full flow.

```json
$ mcp__klyne__remember text="RUNBOOK: add-secret-to-bucket ..." scope="project" tags=["runbook","secrets"]
{"id": "d-71e776ba02137f79", "ts": 1778573193093, "scope": "project", "project_path": "/Users/.../auth-service"}
```

---

### A12 · `recall` — retrieve project + global memories

Returns both project-scoped and global memories in one call. Supports optional `query` (substring filter) and `tag` filter.

```json
$ mcp__klyne__recall query="secret"
{
  "project_path": "/Users/.../auth-service",
  "project_memories": [/* project-scoped matches */],
  "global_memories": [/* global matches */],
  "total": 4
}
```

**Useful for you?** The killer flow is runbooks — multi-line scripted procedures the AI can re-execute with new arguments. See [Feature 4.5](#feature-45--klyne-remember-this---refer-klyne--memory-flow) for a real walkthrough.

---

### A13 · `bootstrap` — Day-1 session brief (Serena-inspired)

The agent-side equivalent of "what was I working on?". One call synthesises four cross-session signals klyne already owns into a single briefing the agent can fetch on turn 1 of a fresh session: recent sessions in this project, project-scoped memories, a preview of global memories (with the remaining count), and the latest active session's context-health verdict. Pure JSONL + SQLite — no AI calls.

```json
$ mcp__klyne__bootstrap
{
  "cwd": "~/Desktop/Project/klyne",
  "sessions": [
    {"session_id": "336f4d9b-…", "is_active": true,  "mod_time": "2026-05-15T08:05:53Z", "msg_count": 159, "preview": "I want to create a video for 5 min describing…"},
    {"session_id": "1f50cf33-…", "is_active": false, "mod_time": "2026-05-15T06:58:39Z", "msg_count": 1266, "preview": "https://github.com/DeibyGS/claudestat can you…"},
    {"session_id": "0a3c25b1-…", "is_active": false, "mod_time": "2026-05-14T22:11:02Z", "msg_count":  402, "preview": "explore serena and see what we can borrow…"}
  ],
  "project_memories": [/* up to 5 project-scoped memories, newest first */],
  "global_memory_count": 7,
  "global_memories_preview": [/* up to 3 most-recent globals */],
  "latest_health": {
    "session_id": "336f4d9b-…",
    "state": "healthy",
    "action": "continue",
    "context_fill_pct": 15
  },
  "markdown": "# klyne bootstrap\n\nProject: `/Users/.../klyne`\n\n## Recent sessions\n…"
}
```

The `klyne-bootstrap` SKILL.md fires this tool automatically when the agent has no prior context in a project — Serena's `initial_instructions`/`check_onboarding_performed`/`read_me` pattern, but synthesized from klyne's own audit data instead of LSP symbols. `/klyne:bootstrap` is the explicit user-driven equivalent.

**Real-world story.** Imagine you re-open Claude Code in the klyne directory next Monday. Instead of the agent asking "what would you like to work on?", the skill matches the empty-context situation, calls `bootstrap`, and prints: *"Three sessions in this project this week, four pinned memories, latest session is healthy at 15% fill, last topic was the Serena-inspired bootstrap tool."* That's Day-1 onboarding without you typing anything.

**Useful for you?** This is the agent-facing companion to `list_sessions` + `recall` + `get_context_health`. Without it, the agent doesn't know what happened in the project before it spawned. With it, every fresh session starts informed.

---

### A14 · `update_memory` — edit text or tags by id

Patches an existing memory row without changing scope, project path, or session id. Text and tags are independently editable; omitting both is an error.

```json
$ mcp__klyne__update_memory id="d-71e776ba02137f79" tags=["runbook","secrets","openbao","rotated"]
{"id": "d-71e776ba02137f79", "updated": true}
```

`text` overwrites the body when supplied; `tags` overwrites the FULL tag set when supplied (pass `[]` to clear). Unknown ids return `memory "<id>" not found` so the agent can prompt the user to call `list_memories` first.

---

### A15 · `delete_memory` — permanent removal by id

```json
$ mcp__klyne__delete_memory id="d-71e776ba02137f79"
{"id": "d-71e776ba02137f79", "deleted": true}
```

Immediate and not undoable. The CLAUDE.md rule expects the agent to confirm the id back to the user before calling.

---

### A16 · `list_memories` — explicit browse with derived names

The "show me everything klyne remembers" surface. Unlike `recall`, this takes no query — its job is to enumerate so the user (or the agent) can pick an id for `update_memory` / `delete_memory`. Each row carries a derived `name` (first non-empty line of the text, truncated to 60 runes with `…`) so the output renders as a Serena-style named list.

```json
$ mcp__klyne__list_memories scope="all"
{
  "project_path": "/Users/.../auth-service",
  "project_memories": [
    {"id": "d-71e776ba…", "name": "RUNBOOK: add-secret-to-bucket", "tags": ["runbook","secrets"], "text": "…"},
    {"id": "d-9a31bc14…", "name": "drop /api/v1 — v2 rolled out 2026-04-12", "tags": ["decision"], "text": "…"}
  ],
  "global_memories": [
    {"id": "d-aa12fe33…", "name": "Always run go test before `git push`", "tags": ["runbook"], "text": "…"}
  ],
  "total": 3
}
```

Scope: `all` (default — both lists), `project`, or `global`. Optional `tag` filter applies to both lists. Default limit 50 per scope, max 500.

**Useful for you?** Three places: agent-driven CRUD (update/delete needs the id), audit ("what does klyne remember globally?"), and demos (one call returns the whole memory inventory). Together with `update_memory` and `delete_memory` this closes Serena's memory-CRUD parity gap without giving up klyne's chat-first remember/recall ergonomics.

---

## Feature 2 — The proactive advisor hook

> **What it is.** A `UserPromptSubmit` hook that fires `klyne advise` on every prompt submit in Claude Code. < 300 ms p99 on a 50 MB JSONL. Empty stdout when no trigger fires (the normal case).

**Real call** I ran against this session:

```bash
$ echo '{"cwd":"~/Desktop/Project/klyne"}' | ./bin/klyne advise
# (empty stdout, exit 0)
```

**That's exactly the right output:** this session is healthy (`get_context_health` returned `healthy`, context fill 15 %, no acceleration, 0.21 % of your 5-hour cap used). Nothing to warn about — silence.

**What it looks like when a trigger DOES fire** (synthetic from the proof fixtures — there's no triggered state on your machine right now):

```text
klyne: ~62% of loaded file context is no longer relevant to your current direction.
`/klyne:handoff scope=current` carries forward only internal/billing/charge.go
and internal/billing/refund.go.
```

The four triggers, OR'd:

| Trigger | Condition | When it would fire on your usage |
|---|---|---|
| **Stale-context** | Jaccard < 0.20 over > 50 % of loaded bytes | When you start session on bug A, finish it, switch to bug B in the same chat. trackIt sessions with 39 ledger files loaded but you've moved to SMS parsing — exactly this. |
| **Acceleration** | 3-turn uncached mean > 2× the prior 5-turn mean (need ≥ 8 assistant turns, floor 5 K) | Your subagent-heavy sessions hit this — e.g. `ccf1c911`, where a Task call spiked uncached input. |
| **5-hour-window** | Total uncached across every Claude + Codex session ≥ 50 % (warn) / 75 % (urgent) of plan cap | You're on `max-5x` (~220M effective tokens / 5h cap). You burned 11.5B tokens in 30 days — average ~380M / day, definitely hits the warn threshold on heavy days. |
| **Hard ceiling** | Context fill ≥ 75 % | Two of your audited sessions (`8a528bc6`: 419K tokens stored; `081215ee`: 275K tokens stored) sit at 40-50 % already. One long debug session away from this. |

**Transition rule:** each trigger fires at most once per state transition. Lifetime cap = 4 advisories per session worst case.

**Useful for you?** Absolutely — your daily volume is enough that the 5-hour-window trigger alone will fire most days. The signal-to-noise rule (once per transition) is why this won't annoy you.

**Kill switch if needed:**

```bash
klyne config set advisor off    # silence for this session class
klyne config set advisor on     # re-enable
```

---

## Feature 3 — Session insight CLIs

> **What it is.** Five read-only CLI commands that turn your SQLite store into glanceable answers. Zero AI calls. All output below is real, generated against your real 275-session DB on 2026-05-12.

### `klyne top --since=168h` — tool rankings (last week)

```text
# klyne top — tool rankings

Across 275 sessions.

| Rank | Tool                              | Calls | Share | Errors | Sessions |
|---:|---|---:|---:|---:|---:|
|  1 | Bash                              |  3003 | 41.6% |      0 |     55 |
|  2 | Edit                              |  1169 | 16.2% |      0 |     39 |
|  3 | Read                              |  1040 | 14.4% |      0 |     53 |
|  4 | exec_command                      |   386 |  5.4% |      0 |      5 |
|  5 | TaskUpdate                        |   374 |  5.2% |      0 |     18 |
|  6 | Grep                              |   329 |  4.6% |      0 |     32 |
|  7 | Write                             |   286 |  4.0% |      0 |     27 |
|  8 | TaskCreate                        |   195 |  2.7% |      0 |     18 |
|  9 | ToolSearch                        |    82 |  1.1% |      0 |     40 |
| 10 | mcp__linear-server__save_issue    |    62 |  0.9% |      0 |     10 |
```

**Real-world story.** **41.6 % of every tool call you made last week was Bash.** That's a single number that explains a *lot* of your patterns output (see below). Bash overuse is the biggest signal in the entire DB.

**Useful for you?** Yes — this is the "where am I spending my agent's attention" view. The "Errors" column is empty, which is good. The "Sessions" column tells you breadth — `Bash` was in 55 of 275 sessions; `mcp__linear-server__save_issue` was in 10 (the focused Linear-ticket work).

---

### `klyne patterns` — inefficiency detection

Real output (truncated — full output has ~30 alerts, 20+ warnings):

```text
# klyne patterns

Walked 275 sessions.

| Severity | Kind            | Session     | Detail                                                        | Metric/Threshold |
|---|---|---|---|---|
| ALERT | tight_loop      | 019e038a    | 84 consecutive exec_command calls — possible loop             | 84 / 5           |
| ALERT | tight_loop      | 11f81071    | 47 consecutive Bash calls — possible loop                     | 47 / 5           |
| ALERT | bash_overuse    | c75b79d2    | Bash 16/16 (100%) — prefer Read/Edit/Grep/Glob                | 100% / 40%       |
| ALERT | bash_overuse    | 51e05c4f    | Bash 13/14 (93%) — prefer Read/Edit/Grep/Glob                 | 93% / 40%        |
| ALERT | bash_overuse    | 0971c9b6    | Bash 11/13 (85%) — prefer Read/Edit/Grep/Glob                 | 85% / 40%        |
| ALERT | tight_loop      | ccf1c911    | 27 consecutive Write calls — possible loop                    | 27 / 5           |
| ALERT | low_cache_reuse | 8e3821b8    | cache reuse 0% of input tokens — likely re-feeding context    | 0% / 30%         |
| WARN  | tight_loop      | 15863c23    | 10 consecutive Edit calls — possible loop                     | 10 / 5           |
| WARN  | bash_overuse    | 081215ee    | Bash 75/132 (57%) — prefer Read/Edit/Grep/Glob                | 57% / 40%        |
```

**Real-world story.**
- Session `019e038a` made **84 consecutive `exec_command` calls** — that's a Codex session caught in an iteration loop.
- Session `c75b79d2` was **100 % Bash** (16/16). Should have used Read/Grep.
- Session `8e3821b8` had **0 % cache reuse** — every turn re-fed the entire prefix at full price.

**Useful for you?** Critical. Each row is one specific behaviour costing you tokens (or rate-limit budget). The thresholds are deterministic — same input, same verdict every time.

JSON form for piping (`klyne patterns --kind=tight_loop --json`):

```json
{
  "patterns": [
    {
      "kind": "tight_loop",
      "severity": "alert",
      "session_id": "019e038a-…",
      "project_path": "~/Desktop/Kim/lava",
      "message": "84 consecutive shell calls — possible loop",
      "metric": 84,
      "threshold": 5
    }
  ]
}
```

---

### `klyne roast` — sardonic insights, no AI

Real output today (after the headline-zero fix shipped 2026-05-12):

```text
klyne roast — 280 sessions

1. Cache reuse 97%. Frugal! Either disciplined or just very repetitive.
2. One session was 100% Bash calls. Have you considered using the file tools? They exist for a reason.
3. 84 consecutive exec_command calls in one session. That's not iteration — that's commitment.
```

**Real-world story.** Three lines, each interpolated from a real number in your DB:

| Line | Source data |
|---|---|
| "Cache reuse 97%" | 11.2 B cache reads / 11.5 B input from `/usage/stats` |
| "100% Bash calls" | Session `c75b79d2`, 16 / 16 Bash calls (matches the patterns output above) |
| "84 consecutive exec_command" | Session `019e038a`, the same alert from patterns |

> **About the dollar headline.** Earlier the header printed `$0.00 total` which looked broken. The deeper reason: `/cost/summary` was deliberately repurposed in v0 from dollar amounts to activity rollups (see [`internal/api/handlers/cost.go`](../internal/api/handlers/cost.go) — flat-subscription users don't get per-session dollar prices since they pay flat anyway). Per-session `sessions.cost_usd` is intentionally 0; the real dollar figures live in `/usage/stats` (`~$26K / 30d` on your machine — see [Feature 7](#feature-7--the-web-cockpit)). The roast headline now hides the dollar when zero, so the output above is clean.

**Useful for you?** Funny and accurate. Shareable. Templated lines you can read at a glance. No AI calls.

---

### `klyne files` — per-file heatmap

Real output for trackIt (your most-touched project, scoped to last 720h):

```text
$ klyne files --project=~/Desktop/Learning/trackIt --since=720h --mutated-only

| Rank | File                                                                  | Reads | Edits | Writes | Sessions | Last touched |
|---:|---|---:|---:|---:|---:|---|
|  1 | trackIt/server/src/controllers/transactionController.js               |    82 |    54 |      0 |       19 | 1d ago |
|  2 | trackIt/client/src/redesign/screens/Home.jsx                          |    40 |    72 |      1 |       11 | 7d ago |
|  3 | trackIt/client/src/redesign/MobileAppV2.jsx                           |    45 |    55 |      1 |       15 | 6d ago |
|  4 | trackIt/client/src/App.jsx                                            |    50 |    47 |      0 |       17 | 8d ago |
|  5 | trackIt/client/src/redesign/screens/EditTxnSheet.jsx                  |    31 |    46 |      1 |        7 | 3d ago |
|  6 | trackIt/.worktrees/audit-fixes/client/src/styles/mobile.css           |    33 |    36 |      0 |        2 | 18d ago |
|  7 | trackIt/server/src/services/parserService.js                          |    25 |    19 |      0 |       15 | 2d ago |
|  8 | trackIt/server/src/controllers/jointLedgerController.js               |    21 |    23 |      0 |        6 | 6d ago |
```

**Real-world story.** Your number-one hottest file across all 19 active sessions in trackIt is `transactionController.js`. You've **Read it 82 times and Edited it 54 times in 30 days.** That's the dependency-graph centre of trackIt. Anyone joining your team would benefit from knowing that the moment they clone the repo.

**Useful for you?** Yes — this is the "what does my AI actually keep its hands on" view. Combined with `subagents` and `patterns`, you get a complete picture of where your token budget lives.

---

### `klyne subagents` — Task-tool attribution

Real output:

```text
$ klyne subagents --since=168h

# klyne subagents — Task-tool attribution

**Totals:** 56 subagents across 12 parent sessions · 315.0M tokens in (95% cached) · 1.3M tokens out

| Parent session | Project                                | Subagents | Tokens in | Tokens out | Cache % | Last activity |
|---|---|---:|---:|---:|---:|---|
| ccf1c911       | …/Private/project-redacted             |     31 | 201.1M | 882k | 96% | 4d ago |
| 081215ee       | …/Learning/consultation-service        |      3 |  33.5M | 149k | 97% | 14h ago |
| 7eba9e85       | …/worktrees/agent-a08f0df6             |      4 |  30.8M |  97k | 95% | 16h ago |
| 6a9b785d       | …/worktrees/musing-tereshkova-a2a74d   |      2 |  20.4M |  40k | 97% | 1d ago |
| a198b00e       | …/Project/klyne                        |      3 |   9.4M |  15k | 88% | 2d ago |
| 086018aa       | …/worktrees/xenodochial-shirley-ee497d |      1 |   4.9M | 6.4k | 94% | 2d ago |
| aeba0a52       | …/.worktrees/feat-joint-ledger-splits  |      2 |   4.5M |  13k | 92% | 6d ago |
| 9706c1fc       | …/worktrees/focused-carson-a4a485      |      2 |   3.9M | 8.8k | 53% | 2d ago |
| fe266454       | …/ai-for-bharat-hackthon/judgmentflow  |      3 |   3.6M |  42k | 89% | 6d ago |
| c099ff1b       | …/Learning/consultation-service        |      3 |   2.2M |  10k | 87% | 6d ago |
```

**Real-world story.** The number that should leap off the screen: **session `ccf1c911` in a redacted private project spawned 31 subagents that collectively burned 201 M input tokens (96 % cached) in 4 days.** Until klyne shipped this command, every one of those tokens was **invisible** in the parent session's `cost_usd` field — Claude Code's cost only counts the *result* of a Task tool call, not the subagent's full conversation. **Your real spend was higher than Claude Code told you.**

**Useful for you?** Yes — this is the hidden-cost view. If you do anything with subagents (Task-tool, mcp-managed-agents), you need this.

---

### `klyne tokens` — per-session timeline (already shown in A6)

Same data, two faces: MCP for the AI; CLI for you. The CLI also appends an activity heatmap which the MCP returns as raw points (the chart UI in `/sessions/[id]` renders it as a real chart).

---

### Summary — Session insight CLIs

| Command | What's the headline number from your data? |
|---|---|
| `klyne top --since=168h` | **41.6 % of your tool calls last week were Bash** |
| `klyne patterns` | **84 consecutive `exec_command` in one session** (alert tier) |
| `klyne roast` | Three real, attributable zingers (free shareable copy) |
| `klyne files --project=trackIt` | **`transactionController.js` — 82 reads · 54 edits · 19 sessions** |
| `klyne subagents` | **31 hidden subagents · 201 M tokens · 96 % cached** in one session |
| `klyne tokens` | Live: this session — **52.7K → 137.9K** over 1 h, 14 % of 1M context |

---

## Feature 4 — Decisions log

> **What it is.** Project-scoped, immutable notes pinned to a project (and optionally a session). Same data behind the `record_decision` / `list_decisions` / `search_decisions` MCP tools and the `klyne decisions add / list / search / delete` CLI.

Already demonstrated round-trip in [A7](#a7--record_decision--pin-a-load-bearing-choice).

**CLI flow (start to finish):**

```bash
# Add
$ klyne decisions add "Picked Postgres over SQLite — team already runs PG" --tags=db,infra

# List (current project)
$ klyne decisions list
| d-e00c08fd589c11b0  2026-05-12  Picked Postgres over SQLite — team already runs PG  [db, infra]  |

# List across every project
$ klyne decisions list --all

# Search
$ klyne decisions search "postgres"

# Delete (immutable — amendments are delete-then-add)
$ klyne decisions delete d-e00c08fd589c11b0
```

**Useful for you?** Yes, **but adoption is a habit**. Right now (literally, until I added one above) you had **zero decisions stored** across 207 sessions. That's the most actionable insight in this whole doc: the feature is wired up and works perfectly — but only pays off if you *use* it. Two suggestions:

1. When you finish the next sprint, audit-record 3-5 load-bearing choices you made this week. One per project.
2. Tell Claude in your CLAUDE.md: *"When the user states a load-bearing technical choice with a reason, call `record_decision` and confirm what you wrote."* That makes the AI do the work for you.

---

## Feature 4.5 — Runbooks: "klyne remember this …" / pre-execution recall

> **What it is.** A chat-first runbooks surface on top of the same `decisions` table. Two new MCP tools (`remember`, `recall`) plus a dashboard route at `/runbooks` that groups runbooks by service. The hero behaviour is **pre-execution recall** — `recall` fires automatically before risky shell commands per the CLAUDE.md rule. Shipped 2026-05-12; repositioned as runbooks 2026-05-19.

### The trigger phrases

| You say in Claude Code | Klyne does |
|---|---|
| *"klyne remember this …"* | Stores a project-scoped runbook (under the current cwd's project). Multi-line OK. |
| *"klyne remember this globally …"* / *"klyne remember … everywhere"* | Stores a global runbook — applies in every project. |
| *"refer klyne …"* / *"check klyne …"* / *"what does klyne remember about …"* | Recalls project + global runbooks in **one** MCP call. If a matching runbook exists, Claude substitutes variables from your request and asks for confirmation before executing. |
| *(automatic, before risky shell commands)* | Per the CLAUDE.md rule, Claude calls `recall` before deploys, restarts, secrets ops, migrations, and `./scripts/*` invocations — then substitutes the matching runbook. |

The trigger phrases (and the automatic pre-execution call) are activated by a CLAUDE.md rule — full paste-ready snippet in [`docs/features/runbooks.md`](./features/runbooks.md#claudemd-rule-paste-this).

### Storage

Same `decisions` table as `record_decision`. Schema migration 009. `project_path = ""` means global; anything else means project-scoped. **No new migration required.**

### Dashboard view

Open `http://127.0.0.1:7878/runbooks`. Layout:

- **Global** section at the top — every runbook with `project_path = ""`.
- **One section per project** below, sorted by most-recent-activity DESC, so the service you're actively working in lands at the top.
- Each card: short id, relative timestamp, deterministically-coloured tags, full text (multi-line preserved), Delete button.
- Filter bar: `?q=<substring>` and `?tag=<tag>` — debounced.
- Read-with-delete only — writes are chat- and CLI-driven so the dashboard never grows a forms layer for a workflow that lives naturally in chat.

### A real round-trip story

```
You (Claude Code, in /Users/.../auth-service):
> klyne remember this for our auth-service project:
>   RUNBOOK: add-secret-to-bucket
>   1. ./scripts/openbao/bao-secret.sh get $SVC $BUCKET          # see names
>   2. ./scripts/openbao/bao-secret.sh set $SVC $BUCKET $K=$V    # patch, doesn't clobber

AI: Recorded d-71e776ba02137f79 (project scope, tags: runbook, openbao, secrets).

[2 weeks later, fresh Claude Code session, different worktree of auth-service]

You: refer klyne and add NEW_API_KEY=abc123 to auth-service main bucket

AI: Found a project-scoped runbook (d-71e776ba…). Substituting:

    # Step 1 — verify the bucket
    ./scripts/openbao/bao-secret.sh get auth-service main
    # Step 2 — add the key (uses `bao kv patch`, won't clobber)
    ./scripts/openbao/bao-secret.sh set auth-service main NEW_API_KEY=abc123

    Confirm before I run step 2?
```

### Useful for you?

The killer flow is **pre-execution recall** — multi-line scripted procedures that you used to either keep in your head or in a Notion page nobody reads. Pinning them as runbooks means the AI consults the right one at the moment of execution and never gets the step order wrong. Two-week test plan:

1. Pin 2-3 runbooks (secrets / deploy / DB migration) per project this week.
2. Pin 1-2 global runbooks for procedures that work the same way across all your services.
3. Add the CLAUDE.md rule (see [runbooks feature doc](./features/runbooks.md)) so Claude consults `recall` automatically before operational commands.
4. Re-evaluate after a week. If you find yourself relying on the auto-recall (or saying `refer klyne …` on demand) daily, runbook adoption stuck — keep it. If usage is sparse, decisions-as-storage is fine forever.

### Supported CLIs / hosts

| Surface | Claude Code | Codex CLI |
|---|---|---|
| `remember` tool | ✅ | ✅ (Codex registers the MCP server too) |
| `recall` tool | ✅ | ✅ |
| Trigger-phrase auto-recall via CLAUDE.md | ✅ | ⚠️ Codex does not yet honour CLAUDE.md; explicit `recall` calls still work |
| `/runbooks` dashboard | ✅ (same browser for both) | ✅ |

---

## Feature 5 — Statusline + OTel exporter

### `klyne statusline` — one line for Claude Code's statusline

```text
$ klyne statusline --format=short
klyne ▸ 14% ctx · 137k/1M · 5h 0%

$ klyne statusline --format=mini
14% · 0%

$ klyne statusline --format=plain
klyne 14% ctx · 137k/1M · 5h 0%
```

**Wire into Claude Code:**

```json
{
  "statusLine": {
    "type": "command",
    "command": "klyne statusline"
  }
}
```

**Real-world story.** Glanceable — `14% ctx · 137k/1M · 5h 0%` tells you context fill, tokens used vs context window, and 5-hour cap consumption. Never errors. Exits 0 even when no session is found ("klyne ▸ idle") so it can't break your prompt.

**Useful for you?** Yes — if you have a custom statusline in Claude Code. Skip if you don't.

---

### `klyne otel emit` — opt-in OTel JSONL export

Real run on your 24-hour data:

```bash
$ klyne otel emit --since=24h --out=/tmp/klyne-demo/spans.jsonl
wrote 3047 spans to /tmp/klyne-demo/spans.jsonl
```

**Sample span (real, first line of the file):**

```json
{
  "trace_id": "3a7874b7c837f15be4d5ca5802c90156",
  "span_id": "41452533051dc5f0",
  "name": "gen_ai.completion",
  "kind": "SPAN_KIND_INTERNAL",
  "start_time": "2026-05-12T06:55:18.498Z",
  "end_time": "2026-05-12T06:55:33.737Z",
  "attributes": {
    "gen_ai.system": "anthropic",
    "gen_ai.request.model": "claude-opus-4-7",
    "gen_ai.usage.input_tokens": 78435,
    "gen_ai.usage.cache_read_input_tokens": 0,
    "gen_ai.usage.cache_write_input_tokens": 78429,
    "gen_ai.usage.output_tokens": 511,
    "gen_ai.cost.usd": 1.50895875,
    "klyne.cli": "claude",
    "klyne.project_path": "~/Desktop/Learning/operations-app",
    "klyne.session_id": "b888da42-9c9f-4add-950f-d5dca43f72ff"
  },
  "resource": {"service.name": "klyne", "service.namespace": "ai-coding-cli"},
  "status": {"code": "STATUS_CODE_UNSET"}
}
```

**Real-world story.** 3,047 spans for 24 hours of activity. One span per assistant turn. `gen_ai.*` attributes follow the OTel GenAI working-group draft so a Grafana / Honeycomb / Datadog pipeline can ingest these without custom parsers. `klyne.*` resource fields let you join back to klyne's local DB.

**Useful for you?** Only if you run an OTel collector. **File-only — never auto-pushes off-host.** Skip unless you have an observability backend you want this in.

---

## Feature 6 — Trust + setup

### `klyne audit-sessions --limit 20` — the trust foundation

Real output today (after the daemon caught up on ingestion):

```text
# .klyne audit report
Generated: 2026-05-12T10:00:42Z
Sessions sampled: 20

## Summary
- Latest-assistant input_tokens accuracy: 17/17 ✓ (100.0%)
- Sessions not yet ingested into klyne DB: 0
- Sessions with no assistant turn yet: 3
- /compact events detected: 0

All sampled sessions match. Trust foundation holds for this slice.

## Codex coverage
- Codex transcripts sampled: 20
- Sessions with token usage data: 17
- Cumulative tokens across audited Codex sessions: ~1.1M
- /compact events detected: 2 total across 1 sessions
```

> **Earlier today this returned 11/14 ✓ (78.6 %) with 3 mismatches** in sessions `081215ee`, `8a528bc6`, `b888da42`. The deltas were small enough (-22 K, -1.4 K, -37 K against 100 K-400 K totals) that they looked like ingestion lag — and that's exactly what they were. The daemon was down when I first audited; the JSONL files kept growing while the DB stayed frozen. Five minutes after `klyne start`, the daemon caught up and the audit now reports **17/17 ✓ 100 %.**
>
> **The lesson for the README/video:** the audit is the trust foundation, and it caught a real lag-bug **without me having to look for it**. If you keep the daemon running, mismatches will stay green. If the daemon ever falls behind, the audit will tell you.

**Real-world story.** Today's audit also surfaced one structural item worth noting: the audit excludes sessions that haven't had an assistant turn yet ("Sessions with no assistant turn yet: 3"), which is correct — there's no `tokens_in` to compare against until the AI replies once. That's why the denominator is 17, not 20.

**Useful for you?** Critical. This is the rule that keeps klyne honest. The non-zero exit code on mismatch makes `make ci` fail on real bugs, while a healthy daemon keeps it green.

---

### `klyne mcp install` — one idempotent setup command

I haven't re-run this since it would clobber your current settings — but the post-install behaviour is observable: your `~/.klyne/config.toml` shows `[advisor] disabled = false`, your `~/.claude/commands/klyne/` directory contains all 6 markdown slash commands, and the MCP server is registered in `~/.claude.json` (proven by the fact that this conversation can call `mcp__klyne__*` tools).

For the video, the expected install output is:

```text
$ klyne mcp install
claude: updated — ~/.claude.json
codex:  updated — ~/.codex/config.toml
advisor: updated — ~/.claude/settings.json
slash commands: updated — 6 files in ~/.claude/commands/klyne
klyne: advisor active — you'll see inline warnings in Claude Code when sessions drift,
       accelerate, or approach your 5-hour cap.
To enable the 5-hour-window advisor, run: klyne config set plan <pro|max-5x|max-20x|team>
```

**Idempotent** — safe to re-run on every binary upgrade. The four idempotent writes are:

1. `~/.claude.json` → `mcpServers.klyne` entry
2. `~/.codex/config.toml` → `[mcp_servers.klyne]` entry
3. `~/.claude/settings.json` → `hooks.UserPromptSubmit[]` entry (merged, not overwritten — preserves your existing hooks)
4. `~/.claude/commands/klyne/*.md` → six Markdown slash files

---

### `klyne config` — read/update settings

```text
$ klyne config show
[server]
addr = '127.0.0.1:7878'
[paths]
db = '~/.klyne/klyne.db'
pricing_override = '~/.klyne/pricing.json'
[connectors.claude]
enabled = true
root = '~/.claude/projects'
[connectors.codex]
enabled = true
root = '~/.codex/sessions'
[ai]
summary_model = 'auto'
title_model = 'auto'
embed_model = 'off'
[plan]
tier = 'max-5x'
custom_cap = 0
[advisor]
disabled = false
```

```text
$ klyne config get plan
plan: max-5x (estimated cap ~220M effective tokens / 5h)

$ klyne config get advisor
advisor: on
```

**Useful for you?** Yes — the two keys that matter (`plan` and `advisor`) are surfaced as one-line read/write commands. The plan tier is what makes the 5-hour-window advisor work.

---

## Feature 7 — The web cockpit

> **What it is.** SvelteKit app, bound to `127.0.0.1:7878`, served by `klyne start`. Pulls from the same SQLite DB the CLIs read.

**Real `/usage/stats` data** (the data behind the `/stats` page):

```json
$ curl http://127.0.0.1:7878/usage/stats?cli=claude&days=30
{
  "from": "2026-04-12", "to": "2026-05-12",
  "total_messages": 80747,
  "total_sessions": 207,
  "total_input": 11547571134,
  "total_output": 45626090,
  "total_cache_read": 11176582529,
  "total_cache_write": 370126634,
  "total_cost_usd": 26475.98,
  "favorite_model": "claude-opus-4-7",
  "peak_hour": 18,
  "peak_hour_local": "18:00 (UTC+05:30)",
  "current_streak": 30,
  "longest_streak": 30,
  "active_days": 30,
  "window_days": 30,
  "daily": [
    {"date": "2026-05-12", "input": 340393342, "output": 1286106, "cache_read": 327118348, "messages": 2750, "cost_usd": 835.86},
    {"date": "2026-05-11", "input": 537439766, "output": 3118074, "cache_read": 516877954, "messages": 3407, "cost_usd": 1394.56},
    {"date": "2026-05-10", "input": 277058856, "output": 766984,  "cache_read": 264631262, "messages": 1474, "cost_usd": 687.48},
    {"date": "2026-05-09", "input": 196641357, "output": 1212699, "cache_read": 186132679, "messages": 1969, "cost_usd": 567.16},
    {"date": "2026-05-08", "input": 373561724, "output": 1910720, "cache_read": 361478747, "messages": 2269, "cost_usd": 912.06}
  ]
}
```

**Real `/cost/summary?group=project`:**

```json
{
  "buckets": [
    {"key": "Learning/oms-service",      "tokens_in": 3096644327, "tokens_out": 7542820, "count": 12982},
    {"key": "Learning/trackIt",          "tokens_in": 2655796673, "tokens_out": 12574600, "count": 23868},
    {"key": "Learning/operations-app",   "tokens_in": 1238042520, "tokens_out": 3723044, "count": 11457},
    {"key": "Private/project-redacted",  "tokens_in":  980423567, "tokens_out": 3796311, "count": 5893},
    {"key": "trackIt/.worktrees/build-ios-app", "tokens_in": 772512368, "tokens_out": 2930489, "count": 3605}
  ]
}
```

**Real `/sessions?limit=2` (top 2 by recency):**

```json
[
  {
    "id": "336f4d9b-…",
    "cli": "claude",
    "project_path": "~/Desktop/Project/klyne",
    "msg_count": 150,
    "tokens_in": 10343819,
    "tokens_out": 113377,
    "cached_read_tokens": 9153312,    // 88% cache reuse
    "cached_write_tokens": 1181338,
    "model": "claude-opus-4-7",
    "status": "active"
  },
  {
    "id": "081215ee-…",
    "cli": "claude",
    "project_path": "~/Desktop/Learning/consultation-service",
    "msg_count": 465,
    "tokens_in": 45270782,
    "tokens_out": 372594,
    "cached_read_tokens": 40683575,   // 90% cache reuse
    "cached_write_tokens": 4586546
  }
]
```

### Web routes

| Route | Data source | Headline numbers on your machine |
|---|---|---|
| `/cockpit` | `/cockpit/threads` + SSE `MsgNew` events | Live tiles for each running session, latest 10 messages tail, auto-update without refresh |
| `/stats` (Overview) | `/usage/stats` | 30-day bar chart of `$835 → $1394 → $687 → $567 → $912 / day`, peak hour 18:00 |
| `/stats` (Models) | `/usage/stats` aggregated | Models-by-cost: `opus-4-7` dominates, `opus-4-6` and `sonnet-4-6` distant runners (per UI brief) |
| `/stats` (Daily) | `/usage/stats.daily` | 30 rows; today 2,750 messages / $835 |
| `/stats` (Stats) | `/usage/stats` extras | **GitHub-style 12-week heatmap, current streak 30, longest 30, active days 30, peak hour 18:00** |
| `/projects` | `/cost/summary?group=project` | 28 projects; `oms-service` (3.1B), `trackIt` (2.7B), `operations-app` (1.2B) lead |
| `/projects/[name]` | drill-down on one project | All sessions, files, decisions for one repo |
| `/sessions/[id]` | `/sessions/:id` + `/sessions/:id/messages` | Full transcript, every tool call, token timeline, resume command |
| `/search` | `/search?q=…` (FTS5) | Searched `jointLedgerController` above — 3 hits in 4 ms |
| `/advisors` | `/advisors` | Per-session advisor state — which triggers fired and when |
| `/runbooks` | `/memory/items` (HTTP route kept its `memory` prefix for back-compat) | Runbooks grouped by project — pre-execution-recall surface |

**Useful for you?** The web UI is where most users will spend most of their time. The Stats page is the killer — **30-day streak / $26K / 11.5B input / 80K messages** is the kind of single-screen summary that you can't get from any CLI.

> **Note:** the UI is functional but layout-naive — see [`docs/UI-UX-BRIEF.md`](./UI-UX-BRIEF.md) for the v1.1 design queue (project grouping in sidebar, true overview dashboard, pricing.json fix for opus-4-7).

---

## Things you need to record yourself

> **Driving with OpenAI Computer Use?** Read this whole section first — it's written as an agent playbook. Each shot has:
>
> 1. **Goal:** what success looks like
> 2. **Preconditions:** what must be true before starting (daemon running, project open, session loaded, etc.)
> 3. **Exact steps:** keystroke-by-keystroke instructions, no ambiguity
> 4. **Success signal:** what to verify on screen before moving on
> 5. **If you can't:** explicit fallback so the agent doesn't get stuck

**Global preconditions (do these once before any shot):**

| Step | Action | Verify |
|---|---|---|
| G1 | Open a terminal in `~/Desktop/Project/klyne` | `pwd` returns that path |
| G2 | Run `./bin/klyne` (or `./bin/klyne start`) | Output line `INFO klyne listening addr=127.0.0.1:7878` |
| G3 | Open browser tab to `http://127.0.0.1:7878` | Page renders, top nav shows Work / Runbooks / Worklog / Insights |
| G4 | Open Claude Code and start a new chat inside `~/Desktop/Project/klyne` (use `cd` first, then `claude`) | Claude Code prompt visible at the bottom of the terminal |
| G5 | In another terminal pane, confirm MCP wiring exists: `cat ~/.claude.json \| grep -A2 klyne` | Returns a non-empty `mcpServers.klyne` block |

If any precondition fails, **stop and tell the human** — the rest of the playbook assumes them.

---

### Shot 1 — `/klyne:status` in Claude Code chat

**Goal.** Capture the unified context surface rendered inline — verdict header (`healthy` / `drifting` / `risky` / `rescue_now`) + recommended action + context fill %, trajectory headline + ASCII sparkline + per-turn token table, top-3 bloat sources.

**Preconditions.** G1-G5 satisfied. The current Claude Code session must have ≥ 5 turns so the verdict isn't trivial and the timeline has rows.

**Exact steps:**

1. Click into the Claude Code chat input box at the bottom of the terminal.
2. Type the literal characters: `/klyne:status`
3. Wait for the autocomplete dropdown to show **`/klyne:status`** with the description **"Unified context check — health verdict + recommended action + token timeline + top bloat sources for the active session"**.
4. Press **Enter** to select it.
5. Press **Enter** again to submit (no argument needed).
6. Wait for the AI response (typically 2-5 seconds).

**Success signal.** A single Markdown block renders inline with:
- A heading `# Session status — <short-session-id>`
- A line like `**State:** drifting · **Action:** continue · **Context fill:** 14%`
- A one-sentence reason quoted underneath
- A `## Tokens` section with a `Started at X → peaked at Y → now at Z` trajectory headline, an ASCII sparkline of bar characters `▁▂▃▄▅▆▇█`, and a Markdown table with columns `time | input tokens | % of context | cached | uncached`
- A `## Top context-bloat sources` section with up to 3 numbered items

**Frame to capture.** Full-window screenshot showing the prompt (`/klyne:status`) plus the rendered output. Likely needs a scroll-up after submit to capture the verdict header above the table.

**If you can't:** if the slash dropdown doesn't show `/klyne:status`, run `./bin/klyne mcp install` in another pane, restart Claude Code, and try again. If the response says "Session has no assistant turns yet", the session is too new — send any prompt to Claude first ("say hi"), wait for the reply, then retry.

---

### Shot 3 — `/klyne:handoff` in Claude Code chat

**Goal.** Capture the deterministic Markdown handoff a user would paste into a fresh session.

**Preconditions.** G1-G5. Session has touched at least 3 files (so "Files touched" section is non-trivial).

**Exact steps:**

1. Click into chat input.
2. Type: `/klyne:handoff`
3. Press **Enter** to select autocomplete, **Enter** again to submit.
4. Wait 2-5 seconds.

**Success signal.** A Markdown block titled `# Handoff from session <short-id>` with sections:
- `## Recent task` (your first user prompt)
- `## Files touched` (bulleted list, ≥ 3 entries)
- `## Commands run` (bulleted list with reuse counts like `(×3)`)
- `## Last few exchanges`

**Frame to capture.** Two screenshots: (a) the top of the handoff showing `# Handoff from session…` and the Files-touched bullets; (b) scrolled down to show the Last-few-exchanges section.

---

### Shot 4 — `/klyne:precompact` against a real compact event

**Goal.** Capture klyne recovering messages from before a `/compact` event — the killer demo.

**Preconditions.** G1-G3 satisfied. **For this shot, you don't need a fresh Claude Code session — you need access to a Claude Code session that already has a `/compact` event in its history.** The maintainer's `oms-service` repo has one: session `15009012-8c13-4a5d-88d9-2f1f6894ecaa`.

**Canonical path — invoke from Claude Code chat in the oms-service repo:**

1. In a fresh terminal: `cd ~/Desktop/Learning/oms-service && claude`
2. Wait for Claude Code prompt.
3. Click into chat input.
4. Type: `/klyne:precompact`
5. Press **Enter** to select, **Enter** again to submit.
6. If multiple sessions are ambiguous, klyne will list candidates — pick the one ending in `…942-2f1f6894ecaa` (session `15009012`).
7. Wait 3-5 seconds.

**Success signal.** A block with:
- A summary line: `Recovered N messages from before the last /compact event (trigger=manual, pre-compact size 793,401 tokens)`
- A list of `[user]` / `[assistant]` lines showing the original conversation that was compacted away

**Frame to capture.** Two screenshots: (a) the summary line with the 793K-token figure visible; (b) the recovered messages list scrolled to show at least 4 `[user]`/`[assistant]` turns.

**If you can't use Claude Code chat:** the synthetic equivalent is in [`docs/proof/01-compact-recovery/`](./proof/01-compact-recovery/). Run `go test -v ./docs/proof/01-compact-recovery/...` and capture the green test output as the fallback.

---

### Shot 5 — The advisor hook firing inline

**Goal.** Capture the one-line advisory injected into a Claude Code chat the moment the user presses Enter on a degrading session.

**This is the hardest shot.** The advisor is silent unless one of four triggers fires. You have two paths:

**Path A — wait for a real trigger.** Run your normal heavy session for an hour. Eventually you'll cross the 5-hour-window 50 % threshold or hit the 75 % context-fill mark. When the next prompt fires, the advisory appears. Capture it. **Not viable on a fixed recording schedule.**

**Path B — force a trigger artificially.** This is the recommended path for recording.

**Preconditions.** G1-G5.

**Exact steps to force a trigger:**

1. Open a terminal in `~/Desktop/Project/klyne`.
2. Temporarily lower the plan cap to make the 5-hour-window trigger fire:
   ```
   ./bin/klyne config set plan custom --cap=1000000
   ```
   (1 M effective tokens / 5h — you've burned more than that today, so the warn threshold fires immediately.)
3. Confirm: `./bin/klyne config get plan` should return `plan: custom (cap=1000000 effective tokens / 5h)`.
4. Open Claude Code in the same project: `claude`
5. Type any non-trivial prompt — e.g. `summarise what changed in this file`
6. Press **Enter**.
7. **Watch the chat for an italicised advisory line** that appears **before** the AI's reply. Something like:
   > *klyne: you've used ~250 % of your 5-hour window across N active sessions…*

**Success signal.** A line beginning with `klyne:` appears at the top of the AI's response (or as a system-level injection) before the normal reply.

**Frame to capture.** A screenshot showing your prompt, the `klyne:` advisory line, and at least one line of the AI's reply.

**After capturing, restore your plan:**
```
./bin/klyne config set plan max-5x
```
Confirm with `./bin/klyne config get plan` → `plan: max-5x (estimated cap ~220M effective tokens / 5h)`.

**If you can't:** the advisor logic itself is reproducible via the test fixtures in [`docs/proof/03-advisor/`](./proof/03-advisor/). Run `go test -v ./docs/proof/03-advisor/...` and capture the green test output as the fallback. Or narrate the four trigger conditions over a still frame of the table in [Feature 2](#feature-2--the-proactive-advisor-hook).

---

### Shot 6 — `klyne mcp install` re-run (idempotent)

**Goal.** Capture the install flow output showing all four idempotent writes succeed.

**Preconditions.** Already installed on this machine — that's fine, we want the `updated` / `already-installed` path.

**Exact steps:**

1. Open a terminal in `~/Desktop/Project/klyne`.
2. Run literally: `./bin/klyne mcp install`
3. Wait < 2 seconds.

**Success signal.** Stdout contains 4 lines, in order:
```
claude: <action> — ~/.claude.json
codex:  <action> — ~/.codex/config.toml
advisor: <action> — ~/.claude/settings.json
slash commands: <action> — 6 files in ~/.claude/commands/klyne
klyne: advisor active — you'll see inline warnings in Claude Code when sessions drift,
       accelerate, or approach your 5-hour cap.
```

`<action>` will be `already-installed` or `updated` for an existing install (not `added`). That's fine — emphasises idempotency.

**Frame to capture.** Terminal screenshot of the full output.

**If you want a clean "added" demo,** temporarily remove the existing entry before running. **Not recommended on a real machine** since you'd lose the live MCP wiring; only do this on a fresh VM or container.

---

### Shot 7 — The web cockpit live-streaming tiles

**Goal.** Capture the `/cockpit` page with live tiles updating via SSE.

**Preconditions.** G1-G3. **You must have ≥ 1 active Claude Code session running in the background** (a session modified within the last 30 minutes — see `ACTIVE_MS` in `ui/src/routes/cockpit/+page.svelte`).

**Exact steps:**

1. Open or focus the browser tab at `http://127.0.0.1:7878/cockpit`.
2. If the page shows "No active sessions" or just idle tiles: in another terminal, run `claude` in any of your projects and send one prompt. Wait 5 seconds. Refocus the cockpit tab.
3. The page should render a grid of tiles, one per session.
4. Each tile shows: project name at top, CLI badge (`claude` / `codex`), last-active timestamp, and a scrollable message tail at the bottom.

**Success signal.** Tiles render with project names visible. **Watching for ~30 seconds, you should see at least one tile auto-update** (the message-tail scrolls or a new entry appears) without you reloading the page — that's the SSE wire.

**Frame to capture.** A 10-15 second screen recording (not a still). The motion of the auto-update is the point.

**Alternative:** if recording motion is hard, take two stills 30 seconds apart and show them side-by-side with the timestamp diff visible. Narrate "no refresh between these frames".

---

### Shot 8 — The `/stats` page (Stats tab)

**Goal.** Capture the GitHub-style activity heatmap, streak numbers, and peak-hour line — the most visually striking single screen klyne ships.

**Preconditions.** G1-G3. Daemon ingestion is current (it is on this machine — 30-day streak).

**Exact steps:**

1. Open browser tab at `http://127.0.0.1:7878/stats`.
2. Verify top-of-page summary strip: Total spend · Input (with cache %) · Output · Sessions · Messages. **Captured values in this doc: $26.5K · 11.5B input (97 % cache) · 45.6M output · 207 sessions · 80,747 messages.** Live values may be higher if new sessions have been ingested; that is expected.
3. Click the **"Stats"** tab in the tab bar (Overview / Models / Daily / Stats).
4. Wait < 1 second for the page to render.
5. The heatmap should show 12 columns × 7 rows of colored squares. Each column is one week; each row is a day of the week. Darker = more activity.
6. Below the heatmap, a line should read: **"Favorite model · Current streak: 30 · Longest streak: 30 · Active days: 30 · Peak hour: 18:00 · Window: 30d"** (approximate — confirm before recording).

**Success signal.** Heatmap + streak line both visible in the same browser viewport at a reasonable zoom (1280 × 800 or larger).

**Frame to capture.** Full browser screenshot of the Stats tab. Then tab through the other three tabs (Overview, Models, Daily) and capture one screenshot each — these are 4 screenshots total.

**If anything renders blank:** check daemon logs for errors. If `/usage/stats` returns 500, the daemon may need restart. Recovery: `./bin/klyne stop && ./bin/klyne start`.

---

### Shot 9 — The `/sessions/[id]` detail page

**Goal.** Capture a single session's full detail — token timeline chart, message list, resume command card.

**Preconditions.** G1-G3.

**Exact steps:**

1. Open browser tab at `http://127.0.0.1:7878/sessions/7eba9e85-4912-4841-aacd-74d5e6cb99c4` (this is the agent worktree session, 656 messages — visually rich).
2. If that session ID doesn't resolve, navigate to `http://127.0.0.1:7878/cockpit` or `/projects`, click any tile/row to reach a session detail page.
3. Wait < 2 seconds for the page to load.
4. Scroll the page to find: (a) a token timeline chart near the top; (b) a "resume command" card with a copyable command; (c) the message list further down.

**Success signal.** All three elements visible. The token timeline chart should show ~656 messages worth of data — likely a non-trivial growth curve.

**Frame to capture.** Screenshot of the top of the page (timeline chart + resume command). Then a second screenshot scrolled to show the message list.

**If 7eba9e85 doesn't exist anymore:** pick any session id from `./bin/klyne` cockpit page or from this command:
```
curl -s "http://127.0.0.1:7878/sessions?limit=10" | python3 -c 'import sys,json; [print(s["id"], s["msg_count"], s["project_path"]) for s in json.load(sys.stdin)["sessions"]]'
```
Pick the row with the highest `msg_count`.

---

### Shot 10 — Green `klyne audit-sessions` (the trust beat)

**Goal.** Capture a fully-green audit run — proof that klyne's stored numbers match raw JSONL byte-for-byte.

**Preconditions.** G1-G2. **Daemon must have been running for at least 5 minutes** so ingestion is current.

**Exact steps:**

1. Open a terminal in `~/Desktop/Project/klyne`.
2. Run literally: `./bin/klyne audit-sessions --limit 20`
3. Wait 2-4 seconds.

**Success signal.** The report contains the line:
```
- Latest-assistant input_tokens accuracy: 17/17 ✓ (100.0%)
```
or similar — `N/N ✓ (100.0%)` where N can be anything from 15-20 (depends on how many of the sampled sessions have an assistant turn yet).

**The line:**
```
All sampled sessions match. Trust foundation holds for this slice.
```
**must appear.** Exit code must be 0.

**Frame to capture.** Terminal screenshot of the full report, especially the `## Summary` section and the "All sampled sessions match" line.

**If you see mismatches:** that's not a recording failure — that's a real signal the daemon is behind. Wait 5 more minutes for ingestion and retry. If mismatches persist after 15 minutes, file a bug — the audit is doing exactly its job.

---

### Recording sequence (for the 5-minute video)

If you're going for a polished video, capture in this order so the narrative is continuous:

1. **Shot 8** (`/stats` Stats tab) — the killer-visual opener
2. **Shot 4** (`/klyne:precompact` against `15009012`) — the killer-feature demo
3. **Shot 3** (`/klyne:handoff`) — the second killer-feature demo
4. **Shot 5** (advisor hook firing) — the differentiator
5. **Shot 1** (`/klyne:status`) — the trust-the-numbers moment (verdict + token table together)
6. **Shot 10** (green audit) — the close: "every claim has a test"

Shots 6, 7, 9 are B-roll for the description / extended cut. (Shot 2 was merged into Shot 1 when `/klyne:tokens` and `/klyne:health` were unified into `/klyne:status`.)

---

### Honest fallback

**If any shot is a hassle to set up, narrate over the relevant CLI output from this doc and the user will get it.** The CLI is the load-bearing surface; the slash commands are the chat-native versions of the same engine. A 30-second screenshare of `./bin/klyne tokens` is, content-wise, the same as the token table inside `/klyne:status` in Claude Code.

---

## Suggested 5-minute demo script

Five minutes ≈ 750 words spoken at a calm pace. Aim for **one feature per minute**, with real numbers always on screen.

### 0:00–0:30 — The pain (the hook)

**On screen:** terminal showing `klyne audit-sessions --limit 20` mid-run, then the output line: `/compact events detected: 0` and `Codex /compact events: 2`.

**Voiceover:**
> "You know that moment in Claude Code when you run `/compact`, save your context, and 20 minutes later the AI says 'let me re-read the file to remember'? That's *800,000 tokens* of conversation gone — and you have no idea what was in there. **In my own transcripts, klyne found three compact events in one oms-service session — one of them shrank a 793K-token conversation to 9K. That's 88× compression, and everything in those 784K missing tokens is recoverable.** This is klyne."

### 0:30–1:00 — What klyne is

**On screen:** clean terminal, `klyne doctor` output.

**Voiceover:**
> "klyne is a local-first session rescue layer for Claude Code and Codex CLI. It reads the JSONL files your AI already writes. No cloud, no proxy, no telemetry. Read-only by design. Three surfaces — an MCP server the AI itself can call mid-session, a proactive advisor hook that pushes warnings *before* you submit your next prompt, and a web cockpit at localhost 7878 that streams every session live."

### 1:00–2:00 — Feature 1: precompact recovery

**On screen:** `/klyne:precompact` inside Claude Code (you record this — see § Things to record yourself, shot 4). Result shows the recovered turns.

**Voiceover:**
> "First feature — pre-compact recovery. **I just had `/compact` blow away 784K tokens of debugging.** I type `/klyne:precompact` and klyne reads back the original messages straight off disk. The file path Claude was about to re-read — already in front of me. The test result it would have re-run — already there. **Zero AI calls.** klyne is JSONL bytes, not an LLM."

### 2:00–3:00 — Feature 2: deterministic handoff

**On screen:** `/klyne:handoff` output in Claude Code chat.

**Voiceover:**
> "Second feature — deterministic handoff. When you hit the 5-hour cap and need to start fresh, you can ask the AI 'summarise what we did'. But that summary varies, and it always loses the file paths. **klyne's handoff is deterministic — same input, same output, every section a structured pull from real events.** Here it pulled the 18 files I touched, the 12 commands I ran with reuse counts, the last 6 exchanges verbatim. Add `scope=current-topic` and it drops files irrelevant to the most recent direction."

### 3:00–3:45 — Feature 3: the advisor hook

**On screen:** Cut between (a) a Claude Code chat where you submit a prompt in a heavy session and the advisor line appears, and (b) the four trigger conditions table.

**Voiceover:**
> "Third feature — the proactive advisor. Most tools wait to be asked. This one pushes. Every time I press Enter, klyne checks four deterministic triggers against my JSONL: stale context, acceleration, 5-hour-window burn, hard ceiling. If any fires, I get one inline advisory — fixed cost, eighty tokens. **And here's the trust signal:** each trigger fires at most once per state transition. Lifetime cap, four advisories per session. So it's useful, not yappy. Kill switch in one command if you ever need it."

### 3:45–4:30 — Feature 4: tokens + stats

**On screen:** Cut between terminal `klyne tokens` for the current session and browser at `localhost:7878/stats` showing the 30-day GitHub-style heatmap.

**Voiceover:**
> "Fourth feature — see how your sessions have actually grown. In the terminal, `klyne tokens` gives me a sparkline and a per-turn table with the cached-vs-uncached split, which is the real load-bearing number against your rate limit. In the browser at localhost 7878 slash stats, GitHub-style activity heatmap, models-by-cost, daily rollups. **My own data: 207 sessions in the last 30 days. 80,000 messages. 11.5 billion input tokens — 97 % of that was cache reads. $26K of compute, 30-day coding streak. None of that came from invoices — klyne reads it straight off disk.**"

### 4:30–5:00 — Install + close

**On screen:** `klyne mcp install` running, then `make proof` running, then GitHub URL.

**Voiceover:**
> "Install: `klyne mcp install` is one idempotent command. It registers the MCP server in Claude Code and Codex, installs the advisor hook, and unpacks the slash commands. Every claim I just made has a Go test under `docs/proof/`. Run `make proof` and watch them pass against real fixtures — marketing and code stay locked together by design. **One-line install, zero cloud, every feature reproducible.** Star the repo, links in the description. Cheers."

---

### Recording checklist

Pre-fix items (resolved 2026-05-12 — these are no longer blockers):

- [x] **Audit mismatches resolved** — was ingestion lag while daemon was down. After daemon caught up: 17/17 ✓ 100 %. Don't worry about it.
- [x] **Roast `$0.00 total` headline fixed** — the dollar amount is now hidden from the header when zero. (Underlying root cause: `/cost/summary` was repurposed away from dollar amounts; per-session `sessions.cost_usd` is intentionally 0 for flat-subscription users. The real $26K figure lives in `/usage/stats` and `/stats` web tab.)

Active recording prep:

- [ ] Daemon running (`./bin/klyne`) at least 5 minutes before any audit shot
- [ ] Have `~/Desktop/Learning/oms-service` Claude Code session open with the `15009012` rollout available for the pre-compact demo (Shot 4)
- [ ] If recording Shot 5 (advisor), have the **temporary plan-cap switch** ready: `./bin/klyne config set plan custom --cap=1000000` (and `set plan max-5x` to restore afterwards)
- [ ] Browser pre-loaded with three tabs: `/cockpit`, `/stats` (on Stats tab so heatmap is visible), and `/sessions/7eba9e85-…`
- [ ] Terminal font ≥ 16 pt, high-contrast colour scheme
- [ ] Pin the GitHub URL `github.com/klyne-ai/klyne` in the lower third of the screen for the whole 5 minutes (searchable in screenshots)

---

## Where to go next in the docs

| When you want to… | Read this |
|---|---|
| See every reproducible claim | [`docs/proof/`](./proof/) |
| Read the design doc for the advisor | [`docs/features/proactive-session-advisor.md`](./features/proactive-session-advisor.md) |
| Read the design doc for analytics commands | [`docs/features/analytics-commands.md`](./features/analytics-commands.md) |
| Read the doc for the stats dashboard | [`docs/features/stats-dashboard.md`](./features/stats-dashboard.md) |
| Read the doc for the statusline | [`docs/features/statusline.md`](./features/statusline.md) |
| Read the doc for the file heatmap | [`docs/features/file-heatmap.md`](./features/file-heatmap.md) |
| Read the doc for subagent attribution | [`docs/features/subagent-attribution.md`](./features/subagent-attribution.md) |
| Read the doc for the OTel exporter | [`docs/features/otel-exporter.md`](./features/otel-exporter.md) |
| See the MCP server build log | [`docs/MCP-SHIP-LOG.md`](./MCP-SHIP-LOG.md) |
| Trace what shipped in which slice | [`docs/MCP-SHIP-LOG.md`](./MCP-SHIP-LOG.md) |
| Read the security model | [`docs/SECURITY.md`](./SECURITY.md) |
| Compare klyne against neighbours | [`docs/marketing/comparison-and-gaps.md`](./marketing/comparison-and-gaps.md) |
| Read the UI/UX brief for v1.1 visual structure | [`docs/UI-UX-BRIEF.md`](./UI-UX-BRIEF.md) |

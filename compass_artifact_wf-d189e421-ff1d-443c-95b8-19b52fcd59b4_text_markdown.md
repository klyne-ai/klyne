# agentdeck — Shipping Spec v1.0

> **Status:** Implementation-ready. Single source of truth.
> **Owner:** solo dev + AI agent team.
> **License:** MIT.
> **Date:** May 6, 2026.
> **v1 scope:** Claude Code + Codex CLI only. Everything else is v2.

---

## TL;DR

- agentdeck is a **local-first mission control** for AI coding CLIs. It does not replace your CLI — you keep running `claude` and `codex` exactly as you do today. agentdeck silently watches the JSONL session files those CLIs already write to disk and gives you one clean browser UI to search them, summarize them, see costs, and recover threads after `/compact` blew away your context.
- v1 ships with **only Claude Code and Codex CLI** because that's what most users have, plus a **smart BYOK auto-selector** that detects what API key you already own and routes each internal task (summarization, thread naming, embeddings) to the best model from your existing keys — so users never juggle subscriptions.
- Stack: **Go backend** (single binary, gopsutil, fsnotify, modernc.org/sqlite v1.44.3) + **SvelteKit 5 frontend** (smallest bundle, fastest cold start), SQLite WAL + FTS5, SSE for real-time. Performance budget: <40 MB RAM idle, <50 ms p95 search, <200 ms cold-tab paint.

---

## 1. What is agentdeck (1-paragraph elevator pitch)

agentdeck is a local-first, open-source dashboard that unifies every AI coding CLI you already use — starting with Claude Code and Codex — into a single browser-based mission control. It runs as a tiny Go daemon on your machine, tails the JSONL session logs your CLIs already write, and gives you searchable summaries, cross-CLI threads, cost tracking, and recovery from `/compact` events. **You don't change your workflow.** You don't create kanban tasks. You don't describe work upfront. You keep typing `claude` and `codex` in your terminal exactly as you do today; agentdeck just makes the history of what happened across all of them visible, searchable, and useful.

---

## 2. The problem we are solving

### Concrete user pains (in the user's own words)

- **"They have just ONE subscription."** Most developers pay for Claude Pro/Max OR ChatGPT Plus, not both. Tools that demand new API keys for every internal feature are dead on arrival.
- **"Performance should not be compromised … smooth flow for the user experience."** Existing dashboards (Electron-based, heavy SPAs) feel sluggish. The bar is "feels like a native CLI."
- **"I had to create a kanban, describe the task… we want to keep it SIMPLE."** The user tried vibe-kanban (BloopAI/vibe-kanban, ~26K GitHub stars as of May 2026) and bounced because it forced an upfront task-description workflow. agentdeck's UX north star is the inverse: zero upfront friction, observe-only, the CLI is still in charge.
- **`/compact` destroys context.** Claude Code auto-compacts long sessions and Codex CLI sessions can drop on disconnect. Users lose the thread of what they were doing and what decisions got made. Recovery is manual, painful, and often impossible.
- **No cross-CLI memory.** A user might debug in Claude Code, then refactor in Codex, then come back the next day and have no unified record of "what was I working on Thursday across all my agents?"
- **Cost surprises.** Codex CLI writes token totals into JSONL but there's no built-in dashboard. Users don't know if they're about to blow their Pro quota until 429s start hitting.
- **Memory bloat.** Multiple CLIs running in parallel terminals can quietly eat several GB of RAM and the user has no visibility.

### What's already there but inadequate

- **vibe-kanban** (BloopAI/vibe-kanban, ~26K stars): forces upfront task creation; user found it confusing.
- **claude-squad / Crystal**: tmux/git-worktree orchestrators; assume you want to spawn parallel agents, not observe ones you already run.
- **claude-code-log, claude-JSONL-browser, ccusage** (ryoppippi/ccusage, 13.8K stars), **Agent Sessions** (macOS only): each solves one slice (HTML export, cost analytics) but none unifies cross-CLI live observation, search, summary, and cost in a portable cross-platform local app.

---

## 3. Why we are building it

1. **The market gap is real and shaped exactly like the user's pain.** The above tools each hit one edge of the problem; none combine "observe-only", "cross-CLI", "performant", and "BYOK-zero-friction" in one product.
2. **Sunsetting / moving competitors.** Anthropic's enforcement against third-party harnesses reusing Claude OAuth tokens — fully enforced as of **April 4, 2026, 12:00 PM PT** per Anthropic's enforcement timeline — wiped out a chunk of the third-party harness market overnight. **OpenCode pushed a commit removing support for Claude Pro/Max account keys and Claude API keys on or around February 19–20, 2026**, with the commit citing "anthropic legal requests" (The Register, Feb 20, 2026); a final cleanup PR (#18186) landed on March 19, 2026. A pure-observer tool that *never* re-uses the OAuth token is structurally safe and fills the vacuum.
3. **JSONL is a stable contract.** Both Claude Code (`~/.claude/projects/<encoded-cwd>/<session-uuid>.jsonl`) and Codex (`~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl`) write rich, append-only JSONL. They have publicly settled on this format. Building on it is durable.
4. **Solo OSS at 1K-stars scale is achievable here.** Comparable single-purpose tools (vibe-kanban 26K, ccusage 13.8K, Crystal) prove the audience and acquisition path.

---

## 4. Strategy

### Positioning (one sentence)

> "agentdeck is the unified browser dashboard for the AI coding CLIs you already run — observe-only, local-first, zero workflow change."

### Differentiators (vs. each competitor)

| Competitor | Their model | Our differentiator |
|---|---|---|
| vibe-kanban | Forces upfront task description / kanban | We never ask the user to describe work; we read JSONL passively |
| claude-squad / Crystal | Spawns and orchestrates agents (tmux / worktree) | We don't launch agents; we observe ones you already launched |
| claude-code-log | Static HTML export, single CLI | Live UI, cross-CLI, search, summaries |
| ccusage | Cost only, terminal | Cost is one panel of many; browser UI |
| Cursor / Zed agent UIs | Rebuild the editor experience | We don't touch your editor or CLI; pure overlay |

### Launch sequence

1. **Stealth alpha (Day 1–14, "v1"):** ship the 14-day plan in §15 to a private GitHub repo. Dogfood with five Claude Code + Codex power users.
2. **Public v1 launch (Day 15):** flip repo public, post on r/ClaudeAI, r/ChatGPTCoding, Hacker News (`Show HN: agentdeck — mission control for Claude Code + Codex`), and X/Twitter with a 30-second screen recording showing the 4 user flows in §6.
3. **Early traction loop (Weeks 3–6):** open "request a connector" issues for OpenCode, Cursor, Aider, Cline, Gemini CLI, GitHub Copilot CLI. PRs welcome, with a templated `Connector` interface (§11).
4. **v1.1 (Week 6–8):** memory monitoring panel, sqlite-vec semantic search opt-in, basic notifications.
5. **v2 (Quarter 2):** ship the top 2 community-requested connectors based on issue thumbs-ups.

### North-star metric for "did we win"

GitHub stars are vanity. The real metric is **weekly active local installs** (anonymized phone-home opt-in: a single ping with daemon version + OS, no PII). Target 1,000 WAU by month 3.

---

## 5. Architecture

### High-level diagram

```
┌─────────────────────────────────────────────────────────────────┐
│  USER'S MACHINE                                                  │
│                                                                  │
│  ┌───────────────────┐     ┌──────────────────┐                  │
│  │  claude (CLI)     │     │  codex (CLI)     │  ← unchanged     │
│  └─────────┬─────────┘     └─────────┬────────┘                  │
│            │ writes                  │ writes                    │
│            ▼                         ▼                           │
│  ~/.claude/projects/...      ~/.codex/sessions/.../              │
│       *.jsonl                    rollout-*.jsonl                 │
│            │                         │                           │
│            └──────────┬──────────────┘                           │
│                       │ fsnotify watch                           │
│            ┌──────────▼─────────────┐                            │
│            │  agentdeckd (Go bin)   │                            │
│            │  ┌──────────────────┐  │                            │
│            │  │ Watcher / Parser │  │                            │
│            │  │ Connector layer  │  │                            │
│            │  │ Summarizer       │──┼──► outbound: Anthropic /  │
│            │  │ Embedder         │  │     OpenAI / Gemini /      │
│            │  │ Search (FTS5)    │  │     Ollama (BYOK only)    │
│            │  │ Cost engine      │  │                            │
│            │  │ HTTP + SSE API   │  │                            │
│            │  └──────────────────┘  │                            │
│            └──────────┬─────────────┘                            │
│                       │ localhost:7878                           │
│            ┌──────────▼─────────────┐                            │
│            │ SvelteKit SPA (static) │  served by daemon          │
│            │ open in browser        │                            │
│            └────────────────────────┘                            │
│                                                                  │
│  ~/.agentdeck/agentdeck.db (SQLite WAL + FTS5)                   │
└─────────────────────────────────────────────────────────────────┘
```

### Tech choices and reasoning

#### Backend: **Go** (recommended) over Rust

- **Single static binary**, cross-compiled for darwin/linux/windows × amd64/arm64 from one CI matrix.
- `gopsutil` gives clean cross-platform process detection for the v1.1 memory panel.
- `fsnotify` handles `~/.claude` and `~/.codex` recursive watches on all three OSes.
- **`modernc.org/sqlite` v1.44.3 (April 2026)** is a pure-Go (CGO-free) SQLite — keeps the cross-compile clean, no toolchain drama.
- Go's developer velocity for a solo dev with AI assistance is meaningfully better than Rust for this kind of I/O-bound, JSON-heavy daemon. Rust would be valid for a CPU-bound product; this is not one.
- We acknowledge Rust's edge in raw performance and memory safety, but the product is bottlenecked on disk/network, not CPU. Go wins on time-to-ship.

#### Frontend: **SvelteKit 5** (recommended) over React

- Svelte 5 stable (currently `svelte@5.55+`, SvelteKit 2.57+) compiles to small, fast vanilla JS — bundles regularly come in under 50 KB gzipped for apps of this complexity, vs. ~150 KB+ for an equivalent React + Tanstack Query app.
- The user explicitly said "UI structured layout should be easy for now, I will tweak it later" — Svelte's HTML-first single-file components are the lowest-friction template for AI agents to generate and a solo dev to tweak.
- SvelteKit's static adapter outputs a folder of static assets we embed into the Go binary with `embed.FS` — single binary still ships the whole UI.
- React + Tanstack Query is acceptable if a contributor strongly prefers it; we will not block PRs that bring in a React port. But canonical v1 is Svelte.

#### Real-time: **Server-Sent Events (SSE)**, not WebSockets

- One-way (server → browser) is all we need: "new message arrived in session X", "summary done", "cost updated".
- SSE works through any HTTP server, auto-reconnects, and is ~10× simpler to implement and debug than WebSockets.
- WebSockets would only matter if we needed bidirectional real-time, which we don't — the browser writes via plain `fetch`.

#### Database: **SQLite (WAL mode) + FTS5**

- WAL = concurrent readers + single writer, perfect for our pattern (one watcher writing, many tabs reading).
- FTS5 ships with SQLite, gives us BM25 ranked full-text search out of the box.
- `sqlite-vec` is an *opt-in* extension (v1.1) for semantic search; we keep it off by default to avoid the embedding-cost question for users who don't want it.

#### PRAGMA settings (apply on every connection via a connection hook)

```sql
PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;
PRAGMA temp_store = MEMORY;
PRAGMA mmap_size = 268435456;     -- 256 MB
PRAGMA busy_timeout = 5000;
PRAGMA foreign_keys = ON;
```

We use **two `*sql.DB` handles** — one for writes (`MaxOpenConns=1`), one for reads (`MaxOpenConns=N`) — to avoid `SQLITE_BUSY` under load.

---

## 6. User flow (install → daily use)

Assumption: user already has Claude Code and/or Codex installed and has been using them.

### Flow A: Install (≤ 60 seconds, zero config)

1. `brew install agentdeck` (macOS) / `curl -fsSL agentdeck.dev/install.sh | sh` (Linux/macOS) / `winget install agentdeck` (Windows). Single binary, no Node, no Python, no Docker.
2. `agentdeck` (no args). Daemon starts, opens `http://127.0.0.1:7878` in default browser.
3. First-run wizard (4 screens, ~30 seconds total):
   - **Welcome.** "We watch your Claude Code and Codex sessions. We never modify your CLIs, never proxy your traffic, never call any external API without your explicit setup."
   - **Detection.** Daemon checks for `~/.claude/projects/`, `~/.codex/sessions/`, `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`. Shows green check / grey dash for each. (We do not read OAuth tokens — see §8.)
   - **Smart model picker** (see §8). Shows the auto-selected model for each internal task with "why this was picked" reasoning. User can accept all or override.
   - **Done.** Drops to the dashboard. agentdeck immediately back-fills history from existing JSONL files.

### Flow B: Daily use (no friction)

1. Open terminal. Run `claude` or `codex` as you always do.
2. Whenever you want to look back: hit `agentdeck` icon in menubar (macOS), or open `localhost:7878` in any browser tab. Dashboard already shows the live session you're in.
3. Search across every session you've ever run: top bar, full-text, ranked, sub-50 ms.

### Flow C: Recover from `/compact` (the killer demo)

1. You're 4 hours into a Claude Code session. `/compact` fires. Context blown.
2. In agentdeck, click the session card → **"Restore context"** button.
3. agentdeck shows the auto-generated rolling summary of everything before the compact (computed at every 50 messages and right before any detected compaction event), plus the last 20 raw messages, all formatted as a single Markdown block.
4. Click **"Copy as resume prompt"**. Paste into Claude Code. You're back in the thread, all key decisions intact.

### Flow D: Find an old thread ("what was I doing last Thursday?")

1. Top-bar search: `payment webhook race condition`.
2. Results ranked by FTS5 BM25 across all sessions, both CLIs, all projects.
3. Click result → opens session view with the matched message highlighted.
4. Click **"Open in CLI"** → copies the exact `claude --resume <session-id>` or `codex resume --last` command to the clipboard, including the correct working directory hint.

---

## 7. Data flow (JSONL → summary → thread → search)

### End-to-end pipeline

```
1. fsnotify event ──► raw event arrives at watcher (per-CLI Connector)
                  ├── Claude: parses ~/.claude/projects/<encoded-cwd>/<uuid>.jsonl line
                  └── Codex:  parses ~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl line

2. Connector normalizes ──► canonical Message{
       id, session_id, cli, project_path, role, content,
       tool_calls, tool_results, tokens_in, tokens_out,
       cost_usd, model, ts, parent_uuid
   }

3. Writer goroutine ──► INSERTs into messages + messages_fts (FTS5 trigger).
                     ──► Updates sessions table (last_msg_at, msg_count, cost_total).

4. Summarizer worker ──► every 50 messages OR on detected /compact event:
       a. fetches last 50 messages
       b. if a prior summary exists, prepends it (rolling summary)
       c. calls Smart-Model-Selected summarization model (§8)
       d. writes session_summaries(session_id, version, text, model, ts)

5. Thread builder ──► nightly job (and on-demand): clusters sessions across CLIs
       by (project_path, time-window, embedding-similarity if vec enabled)
       into Thread rows. A "thread" is a user-visible workstream, e.g.
       "Refactor billing service" — may span 4 Claude sessions and 2 Codex sessions.

6. Search ──► FTS5 BM25 over messages_fts (default).
          ──► sqlite-vec ANN over message_embeddings (opt-in, v1.1).
          ──► Results merged and re-ranked.

7. Browser ──► SSE channel /events streams: msg.new, summary.ready, session.update,
            cost.tick, thread.rebuild. Frontend stores in Svelte 5 runes-based
            caches and re-renders affected panels only.
```

### Schema (essential tables)

```sql
CREATE TABLE sessions (
  id              TEXT PRIMARY KEY,        -- UUID from CLI
  cli             TEXT NOT NULL,           -- 'claude' | 'codex'
  project_path    TEXT NOT NULL,
  encoded_cwd     TEXT,
  started_at      INTEGER NOT NULL,
  last_msg_at     INTEGER NOT NULL,
  msg_count       INTEGER DEFAULT 0,
  tokens_in       INTEGER DEFAULT 0,
  tokens_out      INTEGER DEFAULT 0,
  cost_usd        REAL    DEFAULT 0,
  model           TEXT,
  status          TEXT,                    -- 'active' | 'idle' | 'compacted'
  raw_path        TEXT NOT NULL            -- absolute path to JSONL
);

CREATE TABLE messages (
  id          TEXT PRIMARY KEY,
  session_id  TEXT NOT NULL REFERENCES sessions(id),
  parent_uuid TEXT,
  role        TEXT NOT NULL,               -- 'user'|'assistant'|'tool'|'system'
  content     TEXT NOT NULL,               -- canonical text
  tool_name   TEXT,
  tokens_in   INTEGER, tokens_out INTEGER,
  cost_usd    REAL,
  model       TEXT,
  ts          INTEGER NOT NULL
);

CREATE VIRTUAL TABLE messages_fts USING fts5(
  content, role UNINDEXED, session_id UNINDEXED, ts UNINDEXED,
  content='messages', content_rowid='rowid'
);

CREATE TABLE session_summaries (
  session_id TEXT NOT NULL,
  version    INTEGER NOT NULL,
  text       TEXT NOT NULL,
  model      TEXT NOT NULL,
  ts         INTEGER NOT NULL,
  PRIMARY KEY (session_id, version)
);

CREATE TABLE threads (
  id            TEXT PRIMARY KEY,
  title         TEXT,
  project_path  TEXT,
  first_ts      INTEGER, last_ts INTEGER
);

CREATE TABLE thread_sessions (
  thread_id  TEXT NOT NULL REFERENCES threads(id),
  session_id TEXT NOT NULL REFERENCES sessions(id),
  PRIMARY KEY (thread_id, session_id)
);

-- v1.1 (sqlite-vec):
CREATE VIRTUAL TABLE message_embeddings USING vec0(
  message_id TEXT PRIMARY KEY,
  embedding  FLOAT[768]
);
```

---

## 8. Smart model auto-selection (the BYOK matrix)

### The problem in one sentence

The user said: **"They have just ONE subscription. They don't have to think or juggle between the models."** So agentdeck must work great whether the user has only Claude Pro, only ChatGPT Plus, only a Gemini API key, or some combination.

### CRITICAL legal constraint (Anthropic, fully enforced April 4, 2026, 12:00 PM PT)

agentdeck **cannot** call Anthropic's API using the Claude Code OAuth token stored in `~/.claude`. Anthropic's Claude Code authentication documentation states verbatim:

> "OAuth authentication (used with Free, Pro, and Max plans) is intended exclusively for Claude Code and Claude.ai. Using OAuth tokens obtained through Claude Free, Pro, or Max accounts in any other product, tool, or service — including the Agent SDK — is not permitted and constitutes a violation of the Consumer Terms of Service."

Anthropic enforces this server-side; the API rejects such requests with: **"This credential is only authorized for use with Claude Code and cannot be used for other API requests."** Anthropic engineer Thariq Shihipar (Member of Technical Staff, Claude Code team) stated on the record (Feb 2026): "Third-party harnesses using Claude subscriptions create problems for users and are prohibited by our Terms of Service." Full enforcement was announced for **April 4, 2026, 12:00 PM PT**, after which subscription quotas no longer cover any third-party tool. **OpenCode pushed a commit removing Claude Pro/Max key support on or around February 19–20, 2026** ("anthropic legal requests"), with cleanup PR #18186 landing March 19, 2026.

**Therefore:** if the user has only Claude Pro/Max with no Anthropic API key, agentdeck must not "borrow" the Claude Code subscription for its own internal calls. We need an alternative path.

### OpenAI / ChatGPT Plus is currently more permissive

OpenAI's Codex CLI auth docs flag programmatic use as the recommended path for an API key but do not prohibit third-party access to `~/.codex/auth.json` the way Anthropic does, and OpenAI staff (e.g. Thibault Sottiaux) have publicly endorsed third-party harnesses using ChatGPT-account auth. Even so, **agentdeck takes the conservative, durable path: we never read `~/.codex/auth.json` either.** We rely on user-supplied API keys or a local model. This keeps us legally bulletproof on both vendors and means the next ToS update doesn't break our product.

### Auto-selection rules (executed in order; first match wins)

The daemon detects available credentials at startup and re-detects on settings save:

```
detect_keys():
  has_anthropic = env(ANTHROPIC_API_KEY) is set
  has_openai    = env(OPENAI_API_KEY) is set
  has_gemini    = env(GEMINI_API_KEY) is set
  has_ollama    = http://localhost:11434/api/tags responds 200
  // We do NOT read ~/.claude OAuth tokens — illegal per Anthropic ToS.
  // We do NOT read ~/.codex/auth.json — conservative.
```

For each internal task, we pick:

| Task | Preference order | Reasoning |
|---|---|---|
| **Per-session rolling summary** (1–4K tokens in, ~300 tokens out, runs every ~50 msgs) | 1. Gemini 2.5 Flash-Lite (free tier: 15 RPM, **1,000 RPD**, 250K TPM, 1M-token context per ai.google.dev rate-limits docs) → 2. OpenAI gpt-5-mini → 3. Anthropic claude-haiku-4 → 4. Ollama `llama3.1:8b` | Cheap, long-context. Summarization is the easy job; Gemini's free tier is generous enough to be effectively free for most users. |
| **Thread naming / titling** (300 tokens in, 20 out) | 1. Gemini Flash-Lite → 2. gpt-5-nano → 3. claude-haiku-4 → 4. Ollama `llama3.1:8b` | Tiny task; free tier is fine forever. |
| **Embeddings** (v1.1, ~512 tokens per message) | 1. OpenAI `text-embedding-3-small` → 2. Gemini `text-embedding-004` → 3. local `nomic-embed-text` via Ollama | Quality + cost. OpenAI is the de-facto bar. |
| **Optional "ask agentdeck about my history"** chat (v1.1) | Use whatever the user picked as primary; default to highest-quality available | This is rare and on-demand, so quality matters more than cost. |

### The "I only have Claude Pro" path

If the user ONLY has Claude Pro/Max (i.e. uses Claude Code via OAuth) and **no API key at all**, the wizard says clearly:

> "You're paying for Claude Pro, which agentdeck cannot legally re-use for its own internal tasks (Anthropic ToS, fully enforced April 4, 2026). Pick one:
>
> **(A) Free + recommended:** add a Google Gemini API key. Free tier with no credit card; **1,000 requests/day on Flash-Lite** is more than enough for agentdeck's summary workload (a 50-message rolling summary every ~50 msgs ≈ tens of calls/day for a heavy user). [Get key →]
>
> **(B) Fully local:** install Ollama and pull `llama3.1:8b`. Slower, lower-quality summaries, but zero cloud dependency. [Install Ollama →]
>
> **(C) Skip:** turn off summaries. agentdeck still works as a search-and-cost dashboard. You can enable later."

### Honest trade-off note (explicit per user direction)

This is a **deliberate exception to the "BYOK strict" stance** earlier research recommended. The reason: the "one-subscription user" reality makes pure-BYOK hostile. Recommending Gemini's free tier or local Ollama for a tiny set of background tasks (summarization only) preserves the spirit of BYOK — *no agentdeck-managed API keys, no proxying, no markup* — while solving the real-world friction. Embeddings remain strict-BYOK because they are opt-in v1.1.

### Per-task override UI

A settings panel shows every internal task with a dropdown of compatible models from the detected providers, plus a **"Recommended for this task"** badge on the auto-pick. Changing the dropdown immediately re-routes future calls; no daemon restart.

---

## 9. Implementation phases

### v1 (ships in 14 days, Day 1–14 of §15)

**Connectors:**
- ✅ Claude Code (read-only JSONL parser for `~/.claude/projects/<encoded-cwd>/*.jsonl`)
- ✅ Codex CLI (read-only JSONL parser for `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl`)

**Features:**
- ✅ Live session list (across both CLIs, all projects, all time)
- ✅ Session detail view with full message tree
- ✅ Cross-CLI full-text search (FTS5)
- ✅ Per-session rolling summary (smart auto-selected model)
- ✅ Cost dashboard (tokens, $ per session, per project, per day, per model)
- ✅ `/compact` recovery flow ("Restore context" → copy resume prompt)
- ✅ "Open in CLI" — copy the exact resume command
- ✅ Smart BYOK auto-selector + per-task override
- ✅ First-run wizard
- ✅ SSE live updates
- ✅ Single-binary install: brew, curl-bash, winget

### v1.1 (Week 6–8 after v1 launch)

- 🟡 Memory monitoring panel (per-CLI process RAM via gopsutil)
- 🟡 sqlite-vec semantic search (opt-in)
- 🟡 Threads view (cross-session workstream clustering)
- 🟡 Native menubar/tray app (still served from the Go binary, just adds a tray icon via systray)
- 🟡 Notifications (cost threshold, `/compact` detected, session idle)
- 🟡 "Ask agentdeck about my history" Q&A box

### v2 (Quarter 2)

- 🔵 OpenCode connector
- 🔵 Cursor agent log connector
- 🔵 Aider connector
- 🔵 Cline connector
- 🔵 Gemini CLI connector
- 🔵 GitHub Copilot CLI connector
- 🔵 Multi-machine sync (optional, encrypted, BYO storage)
- 🔵 Public connector SDK + "request a connector" GitHub issue template

**"Request a connector" pattern (community contribution path):** any user can open an issue with the connector template (CLI name, JSONL path, sample log). PRs implementing the `Connector` Go interface (§11) get merged once the v1 core is stable.

---

## 10. Tech stack with versions

### Backend

| Library | Version | Why |
|---|---|---|
| Go | 1.23+ | Stable, GOTOOLCHAIN auto-upgrades |
| `modernc.org/sqlite` | v1.44.3 (April 2026) | Pure Go, CGO-free, cross-compile |
| `github.com/fsnotify/fsnotify` | v1.7.x | File watching |
| `github.com/shirou/gopsutil/v3` | v3.x | Process detection (v1.1 memory panel) |
| `github.com/go-chi/chi/v5` | v5.x | HTTP router |
| `github.com/r3labs/sse/v2` | v2.x | SSE server |
| `github.com/spf13/cobra` | latest | CLI sub-commands (start, stop, doctor) |
| Standard `embed.FS` | stdlib | Embed the static SvelteKit build |

### Frontend

| Library | Version | Why |
|---|---|---|
| Svelte | 5.55+ | Runes, smaller bundle, faster |
| SvelteKit | 2.57+ | Static adapter, TypeScript 6.0 support |
| Vite | 7+ | Build |
| TypeScript | 6.0 | Type safety |
| `@sveltejs/adapter-static` | latest | Static output, embedded into Go binary |
| Tailwind CSS | 4.x | Quick layout (user said "I will tweak later") |
| `lucide-svelte` | latest | Icons |
| No state lib needed | — | Svelte 5 runes are enough |

### Optional / v1.1

| Library | Version | Why |
|---|---|---|
| `asg017/sqlite-vec` | latest | Vector search extension |
| `getlantern/systray` | latest | Tray icon |

---

## 11. Repo structure

```
agentdeck/
├── README.md
├── LICENSE                          # MIT
├── go.mod
├── go.sum
├── Makefile                         # build, build-ui, dev, release
├── .github/
│   ├── workflows/
│   │   ├── ci.yml                   # test, lint
│   │   └── release.yml              # goreleaser, multi-arch
│   └── ISSUE_TEMPLATE/
│       ├── bug.yml
│       ├── feature.yml
│       └── connector_request.yml    # for "request a connector"
├── cmd/
│   └── agentdeck/
│       └── main.go                  # cobra entrypoint
├── internal/
│   ├── app/
│   │   └── app.go                   # wires everything
│   ├── api/
│   │   ├── http.go                  # chi router
│   │   ├── sse.go                   # SSE hub
│   │   └── handlers/                # /sessions /search /summary /cost /settings
│   ├── connectors/
│   │   ├── connector.go             # interface
│   │   ├── claude/
│   │   │   ├── claude.go            # discover, watch, parse
│   │   │   └── parse_test.go
│   │   └── codex/
│   │       ├── codex.go
│   │       └── parse_test.go
│   ├── store/
│   │   ├── db.go                    # open, migrate, read+write *sql.DB
│   │   ├── migrations/
│   │   │   ├── 001_init.sql
│   │   │   ├── 002_fts.sql
│   │   │   └── 003_summaries.sql
│   │   ├── messages.go
│   │   ├── sessions.go
│   │   ├── summaries.go
│   │   └── search.go
│   ├── ai/
│   │   ├── selector.go              # smart BYOK auto-selection
│   │   ├── providers/
│   │   │   ├── anthropic.go
│   │   │   ├── openai.go
│   │   │   ├── gemini.go
│   │   │   └── ollama.go
│   │   └── tasks/
│   │       ├── summarize.go
│   │       ├── title.go
│   │       └── embed.go             # v1.1
│   ├── cost/
│   │   └── pricing.go               # LiteLLM-style table
│   ├── proc/
│   │   └── memory.go                # v1.1, gopsutil
│   └── config/
│       └── config.go                # ~/.agentdeck/config.toml
├── ui/
│   ├── package.json
│   ├── svelte.config.js             # adapter-static
│   ├── vite.config.ts
│   ├── src/
│   │   ├── app.html
│   │   ├── app.css                  # tailwind
│   │   ├── lib/
│   │   │   ├── api.ts               # fetch wrappers
│   │   │   ├── sse.ts               # EventSource client
│   │   │   ├── stores.svelte.ts     # runes
│   │   │   └── components/
│   │   │       ├── SessionList.svelte
│   │   │       ├── SessionView.svelte
│   │   │       ├── SearchBar.svelte
│   │   │       ├── CostPanel.svelte
│   │   │       ├── ModelPicker.svelte
│   │   │       └── RestoreContext.svelte
│   │   └── routes/
│   │       ├── +layout.svelte
│   │       ├── +page.svelte         # dashboard
│   │       ├── sessions/[id]/+page.svelte
│   │       ├── search/+page.svelte
│   │       ├── threads/+page.svelte # v1.1
│   │       └── settings/+page.svelte
│   └── build/                       # output, embedded into Go
├── docs/
│   ├── architecture.md
│   ├── connector-guide.md           # how to write a v2 connector
│   └── byok-matrix.md
├── scripts/
│   ├── install.sh
│   └── release.sh
└── examples/
    └── sample-jsonl/                # fixtures for tests
```

### `Connector` interface (the v2 contribution surface)

```go
package connectors

type Connector interface {
    Name() string                                       // "claude" | "codex" | ...
    Discover(ctx context.Context) ([]string, error)     // existing JSONL files
    Watch(ctx context.Context, events chan<- RawEvent) error
    Parse(line []byte, path string) (*Message, error)
    Pricing() PricingTable                              // for cost engine
}
```

A v2 contributor writes one Go file implementing this and one test file with a fixture. That's it.

---

## 12. Performance targets (concrete)

These are the explicit budgets that a perf regression test will fail on:

| Metric | Budget | Measurement |
|---|---|---|
| Idle RAM (daemon) | < 40 MB | `ps -o rss=` after 1 hour, no active sessions |
| Active RAM (1 live session, 5K msg DB) | < 80 MB | same, with session live |
| Cold-start to UI paint | < 200 ms | from `agentdeck` invocation to first SvelteKit paint |
| FTS5 search p95 | < 50 ms | over 100K messages, 3-token query |
| JSONL ingest throughput | ≥ 5,000 msg/sec | bulk back-fill on first run |
| Summary turnaround | < 3 s | for 50-msg window, Gemini Flash-Lite |
| Disk footprint | < 2× JSONL size | including FTS5 indexes |
| SSE latency (event → browser) | < 100 ms p95 | local loop |
| Binary size | < 25 MB | including embedded UI |

---

## 13. Open questions / risks

1. **Anthropic might tighten further.** Even though we never re-use OAuth tokens, Anthropic could in theory restrict reading `~/.claude/projects/*.jsonl`. **Mitigation:** the JSONL is the user's own data on their own disk; reading it is reading their files. We're confident this stays legal, but we'll add a config flag `claude.enabled = false` so users can opt out per-CLI.
2. **Codex JSONL format is officially "experimental."** The `experimental_resume` config flag and ongoing GitHub discussions suggest format churn is possible. **Mitigation:** keep parser tolerant (skip unknown fields, don't crash on schema drift); pin a recent format version in tests.
3. **Gemini free tier was cut once already.** Per aifreeapi.com (March 2026): "On December 7, 2025, Google reduced free tier quotas by 50–80%." Current free tier (Flash-Lite at 15 RPM / 1,000 RPD) is more than enough for our summary workload, but Google could cut again. **Mitigation:** Ollama fallback path is always one click away; communicate this in the wizard.
4. **`/compact` detection is heuristic.** Claude Code does not (as of May 2026) emit a clean "compaction event" line; we detect it by looking for the synthetic summary message + token-count drop. **Mitigation:** implement detection conservatively, label restoration as "best effort", run a synthetic regression test against fixture JSONL.
5. **macOS Gatekeeper / Windows SmartScreen** will flag an unsigned binary. **Mitigation:** budget for an Apple Developer ID ($99/yr) and code signing; document the "right-click → Open" workaround for the first release while signing is pending.
6. **Telemetry trust.** Even one anonymous ping needs to be defensible. **Mitigation:** off by default in v1; flip to opt-in prompt at v1.1 once we have a written privacy doc.
7. **SQLite single-writer bottleneck under heavy fsnotify burst** (e.g. user replays months of history). **Mitigation:** writer goroutine batches inserts in 50–200 ms windows.

---

## 14. Success metrics

| Metric | Day 14 | Month 1 | Month 3 | Month 6 |
|---|---|---|---|---|
| GitHub stars | 50 (alpha) | 300 | 1,000 | 2,500 |
| Weekly active installs (anon ping) | n/a | 100 | 1,000 | 3,000 |
| Closed bugs / week | — | 5+ | 10+ | 10+ |
| Community connector PRs merged | 0 | 0 | 1 | 3 |
| Hacker News front page | — | once | — | — |
| Release cadence | weekly patch | bi-weekly minor | monthly minor | monthly |

**The single benchmark for "did we win"**: on Day 90, can a stranger install agentdeck on a Mac, open a browser, search their last month of Claude Code + Codex history, and hit "Restore context" on a compacted session — all without reading any docs? If yes, we won.

---

## 15. 14-day shipping plan (day-by-day)

The plan assumes a solo dev driving an AI agent team (Claude Code + Codex), ~6 focused hours/day.

| Day | Backend (Go) | Frontend (Svelte) | Done when |
|---|---|---|---|
| **D1** | Repo scaffold, `cmd/klyne`, cobra root, `internal/store/db.go` with WAL pragmas + dual handles, migration 001 | `npm create svelte@latest ui`, Tailwind, app shell, dummy `+page.svelte` | `agentdeck` starts, opens browser, blank page renders |
| **D2** | `Connector` interface, Claude connector: Discover + Parse (no watch yet), unit tests against fixture JSONL | API client `lib/api.ts`, SessionList component (mock data) | `GET /sessions` returns parsed Claude sessions |
| **D3** | Claude connector: fsnotify watch + writer goroutine, FTS5 migration 002 | SessionList wired to live `/sessions` | new Claude messages appear in DB within 1 s |
| **D4** | Codex connector (Discover, Parse, Watch) — same shape as Claude | SessionView component (renders messages + tool calls) | both CLIs show in unified list |
| **D5** | `/search` endpoint with FTS5 BM25, search-test fixtures | SearchBar with debounce, results page | sub-50ms search across 10K msgs |
| **D6** | SSE hub `/events`, broadcast `msg.new` and `session.update` | `lib/sse.ts`, live-update SessionList | new messages in CLI appear in UI without refresh |
| **D7** | Cost engine + pricing table (LiteLLM-style), per-session cost rollup | CostPanel component | $ per session matches `ccusage` numbers |
| **D8** | `internal/ai/selector.go` (BYOK detection + auto-pick), Gemini provider, OpenAI provider | First-run wizard skeleton | wizard detects keys, shows recommendations |
| **D9** | Anthropic provider (API-key path only — never OAuth), Ollama provider, summarize task | ModelPicker component with "recommended" badges, settings page | summary runs end-to-end on a real session |
| **D10** | `/compact` heuristic detection, summary versioning, "Restore context" endpoint | RestoreContext modal + clipboard copy of resume prompt | demo: compact event → one-click restore |
| **D11** | "Open in CLI" command builders (`claude --resume`, `codex resume`), polish error paths, structured logging | Polish: empty states, error states, keyboard shortcuts | smoke pass on all 4 user flows in §6 |
| **D12** | Cross-platform build matrix (darwin/linux/windows × amd64/arm64), goreleaser config, install.sh, brew tap stub | Tailwind cleanup, mobile-narrow layout (just-in-case) | release artifacts produced in CI |
| **D13** | Bench harness against §12 budgets, fix top 3 hotspots; macOS code-signing dry run | README copy (§16), screenshots, 30-sec demo gif | every §12 budget green |
| **D14** | Private alpha to 5 users, fix 5 reported bugs, tag v1.0.0 | Landing page (`agentdeck.dev`) deployed via Cloudflare Pages | v1.0.0 tag pushed, install.sh works on a fresh VM |

**Day 15:** flip repo public, post on HN / Reddit / X.

---

## 16. README copy template (launch-day marketing voice)

````markdown
# agentdeck

**Mission control for the AI coding CLIs you already run.**

agentdeck is a local-first, open-source dashboard that watches your Claude Code
and Codex sessions and gives you one clean browser UI to search them,
summarize them, see costs, and recover threads after `/compact`.

It does not replace your CLI. It does not ask you to create kanban tasks.
You keep typing `claude` and `codex` in your terminal exactly as you do today.
agentdeck just makes the history of what happened across every session,
across every project, instantly searchable and useful.

## Why

If you live in AI coding CLIs, you've felt all of these:

- `/compact` blew away your context and you can't remember the plan.
- You debugged something three weeks ago and can't find which session.
- You don't know how much you've burned on tokens this month.
- You jump between Claude Code and Codex and have no unified record.

agentdeck fixes all of that, without changing your workflow.

## Install (60 seconds, zero config)

```bash
# macOS
brew install agentdeck

# Linux / macOS
curl -fsSL https://agentdeck.dev/install.sh | sh

# Windows
winget install agentdeck

# Run
agentdeck
```

That's it. Browser opens. You're in.

## What it does

- 🔎 **Cross-CLI search.** FTS5 BM25 across every message in every session,
  Claude Code and Codex, in under 50 ms.
- 📜 **Auto-summaries.** Rolling per-session summary so a `/compact` never
  loses your thread.
- 💸 **Cost dashboard.** Tokens, dollars, per session, per project, per day.
- ♻️ **Restore context.** One click after `/compact` → resume prompt on
  your clipboard.
- 🔑 **Smart BYOK.** Bring whatever key you already have (Anthropic, OpenAI,
  Gemini free tier, or local Ollama). agentdeck picks the right model for
  each internal task and tells you why.
- ⚡ **Tiny.** <25 MB single Go binary, <40 MB RAM idle. No Electron.

## What it isn't

- Not an editor. Not a CLI replacement. Not an agent orchestrator.
  We don't launch agents — we just observe yours.
- Not a SaaS. Everything stays on your machine. We don't have a server.
  We don't proxy your traffic. We don't see your code.

## Local-first by construction

agentdeck reads your CLI's own JSONL session files
(`~/.claude/projects/`, `~/.codex/sessions/`). It never re-uses Claude
Code OAuth tokens — that would violate Anthropic's Consumer Terms (fully
enforced April 4, 2026). For its own internal tasks (summaries, naming),
agentdeck uses an API key you provide, or a local Ollama model.
You stay in full control.

## v1 supports

- Claude Code (`claude`)
- OpenAI Codex CLI (`codex`)

## Coming in v2 (community PRs welcome)

OpenCode · Cursor · Aider · Cline · Gemini CLI · GitHub Copilot CLI

[Open a "request a connector" issue →](.github/ISSUE_TEMPLATE/connector_request.yml)

## License

MIT.
````

---

## 17. What we are NOT building in v1 (explicit non-goals)

- ❌ No OpenCode / Cursor / Aider / Cline / Gemini CLI / GitHub Copilot CLI connectors. (v2.)
- ❌ No re-use of Claude Code OAuth tokens. Ever.
- ❌ No re-use of `~/.codex/auth.json` ChatGPT-Plus tokens, even though it's currently more permissive — durability over expedience.
- ❌ No proxying of CLI traffic.
- ❌ No agent orchestration / spawning. We don't launch `claude` for you.
- ❌ No kanban, no task descriptions, no "describe what you want to do" upfront forms. (This was the user's explicit anti-pattern from vibe-kanban.)
- ❌ No cloud component. No login. No accounts. No telemetry server (until we have a written privacy doc).
- ❌ No memory monitoring panel. (v1.1.)
- ❌ No semantic / vector search. (v1.1, opt-in.)
- ❌ No threads view / cross-session clustering. (v1.1.)
- ❌ No notifications. (v1.1.)
- ❌ No mobile app. (Maybe never; the responsive web UI is enough.)
- ❌ No Electron, no Tauri, no native windowing. The browser is the UI.
- ❌ No editor integration. We are not VS Code; we don't even have an opinion about your editor.

---

## 18. Decision log (for AI agents implementing this)

This section locks in every decision so the agent team doesn't second-guess.

1. **Language: Go.** Final. Do not propose Rust mid-build.
2. **UI framework: SvelteKit 5 with adapter-static.** Final. React PR welcome later, not first.
3. **Database: SQLite via `modernc.org/sqlite` v1.44.3.** Final. No Postgres. No DuckDB.
4. **Real-time: SSE.** Final. No WebSockets in v1.
5. **Search: FTS5.** Final. sqlite-vec is opt-in v1.1.
6. **Cost calc: local LiteLLM-style table** baked into the binary, refreshable from a JSON file in the user's config dir.
7. **No OAuth re-use, ever.** Locked by Anthropic ToS.
8. **Default summary model: Gemini 2.5 Flash-Lite (free tier, 1,000 RPD).** Falls back per §8 matrix.
9. **v1 connectors: Claude Code + Codex CLI only.** Rejecting any v1 PR that adds a third connector.
10. **Style: Tailwind 4, defaults-only.** No design system in v1. The user said "I'll tweak it later."
11. **Telemetry: off in v1.** No phone-home until we have a privacy policy and an opt-in screen.
12. **Repo: single repo.** UI lives in `ui/`, embedded into the Go binary.
13. **Distribution: brew + curl-bash + winget on Day 14.** No Snap, no Flatpak, no Docker image in v1.

---

*This is the final shipping spec. Build it.*
# klyne positioning questions

A working doc for product/positioning questions about klyne. The core question this doc answers, for every command klyne ships: **why would a user run this instead of just asking Claude?**

The honest answer falls into one of four buckets:

| Mode | Meaning | Example |
|---|---|---|
| **Prevention** | klyne sees a degrading signal Claude can't see and warns *before* it bites | advisor hook, statusline |
| **Recovery** | the data Claude needs is gone from its context but lives in klyne's store | `/klyne:precompact`, `/klyne:handoff` after compact |
| **Audit / cross-session** | klyne sees across every session ever indexed; Claude only knows itself | `klyne top`, `klyne files`, `klyne subagents` |
| **Overlap** | Claude can plausibly do this from live context; klyne is the deterministic option | in-session handoff on a healthy short session |

A klyne feature is only worth the install if it's in the first three buckets. Anything in Overlap needs a better answer than "klyne does it too."

---

## Q1: Does `/klyne:handoff` compete with Claude-in-session?

**Realized**: 2026-05-14

**The trap**: invoked from inside the session it's summarizing, current-Claude already has the full conversation in context — real tool calls, real friction, real decisions. It can write a handoff that's often *better* than klyne's reconstruction from JSONL.

**Where klyne uniquely wins** (Claude-in-session *literally cannot* do these):

1. **Prevention** — the `UserPromptSubmit` advisor fires on four signals (stale-context drift, per-turn cost doubling, 5-hour cap at 50%/75%, fill ≥75%) and proactively recommends `/klyne:handoff scope=current` *before* compact bites. Claude has no accurate token count, no 5-hour-cap visibility, can't intercept prompts, only speaks when called.
2. **Recovery** — post-compact, fresh session, or weeks later. Current-Claude has lost what it had; klyne reads uncompacted JSONL.
3. **Audit / cross-session** — FTS5 search, session list, decisions log. Claude knows only the current session.

**Implication for framing**: the headline isn't `generate_handoff` in isolation. It's "klyne sees what Claude can't see." `generate_handoff` is the wrapper that makes that legible. Pitch order: prevention → recovery → audit → summary (the last being a tie with Claude).

**Strong vs weak demo paths**:

- ✅ Strong: advisor fires "you're at 78% of your 5-hour window" → user runs `/klyne:handoff scope=current` → fresh session → context restored.
- ✅ Strong: auto-compact happens → new session → `/klyne:handoff` → ground-truth restoration.
- ❌ Weak: healthy short session → `/klyne:handoff` → Claude could have done this from context.

---

## Per-command comparison — the deep version

Each section answers, concretely: what would Claude try if you didn't have klyne, what klyne actually does, and why the substitution fails (i.e., what klyne sees that Claude can't).

### MCP / slash commands

#### `/klyne:handoff` (`generate_handoff`)

- **What Claude could try**: summarize the live conversation directly. In a healthy short session this is fine.
- **What klyne does**: walks the session's JSONL on disk, extracts files touched / commands run / recent failures / last exchanges into a deterministic Markdown block. With `scope=current-topic`, uses per-file Jaccard relevance to drop stale context.
- **Why Claude's version fails**:
  - After auto-compact, Claude's "conversation" is the post-compact summary — the original tool calls and decisions are gone. klyne still reads the full pre-compact JSONL.
  - In a *new* session, Claude has zero memory of the old one.
  - Claude can hallucinate commit hashes; klyne reads ground truth.
  - Claude has no scope filter for topic drift; klyne's Jaccard pass strips files whose anchor no longer matches current direction.
- **Mode**: Recovery (post-compact, new session) + Prevention (when advisor recommends it).

#### `/klyne:precompact` (`get_pre_compact_context`)

- **What Claude could try**: nothing. After `/compact`, the pre-compact messages are out of Claude's context by definition.
- **What klyne does**: reads the JSONL slice from before the last `compact_boundary` marker (v2.1+ explicit marker, legacy heuristic fallback) and returns it as plain text Claude can read in a fresh turn.
- **Why Claude's version fails**: it doesn't exist. This is the canonical klyne-only feature — the part Claude *provably cannot replicate*.
- **Mode**: Recovery (strongest demo path in all of klyne).

#### `/klyne:health` (`get_context_health`)

- **What Claude could try**: eyeball its remaining context budget. The model has no exact token count of its own input — only the user's visible heuristics.
- **What klyne does**: walks the JSONL, sums token counters from `usage.input_tokens / cache_read / cache_creation` per assistant turn, computes fill ratio against the active model's context window, classifies as `healthy / drifting / risky / rescue_now`, and lists the top bloat sources (which files / tools / past turns are eating context).
- **Why Claude's version fails**:
  - Claude has no read access to the JSONL `usage` fields.
  - Claude can't enumerate "which files in your context are stale."
  - Claude's self-estimate drifts; klyne's number matches what Anthropic actually bills.
- **Mode**: Audit + Prevention input (advisor reads this verdict).

#### `/klyne:sessions` (`list_sessions`)

- **What Claude could try**: nothing. Claude can't enumerate other Claude (or Codex) sessions.
- **What klyne does**: lists every indexed session for the project (id, started_at, tool counts, model, status).
- **Why Claude's version fails**: doesn't exist — Claude has no view of sibling/sister sessions.
- **Mode**: Audit / cross-session.

#### `/klyne:tokens` (`get_token_timeline`)

- **What Claude could try**: hand-wave about cost per turn.
- **What klyne does**: per-turn input/output token series with cached vs uncached split, sparkline, and heatmap.
- **Why Claude's version fails**: Claude reads no `usage` field, so it can't tell you "your last 3 turns went 9K → 15K → 25K uncached." That's the curve that predicts cap blowout — Claude can't see the slope of its own consumption.
- **Mode**: Audit (and the data source for the advisor's acceleration trigger).

#### `remember` / `recall` (memory MCP tools)

- **What Claude could try**: write to the auto-memory dir on disk; CLAUDE.md instructions can route the right phrases there.
- **What klyne does**: indexed table in `~/.klyne/klyne.db` with project / global scoping, tag filtering, web-cockpit visibility at `/memory`. `recall` returns project-scoped AND global memories in one call; runbook variable substitution happens in the AI step that follows.
- **Why Claude's version is weaker (not "fails" — overlaps)**:
  - Claude's auto-memory dir is per-project on the AI side, but klyne adds a UI surface (the `/memory` dashboard) and a queryable store (`klyne decisions list/search`).
  - klyne's `global` scope (`project_path == ""`) means a runbook saved in one project is recallable from every other one — Claude's auto-memory is filesystem-scoped to one project dir.
  - Same chat-driven trigger phrase ("klyne remember this"), but klyne survives moving worktrees and renaming dirs because the project_path is resolved fresh on recall.
- **Mode**: Audit / cross-session — *and* this is one of the Q-marked overlap surfaces. Worth a side-by-side eval against Claude's native auto-memory (open Q4 below).

#### `record_decision` / `list_decisions` / `search_decisions`

- **What Claude could try**: same as memory — write a markdown file.
- **What klyne does**: same `decisions` table as memory, different verb pair (for the "we picked X over Y because Z" flow vs the "remember this runbook" flow). Tag-filterable, substring search, web UI.
- **Why Claude's version is weaker**: same as memory — Claude's auto-memory is unstructured markdown; klyne's is queryable + UI-visible + scopable.
- **Mode**: Audit / cross-session. Effectively a different verb pair on the same underlying value-add as `remember`/`recall`.

#### `code_review_context`

- **What Claude could try**: read the git diff and the PR description.
- **What klyne does**: reads `<project_root>/.code-review-graph/summary.json` (an *upstream* tool's output — code-review-graph) and surfaces high-risk files / recent blockers / frequent reviewers. klyne is the MCP adapter, not the producer.
- **Why Claude's version is weaker**: the diff doesn't carry historical risk signals. "this file regressed twice in the last quarter" lives in the code-review-graph data, not the diff.
- **Mode**: Audit augment. *Open question (Q5)*: is this strong enough on its own to justify the surface, given it depends on a separate tool being installed?

### Proactive / push surfaces (klyne-only by definition)

#### Proactive session advisor (`UserPromptSubmit` hook)

- **What Claude could try**: nothing. Claude only speaks when called and has no view of the 5-hour cap window, no accurate token count, and no way to intercept user prompts.
- **What klyne does**: every user-prompt submission runs `klyne advise` (<300 ms p99 on a 50 MB JSONL). It evaluates four triggers — stale-context Jaccard drift, per-turn cost doubling (3-turn vs 5-turn windows, 5K floor), 5-hour cap pressure at 50%/75%, hard ceiling fill ≥75% — and prepends a one-line advisory to the prompt. Each trigger fires at most once per session before clearing.
- **Why Claude's version fails**: it does not exist. This is the second canonical klyne-only feature (alongside `get_pre_compact_context`). Marketing should arguably lead here.
- **Mode**: Prevention.

#### Statusline (`klyne statusline`)

- **What Claude could try**: nothing. Claude can't write to Claude Code's status bar.
- **What klyne does**: single-line render — `klyne ▸ 38% ctx · 62k/160k · 5h 1%` — installed as Claude Code's `statusLine` hook. Ambient signal that lives in the user's field of view at all times.
- **Why Claude's version fails**: no surface. Claude is text-in-the-chat; the status bar is shell-owned.
- **Mode**: Prevention (ambient, non-interrupting).

### CLI analytics

#### `klyne top`

- **What Claude could try**: count tool calls in the current session.
- **What klyne does**: ranks tools across *all* sessions in the local SQLite. Filter by project, filter by time window, JSON output. Each row: call count, share, error count, distinct sessions.
- **Why Claude's version fails**: scope. "Which tool do I lean on too hard *overall*" is not a single-session question.
- **Mode**: Audit / cross-session.

#### `klyne patterns`

- **What Claude could try**: notice "we're in a loop" sometimes, but only for the current session and only if the user points it out.
- **What klyne does**: deterministic detection of three patterns across all sessions — `tight_loop` (≥5 consecutive same-tool calls), `bash_overuse` (bash share ≥40%), `low_cache_reuse` (cached_read/tokens_in <30% with ≥50K tokens). Each finding has severity + raw metric + threshold so the verdict is auditable.
- **Why Claude's version fails**: Claude has no read access to historical session metrics, no thresholds, and is famously bad at noticing its own loops in real time.
- **Mode**: Audit / cross-session.

#### `klyne roast`

- **What Claude could try**: be sardonic about the user's session if asked. But the "data" would be hallucinated.
- **What klyne does**: templated, deterministic, no-AI-call zingers interpolated with real numbers from the SQLite store. Categories: small-sample, spend, cache reuse, Bash share, tight loop, monoculture.
- **Why Claude's version fails**: trust. A Claude-generated roast can invent numbers; klyne's roast can show its work — every percent is computed from a column.
- **Mode**: Audit (entertainment-shaped, but the determinism is the point).

#### `klyne files`

- **What Claude could try**: grep the current session for `file_path` mentions.
- **What klyne does**: extracts the path argument from every tool call across all sessions (`Read`, `Edit`, `Write`, `Glob`, `Grep`, `MultiEdit`, `NotebookEdit`, `apply_patch`, plus a generic path heuristic), aggregates Reads / Edits / Writes / sessions touched / last-touched.
- **Why Claude's version fails**: it's a single-session view at best. "Which file in this repo do I edit the most often across all my AI sessions" is a fundamentally cross-session question. The real-data example in the docs (`discountEngineService.js` — 51 reads, 60 edits, 8 sessions, 13d ago) cannot come from Claude.
- **Mode**: Audit / cross-session.

#### `klyne subagents`

- **What Claude could try**: nothing useful. The parent session's cost number explicitly *excludes* subagent spend — the cost engine only sees the Task tool's final result, not the subagent's internal conversation.
- **What klyne does**: reads `~/.claude/projects/<project>/<parent-session-id>/subagents/agent-*.jsonl` directly and rolls up tokens-in / tokens-out / cache% per parent session.
- **Why Claude's version fails**: structural. The data Claude sees in its parent-session `usage` field doesn't include subagent spend at all. The maintainer's own transcripts found 134 subagents across 5 parents totaling 555M tokens — hidden from the headline cost number until klyne surfaced it. This is one of the more compelling "klyne sees what Claude can't" stories.
- **Mode**: Audit / cross-session. Tells the user about money they didn't know they were spending.

#### `klyne audit-sessions`

- **What Claude could try**: nothing — Claude cannot read its own JSONL on disk.
- **What klyne does**: re-derives ground truth from the most recently modified transcripts and compares against klyne's own stored values. Currently checks latest-assistant `input_tokens` accuracy (the bug behind the original Opus 4.7 1M context display issue).
- **Why Claude's version fails**: it does not exist. This is the *trust foundation* — every other klyne number is only credible if `audit-sessions` passes.
- **Mode**: Audit (self-trust).

#### `klyne otel emit`

- **What Claude could try**: nothing.
- **What klyne does**: writes one OTel-shaped span per assistant message (gen_ai.* + klyne.* attributes) to a file, ready to upload to a collector. Opt-in, file-only, no network in the default path.
- **Why Claude's version fails**: not an AI-shaped problem. This is a "feed klyne's view into your obs stack" surface.
- **Mode**: Audit (export).

### Web cockpit

#### Work / Memory / Insights tabs at `http://127.0.0.1:7878`

- **What Claude could try**: nothing — Claude has no UI surface.
- **What klyne does**: local web app served by the daemon. Work tab is live terminal aggregation; Memory tab is the dashboard for `remember` / `decisions`; Insights tab is per-session deep dives.
- **Why Claude's version fails**: not a chat-shaped surface. The cockpit is for browsing, scrubbing, deleting — operations that don't fit a chat turn.
- **Mode**: Audit + Prevention (ambient).

#### Stats dashboard (`/stats`)

- **What Claude could try**: nothing.
- **What klyne does**: Overview / Models / Daily / Stats panes + per-session activity heatmap.
- **Why Claude's version fails**: no surface; no cross-session view.
- **Mode**: Audit.

### Infrastructure commands (not user-facing analysis surfaces)

These exist to make klyne work; they don't have a Claude-comparison story and shouldn't be marketed against one.

- `klyne mcp install` / `klyne mcp` — server registration + stdio runtime.
- `klyne start` / `klyne stop` — daemon lifecycle.
- `klyne advise` — hook entry (the analysis surface is the advisor itself, covered above).
- `klyne doctor` — diagnostic dump for support.
- `klyne eval` — internal eval suite for the classifier/advisor.
- `klyne config` — config CRUD (plan tier for the 5-hour-window trigger).
- `klyne completion` — shell completion script.

---

## Open questions to resolve

- **Q2**: Should `/klyne:handoff` invoked from a healthy short session refuse to run and instead say "Claude can do this from context — try me when context is past 50% or after compact"? Self-deprecating but honest. Routes the user toward the strong demo path.
- **Q3**: The advisor → handoff loop is the strongest narrative. Is the install flow persuasive about it? Per the spec, `klyne mcp install` prints a one-line confirmation — but a user who never hits a trigger never sees the advisor work. Should the install run a sample dry-firing on the user's most recent risky session as proof?
- **Q4**: For `remember` / `recall` and decisions, Claude's native auto-memory now overlaps. What's the differentiator that survives a side-by-side eval? Hypotheses: structured query, project/global scoping, web-cockpit dashboard, decision-vs-memory typing, survives-worktree-moves. Needs a worked head-to-head test.
- **Q5**: `code_review_context` depends on `.code-review-graph/` being installed. Is the dependency worth the surface? Or should klyne ship its own cheap risk signal (file-churn from `klyne files`, error rates from `klyne top`) so the surface stands alone?
- **Q6**: Should the marketing copy lead with the advisor (klyne-only, provably) instead of handoff (looks like a Claude feature, has to be defended)? My current bias: yes — the advisor is the part Claude can't replicate, and pre-compact recovery is the close. Handoff is the wrapper, not the headline.
- **Q7**: Is `klyne subagents` underused in the pitch? "555M tokens hidden from your bill until klyne showed them to you" is a stronger story than most of the handoff demos. Worth promoting from analytics-CLI to a first-class cockpit surface?

---

## How to add a new entry

When a new positioning question surfaces in a session:

1. Add a `Q{n}: ...` section near the top with date, the trap (what's tempting to assume), the realization (the case where klyne actually wins), and the framing implication.
2. If it's about a new command/surface, add a deep section in the "Per-command comparison" block: what Claude could try, what klyne does, why Claude's version fails, mode.
3. If it's an open question, add it to the bottom list with enough context that a future reader (or future-you) can act on it.

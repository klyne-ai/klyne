# klyne MCP — Ship Log

**Build date:** 2026-05-09 (slice 7: proactive session advisor)
**Branch:** `init` (klyne-ai/klyne)
**Server version advertised over MCP:** `klyne v0.4.0`

## Slice 7 — Proactive session advisor

The first slice that makes klyne *push* its verdict instead of waiting
to be asked. After installation, `klyne advise` runs as Claude Code's
`UserPromptSubmit` hook on every prompt; when one of four
deterministic triggers crosses its threshold, klyne injects a single
one-line advisory into the AI's context — no AI calls, no heuristics
that could misfire, no token cost beyond the ~60-100-token line itself.

**Triggers (OR'd, transition rule applies to all four):**

  - **Stale-context** — per-file relevance score (Jaccard between
    each file's anchor of nearby user messages and the user's last
    five user-message bag-of-words). Fires when `stale_bytes /
    total_bytes > 0.5` AND total loaded ≥ 8 KB. The advisory names
    the relevant subset so the user knows which files to keep.
  - **Acceleration** — uncached input delta per assistant turn:
    fires when last-3-turn mean exceeds 2× the preceding-5-turn
    mean AND latest delta ≥ 5K. Direction-only — no "exhaust in N
    turns" projection in v1.
  - **Hard ceiling** — fill ≥ 75% (reuses existing classifier).
  - **5-hour window** — sum of (`TokensIn - CachedReadTokens`)
    across every Claude + Codex session under the user's home dir
    in the last five hours, divided by user-configured plan cap.
    Fires at 50% and 75% thresholds. Skipped silently when the
    user has not configured a plan tier.

**Auto-install** — `klyne mcp install` now also wires the
UserPromptSubmit hook into `~/.claude/settings.json`. Idempotent:
re-runs detect a stale binary path and rewrite the entry without
touching unrelated hooks.

**Scoped handoff** — `generate_handoff` accepts an optional
`scope=current-topic` argument. The scoped variant filters the
"Files touched" section to non-stale files only and bumps the
recent-exchanges window from 6 to 10 so the new session inherits
the freshly-shifted direction.

**New CLI surface:**

  - `klyne advise` — hook entrypoint. Reads stdin (Claude Code's
    UserPromptSubmit JSON payload), resolves the active session via
    cwd, runs the four triggers, emits a single-line JSON hook
    output or stays silent.
  - `klyne config set plan {pro|max-5x|max-20x|team|custom}` —
    drives the 5-hour-window denominator. Also `klyne config get
    plan` and `klyne config show`.

**Honest caveats surfaced in product:**

  - The 5-hour cap is user-configured, not API-derived. Anthropic
    does not expose remaining-window budget, so we measure
    consumption directly and divide by the configured tier
    (defaults calibrated against publicly reported community
    numbers).
  - The acceleration advisory says "trending toward your cap" —
    never "you'll hit it in N turns." A specific projection waits
    for v2 with an eval suite behind it.

**Proof under `docs/proof/03-advisor/`** — four assertions:
stale-context fires on topic shift and names the relevant subset;
acceleration fires on doubling and points at the scoped handoff;
five-hour-urgent fires at 75% with the consumption number; the
transition rule fires exactly once, clears when the condition
goes away, and re-fires on re-trip.

**File changes:**

  - `internal/contexthealth/relevance.go` — per-file Jaccard scoring
  - `internal/contexthealth/acceleration.go` — uncached-delta detector
  - `internal/contexthealth/five_hour_window.go` — cross-session aggregator
  - `internal/contexthealth/transition.go` — fire-once state cache
  - `internal/contexthealth/advisor.go` — priority-ordered renderer
  - `internal/config/plan.go` — plan tier enum + cap lookup
  - `internal/mcpserver/hook_install.go` — UserPromptSubmit hook installer
  - `internal/mcpserver/handoff.go` — `scope=current-topic` rendering
  - `cmd/klyne/advise.go` — hook subprocess
  - `cmd/klyne/config.go` — config subcommand
  - `docs/features/proactive-session-advisor.md` — design spec
  - `docs/proof/03-advisor/` — reproducible proof


## Slice 6 — Reproducible proof + README rewrite + integration bug fixes

The first slice that makes klyne's *marketing* defensible. Every
public claim now has a fixture, a Go test, and a `claim.md` with a
side-by-side comparison against what vanilla Claude returns.

**`docs/proof/` directory** — `make proof` runs every test under
this tree. Two scenarios:

  - **`01-compact-recovery/`** — Synthetic Claude session about a
    bank-SMS regex bug. 12 pre-compact messages + `/compact` + 2
    post-compact messages. Tests assert (a) Klyne's
    `get_pre_compact_context` recovers all 12 pre-compact messages
    including high-signal strings (file paths, the user-verified
    `XX1234` mask, the exact npm command, the test result) and (b)
    those same strings do NOT appear in any post-compact line —
    confirming the AI's view, post-compact, has lost them. **1165
    bytes of conversational context recovered per fixture run** is
    surfaced in `t.Logf` so users can see the magnitude.

  - **`02-handoff-equivalence/`** — Synthetic webhook-retry session
    paused mid-task. Tests assert (a) every handoff section header
    is present and populated from fixture content, (b) rendering
    twice produces byte-identical output (deterministic), (c) files
    iterated on are marked with `(×N)` so the new session knows
    which file is central. The verbatim handoff Markdown is captured
    in `dump_test.go` and pasted into `claim.md` as the exact
    side-by-side against vanilla Claude's likely output.

**Public API addition** — `mcpserver.RenderHandoff(snap)` exported
so external proof tests can render a handoff from a snapshot
without going through the MCP request flow. Internal call sites
unchanged.

**Pre-existing bug fixes during integration:**

  - `TestCost_Summary_DefaultGroup` was checking for `model` but the
    handler in `cost.go` is documented to default to `day`, and the
    UI explicitly passes `group=day`. The test was the bug.
  - `TestMigrationsApply` expected 5 migrations; 006/007/008 had
    landed without the test being extended.
  - `TestColdStart` failed once because `bin/klyne` was a stale
    pre-rebrand binary; rebuilt fresh and 562ms cold-start is now
    reproducible.

**README rewrite** — restructured to lead with the two killer
scenarios that match the project's real value prop:

  1. *"After /compact, your AI has lost context. Klyne recovers it."*
     → links directly to `docs/proof/01-compact-recovery/`
  2. *"Vanilla Claude's summary is variable; Klyne's handoff is
     deterministic."*
     → links directly to `docs/proof/02-handoff-equivalence/`

Plus: tools/prompts tables now include `search_messages` and the
five `/mcp__klyne__*` slash commands; install section covers
`klyne mcp install` with auto-detect; "What klyne does NOT claim" section to prevent overreach;
roadmap reflects Codex parity, search, and proof artifacts all
shipped.

**End-to-end smoke verified:**

  - `make proof` → both scenarios green in <2s
  - `make build` → fresh binary at `bin/klyne`
  - Fresh install against temp HOME → writes klyne entries to both
    Claude and Codex configs; preserves unrelated servers

**Per-tool proof coverage matrix:**

| Tool | Proof |
|---|---|
| `get_pre_compact_context` | ✅ `docs/proof/01-compact-recovery/` |
| `generate_handoff` | ✅ `docs/proof/02-handoff-equivalence/` |
| `list_sessions` | (Covered by `internal/mcpserver/sessions_test.go`) |
| `get_context_health` | (Covered by `internal/contexthealth/*_test.go`) |
| `search_messages` | (Covered by `internal/mcpserver/tool_search_test.go` with `httptest`) |

The two README-headlined claims have proof under `docs/proof/`. The
remaining tools have unit tests in their owning packages but are
not currently called out in the README hero, so they don't need a
parallel `docs/proof/` artifact yet. If their claims are ever
elevated to README hero status, a `docs/proof/` companion lands
with that change — by convention.



## Slice 5 — Cross-session full-text search

Triggered by real user pain (2026-05-08): user remembered fixing a
bank-SMS parser bug "a couple days ago" but couldn't locate the
session. The daemon had been indexing every message (FTS5 + BM25 in
`internal/store/search.go` since W1) but the MCP surface didn't
expose search, so Claude Code couldn't reach it from inside the
editor.

Closes that gap with one tool + one slash prompt.

**`search_messages` tool** (`internal/mcpserver/tool_search.go`)
- Calls the daemon's existing `GET /search?q=&limit=&sort=` endpoint
  (no schema duplication; same hits the web UI shows)
- Inputs: `query` (required), `limit` (default 10, max 200), `sort`
  ("recent" default, or "relevance" for BM25 best-match), and
  optional `project_path` for client-side repo scoping
- Outputs: hits with session_id, project_path, cli, role, snippet
  (FTS-highlighted), score, ts; plus took_ms and a `daemon_down`
  flag
- 5-second HTTP timeout — never hang the AI on a stuck daemon
- Honest "daemon down" path: connection-refused or timeout returns
  `{daemon_down: true, reason: "Could not reach the klyne daemon
  at <url> — start it with \`klyne\` and try again."}` instead
  of a generic Go error

**`/mcp__klyne__search` slash prompt** (parallel to the tool)
- Accepts `query` (required), `limit`, `sort`, `project_path`
- Renders hits as a Markdown list with short session ids,
  timestamps, and snippets — the AI can pick a session and call
  `get_pre_compact_context` or `generate_handoff` on it next
- Daemon-down case shown as a plain message, not a stack trace

**Daemon URL discovery**
- Default: `http://127.0.0.1:7878` (matches
  `internal/config/schema.go:87`)
- Override: `KLYNE_BASE_URL` env var — useful for non-default
  ports or remote daemons

**Important caveat**: this is the only MCP tool with a daemon
dependency. The other five (`list_sessions`, `get_context_health`,
`generate_handoff`, `get_pre_compact_context`, install) read JSONL
or local config directly. Search needs SQLite + FTS, which only the
daemon owns. The tool surfaces this clearly to the AI rather than
pretending it's optional.

**Tests:** 7 new test cases covering happy path, empty query,
daemon-down, project_path filter, limit/sort forwarding, and prompt
rendering with both populated hits and daemon-down. Full mcpserver
suite race-clean.

**Per-CLI feature matrix after slice 5:**

| Capability | Status |
|---|---|
| Cross-session full-text search | ✓ via daemon FTS |
| Daemon-required indication | ✓ explicit `daemon_down` flag |
| Slash prompt UX | ✓ `/mcp__klyne__search` |

## Slice 4 — Codex parity, slash prompts, install command

## Slice 4 — Codex parity, slash prompts, install command

Three parallel additions that make the MCP server work for the user's
full workflow, not just Claude Code:

**1. Codex parity for all four tools.** `list_sessions`,
`get_context_health`, `generate_handoff`, and
`get_pre_compact_context` all now resolve and parse Codex transcripts
under `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl`.

- New `codexLocator` walks Codex's date-partitioned storage, reads
  each rollout's `session_meta.payload.cwd`, and returns matches via
  the same "nearest-ancestor wins" semantic Claude uses.
- `SessionCandidate.CLI` field added so downstream code dispatches to
  the right parser. `ListSessionsForCWD` now merges Claude + Codex
  candidates by mtime.
- `LoadSnapshot` switches on `CLIForPath`, calling
  `codex.Connector.Parse` for Codex paths. ContextFillPct uses
  `audit.LatestCodexTokens` (already cache-inclusive per OpenAI's
  API).
- `findSessionByID` walks both storage roots; Codex's lookup matches
  the `rollout-<id>` filename convention then falls back to the
  embedded `session_meta.id`.

**2. `get_pre_compact_context` Codex via `replacement_history`.**
Initial scope assumed Codex didn't expose compact events — wrong.
`type:"compacted"` lines exist and are *richer* than Claude's: the
event embeds the full pre-compaction conversation in
`payload.replacement_history`. We decode it directly instead of
scanning the file backward.

- Honest gaps: Codex doesn't expose `pre_tokens` or `trigger`
  (manual/auto). The output reflects that with empty fields, not
  zeros pretending to be real values.
- Same `limit` semantics as the Claude path: keep the most recent N.

**3. Live MCP prompts surfaced in Claude Code's `/` menu.**
Each of the four tools now has a parallel prompt that runs the same
business logic and returns the result as a user-message injection —
no AI roundtrip needed for the fetch.

- `/mcp__klyne__health` → live `get_context_health`
- `/mcp__klyne__sessions` → live `list_sessions`
- `/mcp__klyne__handoff` → live `generate_handoff`
- `/mcp__klyne__precompact` → live `get_pre_compact_context`

All accept an optional `cwd` argument; default is the MCP
subprocess's cwd.

**4. `klyne mcp install` command.** Auto-detects which host
configs exist and writes (or updates) the klyne entry idempotently.

- Claude: `~/.claude.json` → `mcpServers.klyne = {command, args}`
- Codex:  `~/.codex/config.toml` → `[mcp_servers.klyne]` (key
  format verified against OpenAI's published Codex MCP docs)
- `--platform claude|codex|all` overrides auto-detect.
- Idempotent: re-running after `go install` reports
  `already-installed` and does not touch the file.
- Outcome reporting: `<platform>: <added|updated|already-installed>
  — <path>` per host.

**Tests:** all four tools have dedicated Codex test fixtures; install
has full add/update/idempotent coverage for both file formats. 60+
new test cases; full mcpserver suite race-clean.

**Per-CLI feature matrix after slice 4:**

| Capability | Claude | Codex |
|---|---|---|
| `list_sessions` discovery | ✓ encoded-cwd dir | ✓ scan + meta read |
| Disambiguation (single-active rule) | ✓ | ✓ |
| `get_context_health` (verdict + bloat) | ✓ | ✓ |
| `generate_handoff` Markdown | ✓ | ✓ |
| `get_pre_compact_context` recovery | ✓ scan backward | ✓ replacement_history |
| Pre-compact `pre_tokens` quantification | ✓ | ✗ (Codex doesn't expose) |
| Pre-compact trigger (manual/auto) | ✓ | ✗ (Codex doesn't distinguish) |
| Slash prompt UX (`/mcp__klyne__*`) | ✓ | ✓ |
| `klyne mcp install` | ✓ JSON merge | ✓ TOML merge |

## Slice 3 patch — Read vs Edit distinction in bloat scorecard

Triggered by real-user feedback during testing (2026-05-08): the bloat
scorecard reported "plan-config.server.ts reads × 10" and
"review_plan_container.tsx reads × 21" for a long refactor session,
which read as "the AI keeps re-reading these files because it forgot."
Independent verification of the JSONL showed the actual breakdown was
3 Reads + 7 Edits and 5 Reads + 16 Edits — the bulk was iterative
refactor work, not context loss.

The classifier was treating Edit/MultiEdit as if they were Read calls:
- Bloat label said "X reads" when most operations were Edits.
- The "same file read 5+ times → rescue" rule was firing on Edit
  repetition, falsely escalating refactor sessions.

Fix:
- New `BloatKindFileEdit` and `BloatKindFileMixed` kinds.
- `BloatRow.ReadCount` and `BloatRow.EditCount` separate fields so
  consumers see the breakdown.
- Labels read like `Read+Edit foo.go (3 reads, 7 edits)` instead of
  the unhelpful `Read foo.go`.
- Rescue rule now counts only Read calls. Edit repetition no longer
  triggers rescue.
- Edit-call payloads (`old_string + new_string` bytes) now contribute
  to total context bytes — earlier this was silently undercounted
  because Edit tool-result bodies are tiny.

5 new tests cover the fix; 35/35 contexthealth tests green.

This document is the deliberate, honest record of everything in the
klyne MCP server as it stands at the end of slice 2. It exists so
the next person to look at this code (or you, six months from now)
knows exactly what was built, what was deferred, and what the known
trust boundaries are.

If you are about to launch this, read every section. The "Limitations"
sections are not embarrassments to skim — they are the things that
will surprise users in the wild.

---

## What shipped

### 1. Trust audit foundation (`klyne audit-sessions`)

A separate CLI subcommand that re-derives ground truth from raw JSONL
and compares against klyne's stored values. Walks the user's real
`~/.claude/projects` and `~/.codex/sessions`. Designed to be run
before believing any MCP tool answer.

- **Claude side:** verified at 100% accuracy across 188 real sessions
  (180/180 of those with assistant turns) for the latest-assistant
  cache-aware token total — the number that drives the
  `context_fill_pct` MCP responses.
- **Codex side:** ground-truth extraction works (50 sessions audited),
  but DB comparison is explicitly deferred — see Limitations.
- **Compact-event detection:** Claude (`subtype: "compact_boundary"`)
  surfaces full metadata including `preTokens`. Codex
  (`type: "compacted"`) is detectable but carries no pre-token count.

### 2. Context Health classifier (`internal/contexthealth`)

Pure-Go deterministic state machine. Inputs: session metadata + recent
N messages + cache-aware fill percentage. Outputs: state (healthy /
drifting / risky / rescue_now), action (continue / compact /
start_fresh), single-sentence reason, top-5 bloat scorecard, and the
raw signals it used.

- 18 unit tests covering every rule branch + bloat aggregation.
- No I/O, no AI, no DB. Reusable by any caller.
- Rule thresholds: `>= 75% fill OR same-file ≥5 reads OR same-cmd ≥4
  failures OR (topic-shifted AND >= 40% fill)` → rescue. `>= 50% OR
  3-4 file reads OR ≥3:1 hidden ratio` → risky. `>= 30%` → drifting.

### 3. MCP server with 4 tools (`internal/mcpserver` + `klyne mcp`)

Stdio transport, official `modelcontextprotocol/go-sdk` v1.6.

| Tool | Purpose | Disambiguation aware |
|---|---|---|
| `list_sessions` | Enumerate sessions in cwd's project, with previews + active flag | n/a (always returns the list) |
| `get_context_health` | Classify a session's health, return verdict + bloat | yes |
| `generate_handoff` | Produce a deterministic Markdown rescue prompt | yes |
| `get_pre_compact_context` | Recover messages before the last `/compact` event | yes |

**24 unit tests + manual end-to-end smoke** verified against this very
session as the test fixture (the tools diagnosed the conversation that
was building them). All passing.

### 4. Disambiguation infrastructure

Multi-session-per-project is normal usage. The MCP tools handle it:

- `ListSessionsForCWD` walks up from cwd looking for matching project
  directories, returning every Claude Code transcript with session id,
  first-user-message preview (200 chars), msg count, mtime, and an
  `is_active` flag (mtime within last 30 seconds).
- `PickActiveSession` rule: single candidate → use it; multiple
  candidates with exactly one active → use that one; otherwise →
  return ambiguous + candidate list, AI must call again with explicit
  `session_id`.

---

## Architecture decisions, named

These are choices that shape everything downstream. Each was made
deliberately and is worth understanding before changing.

1. **Official `modelcontextprotocol/go-sdk` v1.6** over rolling our
   own or using `mark3labs/mcp-go`. Reasons: maintained jointly by
   Anthropic and Google, auto-derives JSON schemas from struct tags,
   tracks the latest 2025-11-25 spec version. Cost: forced Go bump
   from 1.21 to 1.25 (minimum SDK requirement).

2. **JSONL-direct, not SQLite-backed** for MCP data plane. The MCP
   server is a self-contained subprocess Claude Code spawns. Reading
   the live JSONL means tools see the conversation as it stands at
   the moment of the call, not whatever the daemon last ingested.
   Cost: re-scans on every call (no caching yet).

3. **One tool surface, four tools.** Could have been three. Could
   have been six. The `list_sessions` + `get_context_health` +
   `generate_handoff` + `get_pre_compact_context` set is the minimum
   that lets the AI: discover sessions, diagnose them, recover from
   them, and pull data the AI cannot otherwise see (the pre-compact
   recovery is the only tool that exposes information `/compact`
   itself destroyed).

4. **Stdout discipline enforced at the entry point.** All `log.*`
   redirected to stderr in `cmd/klyne/mcp.go:69-71`. Any future
   tool that writes to `os.Stdout` directly will silently corrupt the
   JSON-RPC framing. There is no automated check for this — code
   review is the guard.

5. **Disambiguation surfaces ambiguity rather than guessing.** If
   the resolver cannot pick a unique session, every tool returns
   `Ambiguous: true` plus the candidate list. The AI is expected to
   pick by matching session previews against its own conversation
   memory. We never silently pick "newest one" when there's a real
   choice to make.

---

## Known limitations

### Limitations of the v1 product (will surprise users)

1. ~~**Claude Code only.**~~ **RESOLVED in Slice 4.** All MCP tools
   now support both Claude Code and Codex sessions.

2. **No way to authoritatively know "which session am I in."**
   Claude Code does not pass session id to MCP subprocesses. The
   resolver picks based on cwd + active-mtime. If the user has two
   parallel sessions both writing within the last 30 seconds (real
   for power-users), the tool returns ambiguous and asks the AI to
   pick. The AI usually can — it knows from its own context what it
   has been doing — but it's a "hint, please pick" affordance, not a
   guarantee.

3. **Pre-compact recovery is best-effort.** The tool returns the
   most-recent N messages before the last `compact_boundary` line.
   If the session has been compacted multiple times, only the LAST
   boundary's pre-context is recovered. Earlier compactions' data is
   silently inaccessible. (The data is in the JSONL — the tool just
   doesn't expose it. Could be added if needed.)

4. **Compact pre-token counts are Claude-only.** Claude Code records
   `compactMetadata.preTokens` in every compact event. Codex's
   `type: "compacted"` envelope carries no equivalent. So
   `get_pre_compact_context` could return Codex messages but cannot
   say "you lost X tokens in this compact" — the launch claim "16M
   tokens compacted away" is a Claude-only honest number.

5. **Bloat scorecard groups by literal command stem and file path.**
   `npm test` and `npm test --silent` collapse to `npm test`. But
   `pwd && rg --files` collapses to `pwd`, which is uninformative.
   The two-token stem heuristic is conservative — works for most
   cases, fails for compound shell expressions.

5a. **Bloat scorecard rescue triggers vs. lifetime accumulation.**
    The bloat scorecard sums Read/Edit calls across the WHOLE
    session, but the rescue/risky thresholds (e.g. "same file read
    5+ times → rescue") only inspect the LAST 20 messages. A file
    with 6 reads spread across a 600-message session shows in the
    bloat list but does NOT trigger the rescue rule, because the
    repetition is not recent. This is intentional — the rule fires
    on "the AI is currently lost," not "the AI has ever been lost"
    — but the label can mislead users into thinking the bloat list
    drives the rescue verdict directly. It does not; the bloat list
    is descriptive, not prescriptive.

6. **No caching across MCP calls.** Each `get_context_health` re-reads
   the JSONL from scratch. For a 600-message session: ~50ms.
   For a 4000+ message session (your `ccf1c911`): ~300ms. Acceptable
   for v1; cache by `(path, mtime)` if it becomes annoying.

7. **The classifier's repetition thresholds (3 / 5 / 4) are
   guesses, not eval-validated.** Real bloat in your data goes well
   past these (`discountEngineService.js` re-read 231 times in one
   session). The classifier likely under-fires "rescue" on heavy
   sessions. Tuning requires a labelled fixture set we have not
   built yet.

8. **No "Healthy" sub-states.** A short toxic session ending at 8%
   fill is labeled Healthy. The classifier measures volume, not
   quality. A session that produced wrong code in three turns is
   structurally indistinguishable from a productive 10-turn one.
   Unlikely we'll fix this — it requires semantic analysis the
   deterministic path can't do.

### Limitations of the trust foundation (will surprise developers)

9. **Codex DB comparison deferred.** The audit reports JSONL ground
   truth for Codex but does NOT compare against klyne's SQLite
   store. Codex's connector stores per-turn token deltas as System
   messages, not as latest-assistant rows; comparing requires
   aggregating those deltas. Until the comparison ships, klyne
   could silently store wrong Codex token totals and the audit
   wouldn't catch it. (The MCP server reads JSONL directly so this
   is invisible to the MCP user — but anyone using
   `/sessions/{id}/usage` HTTP endpoint for Codex is exposed.)

10. **The audit walks JSONL twice for compact stats.** The first
    pass finds the boundary line index; the second pass parses the
    pre-compact messages. Acceptable for tools running on demand;
    not OK if we ever batch-process every session.

11. **Pre-existing test failures are unrelated and untouched.**
    `TestCost_Summary_DefaultGroup` and `TestMigrationsApply` were
    already broken before any of this work; they remain broken.
    Neither is in klyne's hot path or in the MCP server's
    dependency graph.

### Limitations of the install / packaging (will surprise users)

12. **Single binary, single user.** No `--update` command, no
    auto-update. The user must rebuild + reinstall to upgrade.
    Versioning is hard-coded in `internal/mcpserver/server.go:7`.

13. **Daemon and MCP server are separate processes** that can each
    run independently. They share the same SQLite when both run, but
    the MCP server doesn't depend on the daemon. Opening the web UI
    (the daemon) does NOT magically make MCP smarter; the data plane
    is JSONL-direct either way.

14. **No telemetry.** Every tool call is silent. We do not know
    which tools are useful, which arguments are common, or which
    sessions are queried. This is by design (privacy / trust) but
    it means we have zero data on actual usage patterns once
    installed.

---

## Pre-existing trust observations from your data

Captured from the audit run on 2026-05-08 against your live
~/.claude/projects (188 sessions, 31-day window):

- **100% accuracy** on latest-assistant cache-aware token totals
  (180/180 sessions with assistant turns).
- **27/182 sessions (14.8%)** have ever been `/compact`'d. The
  rescue product applies to 1 in 7 sessions, not most.
- **30.4% of compacts (14/46) were AUTO** — the user did not invoke
  /compact themselves. These are the high-value "you didn't choose
  to lose this" rescue moments.
- **Median compact eats 254K tokens; worst single event ate 974,533.**
  Cumulative across history: ~16.0M tokens compacted away.
- **At session-end, 87.4% of your sessions are "Healthy"** by the
  classifier. Only 1 out of 182 ever ended at >= 75% fill. The
  rescue product saves you in the rare-but-painful tail, not in
  steady state.
- **Top repeated tool calls** (across all sessions, ≥2 in same
  session): `discountEngineService.js` 231 reads,
  `mobile.css` 193 reads, `grep` 1354 invocations cross-session,
  `npm run` 422.

---

## How to roll back

If anything in this install breaks your Claude Code experience:

```bash
# 1. Restore your previous Claude Code config from the backup
#    (the install step writes one alongside ~/.claude.json with a
#    timestamp suffix; replace TIMESTAMP with the backup file's name).
cp ~/.claude.json.klyne-backup-TIMESTAMP ~/.claude.json

# 2. Remove the binary.
rm ~/.local/bin/klyne

# 3. Restart Claude Code.
```

If you only want to disable the MCP server temporarily without
rolling back the install, edit `~/.claude.json`, find the
`mcpServers.klyne` entry, and remove just that key (keep
example-codebase, excalidraw, linear-server intact).

---

## Next-slice candidates (status as of 2026-05-12)

1. ~~**Codex parity for all 4 MCP tools.**~~ **SHIPPED** (Slice 4).
2. ~~**Real-world integration smoke test.**~~ **SHIPPED** (Slice 6+).
3. **Eval harness against labelled fixtures.** Not yet started.
4. **Per-call caching by (path, mtime).** Not yet started.
5. **Pre-compact recovery for earlier-than-last events.** Not yet started.
6. ~~**Web UI as audit/inspection surface.**~~ **SHIPPED** (Slice 6 — `/stats`, `/cockpit`, `/sessions/[id]`).

---

## Quick-start (after install)

```bash
klyne mcp install   # registers MCP + advisor hook + slash commands
klyne               # starts daemon at 127.0.0.1:7878
```

Then in Claude Code, try: `/klyne:health`, `/klyne:tokens`, `/klyne:handoff`, `/klyne:precompact`.

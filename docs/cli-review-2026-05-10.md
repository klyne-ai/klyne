# klyne CLI review — 2026-05-10

> Honest snapshot of every `klyne` CLI command after the slice-7 + token-timeline + advisor-toggle work. Run against one real Claude session and one real Codex session in the same project (`/Users/mohitpatel/Desktop/Project/klyne`). Decide per-command whether the output is useful or whether it needs more work.
>
> **Update (later same day):** the four follow-up items at the bottom of this doc were dispatched to parallel agents and shipped. The "Codex tokens" gap and the parser-warning spam are FIXED in commits `a587bea` (Codex parser) and `2f0f9c1` (version injection). The "Useful today?" column below now reads ✅ for every command. The reproduction sections still document the original behaviour for posterity. See [`docs/MCP-SHIP-LOG.md`](MCP-SHIP-LOG.md) for the slice-8 entry once written.

## Test fixtures

| CLI    | Session ID                                | Project                                                                                  | Notes                                                                |
|--------|-------------------------------------------|------------------------------------------------------------------------------------------|----------------------------------------------------------------------|
| Claude | `6a9b785d-fdc7-46cb-b3ce-a4148e7a5f93`    | `/Users/mohitpatel/Desktop/Project/klyne/.claude/worktrees/musing-tereshkova-a2a74d`     | This very session — the one we used to build slice 7. ~550 turns, started 2026-05-09 21:59 IST. |
| Codex  | `019e107b-91de-7ea1-ad92-cb7cf3072d91`    | `/Users/mohitpatel/Desktop/Project/klyne`                                                | A klyne-project Codex rollout from earlier today (2026-05-10 11:33 IST). 8.2 MB.  |

## TL;DR — what works and what doesn't

| Command                       | Claude       | Codex             | Useful today? |
|-------------------------------|--------------|-------------------|---------------|
| `klyne doctor`                | ✅           | ✅                | yes (post-fix: real version string) |
| `klyne audit-sessions`        | ✅           | ✅                | yes |
| `klyne config show/get/set`   | ✅ (n/a)     | ✅ (n/a)          | yes |
| `klyne tokens` (per session)  | ✅           | ✅ (post-fix)     | yes |
| `klyne advise` (manual stdin) | ✅           | ✅ (post-fix: stderr clean) | yes |
| `klyne start / stop / mcp`    | n/a (daemon) | n/a (daemon)      | not in scope of this review |

**Single biggest gap (FIXED 2026-05-10):** `klyne tokens` returned no data on Codex sessions because Codex's current JSONL stores token usage in standalone `event_msg.token_count` events that the v1 parser did not attach to assistant messages. The fix is post-pass projection inside `mcpserver.loadCodexSnapshot`: the Codex parser keeps per-file state of the latest `token_count` snapshot, then the snapshot loader attributes those values to the nearest-preceding assistant message at build time. Verified live on session `019e107b`: 14-turn timeline now renders. See commit `a587bea`.

---

## 1. `klyne doctor`

**Purpose:** print a diagnostic report — paths, providers, schema version. Sanity check after install or upgrade. No session arg.

### Output (run once, applies to both CLIs equally)

```text
{
  "ok": true,
  "version": "v0.0.0-bootstrap",
  "schema_version": 8,
  "config_path": "/Users/mohitpatel/.klyne/config.toml",
  "db_path": "/Users/mohitpatel/.klyne/klyne.db",
  "db_size_bytes": 129662976,
  "connectors": {
    "claude": { "enabled": true, "root": "/Users/mohitpatel/.claude/projects", "exists": true },
    "codex":  { "enabled": true, "root": "/Users/mohitpatel/.codex/sessions",  "exists": true }
  },
  "providers": { "anthropic": false, "gemini": false, "ollama": true, "openai": false }
}
```

**Verdict: works as expected.** JSON output is machine-parseable. Confirms both connectors are wired and both root directories exist on disk. Reports DB size at 130 MB, schema v8 — useful for support/debugging.

**One note:** `version: v0.0.0-bootstrap` is the placeholder — `make build` doesn't yet inject the real version via `-ldflags`. Worth fixing before shipping a release binary.

---

## 2. `klyne audit-sessions --limit 10`

**Purpose:** verify klyne's stored token counts match raw JSONL ground truth. The trust foundation — if this fails, every other claim is suspect.

### Output

```text
.klyne audit report
Generated: 2026-05-10T09:09:14Z
Sessions sampled: 10

## Summary
- Latest-assistant input_tokens accuracy: 9/9 ✓ (100.0%)
- Sessions not yet ingested into klyne DB: 0
- Sessions with no assistant turn yet: 1
- /compact events detected: 0

All sampled sessions match. Trust foundation holds for this slice.

## Codex coverage
- Codex transcripts sampled: 10
- Sessions with token usage data: 9
- Sessions with no token data yet: 1
- Cumulative tokens across audited Codex sessions: ~1.0M
- Largest single Codex session (latest turn): 206K tokens
- /compact events detected: 2 total across 1 sessions
  (Codex JSONL does not expose pre-compact token counts; manual/auto split unavailable)

**Limitation:** Codex DB comparison deferred to a follow-up slice. Codex's connector stores per-turn
token deltas as system messages, not latest-assistant rows; comparing requires aggregating those
deltas, which the Claude path does not need. Until that lands, the audit reports JSONL ground truth
for Codex but does NOT validate the DB stored values.
```

**Verdict: works for both CLIs.** Claude DB matches raw JSONL 9/9 (one session had no assistant turn yet, expected). Codex side reports JSONL ground truth (~1.0M tokens across 10 sampled sessions, 206K largest single session) but admits up front that DB comparison is deferred.

**The Codex JSONL extractor inside `audit` clearly works** — but `klyne tokens` doesn't share that extractor (see §4). That's the inconsistency to fix.

---

## 3. `klyne config show / get / set`

### `klyne config show`

```toml
[server]
addr = '127.0.0.1:7878'

[paths]
db = '~/.klyne/klyne.db'
pricing_override = '~/.klyne/pricing.json'

[connectors]
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
```

### `klyne config get plan` / `klyne config get advisor`

```text
plan: max-5x (estimated cap ~220M effective tokens / 5h)
advisor: on
```

**Verdict: works, no surprises.** Both sub-keys read cleanly. `klyne config set plan max-5x` and `klyne config set advisor off|on` both verified live in earlier slices.

**Honest framing in the output is good:** `(estimated cap …)` reminds the user the cap is configured, not API-derived. Matches the spec's "no false precision" rule.

---

## 4. `klyne tokens` (the new one)

### Claude — `klyne tokens --session=6a9b785d-… --window=24h`

```text
# Session token usage — `6a9b785d`

Window: last 24h0m0s · 550 turns · model `claude-opus-4-7` (1M context) · times in IST.

Started at 53.6K (5% of context) → peaked at 495.9K (50%) → now at 495.9K (50%).

▁▁▁▁▂▂▂▃▃▃▃▃▄▄▄▄▄▄▅▅▅▅▅▅▆▆▆▆▆▆▆▆▇▇▇▇▇▇▇█
24h ago                              now
first ~53.6K · peak ~495.9K · now ~495.9K

| time     | input tokens | % of context | Δ vs start | uncached |
|----------|-------------:|-------------:|-----------:|---------:|
| 21:59:10 | 53.6K        | 5%           | 0          | 33.9K    |
| 23:14:19 | 152.2K       | 15%          | +98.6K     | 1.4K     |
| 23:24:48 | 216K         | 22%          | +162.4K    | 153      |
| 23:32:12 | 257.5K       | 26%          | +203.9K    | 3.3K     |
| 23:40:42 | 304.5K       | 30%          | +250.9K    | 1K       |
| 11:43:23 | 354.2K       | 35%          | +300.6K    | 700      |
| 12:20:05 | 380.6K       | 38%          | +327K      | 662      |
| 12:53:22 | 426.3K       | 43%          | +372.6K    | 1K       |
| 13:20:48 | 463.5K       | 46%          | +409.9K    | 1.7K     |
| 14:37:47 | 495.9K       | 50%          | +442.3K    | 473      |

**As of 14:37 IST (0s ago), this session is at ~495.9K of input — 50% of the 1M context window.**

_Uncached column = portion of each turn that bills against the 5-hour rate-limit at full rate (cached prefix is ~10× discounted by the provider)._
```

**Verdict for Claude: works as expected.** Trajectory clear, growth visible, percentages climb 5% → 50% over 550 turns. Times localised to IST. Uncached column shows the cache discount working (most turns <2K against the rate limit even though the prefix is ~470K). The 0s freshness anchor confirms we're reading the live transcript.

### Codex — `klyne tokens --session=019e107b-… --window=24h`

```text
2026/05/10 14:37:52 codex: unknown response_item type "custom_tool_call" in /Users/mohitpatel/.codex/sessions/2026/05/10/rollout-2026-05-10T11-33-14-019e107b-91de-7ea1-ad92-cb7cf3072d91.jsonl — skipping
2026/05/10 14:37:52 codex: unknown response_item type "custom_tool_call_output" in …
2026/05/10 14:37:52 codex: failed to decode response_item payload in … json: cannot unmarshal array into Go struct field itemPayload.output of type string
2026/05/10 14:37:52 codex: unknown response_item type "custom_tool_call" in …
2026/05/10 14:37:52 codex: unknown response_item type "custom_tool_call_output" in …
Session `019e107b` has no assistant turns within the last 24h0m0s.
```

**Verdict for Codex: BROKEN.** Two distinct issues:

1. **No data returned.** The session is real (file is 8.2 MB, 9 assistant message rows, 15 token-usage records) — but klyne's Codex parser does not attach the `event_msg.token_count` payload to canonical Messages, so `Message.TokensIn` is zero on every row, so the timeline aggregator drops them. `audit-sessions` works because it has its own `LatestCodexTokens` extractor that walks the raw JSONL directly.
2. **Parser-warning spam on stderr.** Two undocumented response-item types (`custom_tool_call`, `custom_tool_call_output`) produce log lines for every occurrence. Cosmetic for the timeline (stdout is empty anyway) but visible in any command that touches Codex JSONLs.

**Recommended follow-up:** extend `internal/connectors/codex` to also extract token counts from `event_msg.token_count` records and project them onto the chronologically-nearest assistant message — basically what `audit.LatestCodexTokens` already does, but per-turn instead of latest-only. That single change makes `klyne tokens` symmetric across both CLIs and silences most of the parser warnings (or at least demotes them to debug-level).

### My honest read

`klyne tokens` is the most useful command we shipped this slice **for Claude users**. For Codex users, it currently returns nothing. The README and feature docs should be honest about that until the Codex parser is updated. Until then, Codex users should rely on `klyne audit-sessions` for token counts.

---

## 5. `klyne advise` (the hook entrypoint)

**Purpose:** Claude Code's `UserPromptSubmit` hook subprocess. Reads optional JSON on stdin, runs the four advisor triggers, prints a single hook JSON line on stdout when an advisory should fire — silent otherwise. The user does not invoke this directly in normal operation; it is documented here for completeness.

### Manual invocation, no payload

```bash
klyne advise < /dev/null
# (empty stdout, exit 0)
```

**Verdict: correct silent behaviour.** When no cwd/session_id is provided AND multiple sessions exist under the calling cwd, the resolver bails ambiguously. The hook never blocks the user prompt.

### Manual invocation with explicit session

```bash
echo '{"cwd":"/path/to/worktree","session_id":"6a9b785d-…"}' | klyne advise
# 2026/05/10 14:39:02 codex: unknown response_item type ...   (stderr)
# (empty stdout, exit 0)
```

**Verdict: correct — but stderr is noisy.** The 5-hour-window aggregator walks every Claude+Codex JSONL in the home dir; every Codex session with the new format produces parser warnings. The hook itself works (stdout is the only channel Claude Code reads, and that stays clean), but a user running `klyne advise` interactively will see scary-looking stderr noise.

---

## 6. Daemon commands (out of scope for this review)

`klyne start`, `klyne stop`, `klyne mcp`, `klyne mcp install` are tested elsewhere and weren't re-run for this comparison. The MCP install was verified live earlier — it correctly registers the server in `~/.claude.json` and `~/.codex/config.toml` and the advisor hook in `~/.claude/settings.json`.

---

## Summary of follow-ups (all SHIPPED 2026-05-10)

Originally listed in priority order; all four landed via two parallel agents on the same day. Status updates inline:

1. ~~**Fix Codex token-timeline parsing.**~~ ✅ Shipped in `a587bea`. Codex sessions now produce the same per-turn timeline shape as Claude.
2. ~~**Demote Codex parser warnings to debug-level.**~~ ✅ Shipped in `a587bea`. `custom_tool_call` and `custom_tool_call_output` are first-class supported types now; the array-shaped `function_call_output.output` parses correctly; truly-unknown types log once-per-file via a `sync.Map` gate.
3. ~~**Wire `-ldflags` into `klyne doctor`'s version field.**~~ ✅ Shipped in `2f0f9c1`. `make build` injects `git describe --tags --always --dirty`; plain `go build` keeps the v0.0.0-bootstrap default.
4. ~~**Add an integration test for `klyne tokens` against a Codex fixture.**~~ ✅ Shipped in `a587bea` alongside the fix — `TestHandleGetTokenTimeline_CodexSession` and `TestHandleGetTokenTimeline_CodexToleratesCustomToolShapes` in `internal/mcpserver/tool_token_timeline_test.go`.

## What this review IS NOT

- **Does not test the slash-prompt surface (`/klyne:tokens`, `/klyne:health`, etc.).** Those run inside Claude Code and have their own UX quirks; out of scope per the user's request to focus on CLI.
- **Does not test the web cockpit.** Same scope cut.
- **Does not test `klyne start`/`stop`/the MCP server's stdio transport.** Daemon lifecycle is covered by other test surfaces.

The point of this doc is: with the CLI as the sole surface, here is what a user sees today. If anything below the table reads as "yes, that's the bar I'd want to ship to Reddit on," ship it. If not, fix that command before posting.

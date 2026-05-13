# proactive session advisor

> Spec date: 2026-05-09 · Status: shipped (Slice 7, 2026-05-09)

klyne already detects when a Claude Code or Codex session is in trouble
(`get_context_health` classifies `healthy / drifting / risky / rescue_now`).
Today the user has to ask for that verdict via a slash command or the MCP
tool — the AI itself rarely calls it unprompted, and the user usually finds
out about a degrading session only after the next turn slows down or the
5-hour cap bites.

This feature closes the loop. klyne pushes the verdict into the user's
reading flow at the moment they're about to type their next prompt, with a
short advisory that names the actual problem and points at the cheapest
recovery.

## the user problem

Three failure modes happen to power users every week:

1. **Silent rate-limit drain.** The user thinks the session is "fine,"
   keeps going, and discovers at minute 90 of a 5-hour window that they
   burned 60% of the cap on a session whose context was mostly stale.
2. **Topic drift inside one session.** The user starts on bug A, finishes
   it, starts on bug B in the same chat because "all the focus knowledge
   is here." Bug A's files keep riding in the prefix, paying for context
   the new task doesn't use.
3. **Acceleration without warning.** Per-turn cost goes 4K → 9K → 15K →
   25K input tokens uncached. The user doesn't see the acceleration
   curve until the next turn stalls or the cap window blows.

Each of these is detectable from the JSONL klyne already reads. The fix
is a push channel.

## what ships

A `UserPromptSubmit` hook installed by `klyne mcp install`. When the user
types a message in Claude Code, the hook runs first, evaluates four
deterministic triggers against the active session's JSONL, and — if any
fires — prepends a single advisory line to the prompt. The AI then surfaces
that line conversationally in its response.

Concrete deliverables:

1. New CLI subcommand `klyne advise` — the hook entrypoint. Reads the
   active session, evaluates triggers, prints a one-line advisory to
   stdout (or empty when the session is healthy).
2. `klyne mcp install` extended to register the hook in
   `~/.claude/settings.json` under `hooks.UserPromptSubmit`. Idempotent:
   safe to re-run on every binary upgrade.
3. New trigger logic in `internal/contexthealth/`:
   - **Stale-context** — per-file relevance score; flag when stale share
     of loaded file bytes > 50%.
   - **Acceleration** — uncached-input delta doubling over 3 vs 5 turn
     windows, with a 5K floor.
   - **5-hour-window** — consumption ≥ 50% of configured plan cap.
4. `generate_handoff` extended with `scope=current-topic` mode that
   carries forward only files above the relevance threshold plus the
   most recent exchanges.
5. New config: `klyne config set plan {pro|max-5x|max-20x|team|custom}`,
   used as the denominator for the 5-hour-window trigger.
6. `klyne mcp install` first-run prompt that asks the user which plan
   tier they're on (one-time setup; falls back silently when the user
   skips).

## triggers

Four rules, OR'd. Each has its own one-line advisory copy.

### stale-context (relevance drift)

Each file read sits at message index `i` in the JSONL. The bag-of-words of
user messages in window `[i-2, i+2]` becomes that file's **topic anchor**.
The user's **current direction** is the bag-of-words of the last 5 user
messages. Per-file relevance = `Jaccard(anchor, current_direction)`. Files
with score below 0.20 are **stale**. Sum their bytes against the per-file
read+edit attribution that `computeBloat` already computes; fire when
`stale_bytes / total_file_bytes > 0.5`.

Advisory copy:

> klyne: ~62% of loaded file context is no longer relevant to your current
> direction (auth files; you're on billing). `/klyne:handoff
> scope=current` carries forward only `internal/billing/charge.go` and
> `internal/billing/refund.go`.

The relevant-subset names are filled in from the per-file Jaccard scores
(top N still-relevant files by bytes).

### acceleration

Compute uncached-input delta per assistant turn (raw input minus
`cache_read_input_tokens` minus `cache_creation_input_tokens` from the
JSONL `usage` field — klyne's cost package already tracks these after
commit `2cfe320`). Fire when:

```
mean(deltas[-3:]) > 2 * mean(deltas[-8:-3])
AND deltas[-1] >= 5_000
```

Need at least 8 assistant turns; below that the trigger is silent.

Advisory copy:

> klyne: per-turn cost has doubled over the last 3 turns. Continuing here
> will burn through your 5-hour window faster than starting fresh.
> `/klyne:handoff scope=current` keeps the relevant context.

Deliberately direction-only — no "exhaust in N turns" projection in v1.
A specific projection wouldn't pass the 80–90% accuracy bar without an
eval suite, and being wrong on Reddit kills trust.

### 5-hour-window

Sum uncached input tokens across every Claude Code + Codex session under
the user's home dir whose latest event sits within the last 5 hours.
Divide by the configured plan cap. Fire when the ratio crosses 50% (warn)
and again at 75% (urgent).

Advisory copy at 50%:

> klyne: you've used ~52% of your 5-hour window across 3 active sessions.
> This session is the dominant consumer (38% of the burn).

Advisory copy at 75%:

> klyne: you're at ~78% of your 5-hour window. The cheapest next step is
> `/klyne:handoff scope=current` and a fresh session — continuing here
> will likely tip you over the cap mid-task.

The "estimated against your configured plan" caveat appears in the
`/klyne:health` slash output and in the web cockpit. The advisory line
itself stays terse.

### hard ceiling (existing)

`fill ≥ 75%` — already classified as `rescue_now`. Reuses the existing
classifier verdict. Advisory copy comes from the classifier's `Reason`
field and does not need new logic.

## transition rule

A trigger fires **once** on the user-prompt that first observes its
condition. It then stays silent until either (a) the condition clears, or
(b) a different trigger trips. State is held in
`~/.klyne/advisor-state.json`, keyed by session id.

Lifetime cap per session: a session can fire at most one advisory per
trigger before it fully clears. With four triggers that bounds total
advisories at 4 per session in the worst case.

Rationale: this is the "interactive but useful, not annoying" mode the
user explicitly asked for. Firing on every turn while degraded is the
known failure mode of similar tools.

## channel — UserPromptSubmit hook

Claude Code supports `UserPromptSubmit` hooks in `~/.claude/settings.json`:

```json
{
  "hooks": {
    "UserPromptSubmit": [
      {
        "matcher": "*",
        "hooks": [
          {"type": "command", "command": "/abs/path/to/klyne advise"}
        ]
      }
    ]
  }
}
```

`klyne advise` resolves the active session via the same disambiguation
the existing MCP tools use (cwd → project → unique active session,
fallback to most-recently-modified). It writes the advisory line to
stdout as the hook's additional context; on no-fire it writes nothing.

Hard performance budget: **complete in <300 ms** on a 50 MB JSONL. The
hook blocks prompt submission. Fail open: any error or timeout returns
empty stdout so the user's prompt is never blocked.

Token cost: the advisory string is ~60–100 tokens. Fixed-cost per fire,
same model as CLAUDE.md. Zero AI calls anywhere in the path.

## auto-install

`klyne mcp install` is the single command. After this update it does:

1. Register the MCP server in `~/.claude.json` and
   `~/.codex/config.toml` (existing behaviour).
2. Register the `UserPromptSubmit` hook in `~/.claude/settings.json`
   (new). Idempotent merge — preserves any existing hooks.
3. On first run only: prompt the user for plan tier
   (`pro / max-5x / max-20x / team / skip`). Stored in `~/.klyne/config.toml`.
   Skipping silences the 5-hour-window trigger only; the other three
   work without it.
4. Print a one-line confirmation: `klyne: advisor active — you'll see
   inline warnings in Claude Code when sessions drift, accelerate, or
   approach your 5-hour cap.`

Codex CLI's hook surface is verified during implementation. If Codex
exposes a pre-prompt hook equivalent, the install step lights it up
there too. If not, the advisor on Codex is web-cockpit-only in v1: the
dashboard surfaces the same advisories on the session detail page. The
MCP tool surface remains identical across both CLIs.

## scoped handoff

`generate_handoff` adds an optional `scope` argument. When `scope=current-topic`:

1. Compute per-file relevance using the same Jaccard logic as the
   stale-context trigger.
2. Include only files with relevance ≥ 0.20 in the "Files touched"
   section.
3. Include the last 10 user/assistant exchanges (vs the existing
   default of 5) so the new session inherits the recent direction.
4. Skip commands and failures from before the topic shift inflection
   point (defined as the message index where the bag-of-words drift
   exceeds the topic-shift threshold).

The slash command `/klyne:handoff scope=current` surfaces it in the
Claude Code menu. The MCP tool description names `scope` as an optional
argument with two values: `full` (default) and `current-topic`.

## honest caveats — surfaced in product

- The 5-hour cap is **user-configured, not API-derived**. The web cockpit
  and `/klyne:health` output both show "estimated against your configured
  plan." If we don't know the cap, the trigger silently skips.
- The acceleration advisory says "trending toward your cap," not "you'll
  hit it in N turns." A specific projection waits for v2.
- Stale-context relevance is a deterministic Jaccard signal, not semantic
  similarity. False positives are possible when a file is loaded for one
  bug and is also (coincidentally) relevant to a later bug; users can
  ignore the advisory at zero cost. False negatives are also possible;
  the other three triggers backstop.

## out of scope for v1

- Specific turn projections (N-turns-to-cap). Needs an eval suite and
  cap-learning loop.
- Codex `UserPromptSubmit` advisor (Codex CLI doesn't expose this hook
  surface).
- Empirical plan-cap learning from user behaviour. Configured value
  only in v1.
- Per-message AI summarization or relevance re-ranking. Would itself
  burn user tokens. Excluded by the fixed-cost rule.

## testing strategy

Each trigger gets a fixture under `docs/proof/03-advisor/<trigger>/` —
a synthetic JSONL constructed to exercise exactly one rule, plus a Go
test that asserts the advisor produces the expected line and that the
other three triggers stay silent.

Specifically:

1. `stale-context.jsonl` — opens with auth files, switches to billing.
   Test asserts advisory names billing files only.
2. `acceleration.jsonl` — uncached deltas 2K, 3K, 4K, 9K, 15K, 25K.
   Test asserts advisory mentions doubling and points at scoped handoff.
3. `five-hour-window.jsonl` — three concurrent sessions whose total
   uncached input crosses 50% of a configured cap. Test asserts
   advisory names the dominant consumer.
4. `transition.jsonl` — same trigger condition repeated across 5
   prompts. Test asserts advisory fires exactly once.

Plus an end-to-end test for `klyne advise` invoked as the hook subprocess,
asserting the <300 ms perf budget on a 50 MB fixture and graceful
no-output on malformed JSONL.

The existing classifier rubric in `docs/marketing/context-rescue-eval.md`
gets a new section for the three new triggers; threshold tuning lives
there.

## file changes

- `cmd/klyne/advise.go` — new subcommand, the hook entrypoint.
- `cmd/klyne/config.go` — `klyne config set plan` subcommand.
- `internal/contexthealth/relevance.go` — per-file Jaccard scoring.
- `internal/contexthealth/acceleration.go` — uncached-delta acceleration
  detector.
- `internal/contexthealth/five_hour_window.go` — cross-session
  consumption aggregator.
- `internal/contexthealth/transition.go` — per-session state cache.
- `internal/mcpserver/install.go` — auto-install of UserPromptSubmit hook.
- `internal/mcpserver/tool_generate_handoff.go` — `scope=current-topic`
  handler.
- `internal/config/plan.go` — plan tier enum + cap lookup table. Initial
  per-tier cap values are calibrated against publicly reported community
  numbers and the maintainer's own observed sessions; users can always
  override with `klyne config set plan custom --cap=<tokens>`.
  Honest framing in product copy: "estimated against your configured
  plan."

## success criteria

- A user installs klyne, runs `klyne mcp install`, picks their plan
  tier, and the next time they hit one of the four trigger conditions
  in Claude Code they see an inline advisory — without having ever
  typed `/klyne:` anything.
- `make proof` includes the four new fixture tests; CI is green.
- Advisor adds zero AI-model calls. The only token cost on the user's
  side is the advisory string itself, gated by the transition rule.
- Hook completes in <300 ms p99 on the maintainer's largest existing
  session JSONL.

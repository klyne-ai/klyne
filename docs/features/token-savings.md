# token savings

Every additional CLI turn re-sends the entire conversation history as input.
The bigger the session, the more each next turn drains the user's 5-hour
rate-limit cap — and that drain compounds silently. Users typically don't
notice until things slow to a crawl, the cap is hit, or the bill arrives.
This feature makes the cost of doing nothing visible inside the session view,
and makes the two actions that actually save tokens — `/compact` and starting
a fresh session — one click away.

> **Companion CLI surfaces (shipped after this doc was written):**
>
> - `klyne tokens [--session=ID] [--window=Xh]` renders the same per-turn
>   token-cost data this page describes, but on the terminal, with an
>   ASCII sparkline and a row-per-turn table. See
>   [docs/features/proactive-session-advisor.md](proactive-session-advisor.md)
>   for the engine. The CLI is the primary surface today; the UI elements
>   below are still planned.
> - `klyne advise` is the Claude Code `UserPromptSubmit` hook that
>   replaces the "click the break advisor button" flow with an inline
>   one-line advisory injected when one of four deterministic triggers
>   crosses its threshold. No AI calls. Toggle with
>   `klyne config set advisor on|off`.
> - The Codex side of `klyne tokens` is currently broken (returns "no
>   assistant turns") because the parser does not yet attach Codex's
>   `event_msg.token_count` records to canonical messages. Tracked as a
>   known gap in [docs/cli-review-2026-05-10.md](../cli-review-2026-05-10.md).

## what ships

Three concrete UI elements on the session detail page:

1. **Passive context-fill indicator** with a per-turn cost projection,
   expressed as a percentage of the user's rolling 5-hour rate-limit cap.
2. **Active "compact now" CTA** with a concrete savings delta. Appears once
   the indicator crosses ~50% fill.
3. **On-demand AI break advisor** that reads the recent conversation and
   recommends `start_fresh` / `compact` / `continue`. Appears at >60% fill
   and only fires on user click.

## how it solves the user problem

### context-fill bar

Users had no usable mental model for what each CLI turn costs. "It's a long
session" doesn't translate to action; "your next turn will burn ~6% of your
5-hour cap" does. Putting a concrete percentage in front of the user — at
the moment they're about to type their next prompt — is the signal that
triggers compaction. The bar also colour-codes (green / yellow / red) so the
user can read it at a glance without doing arithmetic.

### compact CTA

Users avoid `/compact` for two reasons: (a) they fear losing context the
model is implicitly relying on, and (b) they don't realise that starting a
fresh session is even worse, because they pay the cache-rehydration cost on
the next turn anyway. The CTA addresses both. It shows the post-compact
per-turn cost (still useful, the session keeps working), and it explicitly
exposes the savings delta vs. the do-nothing path. The hidden win — that
compact preserves the prompt cache where a fresh session does not — is
called out in the copy. Friction removed, action taken.

### break advisor

Heuristic drift detection (file-overlap, message-count thresholds, time
gaps) is wrong roughly half the time and doesn't earn the user's trust. The
only thing that justifies the recommendation is actually reading the
conversation. This is exactly what a small Haiku-class model is for: cheap
enough to run on click, smart enough to tell the difference between "user
just finished a task" and "user is mid-debug". The verdict is constrained
to a tiny enum (`start_fresh` / `compact` / `continue` / `unavailable`) so
the user gets one button to press, not a paragraph to read.

## the turn matrix

How per-turn cost evolves under each user action. Numbers are illustrative
and assume a Pro-tier Claude session sitting at 70% context fill.

| Action            | Approx context size | Per-turn input tokens | Per-turn cost (% of 5h limit, illustrative) |
|-------------------|---------------------|-----------------------|---------------------------------------------|
| Continue as-is    | 140k tokens (70% of a 200k window) | ~140,000              | ~6.0%                                       |
| `/compact`        | ~21k tokens (15% of original)      | ~21,000               | ~0.9%                                       |
| Start fresh       | ~5k tokens (system prompt floor)   | ~5,000                | ~0.3%                                       |

> The actual percentages depend on the user's plan tier and observed
> utilization in the last 5h window. The server returns `-1` for any
> `*_pct_5h` field when no recent `/usage` data is available; the UI
> renders `-1` as `—` rather than a misleading zero. See `CalibratedFromOAuth`
> on `SessionUsageResponse` for the live-vs-estimate flag.

## the logic behind it

### calibration: tokens to "% of 5h limit"

The whole point of expressing cost as percent-of-cap (not raw tokens, not
raw dollars) is that it maps directly to the user's lived constraint:
"how many more turns until I'm rate-limited". The math:

1. Read the latest `/usage` snapshot for the session's CLI:
   - For Claude: `internal/usage/oauth.go` calls
     `https://api.anthropic.com/api/oauth/usage` with the user's OAuth
     token, cached for 5 minutes. Returns `tokens_window_5h` and
     `oauth_utilization_5h`.
   - For Codex: `internal/usage/codex_snapshot.go` reads the latest
     `token_count` event from the most recent JSONL session file
     (Codex writes the canonical rate-limit snapshot back into the
     session log on every turn — no network call needed).
2. `tokens_per_pct = tokens_window_5h / oauth_utilization_5h`. This is the
   user's effective cost-per-percent under their current plan and recent
   activity pattern. Re-derived per request so a heavy hour and a light
   hour calibrate differently.
3. `next_turn_tokens = ContextUsed`, where `ContextUsed` is the most recent
   assistant message's reported `tokens_in`. That's the closest empirical
   signal we have to "what did the CLI actually pack into the context for
   that turn" — far more accurate than summing message lengths.
4. `next_turn_pct = next_turn_tokens / tokens_per_pct`.
5. Compact projection uses the constant `CompactRatio = 0.15` (Claude
   Code's `/compact` empirically reduces history to ~10–20% of original,
   we sit at the conservative middle).
6. Restart projection uses a fixed floor of `5000` tokens (system prompt
   plus minimal scaffold).
7. If `oauth_utilization_5h` is zero, OAuth data is missing, or the
   credential loader fails, every `*_pct_5h` field is returned as `-1`
   and `CalibratedFromOAuth = false`. The UI renders `—`.

### break advisor (AI path)

- **Trigger**: user click only. Never auto-fires — calling an LLM on every
  page-paint is both slow and a trust violation.
- **Model selection**: routed through the existing `internal/ai` selector
  with `TaskKind = TaskTitle`, which always picks the cheapest configured
  model (Haiku-class on Anthropic, equivalent tier on Gemini).
- **Prompt input**: the last 10 messages of the session, each truncated to
  1000 chars. Just enough signal to detect "task ended" vs. "task ongoing"
  without paying for the full history.
- **Output**: structured JSON parsed into `BreakAdviceResponse`. Verdict is
  one of `start_fresh` / `compact` / `continue` / `unavailable`.
- **Caching**: in-memory, server-side, 10 minutes per session. Re-clicks
  inside the window are free. The `cached_at` field on the response lets
  the UI show "advised X ago" and invalidate locally if it wants fresher
  advice.
- **Cost guard**: roughly $0.005 per click on Haiku-class models. If no AI
  provider is configured the handler returns `Verdict = "unavailable"`
  with a `Reason` that explains why, rather than a 500.

## endpoints

Both are simple `GET`s, no body, return JSON. Authoritative DTOs live in
`internal/api/contracts.go`.

- `GET /sessions/{id}/usage` → `SessionUsageResponse` — context fill,
  per-turn cost, compact / restart deltas, and the calibration flag.
  See `RouteSessionUsage` in `internal/api/contracts.go`.
- `GET /sessions/{id}/break-advice` → `BreakAdviceResponse` — AI verdict
  with reason and optional `suggested_topic`. See `RouteSessionBreakAdvice`
  in `internal/api/contracts.go`.

## what's NOT in scope (yet)

- **Model context-window detection is heuristic.** `internal/usage/context_window.go`
  string-matches on model name. Fine for v1; will move to a config-driven
  table when the supported model list grows.
- **`CompactRatio` is hard-coded at 0.15.** If empirical observation drifts
  (e.g. Claude Code changes its compaction strategy) this becomes a
  user-configurable value.
- **Pre-titled fresh-session workflow is not wired up.** `BreakAdviceResponse.SuggestedTopic`
  is returned and displayed when the verdict is `start_fresh`, but the UI
  does not yet open a new session pre-titled with that topic. Future
  iteration.
- **Per-CLI calibration mixing.** A session uses its own CLI's `/usage`
  snapshot only — no cross-CLI averaging. This is intentional but called
  out so reviewers don't ask.

## file map

For navigation when reading the implementation:

- `internal/api/contracts.go` — DTOs (`SessionUsageResponse`, `BreakAdviceResponse`,
  `BreakAdviceVerdict`) and route constants (`RouteSessionUsage`,
  `RouteSessionBreakAdvice`).
- `internal/api/handlers/session_usage.go` — per-session usage handler;
  owns the calibration math.
- `internal/api/handlers/session_break_advice.go` — advisor handler; owns
  the prompt, the JSON parser, and the in-memory cache.
- `internal/usage/oauth.go` — Claude OAuth `/usage` fetcher with 5-minute
  TTL cache.
- `internal/usage/codex_snapshot.go` — Codex JSONL snapshot reader.
- `internal/usage/context_window.go` — model name → context-window lookup.
- `ui/src/lib/components/TokenSavings.svelte` — the indicator, CTA, and
  advisor button (planned mount target).
- `ui/src/routes/sessions/[id]/+page.svelte` — mount point on the session
  detail page.
- `ui/src/lib/types.ts` — TypeScript mirrors of the Go DTOs.

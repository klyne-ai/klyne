# klyne context rescue strategy

Review date: 2026-05-08

## The decision

klyne should stop trying to feel like a generic AI coding dashboard. That
position is crowded, and the current product does not create enough urgency:
seeing sessions, tokens, and search results is useful, but it is not a strong
reason for a user to install and keep a new local daemon running.

The sharper product promise is:

> klyne is the black-box recorder for Claude Code and Codex sessions. It
> tells you when a session is becoming unreliable, what is poisoning the
> context, and gives you a clean restart prompt before the agent wastes more
> quota or loses the thread.

This turns klyne from "session viewer" into "context rescue."

## Why this is the right wedge

Power users are not mainly confused by the existence of many sessions. They
are frustrated when a long-running coding agent gets slower, dumber, more
expensive, or loses important context after compaction. That problem is
painful because it appears in the middle of real work:

- The user has already spent time building context.
- The agent has already touched files and made decisions.
- Starting over feels risky.
- Compacting feels opaque.
- Continuing burns tokens and may lower quality.

klyne already has the raw material for this problem: local JSONL history,
tool calls, tool outputs, timestamps, token counts, project paths, resume
commands, and context-fill estimates. The missing part is turning those raw
signals into an opinionated rescue workflow.

## What we can learn from code-review-graph

`code-review-graph` is not a direct competitor to klyne. It operates before
or during an AI coding task: parse the repo, build a structural graph, then give
the agent minimal relevant code context. klyne operates after and alongside
the AI coding task: read the session logs, explain what happened, and rescue the
conversation when context gets bloated.

The overlap is important: both products are really about reducing irrelevant
context.

Useful lessons:

1. **Make the token-saving promise measurable.**  
   `code-review-graph` leads with concrete reduction claims and benchmarks.
   klyne should do the same for session rescue: "this restart prompt is
   12x smaller than the current session context" is stronger than "copy
   handoff prompt."

2. **Create a minimal-context primitive.**  
   Their `get_minimal_context` returns a compact summary, risk, key entities,
   communities, flows, and next-tool suggestions. klyne needs the session
   equivalent: `GET /sessions/{id}/handoff` should return the smallest useful
   context needed to continue the task.

3. **Explain attention waste, not code blast radius.**  
   Their graph traces which files/functions are structurally affected by a
   change. Sessions do not have callers and dependents in that sense. The
   right session-equivalent is an attention/relevance score: of the tool
   results currently being carried in context, which are still referenced by
   the model or user, and which are inert weight?

4. **Prefer structured output over transcript browsing.**  
   Their tools return compact, typed, task-shaped data instead of dumping all
   code. klyne should stop making the transcript the primary object and
   make context health, bloat sources, decisions, and next steps the primary
   objects.

5. **Add next-step suggestions.**  
   Their hint system infers workflow intent from recent tool calls and suggests
   the next useful tool. klyne can infer session intent from recent messages
   and suggest `continue`, `compact`, `start fresh with handoff`, or `open
   related prior session`.

6. **Local-first plus auto-ignored state matters.**  
   Their graph lives locally under `.code-review-graph/`. klyne should keep
   its local-first positioning, but also make the generated handoff/export
   artifacts clearly local and inspectable.

7. **Benchmarks are marketing, not only engineering.**  
   The repository's README uses screenshots, benchmark tables, and limitation
   notes to create trust. klyne needs a similar benchmark: current session
   context size vs rescue handoff size, plus before/after continuation quality
   examples.

What not to copy:

- Do not build a full Tree-sitter code graph inside klyne for v1. That is a
  different product and a large maintenance burden.
- Do not route other tools' MCP calls. That would make klyne a weaker
  workflow orchestrator. Do consider exposing klyne's own session
  intelligence as MCP tools; that is the path from "product the user opens" to
  "memory layer the AI can query." Decide deliberately instead of drifting into
  it.
- Do not expand to 20+ platforms just to mirror their install matrix. The
  Claude/Codex session-rescue wedge is still stronger.
- Do not make graph visualization the main feature. Visuals are useful for
  demos, but the core user need is an actionable rescue recommendation.

Optional code-review-graph integration:

- If a project has `.code-review-graph/`, the handoff endpoint can optionally
  shell out to `code-review-graph get-minimal-context` and add its compact repo
  summary to the restart prompt.
- This should be a v1 enhancement, not a hard dependency. It gives a strong
  demo and costs little because klyne only detects an existing local graph;
  it does not install Python or build the graph itself.
- Context Rescue must still work from JSONL alone.

## Strategic fork: UI product or MCP memory layer

The code-review-graph installer exposes a decision klyne should make
explicitly.

### Option A: klyne-as-UI, code-review-graph-as-data

klyne remains a Go daemon plus Svelte UI. It reads sessions, computes
context health, and generates handoff prompts. When `.code-review-graph/`
exists, it enriches the handoff by calling code-review-graph locally.

This is a feature. It is the right default for v1 because it preserves
klyne's read-only, local-first, no-runtime-surprises posture.

### Option B: klyne-as-MCP-server

klyne exposes session intelligence as MCP tools:

- `get_session_history`
- `find_similar_session`
- `recover_decisions_from_session`
- `get_context_health`
- `generate_handoff_prompt`

Then Claude Code or Codex can ask klyne what happened in this repo last
week without the user opening the UI. This makes klyne an AI memory layer
across sessions.

This is not just a feature. It changes distribution, installer work, launch
copy, and the primary surface of the product. It may be the bigger opportunity,
but it should be chosen deliberately after the v1 rescue loop is trustworthy.

## What to add

### 1. Context Health panel

Add a high-signal panel near the top of the session page, next to or below
the existing token-savings indicator.

States:

- `Healthy` — continue normally.
- `Drifting` — context is growing or topic has shifted; compact soon.
- `Risky` — repeated tool output, test loops, or high fill are likely hurting
  the next turns.
- `Rescue now` — generate a clean handoff and start fresh or compact.

Signals to use in v1:

- Context fill percentage from `/sessions/{id}/usage`.
- Message count and session age.
- Long tool outputs.
- Repeated reads of the same file.
- Repeated command/test output.
- High hidden tool/system message count.
- Topic shift from early-session user prompts to recent prompts.
- Long idle gap followed by a new task.

The UI copy should be direct:

```text
Context Health: Risky
This session has 134K tokens in context and 91 hidden tool/system rows.
The largest repeated source is package-lock.json, read 7 times.
```

### 2. Context bloat scorecard

Show what is eating the context window. This is the "wow" part because users
cannot easily see it in Claude Code or Codex today.

Example output:

```text
Top context bloat
1. Read package-lock.json — 38% of retained tool output, repeated 7 times
2. npm test output — 14%, repeated 5 times
3. Search result dump — 9%, 1 long tool result
```

Implementation can start approximate:

- Use `tool_calls` and `tool_results`.
- Attribute tool result size by character count first.
- Later improve with token estimates if available.
- Detect file paths from tool input JSON when possible.
- Group repeated commands by normalized command string.

The goal is not perfect accounting. The goal is to explain why the session
feels heavy and what the user should do next.

### 3. Clean Handoff prompt

Add a button:

```text
Copy clean restart prompt
```

It should produce Markdown the user can paste into a fresh Claude/Codex
session:

```md
We are working in /path/to/project.

Goal:
- ...

Completed:
- ...

Current state:
- ...

Important decisions:
- ...

Files touched:
- ...

Tests/commands run:
- ...

Known failures:
- ...

Next best step:
- ...
```

The v1 version can be deterministic and local:

- Pull recent user/assistant messages.
- Pull recent tool calls.
- Pull file paths from tool input.
- Pull failed commands from tool results.
- Include the copy-safe `cd '<project>' && claude/codex --resume <id>` command
  only as reference, not as the primary action.

The v2 version can use BYOK AI to summarize decisions and next steps. AI should
enhance the prompt, not be required for the feature to exist.

### 4. Rescue recommendation

Replace generic "AI break advisor" framing with a more concrete rescue action:

- `Continue`
- `Compact`
- `Start fresh with handoff`

The recommendation should explain the tradeoff in one line:

```text
Start fresh with handoff: this session has shifted from backend fixes to launch
strategy, and 52% of the context is already full.
```

This framing is stronger than "break advice" because it maps to the user's
real fear: "Will I lose work if I leave this session?"

## What to remove or de-emphasize

### Remove from the launch story

- Generic cost dashboard positioning.
- "See all your sessions" as the main promise.
- Broad connector roadmap as a headline.
- Menu-bar quota widget ideas.
- Team sharing, relay, plugin API, or mobile remote.
- Any feature that makes klyne look like a weaker CliDeck.

These are not bad features, but they pull the product into better-funded or
more mature categories.

### De-emphasize in UI

- UUID-first session identity. Always show task/topic/preview first.
- Raw cost numbers when pricing is unknown or zero.
- Empty search examples that return zero hits.
- Long transcript browsing as the primary session experience.

### Keep but reposition

- Cockpit: useful as "what is alive right now," not the killer feature.
- Search: useful as recovery support, not the killer feature.
- Token savings: useful as the trigger for Context Rescue, not the whole
  product.
- Resume command: useful as a reliability feature, not enough by itself.

## What the new launch demo should show

The demo should be a single story:

1. A long Codex or Claude session is at high context fill.
2. klyne shows `Context Health: Risky`.
3. The bloat scorecard identifies repeated file reads and test output.
4. The user clicks `Copy clean restart prompt`.
5. The generated handoff contains goal, files touched, decisions, failures,
   and next step.
6. The user starts a fresh session and continues without re-explaining the
   whole project.

Launch copy:

```text
Show HN: klyne — rescue bloated Claude Code and Codex sessions before they
lose the plot
```

Alternative:

```text
klyne shows what is poisoning your AI coding session and gives you a clean
restart prompt.
```

## MVP implementation plan

### Phase 0: trust audit

Before adding more verdicts, validate the numbers klyne already shows.
Every rescue recommendation depends on context-fill, token, and cost accuracy.

Backend/CLI:

- Add `klyne audit-sessions`.
- Walk `~/.claude/projects` and `~/.codex/sessions`.
- Sample real sessions and compare klyne's stored messages, tokens,
  context-fill, and costs against ground truth parsed directly from JSONL.
- Print mismatches by source file and parser field.

Success bar:

- No silent 0% context-fill failures.
- No obvious million-token display bugs.
- Unknown pricing is labelled unknown, not silently treated as proof of zero
  cost.

### Phase 1: eval harness

Steal code-review-graph's benchmark pattern, not its architecture.

Backend/CLI:

- Add `klyne eval --all`.
- Run Context Health against labelled fixture sessions.
- Write `evaluate/reports/summary.md`.
- Track `false_rescue`, `missed_rescue`, `handoff_token_ratio`, and
  `p95_latency_ms`.

This report should become both launch material and a regression guard. The
feature is not credible until it can show where it works and where it fails.

### Phase 2: local deterministic rescue

Backend:

- Add `GET /sessions/{id}/context-health`.
- Return health score, state, reasons, bloat rows, and recommended action.
- Use existing messages, tool calls, tool results, and session usage.
- Compute attention/relevance signals: repeated tool outputs, stale file reads,
  topic shifts, unresolved failures, and recent references to earlier outputs.

Frontend:

- Add `ContextRescue.svelte`.
- Mount it on the session page above the message timeline.
- Show state, top three reasons, top bloat rows, and copy handoff button.

No AI dependency in this phase.

### Phase 3: handoff endpoint

Backend:

- Add `GET /sessions/{id}/handoff`.
- Return deterministic Markdown.
- Include project path, CLI, latest task preview, files touched, commands run,
  known failures, and recent decisions inferred from assistant messages.
- If `.code-review-graph/` exists and `code-review-graph` is available, enrich
  the Markdown with its minimal repo context. Fail closed: if the subprocess
  fails, return the normal JSONL-only handoff.

Frontend:

- Add copy button.
- Add preview modal so users trust the handoff before pasting it.

### Phase 4: AI-enhanced handoff

Backend:

- If BYOK provider exists, improve the deterministic handoff with a small model.
- Keep deterministic fallback.
- Cache per session for 10 minutes.

Frontend:

- Label the source clearly: `local draft` or `AI-polished`.

### Phase 5: explicit MCP decision

After Context Rescue works in the UI, decide whether klyne should also be
an MCP memory layer.

If yes:

- Add an MCP server command.
- Borrow code-review-graph's per-platform installer pattern.
- Start with read-only tools for session history, context health, similar
  sessions, and handoff generation.

If no:

- Keep MCP out of the launch story and focus on the browser UI plus CLI
  audit/eval commands.

### Phase 6: compact trust signal

When a compact event is detected:

- Compare pre-compact handoff with post-compact summary.
- Show preserved decisions and likely-lost details.
- This is a later feature, but it deepens the same Context Rescue story.

## Success criteria

The feature is worth launching only if it can answer these questions in under
10 seconds on a real session:

- Is this session still healthy?
- What exactly is bloating the context?
- Should I continue, compact, or start fresh?
- Can I start fresh without manually reconstructing everything?

If klyne can answer those, it solves a real daily pain for AI coding power
users. If it only lists sessions and token counts, it remains a nice-to-have
dashboard.

## One-sentence product strategy

Do not compete on number of connectors. Compete on being the best local tool
for understanding and rescuing long Claude Code and Codex coding sessions.

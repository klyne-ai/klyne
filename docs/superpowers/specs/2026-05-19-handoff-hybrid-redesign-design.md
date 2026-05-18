# Handoff hybrid redesign — design

> Status: design (approved 2026-05-19). Implementation tracked in `docs/superpowers/plans/2026-05-19-handoff-hybrid-redesign.md`.

## Why this exists

The current `/klyne:handoff` produces a generic dump: long file list sorted by read-count, long command list, untrimmed "last few exchanges", and a one-line "Recent task" that's just the last user message verbatim. The receiving fresh session can't tell what the work is *for*. In a real session, the user had to hand-write the actual continuation context (ticket key, repo-component breakdown, where to start) before pasting — proving the auto-generated portion was inert.

The MCP tool's deterministic / no-AI / air-gapped guarantee is load-bearing for klyne's audit-grade positioning and must survive `/compact` events (JSONL is on disk; in-context state isn't). So the fix can't be "let the model write everything."

## What changes

**One tool, two halves, one slashcommand:**

1. The MCP tool `mcp__klyne__generate_handoff` keeps its name and contract but produces a **richer deterministic skeleton** — relevance-filtered anchor files, regex-mined ticket keys, plan-of-record path, TodoWrite `in_progress`, blockers. Drops the noise sections (Commands run, Last few exchanges, raw Recent task).
2. The slashcommand `/klyne:handoff` becomes a real prompt: it instructs the in-session model to **author three narrative sections** (`Continue from`, `Decided vs Open`, `Read first`) layered on top of the skeleton, with hard guardrails against fabrication.
3. The MCP tool also reports a **`post_compact` flag**. When true, the slashcommand suppresses all narrative authoring and outputs skeleton-only with a banner — because post-compact the model can only see a summary-of-summary and can't be trusted to author narrative.

## Architecture

```
User: /klyne:handoff
  ↓
slashcommand prompt (slashcommands/handoff.md)
  ↓
calls mcp__klyne__generate_handoff (no args)
  ↓
HandoffOutput {
  Markdown:        full deterministic skeleton render (also fine standalone)
  Skeleton:        { ContinueFromHint, PlanOfRecord, AnchorFiles, KnownBlockers, LinkedURLs }
  PostCompact:     bool
  NarrativeSlots:  ["continue_from", "decided_vs_open", "read_first"]
  SessionID, Path, ProjectPath, TokensSource
}
  ↓
slashcommand branch on PostCompact:
  true  → output Markdown VERBATIM in fenced block + post-compact banner. STOP.
  false → author 3 narrative sections, then paste skeleton verbatim below,
          all inside one fenced block. STOP.
```

The MCP tool stays the single source of truth for *what happened*. The slashcommand owns the policy for *how* the model layers intent on top. Direct API callers (none today, but the contract is public) still get a valid deterministic handoff via `Markdown` — they just get the new richer one.

## Skeleton content (deterministic, MCP-rendered)

Render order:

```
# Handoff from session `<short-id>`

Working in `<cwd>` on branch `<branch>`.

## Plan of record
- <path>  (read N× this session; last touched <relative time> ago)

## Anchor files (top 6 by relevance)
| File | State | Last touch |
|------|-------|------------|
| <path> | dirty\|clean | <relative time> |
...
<details><summary>K more files touched (stale / low-relevance)</summary>

- <path>
...
</details>

## Likely ticket / source-of-truth
- <KEY> (mentioned N× in user turns)
- <pasted URL>

## In-progress todos (last TodoWrite)
- [in_progress] <text>
- [pending] <text>

## Recent blockers (last 3 errors)
- <truncated one-line preview>

_Source: <absolute JSONL path>_
```

### Section sources and rules

| Section | Source | Inclusion rule |
|---|---|---|
| Title | `snap.SessionID` | always |
| Working in / branch | `Message.Cwd` + `Message.GitBranch` (already on Message) | always |
| Plan of record | most-recently-read file matching `**/plans/*.md` OR `**/research/**/*.md` OR `**/00-plan.md` | omit section if no match |
| Anchor files | `contexthealth.ScoreFiles` → top 6 non-stale by relevance; stale tail collapsed in `<details>` | omit `<details>` if no stale tail |
| dirty/clean labels | `git status --porcelain` captured into snapshot at render time | label as `unknown` if git call fails |
| Last touch | `LastTouchTs` from relevance verdict, rendered relative ("4m", "1h", "2d") | always when anchor exists |
| Likely ticket | regex `[A-Z]{2,}-\d+` over user-turn `Content`; URL regex `https?://\S+` | only emit a key if it appears ≥ 2× in user turns OR appears inside a pasted URL |
| In-progress todos | parse most recent `TodoWrite` tool call's `input` JSON; emit `in_progress` + `pending` items | omit section if no TodoWrite this session |
| Recent blockers | existing `recentFailures()` walk, cap reduced from 5 → 3 | omit section if no errors |
| Source | `snap.Path` | always |

### Dropped from today's renderer

- **Commands run** — both research agents flagged as noise; `git log`/shell history covers this if needed.
- **Last few exchanges** — replaced by `Continue from` narrative (model-authored, higher signal).
- **Recent task** (= last user message verbatim) — replaced by `Continue from` narrative + regex-mined ticket.

## Narrative slots (model-authored, slashcommand-driven)

Exactly three sections, written by the in-session model under the slashcommand prompt's rules. The model never edits the skeleton; it only adds these three blocks on top.

### `## Continue from`

The directive sentence the next session needs. Ticket ID, branch, cross-component scope, what's done already (so the next session doesn't redo it), what comes next.

Max 4 sentences. No "we did X" history — only "the next session should do Y because Z."

### `## Decided vs Open`

Two short bullet lists: closed decisions on one side, still-open questions on the other.

Max 4 bullets per side. One line each. If the model cannot recall a decision with confidence, it omits it rather than guesses. Empty sections render as `(none)`.

### `## Read first`

2–3 anchor files in priority order, each with a one-line reason. Each cited file MUST appear in the skeleton's anchor list (guardrail enforced by prompt).

### Guardrails (in the slashcommand prompt, not Go code)

```
For each narrative section:

1. Only cite file paths that appear in response.skeleton.anchor_files
   or are visible in your conversation. Never invent paths.
2. Only cite ticket IDs that appear in response.skeleton.likely_ticket
   or are explicitly visible in your conversation. Never invent IDs.
3. "Continue from" max 4 sentences.
4. "Decided vs Open" max 4 bullets per side.
5. "Read first" max 3 entries.
6. If a section would be empty under these rules, write `(none)`.
   Empty is honest; fabricated is not.
7. Never restate skeleton facts.
```

## Composition / final output format

The model emits **one fenced block** with this order:

```markdown
<!-- klyne:handoff v2 -->
# Handoff from session `<id>`

<!-- klyne:authored -->
## Continue from
...
## Decided vs Open
...
## Read first
...

<!-- klyne:deterministic -->
Working in `<cwd>` on branch `<branch>`.

## Plan of record
...
## Anchor files (top 6 by relevance)
...
## Likely ticket / source-of-truth
...
## In-progress todos (last TodoWrite)
...
## Recent blockers (last 3 errors)
...

_Source: <path>_
```

The HTML comment markers (`<!-- klyne:authored -->`, `<!-- klyne:deterministic -->`) let future tooling re-split a pasted handoff into its halves without parsing prose. They render invisibly in markdown viewers.

## Post-compact detection

Server-side: walk the JSONL during snapshot load and record whether a `compact_boundary` line exists. Emit `PostCompact: true` iff:

- `FoundCompact == true`, AND
- the count of parsable user+assistant messages **after** the last `compact_boundary` is `< postCompactTailThreshold` (initial value: **50** messages — enough that "I just ran /compact, then 3 messages later asked for a handoff" trips the flag, but "I /compacted yesterday and have worked through 200 messages since" doesn't).

The threshold lives next to `handoffMaxRecentTurns` in `handoff.go` as a named constant so it's easy to tune from a single place.

When `PostCompact == true`, the slashcommand:
- Outputs `> post-compact: skeleton-only — narrative omitted because the model can no longer see pre-compact turns. Use /klyne:precompact to recover them.` as the first line of the fenced block.
- Outputs `response.Markdown` (the full deterministic skeleton) verbatim below the banner.
- Skips all narrative authoring.

Detection logic reuses the existing scanner pattern from `loadClaudePreCompactMessages` so we don't add a third JSONL walk. Codex sessions reuse the same logic via the existing `LoadPreCompactMessages` dispatch.

## API contract (`HandoffInput` / `HandoffOutput`)

### Input — unchanged interface, deprecated field

```go
type HandoffInput struct {
    SessionID string  // unchanged
    CWD       string  // unchanged
    Scope     string  // DEPRECATED: still accepted, ignored under v2.
                      // Anchor-files now always use the relevance verdict.
                      // Documented as deprecated; emitted constant ignored.
}
```

Keep `Scope` field accepted-but-ignored for one release. Document as deprecated in the JSON schema description. Remove in the release after.

### Output — additive

```go
type HandoffOutput struct {
    // Existing fields (unchanged behavior for direct callers)
    SessionID    string
    Path         string
    ProjectPath  string
    Markdown     string       // NOW: the new richer deterministic skeleton render
    TokensSource int64
    Ambiguous    bool
    Candidates   []CandidateRow

    // NEW
    Skeleton struct {
        Branch           string
        PlanOfRecord     *PlanOfRecordRef    // nil when none found
        AnchorFiles      []AnchorFileRef
        LikelyTickets    []TicketHint
        LinkedURLs       []string
        InProgressTodos  []TodoItem
        PendingTodos     []TodoItem
        KnownBlockers    []string
    }
    PostCompact      bool
    NarrativeSlots   []string  // ["continue_from","decided_vs_open","read_first"] when !PostCompact, [] otherwise
}
```

Direct programmatic callers (none today) get strictly more information than before; their existing `Markdown` consumption still produces a complete, useful handoff with no model dependency.

## Slashcommand rewrite

`internal/mcpserver/slashcommands/handoff.md` becomes:

```markdown
---
description: Generate a hybrid handoff — deterministic skeleton from JSONL + 3 narrative sections you author from live context
---

1. Call `mcp__klyne__generate_handoff` with no arguments.
2. If `response.post_compact` is true:
   - Emit one fenced markdown block.
   - First line of the block: the post-compact banner returned by the server.
   - Then `response.markdown` VERBATIM.
   - STOP. Do not author narrative.
3. Else (`response.post_compact` is false):
   - Emit ONE fenced markdown block containing:
     a. `<!-- klyne:handoff v2 -->` on its own line.
     b. The handoff title from `response.markdown` (first H1 line).
     c. `<!-- klyne:authored -->` marker.
     d. The three narrative sections you write yourself per the rules
        below: `## Continue from`, `## Decided vs Open`, `## Read first`.
     e. `<!-- klyne:deterministic -->` marker.
     f. Everything from `response.markdown` AFTER its first H1 line
        (i.e. the skeleton body), VERBATIM.

Narrative authoring rules (apply to step 3d):
- Only cite file paths that appear in `response.skeleton.anchor_files`
  or in your visible conversation. Never invent paths.
- Only cite ticket IDs that appear in `response.skeleton.likely_tickets`
  or are explicitly visible in your conversation. Never invent IDs.
- `## Continue from` — max 4 sentences. Only "next session should do
  Y because Z." No "we did X" history.
- `## Decided vs Open` — max 4 bullets per side. One line each.
  If you can't recall a decision with confidence, omit it.
- `## Read first` — max 3 entries. Each is one file from the anchor
  list + one short reason. Most-important first.
- If any section would be empty under these rules, write `(none)`.
- Never restate skeleton facts.

Do not summarise, paraphrase, or comment outside the fenced block.
After the fenced block, STOP.
```

## Determinism proof scope

`docs/proof/02-handoff-equivalence/` currently hashes the full handoff markdown for byte-identical reproducibility.

Under v2, the byte-identical guarantee scopes to **`HandoffOutput.Markdown` only** (the deterministic skeleton). The combined slashcommand output is intentionally not byte-stable — narrative is LLM-authored by design.

Updates:
- `claim.md` — rescope to "the deterministic skeleton render (`HandoffOutput.Markdown`) is byte-identical for the same `SessionSnapshot` input at the same git-status capture." Mention narrative sections are explicitly out of scope.
- `proof_test.go` — keep hashing `HandoffOutput.Markdown`. Capture git-status as part of the fixture snapshot so the dirty/clean labels are reproducible.
- Fixture extension: add a `compact_boundary` fixture variant that proves `PostCompact: true` flips correctly when the tail is short, and back to `false` once the tail grows beyond `postCompactTailThreshold`.

## File / package impact

| File | Change |
|---|---|
| `internal/mcpserver/handoff.go` | Rewrite renderer. Add ticket-regex + URL extraction + plan-of-record detector + TodoWrite parser + git-dirty cross-ref + branch surfacing. Drop `commandsRun`, `recentTurns*`, `recentTopic`. Keep `recentFailures` (cap 3). |
| `internal/mcpserver/tool_generate_handoff.go` | Populate the new `Skeleton`, `PostCompact`, `NarrativeSlots` fields. Reuse `LoadPreCompactMessages` shape for post-compact detection (don't re-walk JSONL — add a lightweight `DetectPostCompact(snap)` helper that walks the snapshot's already-loaded messages). |
| `internal/mcpserver/slashcommands/handoff.md` | Full rewrite per above. |
| `internal/mcpserver/handoff_test.go` (new) | Unit tests for each new heuristic in isolation + the assembled skeleton. |
| `internal/mcpserver/tool_generate_handoff_test.go` | Extend with `PostCompact`-true and `PostCompact`-false fixtures and assert `NarrativeSlots` is populated correctly. |
| `docs/proof/02-handoff-equivalence/claim.md` | Rescope claim. |
| `docs/proof/02-handoff-equivalence/proof_test.go` | Capture git-status into fixture; assert hash stability on `HandoffOutput.Markdown` only. |
| `docs/features/handoff.md` | Rewrite to document hybrid + post-compact + narrative slots + guardrails. |
| `internal/contexthealth/relevance.go` | Verify `RelevanceVerdict.RelevantSubset` exposes `LastTouchTs` and `Stale` at the granularity the new renderer needs. Extend if not. |
| `internal/connectors/connector.go` | Verify `Message.GitBranch` is populated by both Claude and Codex connectors. Add if missing. |

## Out of scope (deliberately)

- A `/klyne:handoff --polish` flag that always calls Claude — the slashcommand already gets in-session Claude for free when narrative is enabled; a flag adds surface area without value.
- Cross-session memory ("remember decisions from yesterday's session in today's handoff") — this is `/klyne:bootstrap`'s job, not handoff's.
- Auto-running handoff at session end — separate feature (`/klyne:auto-handoff`) if ever; today's design covers user-initiated only.
- Tried-and-rejected hypotheses as a narrative slot — high hallucination risk, low marginal value over the blockers list.

## Failure modes (catalogued)

| Mode | Mitigation |
|---|---|
| User never typed a ticket key in conversation | `Likely ticket` section omitted; `Continue from` narrative may still mention it if visible in the model's window |
| Heuristic picks the wrong plan-file (multiple plans read) | Pick by `last-touch desc`; if tied, alphabetical first. Documented as "most-recently-read" not "most-relevant" |
| Anchor list is empty (brand-new session, no reads) | Skeleton emits "(no anchor files this session)"; narrative `Read first` becomes `(none)` |
| Model hallucinates a file path | Guardrail 1 catches it at prompt level. Not enforced by code — relies on the model honouring the prompt. Acceptable for a UX flow the user re-reads before pasting |
| Model authors confidently-wrong "Decided" | Guardrail 4 (`omit rather than guess`) + the explicit `(none)` permission removes pressure to fabricate |
| Post-compact false-negative (we say not post-compact but model can't see pre-compact turns anyway) | Threshold of 50 is conservative; worst case model authors a `Continue from` with reduced quality, no banner. The model should self-flag this — slashcommand could add an optional rule "if you cannot see your conversation start, prefix narrative with reduced-confidence note." Cheap to add |
| Post-compact false-positive (we say post-compact but model has plenty of context) | Skeleton-only output; user has full deterministic handoff which is already good. Annoyance only |
| Two compacts in one session | Detect on the most recent `compact_boundary` only — older compacts don't matter |
| Git unavailable / not a repo | `dirty/clean` label becomes `unknown`. Skeleton still renders |

## Migration / backwards compat

- MCP tool name unchanged.
- `HandoffInput.Scope` accepted but ignored (deprecation notice in JSON schema description). Remove in v3.
- `HandoffOutput.Markdown` populated with new richer skeleton render. Direct callers get more signal, no break.
- `HandoffOutput` adds fields. JSON marshalling tolerates added fields for any conforming consumer.
- Slashcommand rewrite is internal — users invoking `/klyne:handoff` see strictly better output (or, post-compact, equivalent output with a clearer banner).

## Acceptance criteria

1. Running `/klyne:handoff` in a non-post-compact session produces a fenced block whose first section is `## Continue from` and whose body includes ticket/branch/repo-component-aware narrative — comparable in quality to the addendum the user had to hand-write today.
2. Running `/klyne:handoff` immediately after `/compact` produces a fenced block prefixed with the post-compact banner and contains only the deterministic skeleton.
3. The deterministic skeleton:
   - Omits `Commands run` and raw `Last few exchanges`.
   - Includes branch, plan-of-record (when present), top-6 anchor files with dirty/clean + last-touch, likely-ticket (when ≥2 mentions OR in URL), TodoWrite `in_progress`+`pending` (when present), recent blockers (cap 3).
4. `HandoffOutput.Markdown` produces byte-identical output for the same `(SessionSnapshot, git-status)` input — proof test green.
5. Going from no-handoff to invoking `/klyne:handoff` costs ≤ 4K output tokens in the non-post-compact path and 0 in the post-compact path.
6. `HandoffInput.Scope` is accepted (no validation error) and silently ignored.

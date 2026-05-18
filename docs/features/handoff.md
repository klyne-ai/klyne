# `/klyne:handoff` — hybrid session handoff

> Status: shipped (v2, 2026-05-19). MCP tool `generate_handoff`. Deterministic skeleton in Go; narrative sections authored by the in-session model under guardrails; post-compact mode falls back to skeleton-only. Byte-identical skeleton render covered by [`docs/proof/02-handoff-equivalence/`](../proof/02-handoff-equivalence/).

Generate a handoff prompt that captures both *what happened* in the current session (JSONL ground truth — files, blockers, tickets, plan of record, todos) and *what should happen next* (model-authored intent — continue-from sentence, decided vs open, read-first list). Paste it into a fresh Claude Code or Codex session.

## Trigger

```text
/klyne:handoff
```

The slashcommand auto-resolves the session from the current working directory and emits a single fenced markdown block. Copy the contents into a fresh session.

## Two modes

**Normal mode (model has live context):** the slashcommand calls `mcp__klyne__generate_handoff`, gets a deterministic skeleton + a `narrative_slots` list, then has the in-session model author three narrative sections on top under strict guardrails (no invented file paths, no invented ticket IDs, omit-rather-than-guess).

**Post-compact mode (model can't see pre-compact turns):** the MCP server flags `post_compact: true` when the JSONL has a `compact_boundary` followed by fewer than 50 turns. The slashcommand suppresses all narrative authoring and emits skeleton-only with a banner pointing at `/klyne:precompact` for raw recovery.

## What the skeleton contains (deterministic)

- **Branch + working directory** — from the message metadata.
- **Plan of record** — most-recently-read planning file (`**/plans/*.md`, `**/research/**/*.md`, `**/00-plan.md`), with read count.
- **Anchor files** (top 6 by `contexthealth` relevance) — labelled dirty/clean from `git status` captured at snapshot-load time, with relative last-touch. Stale tail collapsed in a `<details>` block.
- **Likely ticket / source-of-truth** — keys matching `[A-Z]{2,}-\d+` that appear ≥2× in user turns OR inside a pasted URL; plus the user-pasted URLs themselves.
- **In-progress todos** — parsed from the most recent `TodoWrite` tool call.
- **Recent blockers** — last 3 tool-result errors, truncated per row.
- **Source path** — absolute JSONL path so the receiving session can re-read raw history.

## What the narrative sections contain (model-authored)

- `## Continue from` — max 4 sentences. Ticket, branch, cross-component scope, what's next.
- `## Decided vs Open` — closed decisions on one side, open questions on the other; up to 4 bullets per side.
- `## Read first` — 2–3 anchor files in priority order, each with a one-line reason.

## Ambiguous cwds

Unchanged from v1: when multiple sessions share a project directory, the tool returns `ambiguous: true` with a candidate list. The user picks one and the slashcommand retries with `session_id=<id>`.

## Determinism

The skeleton render (`HandoffOutput.Markdown`) is byte-identical for the same `(SessionSnapshot, GitDirtyFiles)` input. The composed slashcommand output is intentionally not byte-stable — narrative is LLM-authored by design. See [the proof](../proof/02-handoff-equivalence/claim.md).

## Codex sessions

The deterministic skeleton works for Codex transcripts. Post-compact detection currently does NOT — the Codex parser does not yet emit `CompactBoundary` attachments. A Codex transcript with a recent compact will produce a skeleton plus model-authored narrative; the narrative quality may be reduced because the in-session model can't see pre-compact turns. Tracked as a follow-up; the deterministic half remains correct.

## Implementation

- `internal/mcpserver/handoff.go` — extractors + renderer (pure functions of the snapshot).
- `internal/mcpserver/handoff_types.go` — public output types.
- `internal/mcpserver/tool_generate_handoff.go` — MCP handler; assembles `HandoffOutput` (Markdown + Skeleton + PostCompact + NarrativeSlots).
- `internal/mcpserver/data.go` + `data_git.go` — `SessionSnapshot.GitDirtyFiles` capture at load time.
- `internal/mcpserver/slashcommands/handoff.md` — composition + guardrail prompt.

## Migration from v1

`HandoffInput.Scope` is accepted but ignored. The new v2 skeleton uses relevance-verdict filtering unconditionally, making the old `current-topic` mode redundant. Field removal is scheduled for v3.

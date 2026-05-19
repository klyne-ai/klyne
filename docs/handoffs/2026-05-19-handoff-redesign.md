<!-- klyne:handoff v2 -->
# Handoff from session (2026-05-19, handoff-hybrid-redesign)

<!-- klyne:authored -->
## Continue from

The handoff hybrid redesign just shipped end-to-end on branch `init` (16 commits, `00b2fb8` → `0b2677a`). Open work for the next session, in priority order:

1. **Live-smoke the new `/klyne:handoff`** in a real session that has a ticket, a plan-file read, and a TodoWrite — verify the `Continue from / Decided vs Open / Read first` narrative composes correctly on top of the deterministic skeleton, then run it again right after `/compact` to confirm the post-compact banner + skeleton-only path triggers.
2. **Decide on the memory feature.** The friend-agent verdict (saved in auto-memory) recommends REPOSITION as runbooks/ops-guardrail, not KILL — but the user is leaning kill. Pick one of three paths (in `docs/handoffs/2026-05-19-handoff-redesign.md` notes below) and act.
3. **Investigate the UserPromptSubmit hook error** the user is hitting in the current session (the reason they killed it). Likely related to the klyne-hook stub shipped in commit `abc0ec6`. Reproduce, then fix.

## Decided vs Open

**Decided:**
- Handoff v2 lands as hybrid: deterministic skeleton (MCP) + 3 narrative sections (slashcommand-driven model authoring), with post-compact fallback to skeleton-only.
- `HandoffInput.Scope` accepted-but-ignored for v2; removal scheduled for v3.
- Determinism proof rescoped to `HandoffOutput.Markdown` only; narrative is intentionally not byte-stable.

**Open:**
- Memory feature: REPOSITION (runbooks/ops-guardrail framing per friend-agent) vs KILL (user's gut) vs status-quo-plus-instrument. Awaiting user pick.
- `/klyne:tokens` + `/klyne:health` merge — user wants these combined after the handoff redesign ships (now true). Brainstorm scope before designing.
- Codex post-compact detection — Codex parser doesn't emit `CompactBoundary` attachments; deterministic skeleton works, narrative quality degrades gracefully. Tracked as a follow-up.

## Read first

1. `docs/superpowers/specs/2026-05-19-handoff-hybrid-redesign-design.md` — design spec; the source of truth for what we built and why.
2. `docs/features/handoff.md` — the new v2 feature doc; explains the user-facing shape including post-compact behaviour.
3. `internal/mcpserver/slashcommands/handoff.md` — the new slashcommand prompt; this is what drives the in-session model's narrative authoring on top of the MCP skeleton.

<!-- klyne:deterministic -->
Working in `/Users/mohitpatel/Desktop/Project/klyne` on branch `init`.

## Plan of record
- `docs/superpowers/plans/2026-05-19-handoff-hybrid-redesign.md` (the 14-task TDD plan; 2026-05-19)
- `docs/superpowers/specs/2026-05-19-handoff-hybrid-redesign-design.md` (the design spec; 2026-05-19)

## Anchor files (top 8 — recently-edited; no git status capture in this synthetic handoff)

| File | State | Why |
|---|---|---|
| `internal/mcpserver/handoff.go` | clean (committed) | Renderer + all extractors live here; if anything breaks at runtime, start here |
| `internal/mcpserver/handoff_types.go` | clean (committed) | `Skeleton`, `AnchorFileRef`, `TicketHint`, `PlanOfRecordRef`, `TodoItem` types |
| `internal/mcpserver/tool_generate_handoff.go` | clean (committed) | MCP handler — populates `Skeleton`, `PostCompact`, `NarrativeSlots` |
| `internal/mcpserver/slashcommands/handoff.md` | clean (committed) | The slashcommand prompt the in-session model follows |
| `internal/mcpserver/data.go` + `data_git.go` | clean (committed) | `GitDirtyFiles` capture at LoadSnapshot time |
| `internal/mcpserver/handoff_extract_test.go` | clean (committed) | All extractor unit tests |
| `internal/mcpserver/handoff_render_test.go` | clean (committed) | New v2 render-shape tests |
| `docs/proof/02-handoff-equivalence/proof_test.go` | clean (committed) | Rescoped determinism proof (skeleton-only) |

## Likely ticket / source-of-truth
- No external ticket for this work; the spec + plan are the source of truth.

## In-progress todos (from this session)
- (none — all 14 plan tasks completed)

## Recent blockers
- User reports a `UserPromptSubmit` hook error in the current session — reason for killing. Likely tied to the `klyne-hook` stub shipped in `abc0ec6`. Investigate in the new session.

_Source: this handoff was authored manually by Claude in the redesign session, not via `/klyne:handoff` itself — the new slashcommand IS what was just built, so this file dogfoods the v2 layout. Both auto-memory notes are saved at `/Users/mohitpatel/.claude/projects/-Users-mohitpatel-Desktop-Project-klyne/memory/` (the two follow-ups: `project_klyne_followup_tokens_health_merge.md`, `project_klyne_memory_feature_repositioning.md`)._

---

## Memory feature — decision matrix

The user asked the friend agent whether to kill the klyne memory feature. Verdict came back as **REPOSITION**, but the user is leaning **KILL** and explicitly authorised removal "if the agent concludes it does not make any sense." The agent's actual conclusion was "reposition is the right call," which doesn't strictly meet that gate — so memory is still shipped. Pick one:

| Option | What it means | Effort |
|---|---|---|
| **REPOSITION** | Drop the "memory" label; rebrand as runbooks/ops-annotations; merge `/memory` SPA route into `/decisions`; lead with the pre-execution-recall-before-shell-commands hero behaviour | Medium. Pure UI + copy + nav change. The `decisions` table already backs both. |
| **KILL** | Delete `internal/mcpserver/{claude_memory.go,tool_memory.go,tool_memory_crud.go}`, `internal/api/handlers/memory.go`, the `/memory` SPA route, and `docs/features/memory.md`. Strip the CLAUDE.md auto-recall rule. | Low-medium. Mechanical. ~5 Go files + 1 React route + 1 doc. |
| **STATUS QUO + INSTRUMENT** | Keep shipped. Add telemetry on whether the pre-execution `recall` is actually firing. Decide in 2-4 weeks with real data. | Low immediately, real cost is the deferred decision. |

Friend agent's full reasoning is in auto-memory at `~/.claude/projects/-Users-mohitpatel-Desktop-Project-klyne/memory/project_klyne_memory_feature_repositioning.md` — read it first.

## Verification checklist for the new session

When you boot the fresh session in this repo:

1. Run `/klyne:handoff` — should emit ONE fenced block with `<!-- klyne:handoff v2 -->`, three narrative sections (Continue from / Decided vs Open / Read first), then the deterministic skeleton (Working in… / Plan of record / Anchor files table / Likely ticket / In-progress todos / Recent blockers / Source). Should NOT contain `## Commands run` or `## Last few exchanges`.
2. Run `/compact`, then `/klyne:handoff` again — first line of the fenced block should be the post-compact banner `> post-compact: skeleton-only —…`, no narrative sections.
3. Sanity: `GOTOOLCHAIN=auto go build ./...` and `GOTOOLCHAIN=auto go test ./internal/mcpserver/... ./docs/proof/02-handoff-equivalence/` should both be clean.
4. Investigate the `UserPromptSubmit` hook error — check `~/.claude/settings.json` (and project `.claude/settings.json`) for the hook config, run `claude` with stderr visible, and trace the failing exit-code.

## Recommended new-session prompt

Open a fresh Claude Code session in `/Users/mohitpatel/Desktop/Project/klyne` and paste:

> Read `docs/handoffs/2026-05-19-handoff-redesign.md` first — that's the handoff from the previous session. The handoff hybrid redesign is shipped on branch `init` (16 commits, `00b2fb8`..`0b2677a`); I want to (a) live-smoke `/klyne:handoff` end-to-end including a post-compact run, (b) decide on the memory feature using the matrix in that doc, and (c) figure out the `UserPromptSubmit` hook error that's been throwing in the previous session. Start with (a) — run `/klyne:handoff` in this session and we'll inspect the output together.

---
description: Generate a hybrid handoff — deterministic skeleton from JSONL + 3 narrative sections you author from live context
---

1. Call `mcp__klyne__generate_handoff` with no arguments — let it auto-resolve the session from the current working directory.

2. **If `response.post_compact` is true:**
   - Emit ONE fenced markdown block.
   - First line of the block (before the H1) MUST be exactly:
     `> post-compact: skeleton-only — narrative omitted because the model can no longer see pre-compact turns. Use /klyne:precompact to recover them.`
   - Then output `response.markdown` VERBATIM.
   - STOP. Do not author narrative.

3. **If `response.post_compact` is false:**
   - Emit ONE fenced markdown block containing, in this exact order:
     a. `<!-- klyne:handoff v2 -->` on its own line.
     b. The handoff H1 line from `response.markdown` (the first line starting with `# Handoff from session`).
     c. A blank line.
     d. `<!-- klyne:authored -->` on its own line.
     e. The three narrative sections you author yourself per the rules below: `## Continue from`, `## Decided vs Open`, `## Read first`.
     f. A blank line.
     g. `<!-- klyne:deterministic -->` on its own line.
     h. Everything from `response.markdown` AFTER its first H1 line (i.e. the skeleton body starting with `Working in ...`), VERBATIM.

**Narrative authoring rules (apply to step 3e):**

- Only cite file paths that appear in `response.skeleton.anchor_files` or in your visible conversation. Never invent paths.
- Only cite ticket IDs that appear in `response.skeleton.likely_tickets` or are explicitly visible in your conversation. Never invent IDs.
- `## Continue from` — max 4 sentences. Only "next session should do Y because Z." No "we did X" history.
- `## Decided vs Open` — max 4 bullets per side. One line each. If you cannot recall a decision with confidence, omit it. Empty side renders as `(none)`.
- `## Read first` — max 3 entries. Each is one file from the anchor list plus one short reason. Most-important first.
- If any section would be empty under these rules, write `(none)`. Empty is honest; fabricated is not.
- Never restate skeleton facts (they appear below).

Do not summarise, paraphrase, or comment outside the fenced block. After the fenced block, STOP.

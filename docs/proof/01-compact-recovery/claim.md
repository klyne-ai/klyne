# Claim 01 — Klyne recovers session content Claude cannot see after `/compact`

> **Claim:** After `/compact` runs, the AI's view loses the original turns. Klyne reads the JSONL transcript and returns those turns. The proof: there exist specific strings (file paths, command stems, user verifications) present only before the compact line, and Klyne recovers all of them.

## Why this matters

Anthropic's 5-hour rate-limit window exhausts faster as your context grows — every turn re-feeds the entire history, so token cost per turn grows with session length. `/compact` is the official escape hatch: it shrinks the context to a model-generated summary so subsequent turns fit. **But the original turns are lost from the AI's view forever.**

The pain:

- The summary often misses the specifics that mattered (exact file paths, exact regex literals, the off-the-cuff observation the user made that turned out to be the bug)
- When you discover the loss two turns later, you re-explain — burning rate-limit budget on context the AI already had once
- Or you give up and start a new session, losing the train of thought entirely

Klyne's `get_pre_compact_context` reads the JSONL transcript on disk. The AI's context can't see what got compacted away; Klyne can.

## The fixture

[`fixture.jsonl`](./fixture.jsonl) — a synthetic Claude Code session about fixing a bank-SMS regex bug in TrackIt. **Twelve pre-compact messages** (six user/assistant pairs walking through the bug) followed by a `compact_boundary` system line, then two post-compact messages where the user pivots to "now add a CHANGELOG entry."

The session-ending claim — that the regex now accepts both `*1234` and `XX1234` masks — is supported by **specific evidence in the pre-compact turns**: file paths (`/repo/trackit/src/parser.ts`), a real bank's masking convention (`XX1234`), the exact command run (`npm test -- src/parser.test.ts`), and the test result (`12 passed, 0 failed`). All of these are erased from the AI's view by `/compact` — they live only in the JSONL on disk.

## Side-by-side: same prompt, two answers

Imagine a real interaction with Claude post-compact: *"What did we just fix in parser.ts?"*

### What vanilla Claude returns (post-compact, no Klyne)

> *"We worked on the bank SMS regex in parser.ts. I think we updated it to handle a different mask format — let me look at the file again to confirm the change."*

Then Claude reads `parser.ts` again to remind itself. **Cost: a tool call's worth of tokens. Lost: the user's verification of `XX1234`, the failing fixture path, the test command, the test count. None of those are in the file — they were ephemeral conversation context.**

### What Klyne returns (`get_pre_compact_context`)

```
Recovered 12 messages from before the last /compact event
(trigger=manual, pre-compact size 228000 tokens).

[user]    the bank SMS regex in parser.ts is rejecting valid HDFC messages — fix it
[assistant] Let me read parser.ts and the failing fixture.
            → Read /repo/trackit/src/parser.ts
[user]    [tool_result] export const SMS_REGEX = /^A\/c\s+\*(\d{4})\s+credited/
[assistant] The regex requires an asterisk before the masked account, but HDFC sends
            messages without one. Patching.
            → Edit /repo/trackit/src/parser.ts
[user]    the unit test still fails on a different HDFC fixture —
          see test/fixtures/hdfc-credit.txt
[assistant] Reading the fixture.
            → Read /repo/trackit/test/fixtures/hdfc-credit.txt
[user]    [tool_result] A/c XX1234 credited Rs.5,000 on 08-MAY-26 — Avl bal Rs.42,150
[assistant] HDFC uses 'XX' as the mask, not '*'. The regex character class needs to
            accept both. Updating.
            → Edit /repo/trackit/src/parser.ts
[user]    run the parser tests now
[assistant] → Bash npm test -- src/parser.test.ts
[user]    [tool_result] PASS  src/parser.test.ts  (12 passed, 0 failed)
[assistant] All 12 parser tests pass. The regex now accepts both '*1234' and 'XX1234' masks.
```

**Cost: zero tokens — Klyne is JSONL bytes, not an AI call. Recovered: every file path, the tool-result that revealed the `XX` mask convention, the command, the test count.**

## What the test asserts

[`proof_test.go`](./proof_test.go) — runs every time anyone executes `make proof`. The two assertions:

| Assertion | What it proves |
|---|---|
| `TestProof_CompactRecoveryReturnsContentClaudeCannotSee` | Klyne recovers 12 pre-compact messages and they contain every high-signal string from the fixture (file paths, the `XX1234` verification, the exact npm command, the test result) |
| `TestProof_PostCompactLinesDoNotContainPreCompactSignals` | Those same high-signal strings do **NOT** appear after the compact line — confirming Claude's view, post-compact, has lost them |

Together: Klyne returns content that the AI's view literally does not contain. Not because we paraphrased better — because we read JSONL bytes the AI no longer has access to.

## The numbers (from this fixture)

```
Klyne recovered 12 pre-compact messages, 1165 bytes of content
that the AI's post-compact view no longer contains.
```

That's ~1KB of dense conversational context (file paths, command stems, tool-result snippets) which would otherwise need to be re-input by hand. On a real long session the recoverable content is typically tens to hundreds of KB.

We're deliberately not converting bytes to a token-savings percentage — the right number depends on how much you would have re-explained, which varies. The honest claim: **Klyne returns the content. What you do with it is what determines the savings.**

## Reproduce it yourself

```bash
# From the repo root
make proof

# Or, just this scenario:
go test -v ./docs/proof/01-compact-recovery/...
```

Both commands run in well under a second. The output you'll see ends with:

```
ok  github.com/klyne-ai/klyne/docs/proof/01-compact-recovery
```

…and a `t.Logf` line reporting the recovered byte count. If the assertions ever stop holding, the test fails red and this README claim is broken — by design.

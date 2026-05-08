# 05 · Risk Register (Multi-Agent Execution)

15 risks specific to running this build with parallel AI agents. Owners, likelihoods, impacts, and structural mitigations.

| ID | Risk | Likelihood | Impact | Owner | Mitigation |
|---|---|---|---|---|---|
| **R1** | Two agents redefine the same type with subtle differences (e.g., `Message.Tokens` int vs int64) | High | High | W0 | All cross-stream types live in W0-owned files. Any change requires labeled `contract-change` PR. CI rejects unlabeled edits to contract files. |
| **R2** | `internal/store/migrations/*.sql` collisions if W2 or W3 try to "fix" a missing column | High | High | W0 | Migration files are append-only and W0-owned. New tables go in `004_*.sql` etc. — added in a fresh file by whichever workstream introduces the need. |
| **R3** | W7 + W8 + W15 all want to register routes on the chi router — last writer wins | High | Medium | W7 | W7 exposes `Mount(r chi.Router, deps Deps)` and a `RegisterHook(name string, fn func(chi.Router))` callback. W8 and W15 register via the hook, never by editing W7's files. |
| **R4** | TS types drift from Go DTOs silently | High | High | W13 | `ui/scripts/check-contracts.ts` reflects on the parsed Go contract file (using a tiny Go AST tool W0 ships in `tools/dump-contracts/`) and asserts every TS type matches. Runs in CI. |
| **R5** | fsnotify watcher fights the bulk back-fill on first run, double-ingesting | Medium | Medium | W4 / W5 | Each connector implements a "warm-up" mode: on `Watch` start, take an inode/offset snapshot, replay file from byte 0 → DB, then attach watcher seeking from snapshot offset. Tests in W4/W5 cover this. |
| **R6** | SSE hub deadlock when subscriber count × event rate exceeds buffer | Medium | High | W8 | Per-subscriber buffered channel + non-blocking send + drop-with-log policy. Stress test in W8 with 100 subs × 1000 evts/s. |
| **R7** | AI provider latency stalls the writer goroutine if mis-wired | Medium | High | W11 / W12 | Summaries run in their own goroutine pool subscribed to `MsgNew` events, never inline with the writer. Document this invariant in `internal/app/app.go`. |
| **R8** | Race between W14 and W15 over `MessageBubble.svelte` if compact-event rendering bleeds into the bubble | Medium | Medium | W15 | W15 owns its own `RestoreContext.svelte` and uses `MessageBubble` read-only via import. If a bubble change is needed, W15 opens a PR to W14. |
| **R9** | fsnotify on Linux misses events under heavy load (inotify queue overflow) | Low | High | W4 / W5 | Periodic (every 30 s) filesystem scan to reconcile against watched set; backstops fsnotify. |
| **R10** | Codex JSONL format changes mid-development | Medium | Medium | W5 | Spec §13 risk #2 — keep parser tolerant; pin format version in test fixtures; v1 supports current Codex output as of May 2026. |
| **R11** | Anthropic ToS changes before launch | Low | Critical | Human | We never reuse OAuth (§17 #2). Even if Anthropic restricts more, we're already conservative. Re-check the ToS page on Day 13 before tagging. |
| **R12** | Wave 3 integration discovers a contract bug that requires a W0 ripple | Medium | High | Human | Allocate a "contract revision" PR slot in Wave 3. Treat it as a blocking dep: when a contract changes, every affected workstream gets an automatic update PR. |
| **R13** | Solo dev becomes the bottleneck reviewing 7 agent PRs in Wave 1 | High | Medium | Human | Use `code-review:code-review` skill on each PR; batch reviews twice/day; require agents to self-review with `superpowers:requesting-code-review` before opening PR. |
| **R14** | Worktree confusion — agent commits to wrong branch | Medium | Medium | Human | Each agent gets a worktree at `~/klyne-worktrees/W{N}-{slug}` on a branch `wave{X}/W{N}-{slug}`. Use `superpowers:using-git-worktrees`. CI rejects pushes to `main` that aren't via a labeled PR. |
| **R15** | Coverage gate gaming — agent writes shallow tests to hit 80% | Medium | High | Human | Coverage is necessary, not sufficient. Manual review of every test file with `superpowers:requesting-code-review` checklist. |

---

## Top-3 watchlist (re-read before every wave)

1. **R1 / R4 / R12 — Contract drift.** Every cross-stream type lives in exactly one W0-owned file; contract changes are labeled PRs reviewed by you; never silent edits.
2. **R3 — Route registration collisions.** Use the `Mount`/`RegisterHook` pattern from W7. Never let W8 or W15 edit W7's files directly.
3. **R7 — Goroutine stall by provider latency.** Summaries run in their own pool, subscribed to events, never inline with the writer.

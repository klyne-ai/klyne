# Claim — handoff deterministic skeleton equivalence

The deterministic skeleton (`mcpserver.RenderHandoff`, also exposed as `HandoffOutput.Markdown`) is **byte-identical across runs** for the same `(SessionSnapshot, GitDirtyFiles)` input.

What this covers:
- The skeleton body — branch, plan-of-record, anchor files, likely tickets, in-progress todos, recent blockers, source path.
- The render is a pure function of `*SessionSnapshot` plus its captured `GitDirtyFiles` set, with `time.Now()` injected at the top level. Internal calls use the snapshot's load-time `now` so the rendered relative-time strings ("4m", "1h") are stable for a fixed input.

What this does NOT cover:
- The slashcommand's composed output (narrative sections + skeleton). Narrative is intentionally LLM-authored and not byte-stable; that is by design.
- The `PostCompact` flag's value when run against an evolving JSONL — the flag is a deterministic function of the snapshot, but a snapshot taken later may have more post-boundary turns and thus a different flag.

The proof tests below assert structural completeness, byte-equivalence on the skeleton, and correct `PostCompact` behaviour on a fixture with a compact boundary.

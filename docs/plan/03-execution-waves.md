# 03 · Execution Waves

Each wave can start when **all dependencies in the previous wave are merged to `main`**. Within a wave, agents fan out to maximum parallelism.

---

## Wave 0 — Bootstrap (Day 1, ~2d wallclock)

- **Workstreams:** W0
- **Agents in parallel:** 1
- **Merge point:** every contract file compiles, `go test ./...` green, CI green, `docs/contracts.md` complete and **reviewed by you (the human)**.
- **Integration risk:** **highest of any wave.** Drift here cascades to every downstream workstream.
- **Mitigation:** do NOT fan out until you sight-read `connector.go`, `contracts.go`, `sse_events.go`, the three migration SQL files, and `pricing_schema.go`. 30 minutes of human review now saves days of rework later.

---

## Wave 1 — Foundation fan-out (Days 2–4, 7 agents)

- **Workstreams:** W1, W4, W5, W6, W9, W10, W13
- **Agents in parallel:** 7 (one per workstream, each in its own git worktree)
- **Merge point:** all 7 land green to `main`. Use the `superpowers:using-git-worktrees` and `superpowers:dispatching-parallel-agents` skills.
- **Integration risk:** **medium.** Contracts make collisions structurally unlikely, but watch for:
  - W1 + W9 both reasoning about `~/.klyne/` paths logically — W1 owns DB file, W9 owns pricing override file. Document path ownership in `docs/contracts.md`.
  - W4 + W5 both depending on `examples/sample-jsonl/` fixtures — fixtures committed by W0 are **read-only** here.
  - W13 will inevitably need a TS type missing from W0 — agent should open a PR back to W0's contract files (treated as labeled `contract-change` PR, not silent edit).

---

## Wave 2 — Mid-layer fan-out (Days 4–7, up to 6 agents)

- **Workstreams:** W2, W3, W7, W8, W11, W14
- **Agents in parallel:** up to 6 (some take dependencies on Wave 1 outputs — see [`02-dependency-graph.md`](./02-dependency-graph.md))
  - W2 starts when W1 lands.
  - W3 starts when W2 lands. (Or have one agent own W2+W3 sequentially — see Risk register R1.)
  - W7 starts when W2, W3, W6 are all green.
  - W8 starts when W7 has merged its `Mount` hook.
  - W11 starts when W3, W8, W10 are green.
  - W14 starts when W13 lands and W7+W8 are partially up (mocks are fine until they aren't).
- **Merge point:** `klyne start` (W12 in Wave 3) is buildable. `make dev` runs daemon + UI together with hot reload.
- **Integration risk:** **medium-high** — this is where SSE event shapes get exercised end-to-end. If W8's `MsgNew` payload doesn't match what W14's `SessionList` expects, both think the other is wrong.
- **Mitigation:** `ui/scripts/check-contracts.ts` (built in W13) runs in CI on every PR and rejects type drift.

---

## Wave 3 — Integration (Days 8–10, 2 agents)

- **Workstreams:** W12, W15
- **Agents in parallel:** 2. **W12 must be one agent only**, because it touches every package's exported API.
- **Merge point:** the four user flows in spec §6 work end-to-end on a fresh machine with fixture data. `klyne doctor` green.
- **Integration risk:** **high.** This is when bugs that survived unit tests because mocks lied surface for real.
- **Mitigation:** allocate a full day of buffer. Use `superpowers:systematic-debugging` and `verification-loop` skills.

---

## Wave 4 — Hardening (Days 11–13, 2 agents)

- **Workstreams:** W16, W17
- **Agents in parallel:** 2.
- **Merge point:** every spec §12 budget green; release artifacts produced; `install.sh` works on a fresh Linux VM and on a Mac that's not the dev machine.
- **Integration risk:** **low** (each workstream owns disjoint files), but performance tuning may require touching hotspots in W2/W3/W4/W14. Treat those as scoped PRs reviewed by the original owner before merge.

---

## Wave 5 — Ship (Day 14, ad-hoc)

- **Workstreams:** W18
- **Agents in parallel:** 1–N depending on bug volume.
- **Merge point:** `v1.0.0` tag pushed.
- **Integration risk:** unknowable until alpha.
- **Mitigation:** the buffer day, `superpowers:verification-before-completion`, and the discipline to scope-cut bugs to v1.0.1 if they don't block the four flows in spec §6.

---

## Wave-boundary checklist (run before opening the next wave)

- [ ] All workstreams in current wave merged to `main` via PR (no force-pushes).
- [ ] CI green on `main` HEAD.
- [ ] Coverage ≥ 80% on changed lines for every PR in the wave.
- [ ] `docs/contracts.md` is up to date with any contract changes that landed.
- [ ] Manual smoke: `make dev` starts daemon + UI without errors.
- [ ] No open `contract-change` labels left untriaged.
- [ ] You (the human) reviewed every PR with `code-review:code-review` skill.

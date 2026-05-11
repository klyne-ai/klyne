# 06 · Kickoff Sequence (do this today)

These 5 dispatches start the project. Run them in order. **Do not skip ahead.**

---

## Step 1 · Initialize the git repo and worktree base

```bash
cd /Users/mohitpatel/Desktop/Project/klyne

git init
git checkout -b main

git add compass_artifact_wf-d189e421-ff1d-443c-95b8-19b52fcd59b4_text_markdown.md
git add docs/
git commit -m "chore: vendor shipping spec v1.0 + multi-agent build plan"

git remote add origin <your-private-github-url>
git push -u origin main

mkdir -p ~/klyne-worktrees
```

---

## Step 2 · Dispatch W0 (bootstrap agent)

Open a fresh Claude Code session. Use **Claude Opus 4.7 (1M context)**.

Paste the prompt from [`workstreams/W00-bootstrap.md`](./workstreams/W00-bootstrap.md).

**Do not parallelize anything else until W0 lands.** Expected: 1–2 days wallclock.

**Human review checklist for W0 PR before merging:**
- [ ] Sight-read `internal/connectors/connector.go` — interface + types make sense.
- [ ] Sight-read `internal/api/contracts.go` — every route DTO present.
- [ ] Sight-read `internal/api/sse_events.go` — all 6 events typed.
- [ ] Sight-read the 3 SQL migration files.
- [ ] Sight-read `internal/cost/pricing_schema.go`.
- [ ] `make ci` passes locally.
- [ ] `docs/contracts.md` references each contract by file:line and pins the spec section.

---

## Step 3 · After W0 merges: create 7 Wave-1 worktrees

```bash
cd /Users/mohitpatel/Desktop/Project/klyne

git worktree add ~/klyne-worktrees/W1-store     wave1/W1-store
git worktree add ~/klyne-worktrees/W4-claude    wave1/W4-claude
git worktree add ~/klyne-worktrees/W5-codex     wave1/W5-codex
git worktree add ~/klyne-worktrees/W6-config    wave1/W6-config
git worktree add ~/klyne-worktrees/W9-cost      wave1/W9-cost
git worktree add ~/klyne-worktrees/W10-ai       wave1/W10-ai
git worktree add ~/klyne-worktrees/W13-ui-shell wave1/W13-ui-shell
```

---

## Step 4 · Fan out 7 Wave-1 agents

Open one Claude Code (or Codex CLI) session per worktree, set `cwd` to the worktree path, paste the matching prompt:

| Worktree | Prompt file | Recommended model |
|---|---|---|
| `~/klyne-worktrees/W1-store` | [`workstreams/W01-store-db.md`](./workstreams/W01-store-db.md) | Sonnet |
| `~/klyne-worktrees/W4-claude` | [`workstreams/W04-connector-claude.md`](./workstreams/W04-connector-claude.md) | Sonnet |
| `~/klyne-worktrees/W5-codex` | [`workstreams/W05-connector-codex.md`](./workstreams/W05-connector-codex.md) | Sonnet (or **Codex CLI** for dogfooding) |
| `~/klyne-worktrees/W6-config` | [`workstreams/W06-config.md`](./workstreams/W06-config.md) | Sonnet |
| `~/klyne-worktrees/W9-cost` | [`workstreams/W09-cost-engine.md`](./workstreams/W09-cost-engine.md) | Sonnet |
| `~/klyne-worktrees/W10-ai` | [`workstreams/W10-ai-providers.md`](./workstreams/W10-ai-providers.md) | Sonnet + `claude-api` skill |
| `~/klyne-worktrees/W13-ui-shell` | [`workstreams/W13-ui-shell.md`](./workstreams/W13-ui-shell.md) | Sonnet + `frontend-patterns` |

**Tip:** Use Codex CLI for one of W4 or W5 — you'll dogfood the cross-CLI story and surface bugs faster.

---

## Step 5 · Set up your review cadence

- **Twice daily** (e.g., 11am and 5pm), use `code-review:code-review` skill on every open PR.
- **End of each wave**, use `superpowers:verification-before-completion` and run `make ci` against the merged branch.
- Log integration issues as **`contract-change`** candidates and triage immediately. A 30-minute contract patch in Wave 1 saves a 3-day rework in Wave 3.
- Update the Status Board in [`README.md`](./README.md) as workstreams progress.

---

## After Wave 1: Wave 2 kickoff (symmetric)

When all 7 Wave-1 PRs are merged:

```bash
git worktree add ~/klyne-worktrees/W2-store-daos      wave2/W2-store-daos
git worktree add ~/klyne-worktrees/W3-store-search    wave2/W3-store-search
git worktree add ~/klyne-worktrees/W7-api-handlers    wave2/W7-api-handlers
git worktree add ~/klyne-worktrees/W8-sse-hub         wave2/W8-sse-hub
git worktree add ~/klyne-worktrees/W11-selector       wave2/W11-selector
git worktree add ~/klyne-worktrees/W14-ui-components  wave2/W14-ui-components
```

Then dispatch using the matching `workstreams/W##-*.md` files.

**Important:** within Wave 2, some workstreams gate on others within the same wave (W3 → W2, W7 → W2+W3+W6, W8 → W7, W11 → W3+W8+W10, W14 → W13+W7+W8). Stagger dispatches accordingly or have agents start with mocks while real deps land.

---

## Useful skills to invoke at each stage

| Stage | Skill |
|---|---|
| Worktree setup | `superpowers:using-git-worktrees` |
| Parallel dispatch | `superpowers:dispatching-parallel-agents` |
| Per-workstream execution | `superpowers:test-driven-development`, `superpowers:executing-plans` |
| PR review | `code-review:code-review`, `superpowers:requesting-code-review` |
| Pre-merge | `superpowers:verification-before-completion` |
| When stuck | `superpowers:systematic-debugging` |
| Final ship | `superpowers:finishing-a-development-branch` |

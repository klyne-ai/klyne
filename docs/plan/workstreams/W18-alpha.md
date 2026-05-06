# W18 · Alpha Bug-Bash + Final Cut

> **Wave:** 5 · **Effort:** S · **Depends on:** W17 · **Recommended skills:** `superpowers:systematic-debugging` + `general-purpose` (one agent per bug, ad-hoc)

---

## Universal preamble

You are working on the agentdeck repo. Spec at `compass_artifact_wf-d189e421-ff1d-443c-95b8-19b52fcd59b4_text_markdown.md`. §18 decisions are LOCKED. TDD-first; ≥80% coverage. After every change >30 lines run `make ci`. Use `superpowers:verification-before-completion` before claiming done.

**Single-writer rule:** as bugs are assigned, agents may need to touch files outside their historical workstream. **Require explicit owner sign-off** (or human review) before changing files outside your dispatched bug's scope.

---

## Goal

Run spec §15 Day 14: 5-user alpha, fix top 5 bugs, tag `v1.0.0`.

This is human-led with ad-hoc agent dispatches per bug.

---

## Process

### 1. Distribute alpha to 5 users
- Send the install script + brief usage notes.
- Ask them to run their normal Claude Code / Codex sessions for 24 hours with `agentdeck` running.
- Collect feedback via a private GitHub issue label `alpha-feedback`.

### 2. Triage
- Classify every issue as: `must-fix-v1` (blocks one of spec §6 flows), `nice-to-have-v1.0.1`, or `feature-v1.1`.
- **Only `must-fix-v1` blocks tagging.**

### 3. Per-bug agent dispatch
Use the per-bug template below.

### 4. Tag
Once `must-fix-v1` queue is empty:
```bash
git tag v1.0.0
git push --tags
```
- Verify release CI green.
- Update `docs/CHANGELOG.md`.
- Flip repo public.
- Post launch (HN / Reddit / X — see spec §4 launch sequence).

---

## Per-bug agent dispatch template

> **Goal:** Fix bug `<bug-id>` reported by alpha user `<name>`. Root-cause first using `superpowers:systematic-debugging`. Write a regression test, then fix. **Do not expand scope beyond the reported bug.**
>
> **Spec context:** `<which spec section the bug touches>`
>
> **Owned paths:** whatever the bug touches; **obtain explicit owner sign-off** if you must change a file outside the historical workstream that owns it.
>
> **Acceptance criteria:**
> - Regression test fails before fix, passes after.
> - Coverage doesn't regress.
> - All four spec §6 flows still work end-to-end.
> - Reviewer signs off via PR.
>
> **Hard boundaries:**
> - Do NOT refactor adjacent code.
> - Do NOT add features.
> - Do NOT touch CI/release configs unless the bug is in CI/release.

---

## Acceptance criteria for v1.0.0 tag

Spec §14 Day-14 row:
- [ ] 50 stars (alpha-private — N/A pre-public).
- [ ] All four spec §6 flows work for all 5 alpha users.
- [ ] No `must-fix-v1` bugs open.
- [ ] All spec §12 perf budgets green (W16 bench in CI).
- [ ] `bash scripts/install.sh` works on fresh Linux + macOS VMs.
- [ ] `agentdeck doctor` returns green on all 3 OSes.
- [ ] README + landing page proofread.
- [ ] CHANGELOG.md complete.

---

## Public launch checklist (Day 15)

- [ ] Flip repo public on GitHub.
- [ ] Post `Show HN: agentdeck — mission control for Claude Code + Codex`.
- [ ] Post on r/ClaudeAI and r/ChatGPTCoding.
- [ ] Post 30-second demo on X / Twitter.
- [ ] Open "request a connector" issue templates ready (W0 already shipped these).

---

## Done

When `v1.0.0` is tagged and pushed, repo is public, and the launch posts are live.

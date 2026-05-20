# Productivity Dashboard — Prototype Results (2026-05-20)

Built overnight per the user's instruction ("a real working prototype by morning"). Verified end-to-end against the **real** `~/.klyne/klyne.db` for the 2026-05-19 window. This file is the honest what-works / what's-stubbed / known-bugs record.

## How to run it

The user's live klyne daemon occupies `127.0.0.1:7878` — do **not** kill it. To see the prototype:

1. From the worktree root `/Users/mohitpatel/Desktop/Project/klyne/.worktrees/productivity-dashboard`:
   `GOTOOLCHAIN=go1.25.3 go build ./...` (toolchain must be pinned; `go1.21` on PATH is too old).
2. Stop the live daemon OR run this build pointed at a free port (config `~/.klyne/config.toml` `addr`). Then open `/productivity` in the UI, or `curl 'localhost:<port>/productivity?since=<ms>&until=<ms>'`.
3. `since`/`until` are epoch-ms; omit for "today". For the 2026-05-19 demo pass that day's local-midnight bounds.

Reproduce the exact verification: a throwaway read-only harness was used (`internal/api/handlers/zzz_verifyreal_test.go`) and intentionally **not committed/deleted**; recreate it to dump `/tmp/prodreport.json` if needed.

## What works (verified against real data, 2026-05-19)

- `GET /productivity` returns HTTP 200 with the full `productivity.Report` JSON.
- `day` correctly = `2026-05-19` (was a bug, fixed — B1).
- Repo discovery canonicalizes: **7 real services**, worktrees grouped under one canonical repo, non-git parent dirs (`Desktop`/`Learning`/`Project`) excluded (was 28 bogus services — fixed, B2).
- `klyne` = one service with `init` (pushed, ahead 9, 699 session-min) + `feat/productivity-dashboard` worktree branch under it.
- The day's real work surfaces with **session-anchored minutes**: consultation-service `feat/labstack-integration` 15 commits / 141 min (+ `master` 55 min), oms-service `feat/CLI-1397-labstack-auto-refund` 207 min, operations-app `feature/CLI-1325-labstack-integration` 104 min, product-service `feature/cli-1336-labstack-test-id`.
- Identity filter works: user commits (`mohit-clinikk <mohit@clinikk.com>`, `mohitpatel9753@gmail.com`) included; **Ravi Ranjan and Jenkins correctly excluded**.
- Ship-state machine, `unpushed` / `done-uncommitted` risk signals populate.
- Narratives are git-grounded: contain short SHAs, **zero `"PR #"`** substrings (anti-hallucination guard holds — directly fixes the original "PR #57" failure).
- Reflection layer: when no 2026-05-19 reflection existed the report returned `reflection_status:"missing"` + nudge; after reflections were recorded for that date a later run returned `"current"` with empty nudge — **both Layer-3 paths proven**.
- Rough read-only `/productivity` Svelte page renders the grouped Service→Branch→narrative view (deliberately unstyled; real UI/UX is a separate brainstorm per spec scope).
- `go test ./internal/productivity/... ./internal/api/... ./internal/store/...` pass, except one **pre-existing, unrelated** failure (`TestNewRouter_AllRoutesPresent/GET_/settings`, reproduced on the untouched base commit).

## Fixed since the first prototype pass (2026-05-20)

- **Ahead-count inflation FIXED.** `aheadBehind`/`hasUpstream` treated "no local upstream tracking ref" as "no remote", reporting a detached/untracked branch's whole history as unpushed (the bogus "4192 commits ahead"). Now falls back through `origin/<branch>` → remote default ref before the whole-history case. Verified: counts are now meaningful (44, 35, 24…). Unit test `TestScanRepo_PushedWithoutLocalTracking` added.
- **UI render hang FIXED.** Detached worktrees all report branch name `HEAD` and a repo can have two identical risk strings, so the keyed `{#each}` blocks threw `each_key_duplicate` and the page hung on "loading…". Keyed by index. Also added optional `?since=&until=` epoch-ms window to the page URL.
- **`ticket_id` is NOT broken** (an earlier draft of this file claimed it was — that was wrong; it was only ever observed on genuinely ticketless branches). Verified: `feat/CLI-1397-…` → `CLI-1397`, `feature/cli-1336-…` → `CLI-1336` parse correctly.
- **Production half MERGED** (branches `feat/pd-snapshot` + `feat/pd-worklog`): session-end git snapshot capture (D6 snapshot half — `productivity.CaptureSessionSnapshots`, `store.InsertGitSnapshot`, wired non-fatally into both session-end paths) and the 7 worklog-reflection improvements (§7.2, in `internal/worklog`, consuming this substrate).

## Honest caveats / known issues (NOT masked)

1. **Ship-states read `pushed-to-remote`, not `committed-local-only`** for the labstack repos. The original spec success-criterion 1 expected consultation-service CLI-1396 as committed-local; it is now the next day and that work has since been pushed — the dashboard correctly reports *current* truth. Data drift, not a defect; the committed-local path is exercised by unit tests and the klyne feature branches.
2. **Two `klyne-showcase-*` temp repos under `os.TempDir()` still appear as services.** Left intentionally (filtering by tempdir prefix risked excluding legitimate repos); cosmetic noise only.
3. **Identity seed is hardcoded** (`mohitpatel9753@gmail.com`, `coders@clinikk.com`, `mohit@clinikk.com`). Config-wiring is a documented follow-up; the seed is the placeholder.
4. **klyne's own feature branches show ~67 commits each with identical narratives** — `feat/pd-snapshot`/`feat/pd-worklog`/`feat/productivity-dashboard` share most history, so per-branch dedup of shared commits is a refinement worth doing.

## Stubbed — remaining follow-up (spec §11)

- **`dashboard_cache` memoization** — the dashboard computes live each request (acceptable at current repo count; caching is a perf follow-up). This is now the ONLY remaining §11 stub — snapshot capture and the worklog improvements are built and merged.

## Process deviation (disclosed)

Subagent-driven-development was run in a condensed form: implementer subagents built in coherent chunks with TDD + commits; the controller performed spec/quality review and end-to-end verification (instead of separate per-task reviewer subagents) to fit the overnight budget. All work is on isolated branch `feat/productivity-dashboard` (worktree), never on `init`. Spec + plan await user review per the brainstorming gate before any merge.

# Contributing to klyne

Thanks for your interest! klyne is MIT-licensed and welcomes outside
contributions. This document explains the local dev loop and what kind
of changes are easiest to land.

## Local development

```bash
# Prereqs: Go 1.25+, Node 20+, npm, fsnotify-compatible OS (macOS / Linux).

# 1. Build everything (UI + Go binaries).
make build

# 2. Run the daemon.
./bin/klyne daemon

# 3. In a second terminal, run the UI dev server (hot reload).
cd ui && npm run dev
# Visit http://127.0.0.1:5173 (talks to the daemon at :7878).
```

## Tests + checks before opening a PR

```bash
# Go
go vet ./...
go test -race ./...

# UI
cd ui
npm run check     # svelte-check + contract-drift guard
npm test          # vitest
npm run build     # production build, also exercised by the embed.FS
```

CI (`.github/workflows/ci.yml`) runs the same set on every PR.

### Test policy

Every PR that changes behaviour MUST include a test covering the change:

- **Adding a feature** → add a test that fails without your code and
  passes with it. The point isn't 100% line coverage; it's that the
  behaviour your change relies on is locked in so a future PR can't
  silently regress it.
- **Fixing a bug** → add a regression test that reproduces the bug
  before applying the fix (TDD). The test should fail on `main`
  without your fix.
- **Refactoring** → no new test required if the existing suite covers
  the surface, but you MUST run the full suite and report green.
- **Docs / chore / dep-bump** → no test required.

Local coverage check:

```bash
# Per-package
go test -cover ./internal/api/handlers/

# Whole repo with profile (then view in browser)
go test -coverprofile=/tmp/cov.out ./...
go tool cover -html=/tmp/cov.out

# UI
cd ui && npm run test:coverage
```

The current coverage floor on the public-facing packages is ~65%
(handlers) to ~90% (productivity/store). PRs are not blocked on
coverage drops, but reviewers will ask for tests when a behaviour
change lands without one.

### Tests that need real external state

A few tests are guarded against environments where they can't run:

- `internal/worklog/TestDogfoodKillCriteria` — needs a real
  `~/.klyne/klyne.db` with ≥ 5 days of entries. Skipped automatically
  in CI; only fires on a maintainer's machine.
- Any test calling `withFakeHome` / `setHomeDir` mutates a global — do
  NOT run them in parallel with each other.

## Commit style

Conventional Commits: `feat`, `fix`, `chore`, `docs`, `refactor`,
`test`, `perf`. Scopes match the package (`feat(ui):`, `fix(codex):`,
`chore(release):`).

## Reporting bugs

Use the issue templates under `.github/ISSUE_TEMPLATE/`. For security
issues, see [SECURITY.md](SECURITY.md) — do **not** open a public issue
for an unpatched vulnerability.

## What's out of scope

- Cloud sync, multi-user, team aggregation — explicit non-goals.
- New AI providers as daemon-internal subprocesses. AI work happens in
  the user's interactive CLI session via hooks; the daemon never spawns
  LM calls. See `docs/SECURITY.md` for the structural guarantee.
- UI redesigns without a spec discussion first.

## License

By contributing, you agree your contributions are licensed under the
[MIT License](LICENSE).

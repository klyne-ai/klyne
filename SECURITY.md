# Security policy

klyne is local-first by design — see [docs/SECURITY.md](docs/SECURITY.md)
for the authoritative privacy + threat-model reference with file:line
citations into the codebase.

## Reporting a vulnerability

**Please do not file public issues for unpatched vulnerabilities.**

Use GitHub's private vulnerability reporting:
<https://github.com/klyne-ai/klyne/security/advisories/new>

Include:
- klyne version (`klyne version`) and OS.
- Steps to reproduce.
- The smallest patch / config / repro you have.
- Whether the issue affects the daemon (`klyne daemon`), the CLI, the
  hook binary (`klyne-hook`), or the UI.

We aim to acknowledge within 72 hours and ship a patched release within
two weeks for confirmed issues.

## Supported versions

The latest `v0.*.*` release receives security fixes. Older patch
versions of the same minor are picked up on a best-effort basis.

## What's in scope

- The Go daemon (`cmd/klyne`, `internal/`)
- The hook stub (`cmd/klyne-hook`)
- The SvelteKit dashboard (`ui/`)
- The install / release pipeline (`scripts/install.sh`, `.goreleaser.yml`,
  `.github/workflows/`)

## What's out of scope

- Anything off the local machine (cloud sync isn't a feature).
- AI provider behaviour upstream of klyne (Claude Code, Codex CLI).
- Operating-system-level issues (kernel, libc, sandbox).

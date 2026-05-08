# W17 · Release Pipeline + Install Paths + Landing Page + README

> **Wave:** 4 · **Effort:** M · **Depends on:** W12, W14, W16 · **Recommended skills:** `general-purpose` + `deployment-patterns`

---

## Universal preamble

You are working on the klyne repo. Spec at `compass_artifact_wf-d189e421-ff1d-443c-95b8-19b52fcd59b4_text_markdown.md`. §18 decisions are LOCKED. After every change >30 lines run `make ci`. Use `superpowers:verification-before-completion` before claiming done.

**Single-writer rule:** modify only files under "Owned paths".

---

## Goal

Produce shippable artifacts and the public-facing materials. Multi-arch binaries via goreleaser, brew formula, winget manifest, install scripts, README copy from spec §16, and a landing page deployable to Cloudflare Pages.

---

## Spec sections

- §15 D12–D14 (release tasks)
- §16 (README template)

---

## Owned paths

```
.github/workflows/release.yml           (goreleaser, multi-arch, embed.FS of ui/build/ baked in)
.goreleaser.yaml
scripts/install.sh                      (Linux/macOS curl-bash installer)
scripts/install.ps1                     (Windows installer)
scripts/release.sh                      (manual release helper)
scripts/sign-darwin.sh                  (codesign + notarize stub; reads env vars; no-ops if absent)
Formula/klyne.rb                    (Homebrew tap stub)
winget/manifest.yaml                    (winget package manifest)
README.md                               (spec §16 verbatim — overwrites W0 stub)
docs/architecture.md
docs/connector-guide.md                 (how to write a v2 connector)
docs/byok-matrix.md
docs/CHANGELOG.md
docs/release-checklist.md
docs/landing/                           (static site for klyne.dev — Cloudflare Pages)
```

---

## Inputs

- W12 working binary (`klyne`).
- W14 built UI in `ui/build/` (must exist before `go build`).
- W16 perf docs at `docs/perf.md`.

---

## Outputs

- Tagged release `v1.0.0` with multi-arch artifacts:
  - `darwin/amd64`, `darwin/arm64`
  - `linux/amd64`, `linux/arm64`
  - `windows/amd64`, `windows/arm64`
- Brew formula stub (real tap in v1.1; v1 ships as a stub pointing to GitHub releases).
- winget manifest stub.
- `install.sh` that downloads the right artifact for the host and places `klyne` on `$PATH`.
- README rendered correctly on GitHub.
- Landing page deployable to Cloudflare Pages (just static files).

---

## Acceptance criteria

- [ ] `git tag v1.0.0-rc1 && git push --tags` triggers release CI; artifacts produced for all 6 OS×arch combinations.
- [ ] `bash scripts/install.sh` works on a fresh Linux VM (Ubuntu 22.04).
- [ ] `bash scripts/install.sh` works on a fresh macOS (any 14+).
- [ ] `winget install klyne` skeleton documented (manifest may not be published in v1; document the future flow).
- [ ] **Binary size ≤ 25 MB** including embedded UI (spec §12).
- [ ] **README is spec §16 verbatim** with project-specific links filled in.
- [ ] `docs/connector-guide.md` covers how to add a v2 connector with a worked example.
- [ ] `docs/byok-matrix.md` covers the full §8 matrix.
- [ ] Landing page renders cleanly with the 30-second demo gif (placeholder if not yet recorded).

---

## Testing requirements

Manual:
1. Install on a fresh Linux VM. Run `klyne doctor`. Run `klyne`. Open browser. Walk flows §6.
2. Install on a fresh macOS. Same.
3. Build on Windows; manual smoke (Windows install scripts may stay v1.0.1).

Automated:
- `.github/workflows/release.yml` runs goreleaser in dry-run on every PR; full release on tag push.
- `scripts/install.sh` has a `--dry-run` flag tested in CI.

---

## macOS code signing (best-effort for v1)

- `scripts/sign-darwin.sh` reads `APPLE_DEVELOPER_ID`, `APPLE_KEYCHAIN_PASS`, `APPLE_TEAM_ID` from env.
- If unset → no-op with a warning.
- If set → codesign + notarize via `notarytool`.
- Spec §13 risk #5 — Gatekeeper will flag unsigned binaries; document the "right-click → Open" workaround in `README.md` if signing isn't configured for v1.

---

## Hard boundaries

- Do **NOT** modify Go source — only build configs and docs.
- Do **NOT** add Snap, Flatpak, or Docker images — spec §18 #13 locks distribution to brew + curl-bash + winget.
- Do **NOT** ship telemetry — spec §18 #11.

---

## Done

When `git tag v1.0.0-rc1 && git push --tags` produces working binaries on every supported OS/arch, `bash scripts/install.sh` lands `klyne` on `$PATH` cleanly, and the landing page is live at `klyne.dev` (Cloudflare Pages).

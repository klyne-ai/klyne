# Releasing Klyne

Klyne releases are built by GitHub Actions from a signed version tag. The
release contains macOS, Linux, and Windows archives, SHA-256 checksums, a
keyless Sigstore signature for the checksum manifest, GitHub build-provenance
attestations, and generated release notes.

## One-time repository setup

Protect release tags matching `v*`.

The workflow uses GitHub OIDC for Sigstore and provenance, so no signing key is
stored in repository secrets. GitHub provides the short-lived `GITHUB_TOKEN`
used to publish release assets back to this repository; no additional release
secret is required.

## Preflight

1. Run the `release` workflow manually. A manual run creates and uploads a
   snapshot artifact but does not publish a GitHub release.
2. Download the snapshot and confirm that every archive contains `klyne`,
   `klyne-hook`, `README.md`, and `LICENSE`.
3. Test the darwin/arm64 and darwin/amd64 archives on macOS, and the linux/amd64
   and linux/arm64 archives on Linux.
4. Run `make install-test` locally.

## Publish v0.1.0

Start from the exact reviewed commit on the default branch:

```bash
git pull --ff-only
git tag -s v0.1.0 -m "klyne v0.1.0"
git push origin v0.1.0
```

The tag starts `.github/workflows/release.yml`. Do not announce the release
until the workflow, GitHub release assets, and attestations all finish
successfully.

## Verify a release

Download the archive, checksum manifest, and Sigstore bundle from the release.
Then verify the signed manifest:

```bash
cosign verify-blob \
  --certificate-identity "https://github.com/klyne-ai/klyne/.github/workflows/release.yml@refs/tags/v0.1.0" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  --bundle checksums.txt.sigstore.json \
  checksums.txt
```

Verify the downloaded archive against that manifest:

```bash
shasum -a 256 -c checksums.txt
```

Or verify GitHub's build provenance directly:

```bash
gh attestation verify klyne_0.1.0_darwin_arm64.tar.gz \
  --repo klyne-ai/klyne
```

## macOS Gatekeeper status

Sigstore and GitHub attestations prove release origin and integrity, but they
are not Apple Developer ID code signing or notarization. Before claiming
"notarized for macOS," add an Apple Developer ID certificate and App Store
Connect notarization credentials to a macOS signing job, then verify the
result with `codesign --verify --deep --strict` and `spctl --assess`.

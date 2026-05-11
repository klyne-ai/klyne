#!/usr/bin/env sh
# install.sh — one-command installer for klyne.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/klyne-ai/klyne/init/scripts/install.sh | sh
#
# Environment overrides:
#   KLYNE_VERSION   pin a release tag (e.g. v0.5.0). Default: latest release.
#   PREFIX          install root. Binary lands at $PREFIX/klyne.
#                   Default: /usr/local/bin
#
# This script is intentionally POSIX-sh so it runs under both bash and the
# default shells used by `curl | sh` invocations on macOS and Linux.

set -eu
# Emulate `set -o pipefail` in plain sh by failing on any command in a pipe.
# Not all POSIX shells support this, so guard it.
# shellcheck disable=SC3040
(set -o pipefail 2>/dev/null) && set -o pipefail || true

REPO="klyne-ai/klyne"
BINARY="klyne"
PREFIX="${PREFIX:-/usr/local/bin}"
KLYNE_VERSION="${KLYNE_VERSION:-}"

# ---- helpers ----------------------------------------------------------------

log() {
  printf '==> %s\n' "$*" >&2
}

err() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || err "required command not found: $1"
}

# Pick the first available downloader; we'll use it consistently.
download_to() {
  url="$1"
  out="$2"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL --retry 3 --retry-delay 1 -o "$out" "$url"
  elif command -v wget >/dev/null 2>&1; then
    wget -q -O "$out" "$url"
  else
    err "need either curl or wget on PATH"
  fi
}

# Pick the first available sha256 hasher.
sha256_of() {
  file="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$file" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$file" | awk '{print $1}'
  else
    err "need either sha256sum or shasum on PATH"
  fi
}

# ---- detect platform --------------------------------------------------------

detect_os() {
  uname_s="$(uname -s)"
  case "$uname_s" in
    Darwin) printf 'darwin' ;;
    Linux)  printf 'linux'  ;;
    *)      err "unsupported OS: $uname_s (supported: Darwin, Linux)" ;;
  esac
}

detect_arch() {
  uname_m="$(uname -m)"
  case "$uname_m" in
    x86_64|amd64)        printf 'amd64' ;;
    arm64|aarch64)       printf 'arm64' ;;
    *) err "unsupported arch: $uname_m (supported: amd64/x86_64, arm64/aarch64)" ;;
  esac
}

# ---- resolve version --------------------------------------------------------

resolve_version() {
  if [ -n "$KLYNE_VERSION" ]; then
    printf '%s' "$KLYNE_VERSION"
    return
  fi
  need_cmd grep
  need_cmd sed
  api_url="https://api.github.com/repos/${REPO}/releases/latest"
  tmp_json="$(mktemp)"
  trap 'rm -f "$tmp_json"' EXIT
  download_to "$api_url" "$tmp_json" || err "failed to fetch latest release metadata"
  # Strict grep on `"tag_name": "vX.Y.Z"` — avoids depending on jq.
  tag="$(grep -E '"tag_name"[[:space:]]*:' "$tmp_json" | head -n1 | sed -E 's/.*"tag_name"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/')"
  rm -f "$tmp_json"
  trap - EXIT
  [ -n "$tag" ] || err "could not parse latest release tag from $api_url"
  printf '%s' "$tag"
}

# ---- main -------------------------------------------------------------------

main() {
  need_cmd uname
  need_cmd mktemp
  need_cmd tar
  need_cmd install

  os="$(detect_os)"
  arch="$(detect_arch)"
  version="$(resolve_version)"
  # Archive name template MUST match .goreleaser.yml `archives.name_template`.
  # GoReleaser strips the leading `v` from {{ .Version }} when expanding,
  # so the asset name uses the bare semver.
  version_bare="${version#v}"
  archive="${BINARY}_${version_bare}_${os}_${arch}.tar.gz"
  base_url="https://github.com/${REPO}/releases/download/${version}"
  archive_url="${base_url}/${archive}"
  checksums_url="${base_url}/checksums.txt"

  log "installing ${BINARY} ${version} for ${os}/${arch}"
  log "  archive:   ${archive_url}"
  log "  checksums: ${checksums_url}"

  workdir="$(mktemp -d)"
  # Best-effort cleanup; safe to re-run the installer.
  trap 'rm -rf "$workdir"' EXIT INT TERM

  archive_path="${workdir}/${archive}"
  checksums_path="${workdir}/checksums.txt"

  download_to "$archive_url" "$archive_path" \
    || err "failed to download archive: $archive_url"
  download_to "$checksums_url" "$checksums_path" \
    || err "failed to download checksums: $checksums_url"

  # Verify sha256 BEFORE we extract anything.
  log "verifying sha256"
  actual="$(sha256_of "$archive_path")"
  expected="$(grep " ${archive}\$" "$checksums_path" | awk '{print $1}' | head -n1)"
  [ -n "$expected" ] || err "no checksum entry for ${archive} in checksums.txt"
  [ "$actual" = "$expected" ] || err "sha256 mismatch for ${archive}: got ${actual}, expected ${expected}"

  log "extracting"
  tar -xzf "$archive_path" -C "$workdir"

  extracted_binary="${workdir}/${BINARY}"
  [ -f "$extracted_binary" ] || err "archive did not contain expected binary: ${BINARY}"

  # Install with sudo if PREFIX isn't writable, but only prompt when we're
  # interactive. Non-interactive (`curl | sh`) just falls through to install
  # and surfaces a clear permission error.
  install_dir="$PREFIX"
  log "installing to ${install_dir}/${BINARY}"
  if [ -w "$install_dir" ] || [ -w "$(dirname "$install_dir")" ]; then
    install -m 0755 "$extracted_binary" "${install_dir}/${BINARY}"
  elif command -v sudo >/dev/null 2>&1; then
    log "elevating with sudo (target not writable by current user)"
    sudo install -m 0755 "$extracted_binary" "${install_dir}/${BINARY}"
  else
    err "cannot write to ${install_dir} and sudo is unavailable. Re-run with PREFIX=\$HOME/.local/bin"
  fi

  log "installed: ${install_dir}/${BINARY}"
  log "verify with: ${BINARY} --version"
}

main "$@"

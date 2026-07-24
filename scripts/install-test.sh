#!/usr/bin/env sh

set -eu

project_dir="$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)"
test_dir="$(mktemp -d)"
trap 'rm -rf "$test_dir"' EXIT INT TERM

fixture_dir="${test_dir}/fixture"
fake_bin="${test_dir}/fake-bin"
install_dir="${test_dir}/installed"
mkdir -p "$fixture_dir" "$fake_bin" "$install_dir"

printf '#!/usr/bin/env sh\nprintf "klyne v0.1.0\\n"\n' >"${fixture_dir}/klyne"
printf '#!/usr/bin/env sh\nprintf "klyne-hook v0.1.0\\n"\n' >"${fixture_dir}/klyne-hook"
chmod +x "${fixture_dir}/klyne" "${fixture_dir}/klyne-hook"

os="$(uname -s)"
case "$os" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) printf 'unsupported test OS: %s\n' "$os" >&2; exit 1 ;;
esac

arch="$(uname -m)"
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) printf 'unsupported test arch: %s\n' "$arch" >&2; exit 1 ;;
esac

archive="klyne_0.1.0_${os}_${arch}.tar.gz"
tar -czf "${fixture_dir}/${archive}" -C "$fixture_dir" klyne klyne-hook

if command -v sha256sum >/dev/null 2>&1; then
  checksum="$(sha256sum "${fixture_dir}/${archive}" | awk '{print $1}')"
else
  checksum="$(shasum -a 256 "${fixture_dir}/${archive}" | awk '{print $1}')"
fi
printf '%s  %s\n' "$checksum" "$archive" >"${fixture_dir}/checksums.txt"

printf '%s\n' \
  '#!/usr/bin/env sh' \
  'set -eu' \
  'out=""' \
  'url=""' \
  'while [ "$#" -gt 0 ]; do' \
  '  case "$1" in' \
  '    -o) shift; out="$1" ;;' \
  '    http://*|https://*) url="$1" ;;' \
  '  esac' \
  '  shift' \
  'done' \
  '[ -n "$out" ] && [ -n "$url" ]' \
  'case "$url" in' \
  '  */checksums.txt) source_file="${KLYNE_TEST_FIXTURE}/checksums.txt" ;;' \
  '  *.tar.gz) source_file="${KLYNE_TEST_FIXTURE}/${url##*/}" ;;' \
  '  *) printf "unexpected URL: %s\n" "$url" >&2; exit 1 ;;' \
  'esac' \
  'cp "$source_file" "$out"' \
  >"${fake_bin}/curl"
chmod +x "${fake_bin}/curl"

PATH="${fake_bin}:${PATH}" \
  KLYNE_TEST_FIXTURE="$fixture_dir" \
  KLYNE_VERSION=v0.1.0 \
  PREFIX="$install_dir" \
  sh "${project_dir}/scripts/install.sh"

test "$("${install_dir}/klyne")" = "klyne v0.1.0"
test "$("${install_dir}/klyne-hook")" = "klyne-hook v0.1.0"

printf '%064d  %s\n' 0 "$archive" >"${fixture_dir}/checksums.txt"
bad_install_dir="${test_dir}/bad-install"
if PATH="${fake_bin}:${PATH}" \
  KLYNE_TEST_FIXTURE="$fixture_dir" \
  KLYNE_VERSION=v0.1.0 \
  PREFIX="$bad_install_dir" \
  sh "${project_dir}/scripts/install.sh" >"${test_dir}/bad-checksum.log" 2>&1; then
  printf 'installer accepted an invalid checksum\n' >&2
  exit 1
fi
grep -q 'sha256 mismatch' "${test_dir}/bad-checksum.log"

printf 'installer smoke test passed (%s/%s)\n' "$os" "$arch"

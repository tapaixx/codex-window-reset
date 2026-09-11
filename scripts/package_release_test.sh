#!/usr/bin/env bash
set -euo pipefail
release_dir="${1:?release directory required}"
version="${2:?version required}"
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
packager="$script_dir/package-release.sh"

semver_release_dir=$(mktemp -d "${TMPDIR:-/tmp}/codex-window-reset-semver-test.XXXXXX")
trap 'rm -rf -- "$semver_release_dir"' EXIT
: > "$semver_release_dir/codex-window-reset-linux-amd64.so"
: > "$semver_release_dir/codex-window-reset-linux-arm64.so"

assert_invalid_version() {
  local candidate=$1 output
  if output=$("$packager" "$candidate" "$semver_release_dir" 2>&1); then
    printf 'expected invalid version to be rejected: %s\n' "$candidate" >&2
    exit 1
  fi
  if [[ $output != *"package-release: version must be semantic version text without a leading v: $candidate"* ]]; then
    printf 'unexpected rejection for %s:\n%s\n' "$candidate" "$output" >&2
    exit 1
  fi
}

assert_valid_prerelease() {
  local candidate=$1
  "$packager" "$candidate" "$semver_release_dir" >/dev/null
  test -f "$semver_release_dir/codex-window-reset_${candidate}_linux_amd64.zip"
  test -f "$semver_release_dir/codex-window-reset_${candidate}_linux_arm64.zip"
}

assert_invalid_version '1.2.3-01'
assert_invalid_version '1.2.3-alpha.01'
assert_valid_prerelease '1.2.3-0'
assert_valid_prerelease '1.2.3-alpha.1+build.5'

for arch in amd64 arm64; do
  test -f "${release_dir}/codex-window-reset-linux-${arch}.so"
  test -f "${release_dir}/codex-window-reset_${version}_linux_${arch}.zip"
  test -f "${release_dir}/codex-window-reset-linux-${arch}.so.sha256"
  unzip -Z1 "${release_dir}/codex-window-reset_${version}_linux_${arch}.zip" | diff -u - <(printf 'codex-window-reset.so\n')
done
test -f "${release_dir}/checksums.txt"
test "$(wc -l < "${release_dir}/checksums.txt")" -eq 4
sha256sum --check "${release_dir}/checksums.txt"

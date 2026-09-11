#!/usr/bin/env bash
set -euo pipefail
release_dir="${1:?release directory required}"
version="${2:?version required}"
for arch in amd64 arm64; do
  test -f "${release_dir}/codex-window-reset-linux-${arch}.so"
  test -f "${release_dir}/codex-window-reset_${version}_linux_${arch}.zip"
  test -f "${release_dir}/codex-window-reset-linux-${arch}.so.sha256"
  unzip -Z1 "${release_dir}/codex-window-reset_${version}_linux_${arch}.zip" | diff -u - <(printf 'codex-window-reset.so\n')
done
test -f "${release_dir}/checksums.txt"
test "$(wc -l < "${release_dir}/checksums.txt")" -eq 4
sha256sum --check "${release_dir}/checksums.txt"

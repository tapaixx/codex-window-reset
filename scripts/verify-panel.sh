#!/usr/bin/env bash
set -euo pipefail

# Exercise the same embedded response and linker version used by the plugin.
panel_version=${PANEL_VERSION:-0.0.0-ci}
if [[ ${GITHUB_REF_TYPE:-} == tag ]]; then
  panel_version=${GITHUB_REF_NAME#v}
fi
if [[ ! $panel_version =~ ^[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.+-]+)?$ ]]; then
  printf 'Invalid panel version: %s\n' "$panel_version" >&2
  exit 1
fi
fixture_dir=$(mktemp -d /tmp/cwr-panel-fixture.XXXXXX)
trap 'rm -f -- "$fixture_dir/panel.html" "$fixture_dir/simulation.json"; rmdir -- "$fixture_dir"' EXIT
CWR_BROWSER_FIXTURE_DIR="$fixture_dir" go test -count=1 -run '^TestExportEmbeddedPanelForBrowser$' \
  -ldflags="-X github.com/tapaixx/codex-window-reset.pluginVersion=$panel_version" .
BROWSER_PANEL_FILE="$fixture_dir/panel.html" BROWSER_EXPECTED_VERSION="v$panel_version" \
  BROWSER_SIMULATION_FILE="$fixture_dir/simulation.json" \
  node --test web/tests/*.test.mjs

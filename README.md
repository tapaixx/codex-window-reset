# Codex Window Reset

Codex Window Reset is a Linux-only native plugin for CLIProxyAPI. It gives one
trusted Operator a panel for inspecting CLIProxyAPI-managed Codex Accounts,
planning short-window preheating before work periods, running explicitly
authorized Health Probes, and performing deliberate one-account Quota Resets.
Automatic scheduling never spends Reset Credits.

## Requirements and security

Use a CLIProxyAPI installation that supports native plugins, with a matching
Linux `amd64` or `arm64` host. Production Go uses only the standard library.
The panel is served as one self-contained `/panel` document with inline CSS
and JavaScript. It has no frontend dependency, secondary plugin-resource
requests, or page polling loop.

CLIProxyAPI owns its own panel/login/authentication flow and persistence. This
plugin does not ask for, write, persist, log, or expose the CLIProxyAPI
management key; it has no plugin-owned login and no plugin-owned secret
storage. The browser reads CLIProxyAPI's already-saved key transiently from
the host-owned local storage entry and sends it only as the Management API
authorization header. Reloading or closing the page discards plugin state.

The plugin does not receive or display Codex access tokens in the panel.
Account identities are masked by default.

## Install

Download the matching `.so` asset from the GitHub Release and place it in the
CLIProxyAPI plugin directory with its architecture-specific name:

```text
Linux amd64: /CLIProxyAPI/plugins/codex-window-reset-linux-amd64.so
Linux arm64: /CLIProxyAPI/plugins/codex-window-reset-linux-arm64.so
```

For example, after stopping or putting the host into its normal plugin
maintenance state:

```bash
sudo install -m 0755 codex-window-reset-linux-amd64.so \
  /CLIProxyAPI/plugins/codex-window-reset-linux-amd64.so
```

Use the `arm64` filename and asset on an arm64 host. Enable native plugins in
the existing CLIProxyAPI configuration, restart or reload CLIProxyAPI using
its normal procedure, and open the registered resource. No plugin-specific
management key or login setting is required.

The default panel URL is:

```text
/v0/resource/plugins/codex-window-reset/panel
```

CLIProxyAPI may register an architecture-suffixed plugin ID. In that case use
`/v0/resource/plugins/<registered-plugin-id>/panel`; the plugin registration
response or the host's plugin menu is authoritative.

The release ZIPs are plugin-store packages. Each ZIP contains exactly one
root entry named `codex-window-reset.so`; the direct `.so` assets above are
the architecture-specific installation artifacts.

## First startup and activation

A new installation starts inert. It performs no automatic Codex request until
the Operator has selected at least one Scheduled Account, configured a valid
schedule, enabled it, and saved it. Startup or plugin registration alone does
not activate preheating.

To activate automatic work:

1. Open the panel. Account metadata comes from CLIProxyAPI's
   `/v0/management/auth-files` endpoint.
2. Select accounts for scheduled preheating. The persistent Scheduled toggle
   is separate from the temporary Action Selection checkboxes.
3. Set an IANA timezone, weekdays, ordered Critical Work Periods, preheat
   lead/span, quota floors, blackout periods, and probe settings.
4. Enable the schedule and save it. The server validates the revision and
   requires at least one Scheduled Account.

The scheduler makes at most one Preheat Request per account in each bounded
preheat window and does not catch up missed occurrences. A Preheat Request is
a real minimal Codex request: it can consume ordinary quota and may begin a
Short Window, but it never consumes a Reset Credit.

## Safe operator actions

### Health Probe

The Probe action is manual and requires explicit quota acknowledgement. It is
a real Codex model request, may consume ordinary quota, and may begin a Short
Window. It can be authorized for an unavailable account when the Operator
acknowledges that override; disabled accounts cannot be probed. Use the
explicit quota refresh action when current quota data is needed.

### Quota Reset

Reset is manual only and accepts exactly one account. Before confirmation, the
panel requires a successful current quota and Reset Credit refresh. Confirming
the action calls the upstream reset-credit endpoint and consumes one applicable
Reset Credit. Treat the Reset Credit as scarce and irreversible: it is not a
refresh, a Probe, or an automatic scheduling action. The plugin records a
durable Reset Audit and uses an idempotency key so a retry cannot consume the
same credit twice.

### Quota freshness and holds

There is no page-open or interval quota polling. Two deliberately separate
snapshot paths exist:

- The panel calls CLIProxyAPI `/v0/management/api-call` only for an explicit
  quota refresh and after a confirmed Reset. These display snapshots live only
  in page memory and disappear on reload.
- The plugin runtime refreshes its own decision snapshot immediately before
  and after a Probe, Preheat Request, or Quota Reset. Runtime snapshots stay in
  memory and become stale exactly five minutes after capture.

A stale runtime snapshot cannot authorize a `sufficient_window` decision.

If a successful snapshot shows any recognized Long Window at or below the
configured Long Window floor (10% by default), automatic preheating enters a
persistent Guardrail Hold. Staleness or a failed refresh does not clear it.
Refresh successfully again and prove that every recognized Long Window is
above the floor to recover; only that later successful evidence clears the
hold. If there is no Guardrail Hold and quota refresh is unknown, automatic
preheating fails open and records `quota_unknown_fail_open` for auditability.

## Persistence and deletion boundaries

When `/CLIProxyAPI/plugins` exists, the default data directory is:

```text
/CLIProxyAPI/plugins/codex-window-reset
```

In a development or other host layout without that directory, the fallback is
`plugins/codex-window-reset` relative to the process working directory. The
directory contains:

```text
config.json          versioned schedule configuration and revision
runtime-state.json   occurrence state, next runs, compensation, and holds
history.json         the most recent 100 operational records
reset-audit.json     Reset Audits retained for 365 days
```

Writes use temporary files, synchronization, and atomic replacement. A
corrupt configuration disables scheduling and reports `store_corrupt`; it is
not silently replaced with defaults.

Clearing ordinary history removes `history.json` records and in-memory quota
snapshots only. It does not remove configuration, credentials owned by
CLIProxyAPI, runtime schedule state, or Reset Audit. Clearing Reset Audit is a
separate destructive action requiring the exact phrase `DELETE AUDIT`, and it
also never deletes host credentials or configuration. Reset Audit records
contain sanitized operational evidence, not access tokens, management keys,
raw credential JSON, or full upstream bodies.

## Upgrade and rollback

Before an upgrade, stop or disable the host plugin according to CLIProxyAPI's
normal procedure and back up the plugin data directory. Replace the matching
architecture `.so`, restart CLIProxyAPI, confirm the panel URL and registration
version, and run a manual quota refresh before enabling new schedule behavior.
Keep `config.json`, `runtime-state.json`, `history.json`, and
`reset-audit.json` unless the release notes require a migration.

To roll back, stop or disable the plugin, restore the previously verified
architecture-specific `.so`, restart the host, and verify the panel and status
before re-enabling the schedule. Restore the data-directory backup only when
the newer binary has changed persisted formats; deleting data is not a normal
rollback step and can destroy operational or Reset Audit evidence.

## Verification

The current release is `v0.0.5`; scripts receive and store `0.0.5` without the
leading `v`. The GitHub repository is
<https://github.com/tapaixx/codex-window-reset>. A release contains these
seven named assets:

```text
codex-window-reset_0.0.5_linux_amd64.zip
codex-window-reset_0.0.5_linux_arm64.zip
checksums.txt
codex-window-reset-linux-amd64.so
codex-window-reset-linux-arm64.so
codex-window-reset-linux-amd64.so.sha256
codex-window-reset-linux-arm64.so.sha256
```

Verify downloaded assets from their directory with:

```bash
sha256sum --check checksums.txt
unzip -Z1 codex-window-reset_0.0.5_linux_amd64.zip \
  | diff -u - <(printf 'codex-window-reset.so\n')
unzip -Z1 codex-window-reset_0.0.5_linux_arm64.zip \
  | diff -u - <(printf 'codex-window-reset.so\n')
```

From a checkout, the release gates are:

```bash
bash -n scripts/package-release.sh scripts/package_release_test.sh
go test ./...
go test -race ./...
go vet ./...
node --test web/tests/*.test.mjs
CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \
  go build -buildmode=c-shared -o /tmp/codex-window-reset-linux-amd64.so .
CC=aarch64-linux-gnu-gcc CGO_ENABLED=1 GOOS=linux GOARCH=arm64 \
  go build -buildmode=c-shared -o /tmp/codex-window-reset-linux-arm64.so .
```

The arm64 shared library must be built with the cross compiler and must not be
executed on an amd64 host. CI and tag releases run the same Go, race, vet,
Node, and two-architecture build gates before packaging. The aggregate
`checksums.txt` is sorted `sha256sum` output for only the two `.so` files and
two ZIPs; the individual `.sha256` files are not included in that manifest.

## License and attribution

Codex Window Reset is distributed under the MIT License. `NOTICE` preserves
the attribution for protocol behavior derived from
<https://github.com/tapaixx/codex-health-monitor>.

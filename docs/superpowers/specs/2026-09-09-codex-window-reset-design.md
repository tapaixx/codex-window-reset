# Codex Window Reset Design

**Status:** Approved

**Date:** 2026-09-09

**Reference implementation:** <https://github.com/tapaixx/codex-health-monitor>

**Domain language:** [`../../../CONTEXT.md`](../../../CONTEXT.md)

## Purpose

Codex Window Reset is a native CLIProxyAPI plugin for a single trusted Operator. It observes Codex Account health and quota windows, schedules minimal Probe Requests before configured work periods, simulates the expected benefit of preheating, and permits deliberate single-account Quota Resets with durable audit records.

The product must distinguish three actions that the reference implementation conflates:

- A **Health Probe** is a manually authorized real model request intended to verify that an account can complete a Codex request.
- A **Preheat Request** is the same underlying Probe Request executed by the scheduler to begin a Short Window before expected work.
- A **Quota Reset** calls the reset-credit consume endpoint and spends a scarce Reset Credit. It is always manual.

## Goals

- Run inside the CLIProxyAPI plugin ABI as a Go C-shared library.
- Opt accounts into scheduled preheating explicitly and start every installation inert.
- Maximize Available Coverage during Critical Work Periods while penalizing Idle Window time.
- Keep scheduling deterministic, explainable, bounded, and restart-safe.
- Report account-request success separately from whether a quota-window change was verified.
- Treat long-window quota as a guardrail without making transient quota-endpoint failures halt all preheating.
- Provide a dense but responsive operations panel based on the supplied blue-and-white dashboard reference.
- Preserve proven upstream and host Management API behavior from `codex-health-monitor` without inheriting its duplicate schedulers, global registries, or script-order overrides.

## Non-goals for the first release

- A standalone service, sidecar, desktop application, or dual-entry core package.
- Multiple users, RBAC, or per-account administrator permissions.
- Automatic consumption of Reset Credits.
- Historical Schedule Recommendations or automatic configuration changes.
- Periodic quota polling while the panel is open.
- Legacy `interval` or `daily_times` scheduling modes.
- Per-account calendars, holiday calendars, or date-specific schedule exceptions.
- Catch-up execution for occurrences missed while the plugin was stopped.
- Linux targets other than `amd64` and `arm64`, or macOS/Windows builds.

## Constraints

- Go version: 1.24.
- Runtime dependencies: Go standard library and CLIProxyAPI host ABI only.
- Build mode: Linux CGO `-buildmode=c-shared` for `amd64` and `arm64`.
- Frontend: one embedded `/panel` response with inline CSS and JavaScript; no frontend framework, runtime package dependency, or secondary plugin-resource request.
- Management authentication is owned by CLIProxyAPI. The panel may consume the host-owned authentication context transiently to authorize API requests, but it must not prompt for the key or create a plugin-owned stored copy, log entry, or response field.
- Access tokens must never enter plugin state files, history, audit responses, frontend payloads, or logs.
- Copied or substantially derived code must retain the reference project's MIT license and attribution.

## Architecture

The implementation selectively ports validated protocol and release code from the reference project into focused modules:

```text
CLIProxyAPI plugin ABI
        |
        v
Application Runtime
  |-- account discovery and host adapter
  |-- probe orchestration and strict SSE parser
  |-- quota snapshots and reset-credit operations
  |-- work-calendar planner and scheduler
  |-- deterministic strategy simulator
  |-- configuration, runtime state, history, and audit stores
        |
        +-- atomic JSON files
        +-- in-memory quota snapshot registry
```

The proposed source boundaries are:

```text
main.go                       C ABI exports and plugin dispatch only
internal/host/                typed CLIProxyAPI host adapter
internal/domain/              domain types, states, and stable error codes
internal/accounts/            account discovery and identity projection
internal/probe/               Probe Request construction and SSE parsing
internal/quota/               usage parsing, snapshots, guardrails, resets
internal/schedule/            calendar validation, occurrence planning, runtime
internal/simulate/            pure strategy simulation using schedule rules
internal/store/               versioned atomic JSON repositories
internal/app/                 use-case orchestration and concurrency ownership
internal/management/          HTTP request decoding and response envelopes
web/panel.html                panel shell
web/styles.css                design tokens, layout, and responsive behavior
web/modules/                  API, state, accounts, schedule, simulator, history
```

`internal/app.Runtime` owns all mutable registries and goroutine lifecycles. There are no package-global quota or managed-runtime registries. The exported C ABI keeps only the synchronized pointer required to hand calls to the active Runtime.

CLIProxyAPI resource routes are exact matches, not prefix or wildcard routes.
Management registration therefore declares `/panel` as the only resource and
menu entry. The server inlines embedded CSS and ordered JavaScript sources into
that response; the browser never requests `/styles.css` or `/modules/*`.
Source modules retain explicit imports/exports for Node tests, but the served
classic script contains neither module syntax nor cross-resource loading.

## Configuration model

The persisted configuration is a full, versioned replacement document:

```go
type Config struct {
    SchemaVersion               int           `json:"schema_version"`
    Revision                    int64         `json:"revision"`
    Enabled                     bool          `json:"enabled"`
    Timezone                    string        `json:"timezone"`
    Weekdays                    []int         `json:"weekdays"`
    WorkPeriods                 []LocalPeriod `json:"work_periods"`
    PreheatLeadMinutes          *int          `json:"preheat_lead_minutes"`
    PreheatSpanMinutes          *int          `json:"preheat_span_minutes"`
    ProductivityMinutes         int           `json:"productivity_minutes"`
    RemainingQuotaFloorPercent int           `json:"remaining_quota_floor_percent"`
    RemainingWindowFloorMinutes int          `json:"remaining_window_floor_minutes"`
    LongWindowFloorPercent      int           `json:"long_window_floor_percent"`
    BlackoutPeriods             []LocalPeriod `json:"blackout_periods"`
    ProbeModel                  string        `json:"probe_model"`
    ProbeTimeoutSeconds         int           `json:"probe_timeout_seconds"`
    ScheduledAccountKeys        []string      `json:"scheduled_account_keys"`
}

type LocalPeriod struct {
    Start string `json:"start"`
    End   string `json:"end"`
}
```

First-install values are:

- `enabled`: `false`
- `timezone`: `Asia/Shanghai`
- `weekdays`: Monday through Friday
- suggested `work_periods`: `09:00-12:00` and `13:30-19:00`
- `preheat_lead_minutes`: absent and required before activation
- `preheat_span_minutes`: absent and required before activation
- `productivity_minutes`: `60`, simulator-only
- `remaining_quota_floor_percent`: `20`
- `remaining_window_floor_minutes`: `60`
- `long_window_floor_percent`: `10`
- `blackout_periods`: empty
- `probe_model`: `gpt-5.6-luna`, editable only in advanced settings
- `probe_timeout_seconds`: `30`
- `scheduled_account_keys`: empty

Validation rules are:

- `timezone` must load as an IANA location.
- `weekdays` contains unique integers from `1` through `7`.
- Each clock is strict `HH:mm` and each period has `start < end`.
- Work periods and Blackout Periods are sorted, non-overlapping within their own lists, and do not cross local midnight.
- Preheat lead and span are both required to enable scheduling, each from `1` through `1440` minutes.
- The derived Preheat Window must begin on the same local date as its Critical Work Period.
- Percentage thresholds are integers from `0` through `100`.
- Remaining-window and productivity minutes are positive.
- `probe_timeout_seconds` is from `5` through `120`.
- Scheduled account keys are unique, non-empty stable identifiers discovered through the host.
- Enabling requires at least one Scheduled Account and at least one Work Period.

`PUT /schedule` requires the caller's current `revision`. A mismatch returns `revision_conflict` without modifying the stored configuration.

## Local-time semantics

The Operator edits the Work Calendar in the configured IANA timezone. Local work times remain local wall-clock times when UTC offsets change. Each occurrence is converted to a UTC instant immediately before it is scheduled.

An occurrence identity consists of local date, Work Period identity, and account key. The identity prevents a daylight-saving clock rollback from executing the same local occurrence twice. When a local Preheat Window intersects a nonexistent clock interval, the planner uses its first valid instant; if the entire window is nonexistent, the occurrence is recorded as missed.

## Occurrence planning

For a Critical Work Period beginning at `work_start`, the Preheat Window is:

```text
[work_start - lead - span, work_start - lead)
```

No default exists for `lead` or `span`. For example, if work begins at `09:00`, lead is `120` minutes, and span is `60` minutes, the Preheat Window is `06:00-07:00`.

The planner subtracts overlapping Blackout Periods from the Preheat Window. It sorts eligible account keys by a SHA-256 rank derived from local occurrence identity and account key, then distributes the ranked accounts across the midpoint of equal-duration slots in the remaining allowed time. This produces deterministic, approximately even staggering without persisting random choices.

If no allowed time remains, every affected account occurrence is recorded as missed. On Runtime startup, every previously planned but unexecuted occurrence is also recorded as missed, regardless of whether its original Preheat Window is still open. Missed occurrences are never caught up.

## Automatic preheat flow

At the planned account instant:

1. Verify that scheduling remains enabled and the account remains selected.
2. Skip `Disabled` and host-`Unavailable` accounts. Preserve their Scheduled Account membership.
3. Attempt to refresh the account's quota.
4. If refresh succeeds:
   - Select the shortest recognized Usage Window as the Short Window.
   - Treat every longer recognized window as a Long Window.
   - Create or clear the Guardrail Hold from the configured long-window floor.
   - Skip when a Guardrail Hold exists.
   - Skip with `sufficient_window` when short-window remaining quota is at least `20%` and remaining time is at least `60` minutes.
5. If refresh fails:
   - Skip while a previously established Guardrail Hold exists.
   - Otherwise proceed despite unknown current quota and record that the decision was fail-open.
6. Execute one Probe Request.
7. Attempt an operation-after quota refresh.
8. Record Request Outcome and Window Outcome separately.

Eligible request failures are network errors, timeouts, HTTP `429`, and HTTP `5xx`. They receive one Compensation Attempt approximately five minutes later, only if it still satisfies account and schedule eligibility. Authentication, authorization, payment, disabled-account, validation, and unexpected-output failures are not retried. A successful request with an unverified window effect is not retried.

## Manual Health Probe flow

The accounts table has two independent controls:

- A persistent automatic-preheat toggle changes Scheduled Account membership only after schedule save.
- An Action Selection checkbox selects accounts for the next manual bulk action only.

A manual Health Probe:

- requires at least one Action Selection;
- runs at most three accounts concurrently;
- uses account-level single-flight;
- rejects `Disabled` accounts;
- allows the Operator to override host `Unavailable` state;
- warns that it is a real model request that may begin a Short Window and consume quota;
- attempts quota refresh before and after the Probe Request;
- proceeds even if the pre-request quota refresh fails because the action is explicitly authorized.

## Probe protocol

The first release ports the reference request shape:

- endpoint: `POST https://chatgpt.com/backend-api/codex/responses`
- default model: `gpt-5.6-luna`
- instruction: return exactly `OK`
- streaming enabled, storage disabled, reasoning effort `low`

Success requires all of the following:

- HTTP status is `2xx`.
- The SSE stream contains `response.completed`.
- No `response.failed`, `response.incomplete`, or `error` event occurs.
- Final extracted text, after trimming whitespace, equals `OK` exactly.

The parser accepts final text from terminal response data, `response.output_text.done`, completed output items, or accumulated text deltas. It ignores non-`data:` lines, empty data, and `[DONE]`, supports a single JSON completed response, and limits scanned event data to 4 MiB.

## Request and window outcomes

Request Outcome values are:

```text
succeeded
unauthorized
forbidden
payment_required
rate_limited
upstream_error
network_error
timeout
response_error
unexpected_output
credential_error
disabled
```

Window Outcome values are:

```text
verified_started
already_active
unchanged
unverified
not_observed
```

`verified_started` requires a successful pre-request and post-request snapshot showing a new short-window reset boundary. `already_active` means the pre-request snapshot already showed an active Short Window. Missing, failed, delayed, or ambiguous quota data produces `unverified` or `not_observed`, never a fabricated success. Request success and window verification are displayed as separate fields.

## Quota and Guardrail Hold

The implementation keeps two quota projections with different authority and
lifetime:

- **Runtime decision snapshots** call the upstream usage and reset-credit
  endpoints through the plugin's host HTTP capability. They are refreshed
  only immediately before and after a Probe/Preheat operation and immediately
  after a Quota Reset. They drive window outcomes and Guardrail Holds.
- **Panel display snapshots** are obtained only when the Operator clicks
  refresh (and after a confirmed Reset) by sending CLIProxyAPI
  `/v0/management/api-call` a `$TOKEN$` request template plus the account's
  `Chatgpt-Account-Id`. They exist only in page memory and never authorize a
  runtime decision.

Runtime refresh is triggered only:

- immediately before a Health Probe or Preheat Request;
- immediately after a Probe Request;
- immediately after a Quota Reset.

There is no upstream refresh on page open and no interval polling. Runtime
Usage Snapshots remain only in Runtime memory and become stale after five
minutes. A Stale Snapshot can remain visible but cannot authorize a
`sufficient_window` decision. Reloading the page discards panel display
snapshots.

The exception is a Guardrail Hold. Once a successful snapshot shows any Long Window at or below `10%`, the hold persists in runtime state even after that snapshot becomes stale. Only a later successful refresh proving every recognized Long Window above the floor clears it.

When refresh fails without a Guardrail Hold, automatic preheating deliberately fails open. The operation record must say `quota_unknown_fail_open` so the decision is auditable.

## Quota Reset

Quota Reset is limited to one Codex Account per action. The panel first requires a successful current quota/reset-credit refresh, shows the account and remaining applicable Reset Credits, then uses a standard confirmation dialog.

On confirmation:

1. The browser generates a UUID idempotency key.
2. The server atomically appends a pending Reset Audit before the upstream call.
3. Server-side account single-flight prevents concurrent resets.
4. The plugin calls `POST /backend-api/wham/rate-limit-reset-credits/consume` with the idempotency key as `redeem_request_id`.
5. It refreshes quota after the call.
6. It records `succeeded`, `failed`, or `unknown` without deleting the pending evidence.

Reusing an idempotency key returns its stored outcome and never calls consume again. A crash-ambiguous pending record becomes `unknown` on restart and is not automatically retried.

## Persistence

The plugin data directory contains:

```text
config.json          versioned Config and revision
runtime-state.json   occurrence states, next runs, compensation, Guardrail Holds
history.json         most recent 100 operational records
reset-audit.json     Reset Audits retained for 365 days
```

Writes use a same-directory temporary file, file sync, atomic rename, and directory sync where supported. Configuration corruption disables scheduling and exposes `store_corrupt` in status while leaving the panel and manual diagnostics accessible. It does not silently replace the file with defaults.

Ordinary data deletion clears `history.json` and in-memory snapshots only. It does not delete configuration, credentials, runtime schedule state, or Reset Audit. Reset Audit deletion is a separate action that requires the exact confirmation phrase `DELETE AUDIT`; it never deletes account credentials or configuration.

Reset Audit stores the idempotency key, timestamps, stable account key, masked identity snapshot, prior applicable-credit count when known, outcome, HTTP category, and correlation ID. It does not store an access token, management key, raw credential JSON, or full upstream body.

## Management API

Every management route is relative to the plugin ID derived from CLIProxyAPI's runtime `resource_base_path`; no browser source hardcodes `codex-window-reset`.

```text
GET    /status
GET    /accounts
GET    /schedule
PUT    /schedule
POST   /simulate
POST   /probes
GET    /history
DELETE /history
GET    /quota
POST   /quota/refresh
POST   /quota/reset
GET    /reset-audit
DELETE /reset-audit
```

`POST /probes` accepts account keys and returns HTTP `202` with a run ID. `/status` exposes current run progress; overlapping manual bulk runs return `run_in_progress`.

`POST /simulate` accepts a draft Config that may be disabled but must otherwise validate. It returns calculated work minutes, each derived Preheat Window, planned window coverage, idle minutes, strategy comparison, and 24-hour timeline segments. The panel does not reimplement schedule math in JavaScript.

`POST /quota/refresh` accepts one or more account keys, uses at most three workers, and preserves the last successful in-memory snapshot when a later refresh fails while also exposing the refresh error.

`POST /quota/reset` accepts exactly:

```json
{
  "account_key": "stable-account-key",
  "idempotency_key": "uuid"
}
```

`DELETE /reset-audit` requires header `X-Confirmation: DELETE AUDIT`.

All responses use an envelope:

```json
{
  "ok": false,
  "error": {
    "code": "guardrail_hold",
    "message": "Long window quota remains below the configured floor.",
    "retryable": false,
    "correlation_id": "opaque-id"
  }
}
```

Domain error codes are stable English identifiers. The initial set is:

```text
config_invalid
revision_conflict
run_in_progress
account_busy
account_disabled
account_unavailable
guardrail_hold
quota_refresh_failed
probe_failed
window_unverified
idempotency_conflict
reset_outcome_unknown
store_corrupt
```

Chinese UI messages map from these codes and may include sanitized details. HTTP status and `retryable` remain machine-readable. Logs contain correlation ID, stable account fingerprint, error code, latency, and HTTP category only.

## Panel information architecture

The chosen layout is a **single scrolling operations page**:

```text
Header: product, version, load state, primary actions
Summary: scheduled, healthy, paused, Guardrail Hold, stale snapshot counts
Accounts: persistent status table or mobile account cards
Strategy configuration and deterministic simulator: side by side when space permits
Operational history and Reset Audit: separate sections below
```

The account view includes transient Action Selection, persistent preheat
toggle, masked identity, plan label, explicit status, Short and Long Window
remaining/reset text, Reset Credits, request/window outcomes, HTTP status and
latency, snapshot time/staleness, and a sanitized error reason.

Strategy configuration includes timezone, weekdays, ordered Work Periods, required preheat lead/span, Productivity Estimate, three quota thresholds, Blackout Periods, scheduled accounts, and advanced probe model/timeout. Unsaved edits remain local; “restore changes” restores the last server revision, not product defaults.

The simulator compares first-use window activation with the configured Preheat Schedule. It shows assumptions separately from observed quota, a 24-hour timeline, Available Coverage, Idle Window, and net gain. It does not label output as a historical recommendation.

## Visual system and accessibility

The supplied reference image defines the product direction: a light, dense, blue-and-white operations console with restrained status colors and minimal motion.

Core tokens are:

```text
primary           #3478F6
background        #F4F7FB
surface           #FFFFFF
foreground        #1D2939
muted-foreground  #667085
border            #E4E9F1
success           #12A16B
warning           #E88618
destructive       #E5484D
focus-ring        #3478F6
```

Typography uses a local system stack: `Inter`, `Noto Sans SC`, `Microsoft YaHei`, `system-ui`, and `sans-serif`, with `ui-monospace` for IDs and timestamps. No remote font request is required.

The panel defaults to the light palette and follows CLIProxyAPI's host theme when a supported host theme signal exists. Status is never conveyed by color alone. Icon-only controls have accessible names, all interactions are keyboard reachable, focus indicators remain visible, normal text meets 4.5:1 contrast, and primary touch targets are at least 44 by 44 CSS pixels. Transitions use 150-250 ms and are disabled or reduced under `prefers-reduced-motion`.

Responsive verification targets are 375, 768, 1024, and 1440 CSS pixels. Below 768 pixels, account rows and history rows become labeled cards and all sections stack without requiring page-level horizontal scrolling.

Identity fields are masked by default. Reveal state exists only for the current
page session. Access tokens are never sent to the panel. Browser code reads
CLIProxyAPI's already-saved management key transiently from the host-owned
local-storage entry and attaches it to Management API requests. It never
writes storage, shows a plugin login, or creates a plugin-specific credential.
Account metadata comes from `/v0/management/auth-files`; operator-requested
display quota comes from `/v0/management/api-call`.

## Testing strategy

Implementation follows test-first cycles.

Go unit tests cover:

- strict time, timezone, period, threshold, account, and activation validation;
- DST gaps and duplicate local hours;
- Preheat Window derivation and Blackout subtraction;
- deterministic account staggering;
- missed occurrence behavior across restart;
- shortest-window selection and long-window Guardrail Holds;
- successful refresh, fail-open refresh failure, and held refresh failure;
- Sufficient Window skipping;
- one eligible Compensation Attempt and non-retryable errors;
- Request Outcome and Window Outcome separation;
- strict SSE terminal parsing and 4 MiB limit;
- quota and reset-credit payload compatibility;
- manual concurrency limit and account single-flight;
- reset idempotency, pending-audit persistence, replay, and crash ambiguity;
- atomic JSON writes, retention, deletion boundaries, and corrupt-store startup;
- simulation results matching scheduler primitives.

Management contract tests cover every route, HTTP status, response envelope,
revision conflict, dynamic plugin ID, the single embedded `/panel` resource,
authentication pass-through, and absence of secrets. Tests also prove that
the served panel is self-contained and undeclared asset paths return not found.

Node's built-in test runner covers browser API-path derivation, state transitions, error-code localization, identity masking, sensitive-field rejection, responsive markup, accessibility labels, and the absence of duplicated global function definitions. No test requires a frontend framework.

CI runs:

```text
go test ./...
go test -race ./...
go vet ./...
node --test
```

Release automation builds Linux `amd64` and `arm64` shared libraries, plugin-store ZIP archives, individual SHA-256 files, an aggregate checksum manifest, and a GitHub Release.

## Acceptance criteria

- A new installation performs no automatic Codex request until the Operator completes and enables a valid schedule with at least one Scheduled Account.
- The scheduler produces deterministic account occurrences and never catches up missed work.
- A known low Long Window blocks preheating until a successful refresh proves recovery; unknown quota without a hold proceeds and is auditable.
- Manual and scheduled Probe Requests disclose quota side effects and keep request/window outcomes separate.
- A successful but unverified preheat is not retried.
- A Quota Reset can target only one account, requires confirmation, uses server-side single-flight and durable idempotency, and produces a Reset Audit.
- Ordinary history deletion cannot remove configuration, credentials, runtime state, or reset audit.
- The panel never owns a CLIProxyAPI management key or receives a Codex access token.
- Desktop and mobile layouts preserve all essential state without page-level horizontal scrolling.
- The complete verification suite passes for both supported release architectures.

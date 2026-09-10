# Task 6 implementation report

## Status

Implemented Task 6: quota parsing, in-memory snapshot caching, guardrail holds,
and the one-shot quota-reset HTTP primitive.

## Changed files

- `internal/quota/parse.go`
- `internal/quota/service.go`
- `internal/quota/parse_test.go`
- `internal/quota/service_test.go`
- `.superpowers/sdd/2026-09-09-codex-window-reset/task-6-report.md` (this ignored report)

No existing files outside the four Task 6 package files were changed.

## Requirement evidence

- `ParseUsage` accepts snake-case and camelCase rate-limit payloads, named
  primary/secondary windows, arbitrary recognized window lists, percentage or
  fractional usage, numeric/RFC3339 reset boundaries, and missing reset
  boundaries. It sorts recognized windows by duration and marks only the
  shortest as `Short`.
- Invalid JSON, missing/unusable windows, and non-positive durations produce a
  sanitized parse error without returning upstream payload data.
- Refresh performs one usage GET and one reset-credit GET per account flight.
  Credentials are obtained through `accounts.AuthMaterial` immediately before
  each host request and are not retained in the service, logs, errors, or
  returned reset results.
- Same-account refreshes share one flight; different account keys do not share
  a flight. `Get` reads memory only and marks a snapshot stale at the inclusive
  five-minute boundary without deleting it.
- A usage refresh failure keeps the previous successful snapshot, records a
  sanitized `quota_refresh_failed` state and last-attempt timestamp, and
  exposes only `SnapshotView.RefreshErrorCode`.
- Reset-credit detail is best effort: embedded usage counts remain partial when
  detail is unavailable, while `ResetInfoComplete` is true only after a
  successful, structurally parsed dedicated response.
- `Evaluate` uses short-window quota/time floors, rejects stale snapshots for
  `sufficient_window`, fails open as `quota_unknown_fail_open` without a known
  hold, and applies long-window guardrails. Hold creation and clearing use the
  runtime repository's transactional `Update`; failed or stale refreshes never
  clear a hold, and a persisted hold blocks after service reconstruction.
- `Reset` sends exactly one POST to the consume endpoint with
  `{"redeem_request_id":"<key>"}`, returns only status category and sanitized
  errors, and has no retry or application-level idempotency policy.
- All persisted or JSON-crossing time values are normalized to UTC through the
  existing domain types and the quota parser.

## TDD evidence

Tests were written before production code. The required RED command was:

```text
docker run --rm -v /share/codeSpace/codex-window-reset/.worktrees/codex-window-reset:/src -w /src golang:1.24 go test ./internal/quota -v
```

It exited 1 with the expected missing-symbol failure:

```text
internal/quota/parse_test.go:13:14: undefined: ParseUsage
internal/quota/service_test.go:178:7: undefined: New
internal/quota/service_test.go:204:7: undefined: New
internal/quota/service_test.go:294:7: undefined: New
... undefined quota symbols ...
FAIL
```

The GREEN focused run passed after implementation. Tests cover shortest-window
selection, casing/payload compatibility, arbitrary windows, fractional usage,
missing reset boundaries, invalid payloads, independent and single-flight
refreshes, zero-call `Get`, exact staleness, stale-preserving errors, fail-open,
hold persistence/clearing/restart, reset body/no-retry, and partial reset info.

## Verification evidence

All Go commands below use the official Go 1.24 Docker image.

Focused tests:

```text
docker run --rm -v /share/codeSpace/codex-window-reset/.worktrees/codex-window-reset:/src -w /src golang:1.24 go test -count=1 ./internal/quota -v
```

Output ended with:

```text
PASS
ok   github.com/tapaixx/codex-window-reset/internal/quota 0.054s
```

Full tests:

```text
docker run --rm -v /share/codeSpace/codex-window-reset/.worktrees/codex-window-reset:/src -w /src golang:1.24 go test -count=1 ./...
```

Output:

```text
?    github.com/tapaixx/codex-window-reset [no test files]
ok   github.com/tapaixx/codex-window-reset/internal/accounts 0.006s
ok   github.com/tapaixx/codex-window-reset/internal/domain 0.010s
ok   github.com/tapaixx/codex-window-reset/internal/host 0.010s
ok   github.com/tapaixx/codex-window-reset/internal/probe 0.025s
ok   github.com/tapaixx/codex-window-reset/internal/quota 0.070s
ok   github.com/tapaixx/codex-window-reset/internal/schedule 0.102s
ok   github.com/tapaixx/codex-window-reset/internal/simulate 0.038s
ok   github.com/tapaixx/codex-window-reset/internal/store 0.249s
```

Vet:

```text
docker run --rm -v /share/codeSpace/codex-window-reset/.worktrees/codex-window-reset:/src -w /src golang:1.24 go vet ./...
```

Output: exit 0, no diagnostics.

Race-enabled full suite:

```text
docker run --rm -v /share/codeSpace/codex-window-reset/.worktrees/codex-window-reset:/src -w /src golang:1.24 go test -race ./...
```

Output:

```text
?    github.com/tapaixx/codex-window-reset [no test files]
ok   github.com/tapaixx/codex-window-reset/internal/accounts 1.018s
ok   github.com/tapaixx/codex-window-reset/internal/domain 1.038s
ok   github.com/tapaixx/codex-window-reset/internal/host 1.023s
ok   github.com/tapaixx/codex-window-reset/internal/probe 1.076s
ok   github.com/tapaixx/codex-window-reset/internal/quota 1.099s
ok   github.com/tapaixx/codex-window-reset/internal/schedule 1.933s
ok   github.com/tapaixx/codex-window-reset/internal/simulate 1.110s
ok   github.com/tapaixx/codex-window-reset/internal/store 1.345s
```

Formatting:

```text
docker run --rm -v /share/codeSpace/codex-window-reset/.worktrees/codex-window-reset:/src -w /src golang:1.24 gofmt -d internal/quota/parse.go internal/quota/service.go internal/quota/parse_test.go internal/quota/service_test.go
```

Output: empty; no formatting diff.

Staged diff checks:

```text
git diff --cached --check
git diff --cached --stat
```

Output:

```text
 internal/quota/parse.go        | 504 ++++++++++++++++++++++++++++++++
 internal/quota/parse_test.go   | 156 ++++++++++
 internal/quota/service.go      | 536 ++++++++++++++++++++++++++++++++++
 internal/quota/service_test.go | 643 +++++++++++++++++++++++++++++++++++++++++
 4 files changed, 1839 insertions(+)
```

## Self-review

- The implementation uses only Go 1.24 standard-library packages and the
  existing `accounts`, `domain`, and `host` boundaries.
- No service path logs or persists upstream credentials, response bodies, or
  caller idempotency keys.
- Snapshot state is instance-local and memory-only; persistent state changes are
  limited to guardrail holds and use transactional repository updates.
- The staged diff contains only the four requested Task 6 source/test files;
  the report is force-added separately because `.superpowers/` is ignored.
- No live Codex request was made. Upstream behavior is covered by the injected
  host test double and parser fixtures.

## Concerns

No material concerns. Deployment still depends on the host adapter exposing the
documented HTTP and just-in-time credential capabilities.

The commit SHA is intentionally not embedded here because changing this report
after committing would change the commit being reported.

Commit message:

```text
feat: add quota snapshots and persistent guardrails
```

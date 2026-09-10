# Task 8 report: restart-safe scheduler and one-shot compensation

## Status

Implemented in `8c235a8` (`feat: add restart-safe preheat scheduler`).

## Implementation summary

- Added `internal/schedule.Scheduler` with a single owner goroutine, serialized
  commands, injected clock/timer support, and transactional runtime-state
  transitions.
- Startup recovery marks persisted `planned`, `running`, and timer-owned
  compensation occurrences as `missed` before any timers are installed. This
  prevents restart catch-up, including for an occurrence whose nominal window
  has not elapsed.
- Reconciliation plans today plus the next seven local dates, persists stable
  occurrence identities and `NextRuns`, handles planner-marked/past plans as
  missed, cancels removed or disabled timers, and schedules the next local
  midnight. Occurrence callbacks reconcile the horizon again without polling.
- Occurrences are persisted as `running` before executor I/O and as terminal
  states afterward. Compensation is scheduled only for eligible transient
  failures, rechecks current config/account/guardrail eligibility at due time,
  persists `CompensationAttempted` before the request, and clears the due time
  unconditionally after the one allowed attempt.
- Wired the scheduler into `Runtime.Start` and `Runtime.Stop`. Added a shared
  preheat path so compensation records use a distinct trigger and do not create
  another compensation due time.
- Added fake-clock scheduler tests for startup miss/no catch-up, exactly-once
  future execution, seven-day planning, disabled cancellation, midnight
  horizon advancement, live compensation, restart compensation miss, and
  compensation eligibility recheck. Added the Runtime startup recovery test.

## TDD evidence

The RED transcript was not captured. The task brief specifies the expected
pre-implementation compile failure and the focused RED command, but no command
output, pre-implementation tree, or implementer report is available in the
repository or inspected Git history. The committed diff only preserves the
final tests and implementation, so a failing RED result cannot be claimed.

The final committed test names provide behavioral evidence:

- `TestStartupMarksEveryUnexecutedPastPlanMissed`
- `TestFutureOccurrenceFiresExactlyOnce`
- `TestReconcilePlansTodayAndSevenFollowingLocalDates`
- `TestDisabledReconcileCancelsFutureTimersWithoutChangingSelectionHistory`
- `TestMidnightCallbackAdvancesPlanningHorizon`
- `TestCompensationFiresOnceWhileProcessOwnsTimer`
- `TestRestartMarksPendingCompensationMissed`
- `TestCompensationEligibilityIsRecheckedBeforeAttempt`
- `TestRuntimeStartMarksPersistedPreheatMissed`

## Verification

The implementer reported these checks under Go 1.24. The repository's recorded
toolchain invocation uses the official `golang:1.24-bookworm` image. These
commands were not rerun while reconstructing this report.

```text
docker run --rm -v "$PWD":/src -w /src golang:1.24-bookworm go test -count=1 ./...
```

Result reported: exit 0; the full Go suite passed for the root package and the
accounts, app, domain, host, probe, quota, schedule, simulate, and store
packages.

```text
docker run --rm -v "$PWD":/src -w /src golang:1.24-bookworm go test -race ./...
```

Result reported: exit 0; the full race-enabled suite passed with no race
reports.

```text
docker run --rm -v "$PWD":/src -w /src golang:1.24-bookworm go vet ./...
```

Result reported: exit 0 with no diagnostics.

```text
docker run --rm -v "$PWD":/src -w /src golang:1.24-bookworm gofmt -d internal/app/preheat.go internal/app/restart_test.go internal/app/runtime.go internal/schedule/scheduler.go internal/schedule/scheduler_test.go
```

Result reported: empty output; formatting is clean.

## Files changed

- `internal/schedule/scheduler.go` (new)
- `internal/schedule/scheduler_test.go` (new)
- `internal/app/runtime.go`
- `internal/app/preheat.go`
- `internal/app/restart_test.go` (new)

The committed range changes five files: 1,398 insertions and 18 deletions.

## Self-review

- Timer callbacks only enqueue owner-loop commands; state claims, timer
  replacement, and reconciliation are serialized by the scheduler owner.
- Startup ownership recovery is persisted before timer setup, and no executor
  call is made for recovered work marked missed.
- The fake-clock tests exercise the no-catch-up, exactly-once, horizon, and
  one-shot compensation paths without depending on scheduled wall-clock
  intervals for the behavior under test.
- Compensation eligibility is checked outside the repository update, then the
  occurrence is revalidated and marked attempted transactionally before the
  executor call. Account selection is not modified by disabled reconciliation.
- Runtime stop paths cancel scheduler timers and the owner goroutine along with
  existing manual work.

## Concerns (before fix round 1)

- The RED evidence and verbatim verification transcript are absent; the
  verification results above are the implementer's reported outcomes, not a
  test run performed during this reconstruction.
- The scheduler ignores errors from the post-executor state updates and the
  callback reconciliation. A persistence failure after external I/O can leave
  an occurrence in an intermediate state without surfacing the error.
- The scheduler tests still use short `time.Sleep` polling and one-second
  timeouts in helpers, despite using a fake clock; the core scheduling time
  advancement is deterministic, but the tests are not completely free of
  wall-clock waits.
- The inherited `compensable` helper treats HTTP 429 as compensable even if a
  contradictory successful request outcome is supplied. Normal probe results
  should not have that combination, but an explicit invariant test or outcome
  guard would close the gap.

## Fix round 1: Important findings

### Status

DONE. All six Important findings were fixed in the scoped scheduler, runtime,
preheat, and regression-test changes. The two ledgered Minors were not
addressed.

### RED regression evidence

Tests added or rewritten before the production corrections:

- `TestReconcileRefreshesChangedPlannerOutputAndReplacesTimer`
- `TestReconcileReplacesMidnightTimerWhenTimezoneChanges`
- `TestQueuedCallbackAfterStopDoesNotExecute`
- `TestSchedulerReportsOccurrencePersistenceAndReconcileFailures`
  (`claim`, `terminal`, `post-callback reconcile`)
- `TestSchedulerReportsCompensationLoadFailure`
- `TestRuntimeStartRecoversWithCorruptConfigWithoutActivatingSchedule`
- `TestPreheatCompensationOnlyForEligibleAutomaticFailures/success_with_rate_limit_status`
- `TestMidnightCallbackAdvancesPlanningHorizon` was rewritten to use
  `2026-09-17`, outside the initial `2026-09-09` through `2026-09-16`
  horizon, before advancing the fake clock.

Command:

```bash
docker run --rm -v "$PWD":/src -w /src golang:1.24-bookworm go test -count=1 ./internal/schedule ./internal/app -run 'Test(ReconcileRefreshes|ReconcileReplacesMidnight|QueuedCallback|SchedulerReports|RuntimeStartRecoversWithCorrupt|PreheatCompensationOnly)' -v
```

Output:

```text
=== RUN   TestReconcileRefreshesChangedPlannerOutputAndReplacesTimer
    scheduler_test.go:360: refreshed planned_at=2026-09-09 07:30:00 +0000 UTC, want 2026-09-09 09:30:00 +0000 UTC
--- FAIL: TestReconcileRefreshesChangedPlannerOutputAndReplacesTimer (0.00s)
=== RUN   TestReconcileReplacesMidnightTimerWhenTimezoneChanges
    scheduler_test.go:401: midnight timer was not replaced for new timezone
--- FAIL: TestReconcileReplacesMidnightTimerWhenTimezoneChanges (0.00s)
=== RUN   TestQueuedCallbackAfterStopDoesNotExecute
    scheduler_test.go:424: queued callback executed 1 operations after Stop
--- FAIL: TestQueuedCallbackAfterStopDoesNotExecute (0.00s)
=== RUN   TestSchedulerReportsOccurrencePersistenceAndReconcileFailures
=== RUN   TestSchedulerReportsOccurrencePersistenceAndReconcileFailures/claim
    scheduler_test.go:441: scheduler failure was not reported
=== RUN   TestSchedulerReportsOccurrencePersistenceAndReconcileFailures/terminal
    scheduler_test.go:466: scheduler failure was not reported
=== RUN   TestSchedulerReportsOccurrencePersistenceAndReconcileFailures/post-callback_reconcile
    scheduler_test.go:483: scheduler failure was not reported
--- FAIL: TestSchedulerReportsOccurrencePersistenceAndReconcileFailures (3.00s)
    --- FAIL: TestSchedulerReportsOccurrencePersistenceAndReconcileFailures/claim (1.00s)
    --- FAIL: TestSchedulerReportsOccurrencePersistenceAndReconcileFailures/terminal (1.00s)
    --- FAIL: TestSchedulerReportsOccurrencePersistenceAndReconcileFailures/post-callback_reconcile (1.00s)
=== RUN   TestSchedulerReportsCompensationLoadFailure
    scheduler_test.go:507: scheduler failure was not reported
--- FAIL: TestSchedulerReportsCompensationLoadFailure (1.00s)
FAIL
FAIL	github.com/tapaixx/codex-window-reset/internal/schedule	4.009s
=== RUN   TestPreheatCompensationOnlyForEligibleAutomaticFailures
=== RUN   TestPreheatCompensationOnlyForEligibleAutomaticFailures/network
=== RUN   TestPreheatCompensationOnlyForEligibleAutomaticFailures/timeout
=== RUN   TestPreheatCompensationOnlyForEligibleAutomaticFailures/rate_limited
=== RUN   TestPreheatCompensationOnlyForEligibleAutomaticFailures/server
=== RUN   TestPreheatCompensationOnlyForEligibleAutomaticFailures/unauthorized
=== RUN   TestPreheatCompensationOnlyForEligibleAutomaticFailures/validation
=== RUN   TestPreheatCompensationOnlyForEligibleAutomaticFailures/unexpected_output
=== RUN   TestPreheatCompensationOnlyForEligibleAutomaticFailures/success_unverified
=== RUN   TestPreheatCompensationOnlyForEligibleAutomaticFailures/success_with_rate_limit_status
    preheat_test.go:139: due=2026-09-09 12:05:00 +0000 UTC, want due=false
--- FAIL: TestPreheatCompensationOnlyForEligibleAutomaticFailures (0.00s)
    --- PASS: TestPreheatCompensationOnlyForEligibleAutomaticFailures/network (0.00s)
    --- PASS: TestPreheatCompensationOnlyForEligibleAutomaticFailures/timeout (0.00s)
    --- PASS: TestPreheatCompensationOnlyForEligibleAutomaticFailures/rate_limited (0.00s)
    --- PASS: TestPreheatCompensationOnlyForEligibleAutomaticFailures/server (0.00s)
    --- PASS: TestPreheatCompensationOnlyForEligibleAutomaticFailures/unauthorized (0.00s)
    --- PASS: TestPreheatCompensationOnlyForEligibleAutomaticFailures/validation (0.00s)
    --- PASS: TestPreheatCompensationOnlyForEligibleAutomaticFailures/unexpected_output (0.00s)
    --- PASS: TestPreheatCompensationOnlyForEligibleAutomaticFailures/success_unverified (0.00s)
    --- FAIL: TestPreheatCompensationOnlyForEligibleAutomaticFailures/success_with_rate_limit_status (0.00s)
FAIL
FAIL	github.com/tapaixx/codex-window-reset/internal/app	0.006s
```

The corrected midnight test was intentionally a separate test-fixture
regression and was not expected to fail against the old scheduler; the old
test was false-green because it inserted an occurrence already inside the
initial horizon.

### Corrections

- Existing planned identities now replace their persisted planner payload and
  `NextRuns`; timer due times carry generations, so changed occurrence timers,
  midnight timers, and stale callbacks cannot retain old instants.
- Claim, terminal-save, and callback-reconciliation errors are returned or
  observed. Compensation load/claim/terminal/reconciliation errors follow the
  same path. A private `schedulerErrorObserver` hook is implemented by
  `Runtime.ReportSchedulerError`, which records the sanitized error code in
  `Runtime.Status`; failed timer ownership is rearmed for a short retry delay.
- Stop marks the scheduler as stopping, cancels active executor context, fences
  new executor entry, waits for an already-owned call, then finalizes the
  stopped state. Late queued callbacks are ignored.
- Corrupt configuration still returns an inert diagnostic Runtime, but its
  scheduler is constructed so `Start` always performs persisted ownership
  recovery with scheduling disabled.
- Compensation now requires an eligible failed outcome; a contradictory
  successful 429 result does not schedule a due time.

### Final focused verification

Tests:

- `TestStartupMarksEveryUnexecutedPastPlanMissed`
- `TestFutureOccurrenceFiresExactlyOnce`
- `TestReconcileRefreshesChangedPlannerOutputAndReplacesTimer`
- `TestReconcileReplacesMidnightTimerWhenTimezoneChanges`
- `TestQueuedCallbackAfterStopDoesNotExecute`
- `TestSchedulerReportsOccurrencePersistenceAndReconcileFailures` (`claim`,
  `terminal`, `post-callback reconcile`)
- `TestSchedulerReportsCompensationLoadFailure`
- `TestReconcilePlansTodayAndSevenFollowingLocalDates`
- `TestMidnightCallbackAdvancesPlanningHorizon`
- `TestCompensationFiresOnceWhileProcessOwnsTimer`
- `TestCompensationEligibilityIsRecheckedBeforeAttempt`
- `TestPreheatCompensationOnlyForEligibleAutomaticFailures` (all nine
  subtests, including `success_with_rate_limit_status`)
- `TestRuntimeStartMarksPersistedPreheatMissed`
- `TestRuntimeStartRecoversWithCorruptConfigWithoutActivatingSchedule`

Command:

```bash
docker run --rm -v "$PWD":/src -w /src golang:1.24-bookworm go test -count=1 ./internal/schedule ./internal/app -run 'Test(Start|Future|Compensation|Reconcile|Midnight|Queued|SchedulerReports|RuntimeStart|PreheatCompensationOnly)' -v
```

Output:

```text
=== RUN   TestStartupMarksEveryUnexecutedPastPlanMissed
--- PASS: TestStartupMarksEveryUnexecutedPastPlanMissed (0.00s)
=== RUN   TestFutureOccurrenceFiresExactlyOnce
--- PASS: TestFutureOccurrenceFiresExactlyOnce (0.00s)
=== RUN   TestReconcileRefreshesChangedPlannerOutputAndReplacesTimer
--- PASS: TestReconcileRefreshesChangedPlannerOutputAndReplacesTimer (0.00s)
=== RUN   TestReconcileReplacesMidnightTimerWhenTimezoneChanges
--- PASS: TestReconcileReplacesMidnightTimerWhenTimezoneChanges (0.00s)
=== RUN   TestQueuedCallbackAfterStopDoesNotExecute
--- PASS: TestQueuedCallbackAfterStopDoesNotExecute (0.00s)
=== RUN   TestSchedulerReportsOccurrencePersistenceAndReconcileFailures
=== RUN   TestSchedulerReportsOccurrencePersistenceAndReconcileFailures/claim
=== RUN   TestSchedulerReportsOccurrencePersistenceAndReconcileFailures/terminal
=== RUN   TestSchedulerReportsOccurrencePersistenceAndReconcileFailures/post-callback_reconcile
--- PASS: TestSchedulerReportsOccurrencePersistenceAndReconcileFailures (0.00s)
    --- PASS: TestSchedulerReportsOccurrencePersistenceAndReconcileFailures/claim (0.00s)
    --- PASS: TestSchedulerReportsOccurrencePersistenceAndReconcileFailures/terminal (0.00s)
    --- PASS: TestSchedulerReportsOccurrencePersistenceAndReconcileFailures/post-callback_reconcile (0.00s)
=== RUN   TestSchedulerReportsCompensationLoadFailure
--- PASS: TestSchedulerReportsCompensationLoadFailure (0.00s)
=== RUN   TestReconcilePlansTodayAndSevenFollowingLocalDates
--- PASS: TestReconcilePlansTodayAndSevenFollowingLocalDates (0.00s)
=== RUN   TestMidnightCallbackAdvancesPlanningHorizon
--- PASS: TestMidnightCallbackAdvancesPlanningHorizon (0.00s)
=== RUN   TestCompensationFiresOnceWhileProcessOwnsTimer
--- PASS: TestCompensationFiresOnceWhileProcessOwnsTimer (0.00s)
=== RUN   TestCompensationEligibilityIsRecheckedBeforeAttempt
--- PASS: TestCompensationEligibilityIsRecheckedBeforeAttempt (0.00s)
PASS
ok  	github.com/tapaixx/codex-window-reset/internal/schedule	0.019s
=== RUN   TestPreheatCompensationOnlyForEligibleAutomaticFailures
=== RUN   TestPreheatCompensationOnlyForEligibleAutomaticFailures/network
=== RUN   TestPreheatCompensationOnlyForEligibleAutomaticFailures/timeout
=== RUN   TestPreheatCompensationOnlyForEligibleAutomaticFailures/rate_limited
=== RUN   TestPreheatCompensationOnlyForEligibleAutomaticFailures/server
=== RUN   TestPreheatCompensationOnlyForEligibleAutomaticFailures/unauthorized
=== RUN   TestPreheatCompensationOnlyForEligibleAutomaticFailures/validation
=== RUN   TestPreheatCompensationOnlyForEligibleAutomaticFailures/unexpected_output
=== RUN   TestPreheatCompensationOnlyForEligibleAutomaticFailures/success_unverified
=== RUN   TestPreheatCompensationOnlyForEligibleAutomaticFailures/success_with_rate_limit_status
--- PASS: TestPreheatCompensationOnlyForEligibleAutomaticFailures (0.00s)
    --- PASS: TestPreheatCompensationOnlyForEligibleAutomaticFailures/network (0.00s)
    --- PASS: TestPreheatCompensationOnlyForEligibleAutomaticFailures/timeout (0.00s)
    --- PASS: TestPreheatCompensationOnlyForEligibleAutomaticFailures/rate_limited (0.00s)
    --- PASS: TestPreheatCompensationOnlyForEligibleAutomaticFailures/server (0.00s)
    --- PASS: TestPreheatCompensationOnlyForEligibleAutomaticFailures/unauthorized (0.00s)
    --- PASS: TestPreheatCompensationOnlyForEligibleAutomaticFailures/validation (0.00s)
    --- PASS: TestPreheatCompensationOnlyForEligibleAutomaticFailures/unexpected_output (0.00s)
    --- PASS: TestPreheatCompensationOnlyForEligibleAutomaticFailures/success_unverified (0.00s)
    --- PASS: TestPreheatCompensationOnlyForEligibleAutomaticFailures/success_with_rate_limit_status (0.00s)
=== RUN   TestRuntimeStartMarksPersistedPreheatMissed
--- PASS: TestRuntimeStartMarksPersistedPreheatMissed (0.00s)
=== RUN   TestRuntimeStartRecoversWithCorruptConfigWithoutActivatingSchedule
--- PASS: TestRuntimeStartRecoversWithCorruptConfigWithoutActivatingSchedule (0.00s)
PASS
ok  	github.com/tapaixx/codex-window-reset/internal/app	0.006s
```

### Full verification

```bash
docker run --rm -v "$PWD":/src -w /src golang:1.24-bookworm go test -count=1 ./...
```

```text
?    	github.com/tapaixx/codex-window-reset	[no test files]
ok   	github.com/tapaixx/codex-window-reset/internal/accounts	0.006s
ok   	github.com/tapaixx/codex-window-reset/internal/app	0.011s
ok   	github.com/tapaixx/codex-window-reset/internal/domain	0.012s
ok   	github.com/tapaixx/codex-window-reset/internal/host	0.010s
ok   	github.com/tapaixx/codex-window-reset/internal/probe	0.018s
ok   	github.com/tapaixx/codex-window-reset/internal/quota	0.069s
ok   	github.com/tapaixx/codex-window-reset/internal/schedule	0.114s
ok   	github.com/tapaixx/codex-window-reset/internal/simulate	0.032s
ok   	github.com/tapaixx/codex-window-reset/internal/store	0.280s
```

```bash
docker run --rm -v "$PWD":/src -w /src golang:1.24-bookworm go test -race ./...
```

```text
?    	github.com/tapaixx/codex-window-reset	[no test files]
ok   	github.com/tapaixx/codex-window-reset/internal/accounts	1.026s
ok   	github.com/tapaixx/codex-window-reset/internal/app	1.033s
ok   	github.com/tapaixx/codex-window-reset/internal/domain	1.048s
ok   	github.com/tapaixx/codex-window-reset/internal/host	1.021s
ok   	github.com/tapaixx/codex-window-reset/internal/probe	1.080s
ok   	github.com/tapaixx/codex-window-reset/internal/quota	1.110s
ok   	github.com/tapaixx/codex-window-reset/internal/schedule	2.050s
ok   	github.com/tapaixx/codex-window-reset/internal/simulate	1.153s
ok   	github.com/tapaixx/codex-window-reset/internal/store	1.350s
```

```bash
docker run --rm -v "$PWD":/src -w /src golang:1.24-bookworm go vet ./...
```

Output: empty; exit 0.

### Final fix-round-2 rerun after compensation terminal-save coverage

The final tree was rerun after adding
`TestCompensationTerminalPersistenceFailureRearmsAndRetriesTerminalSave`.

Focused command:

```bash
docker run --rm -v /share/codeSpace/codex-window-reset/.worktrees/codex-window-reset:/src -w /src golang:1.24-bookworm go test -count=1 ./internal/schedule ./internal/app -run 'Test(Start|Future|Compensation|Reconcile|Midnight|Queued|SchedulerReports|TerminalPersistenceFailure|PostReconcileFailure|RuntimeStart|PreheatCompensationOnly)' -v
```

Focused output:

```text
=== RUN   TestStartupMarksEveryUnexecutedPastPlanMissed
--- PASS: TestStartupMarksEveryUnexecutedPastPlanMissed (0.00s)
=== RUN   TestFutureOccurrenceFiresExactlyOnce
--- PASS: TestFutureOccurrenceFiresExactlyOnce (0.00s)
=== RUN   TestReconcileRefreshesChangedPlannerOutputAndReplacesTimer
--- PASS: TestReconcileRefreshesChangedPlannerOutputAndReplacesTimer (0.00s)
=== RUN   TestReconcileReplacesMidnightTimerWhenTimezoneChanges
--- PASS: TestReconcileReplacesMidnightTimerWhenTimezoneChanges (0.00s)
=== RUN   TestQueuedCallbackAfterStopDoesNotExecute
--- PASS: TestQueuedCallbackAfterStopDoesNotExecute (0.00s)
=== RUN   TestSchedulerReportsOccurrencePersistenceAndReconcileFailures
=== RUN   TestSchedulerReportsOccurrencePersistenceAndReconcileFailures/claim
=== RUN   TestSchedulerReportsOccurrencePersistenceAndReconcileFailures/terminal
=== RUN   TestSchedulerReportsOccurrencePersistenceAndReconcileFailures/post-callback_reconcile
--- PASS: TestSchedulerReportsOccurrencePersistenceAndReconcileFailures (0.00s)
    --- PASS: TestSchedulerReportsOccurrencePersistenceAndReconcileFailures/claim (0.00s)
    --- PASS: TestSchedulerReportsOccurrencePersistenceAndReconcileFailures/terminal (0.00s)
    --- PASS: TestSchedulerReportsOccurrencePersistenceAndReconcileFailures/post-callback_reconcile (0.00s)
=== RUN   TestTerminalPersistenceFailureRearmsAndRetriesTerminalSave
--- PASS: TestTerminalPersistenceFailureRearmsAndRetriesTerminalSave (0.00s)
=== RUN   TestCompensationTerminalPersistenceFailureRearmsAndRetriesTerminalSave
--- PASS: TestCompensationTerminalPersistenceFailureRearmsAndRetriesTerminalSave (0.00s)
=== RUN   TestPostReconcileFailureRearmsReconciliation
--- PASS: TestPostReconcileFailureRearmsReconciliation (0.00s)
=== RUN   TestSchedulerReportsCompensationLoadFailure
--- PASS: TestSchedulerReportsCompensationLoadFailure (0.00s)
=== RUN   TestReconcilePlansTodayAndSevenFollowingLocalDates
--- PASS: TestReconcilePlansTodayAndSevenFollowingLocalDates (0.00s)
=== RUN   TestMidnightCallbackAdvancesPlanningHorizon
--- PASS: TestMidnightCallbackAdvancesPlanningHorizon (0.00s)
=== RUN   TestCompensationFiresOnceWhileProcessOwnsTimer
--- PASS: TestCompensationFiresOnceWhileProcessOwnsTimer (0.00s)
=== RUN   TestCompensationEligibilityIsRecheckedBeforeAttempt
--- PASS: TestCompensationEligibilityIsRecheckedBeforeAttempt (0.00s)
PASS
ok  	github.com/tapaixx/codex-window-reset/internal/schedule	0.027s
=== RUN   TestPreheatCompensationOnlyForEligibleAutomaticFailures
=== RUN   TestPreheatCompensationOnlyForEligibleAutomaticFailures/network
=== RUN   TestPreheatCompensationOnlyForEligibleAutomaticFailures/timeout
=== RUN   TestPreheatCompensationOnlyForEligibleAutomaticFailures/rate_limited
=== RUN   TestPreheatCompensationOnlyForEligibleAutomaticFailures/server
=== RUN   TestPreheatCompensationOnlyForEligibleAutomaticFailures/unauthorized
=== RUN   TestPreheatCompensationOnlyForEligibleAutomaticFailures/validation
=== RUN   TestPreheatCompensationOnlyForEligibleAutomaticFailures/unexpected_output
=== RUN   TestPreheatCompensationOnlyForEligibleAutomaticFailures/success_unverified
=== RUN   TestPreheatCompensationOnlyForEligibleAutomaticFailures/success_with_rate_limit_status
--- PASS: TestPreheatCompensationOnlyForEligibleAutomaticFailures (0.00s)
    --- PASS: TestPreheatCompensationOnlyForEligibleAutomaticFailures/network (0.00s)
    --- PASS: TestPreheatCompensationOnlyForEligibleAutomaticFailures/timeout (0.00s)
    --- PASS: TestPreheatCompensationOnlyForEligibleAutomaticFailures/rate_limited (0.00s)
    --- PASS: TestPreheatCompensationOnlyForEligibleAutomaticFailures/server (0.00s)
    --- PASS: TestPreheatCompensationOnlyForEligibleAutomaticFailures/unauthorized (0.00s)
    --- PASS: TestPreheatCompensationOnlyForEligibleAutomaticFailures/validation (0.00s)
    --- PASS: TestPreheatCompensationOnlyForEligibleAutomaticFailures/unexpected_output (0.00s)
    --- PASS: TestPreheatCompensationOnlyForEligibleAutomaticFailures/success_unverified (0.00s)
    --- PASS: TestPreheatCompensationOnlyForEligibleAutomaticFailures/success_with_rate_limit_status (0.00s)
=== RUN   TestRuntimeStartMarksPersistedPreheatMissed
--- PASS: TestRuntimeStartMarksPersistedPreheatMissed (0.00s)
=== RUN   TestRuntimeStartRecoversWithCorruptConfigWithoutActivatingSchedule
--- PASS: TestRuntimeStartRecoversWithCorruptConfigWithoutActivatingSchedule (0.00s)
PASS
ok  	github.com/tapaixx/codex-window-reset/internal/app	0.006s
```

Full test command:

```bash
docker run --rm -v /share/codeSpace/codex-window-reset/.worktrees/codex-window-reset:/src -w /src golang:1.24-bookworm go test -count=1 ./...
```

Full test output:

```text
?   	github.com/tapaixx/codex-window-reset	[no test files]
ok  	github.com/tapaixx/codex-window-reset/internal/accounts	0.008s
ok  	github.com/tapaixx/codex-window-reset/internal/app	0.014s
ok  	github.com/tapaixx/codex-window-reset/internal/domain	0.012s
ok  	github.com/tapaixx/codex-window-reset/internal/host	0.011s
ok  	github.com/tapaixx/codex-window-reset/internal/probe	0.016s
ok  	github.com/tapaixx/codex-window-reset/internal/quota	0.088s
ok  	github.com/tapaixx/codex-window-reset/internal/schedule	0.123s
ok  	github.com/tapaixx/codex-window-reset/internal/simulate	0.031s
ok  	github.com/tapaixx/codex-window-reset/internal/store	0.265s
```

Race command:

```bash
docker run --rm -v /share/codeSpace/codex-window-reset/.worktrees/codex-window-reset:/src -w /src golang:1.24-bookworm go test -race ./...
```

Race output:

```text
?   	github.com/tapaixx/codex-window-reset	[no test files]
ok  	github.com/tapaixx/codex-window-reset/internal/accounts	1.021s
ok  	github.com/tapaixx/codex-window-reset/internal/app	1.031s
ok  	github.com/tapaixx/codex-window-reset/internal/domain	1.036s
ok  	github.com/tapaixx/codex-window-reset/internal/host	1.022s
ok  	github.com/tapaixx/codex-window-reset/internal/probe	1.069s
ok  	github.com/tapaixx/codex-window-reset/internal/quota	1.101s
ok  	github.com/tapaixx/codex-window-reset/internal/schedule	1.977s
ok  	github.com/tapaixx/codex-window-reset/internal/simulate	1.108s
ok  	github.com/tapaixx/codex-window-reset/internal/store	1.315s
```

Vet command:

```bash
docker run --rm -v /share/codeSpace/codex-window-reset/.worktrees/codex-window-reset:/src -w /src golang:1.24-bookworm go vet ./...
```

Vet output: empty; exit 0.

Formatting command:

```bash
docker run --rm -v /share/codeSpace/codex-window-reset/.worktrees/codex-window-reset:/src -w /src golang:1.24-bookworm gofmt -d internal/schedule/scheduler.go internal/schedule/scheduler_test.go
```

Formatting output: empty; exit 0.

## Fix round 2: recover timer ownership after terminal/reconcile failures

### Status

DONE. The scheduler now retains an in-memory terminal result when its
terminal persistence fails, rearms a timer, and retries only that durable
write without repeating executor I/O. `reconcileTimers` retains timer
ownership for those `running` occurrences. Callback reconciliation failures
schedule a separate retry timer, so a consumed occurrence or midnight timer
cannot strand horizon advancement. Compensation terminal saves use the same
one-shot durable retry path.

### Test files

- `internal/schedule/scheduler.go`
- `internal/schedule/scheduler_test.go`

The focused regression tests are:

- `TestTerminalPersistenceFailureRearmsAndRetriesTerminalSave`
- `TestCompensationTerminalPersistenceFailureRearmsAndRetriesTerminalSave`
- `TestPostReconcileFailureRearmsReconciliation`

The planner fixture gained a one-shot planning-error queue so the
post-reconcile test fails once and then proves recovery on the retry timer.

### RED evidence

Command:

```bash
docker run --rm -v /share/codeSpace/codex-window-reset/.worktrees/codex-window-reset:/src -w /src golang:1.24-bookworm go test -count=1 ./internal/schedule -run 'Test(TerminalPersistenceFailure|PostReconcileFailure)' -v
```

Output:

```text
=== RUN   TestTerminalPersistenceFailureRearmsAndRetriesTerminalSave
    scheduler_test.go:523: compensation timer was not installed for 2026-09-09 06:31:01 +0000 UTC
--- FAIL: TestTerminalPersistenceFailureRearmsAndRetriesTerminalSave (1.00s)
=== RUN   TestPostReconcileFailureRearmsReconciliation
    scheduler_test.go:585: compensation timer was not installed for 2026-09-09 06:31:01 +0000 UTC
--- FAIL: TestPostReconcileFailureRearmsReconciliation (1.00s)
FAIL
FAIL	github.com/tapaixx/codex-window-reset/internal/schedule	2.009s
FAIL
```

### GREEN focused verification

Command:

```bash
docker run --rm -v /share/codeSpace/codex-window-reset/.worktrees/codex-window-reset:/src -w /src golang:1.24-bookworm go test -count=1 ./internal/schedule -run 'Test(TerminalPersistenceFailure|PostReconcileFailure)' -v
```

Output:

```text
=== RUN   TestTerminalPersistenceFailureRearmsAndRetriesTerminalSave
--- PASS: TestTerminalPersistenceFailureRearmsAndRetriesTerminalSave (0.00s)
=== RUN   TestPostReconcileFailureRearmsReconciliation
--- PASS: TestPostReconcileFailureRearmsReconciliation (0.00s)
PASS
ok  	github.com/tapaixx/codex-window-reset/internal/schedule	0.009s
```

### Full focused scheduler/app verification

Command:

```bash
docker run --rm -v /share/codeSpace/codex-window-reset/.worktrees/codex-window-reset:/src -w /src golang:1.24-bookworm go test -count=1 ./internal/schedule ./internal/app -run 'Test(Start|Future|Compensation|Reconcile|Midnight|Queued|SchedulerReports|TerminalPersistenceFailure|PostReconcileFailure|RuntimeStart|PreheatCompensationOnly)' -v
```

Output:

```text
=== RUN   TestStartupMarksEveryUnexecutedPastPlanMissed
--- PASS: TestStartupMarksEveryUnexecutedPastPlanMissed (0.00s)
=== RUN   TestFutureOccurrenceFiresExactlyOnce
--- PASS: TestFutureOccurrenceFiresExactlyOnce (0.00s)
=== RUN   TestReconcileRefreshesChangedPlannerOutputAndReplacesTimer
--- PASS: TestReconcileRefreshesChangedPlannerOutputAndReplacesTimer (0.00s)
=== RUN   TestReconcileReplacesMidnightTimerWhenTimezoneChanges
--- PASS: TestReconcileReplacesMidnightTimerWhenTimezoneChanges (0.00s)
=== RUN   TestQueuedCallbackAfterStopDoesNotExecute
--- PASS: TestQueuedCallbackAfterStopDoesNotExecute (0.00s)
=== RUN   TestSchedulerReportsOccurrencePersistenceAndReconcileFailures
=== RUN   TestSchedulerReportsOccurrencePersistenceAndReconcileFailures/claim
=== RUN   TestSchedulerReportsOccurrencePersistenceAndReconcileFailures/terminal
=== RUN   TestSchedulerReportsOccurrencePersistenceAndReconcileFailures/post-callback_reconcile
--- PASS: TestSchedulerReportsOccurrencePersistenceAndReconcileFailures (0.00s)
    --- PASS: TestSchedulerReportsOccurrencePersistenceAndReconcileFailures/claim (0.00s)
    --- PASS: TestSchedulerReportsOccurrencePersistenceAndReconcileFailures/terminal (0.00s)
    --- PASS: TestSchedulerReportsOccurrencePersistenceAndReconcileFailures/post-callback_reconcile (0.00s)
=== RUN   TestTerminalPersistenceFailureRearmsAndRetriesTerminalSave
--- PASS: TestTerminalPersistenceFailureRearmsAndRetriesTerminalSave (0.00s)
=== RUN   TestPostReconcileFailureRearmsReconciliation
--- PASS: TestPostReconcileFailureRearmsReconciliation (0.00s)
=== RUN   TestSchedulerReportsCompensationLoadFailure
--- PASS: TestSchedulerReportsCompensationLoadFailure (0.00s)
=== RUN   TestReconcilePlansTodayAndSevenFollowingLocalDates
--- PASS: TestReconcilePlansTodayAndSevenFollowingLocalDates (0.00s)
=== RUN   TestMidnightCallbackAdvancesPlanningHorizon
--- PASS: TestMidnightCallbackAdvancesPlanningHorizon (0.00s)
=== RUN   TestCompensationFiresOnceWhileProcessOwnsTimer
--- PASS: TestCompensationFiresOnceWhileProcessOwnsTimer (0.00s)
=== RUN   TestCompensationEligibilityIsRecheckedBeforeAttempt
--- PASS: TestCompensationEligibilityIsRecheckedBeforeAttempt (0.00s)
PASS
ok  	github.com/tapaixx/codex-window-reset/internal/schedule	0.019s
=== RUN   TestPreheatCompensationOnlyForEligibleAutomaticFailures
=== RUN   TestPreheatCompensationOnlyForEligibleAutomaticFailures/network
=== RUN   TestPreheatCompensationOnlyForEligibleAutomaticFailures/timeout
=== RUN   TestPreheatCompensationOnlyForEligibleAutomaticFailures/rate_limited
=== RUN   TestPreheatCompensationOnlyForEligibleAutomaticFailures/server
=== RUN   TestPreheatCompensationOnlyForEligibleAutomaticFailures/unauthorized
=== RUN   TestPreheatCompensationOnlyForEligibleAutomaticFailures/validation
=== RUN   TestPreheatCompensationOnlyForEligibleAutomaticFailures/unexpected_output
=== RUN   TestPreheatCompensationOnlyForEligibleAutomaticFailures/success_unverified
=== RUN   TestPreheatCompensationOnlyForEligibleAutomaticFailures/success_with_rate_limit_status
--- PASS: TestPreheatCompensationOnlyForEligibleAutomaticFailures (0.00s)
    --- PASS: TestPreheatCompensationOnlyForEligibleAutomaticFailures/network (0.00s)
    --- PASS: TestPreheatCompensationOnlyForEligibleAutomaticFailures/timeout (0.00s)
    --- PASS: TestPreheatCompensationOnlyForEligibleAutomaticFailures/rate_limited (0.00s)
    --- PASS: TestPreheatCompensationOnlyForEligibleAutomaticFailures/server (0.00s)
    --- PASS: TestPreheatCompensationOnlyForEligibleAutomaticFailures/unauthorized (0.00s)
    --- PASS: TestPreheatCompensationOnlyForEligibleAutomaticFailures/validation (0.00s)
    --- PASS: TestPreheatCompensationOnlyForEligibleAutomaticFailures/unexpected_output (0.00s)
    --- PASS: TestPreheatCompensationOnlyForEligibleAutomaticFailures/success_unverified (0.00s)
    --- PASS: TestPreheatCompensationOnlyForEligibleAutomaticFailures/success_with_rate_limit_status (0.00s)
=== RUN   TestRuntimeStartMarksPersistedPreheatMissed
--- PASS: TestRuntimeStartMarksPersistedPreheatMissed (0.00s)
=== RUN   TestRuntimeStartRecoversWithCorruptConfigWithoutActivatingSchedule
--- PASS: TestRuntimeStartRecoversWithCorruptConfigWithoutActivatingSchedule (0.00s)
PASS
ok  	github.com/tapaixx/codex-window-reset/internal/app	0.006s
```

### Full verification

Command:

```bash
docker run --rm -v /share/codeSpace/codex-window-reset/.worktrees/codex-window-reset:/src -w /src golang:1.24-bookworm go test -count=1 ./...
```

Output:

```text
?   	github.com/tapaixx/codex-window-reset	[no test files]
ok  	github.com/tapaixx/codex-window-reset/internal/accounts	0.007s
ok  	github.com/tapaixx/codex-window-reset/internal/app	0.009s
ok  	github.com/tapaixx/codex-window-reset/internal/domain	0.019s
ok  	github.com/tapaixx/codex-window-reset/internal/host	0.011s
ok  	github.com/tapaixx/codex-window-reset/internal/probe	0.016s
ok  	github.com/tapaixx/codex-window-reset/internal/quota	0.071s
ok  	github.com/tapaixx/codex-window-reset/internal/schedule	0.167s
ok  	github.com/tapaixx/codex-window-reset/internal/simulate	0.022s
ok  	github.com/tapaixx/codex-window-reset/internal/store	0.240s
```

Command:

```bash
docker run --rm -v /share/codeSpace/codex-window-reset/.worktrees/codex-window-reset:/src -w /src golang:1.24-bookworm go test -race ./...
```

Output:

```text
?   	github.com/tapaixx/codex-window-reset	[no test files]
ok  	github.com/tapaixx/codex-window-reset/internal/accounts	1.020s
ok  	github.com/tapaixx/codex-window-reset/internal/app	1.027s
ok  	github.com/tapaixx/codex-window-reset/internal/domain	1.044s
ok  	github.com/tapaixx/codex-window-reset/internal/host	1.033s
ok  	github.com/tapaixx/codex-window-reset/internal/probe	1.075s
ok  	github.com/tapaixx/codex-window-reset/internal/quota	1.089s
ok  	github.com/tapaixx/codex-window-reset/internal/schedule	1.953s
ok  	github.com/tapaixx/codex-window-reset/internal/simulate	1.109s
ok  	github.com/tapaixx/codex-window-reset/internal/store	1.330s
```

Command:

```bash
docker run --rm -v /share/codeSpace/codex-window-reset/.worktrees/codex-window-reset:/src -w /src golang:1.24-bookworm go vet ./...
```

Output: empty; exit 0.

Command:

```bash
docker run --rm -v /share/codeSpace/codex-window-reset/.worktrees/codex-window-reset:/src -w /src golang:1.24-bookworm gofmt -d internal/schedule/scheduler.go internal/schedule/scheduler_test.go
```

Output: empty; exit 0.

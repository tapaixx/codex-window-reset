# Release verification evidence

This record is for Task 14 and is based on the final reviewed implementation
at `814fbee`.

The release contract is `v0.0.1` as a tag and `0.0.1` as the tag-stripped
version, with Linux `amd64` and `arm64` shared libraries. The native plugin ABI
must export `cliproxy_plugin_init`, `cliproxyPluginCall`, `cliproxyPluginFree`,
and `cliproxyPluginShutdown`.

The seven release assets are:

```text
codex-window-reset_0.0.1_linux_amd64.zip
codex-window-reset_0.0.1_linux_arm64.zip
checksums.txt
codex-window-reset-linux-amd64.so
codex-window-reset-linux-arm64.so
codex-window-reset-linux-amd64.so.sha256
codex-window-reset-linux-arm64.so.sha256
```

## Acceptance-criteria matrix

Each requirement below is copied verbatim from the approved design. Test names
are the exact Go test function names, Go subtest names where relevant, or Node
test names in the listed source files.

| # | Acceptance criterion (verbatim) | Exact tests and source files | Evidence boundary |
| --- | --- | --- | --- |
| 1 | A new installation performs no automatic Codex request until the Operator completes and enables a valid schedule with at least one Scheduled Account. | `TestDefaultConfigStartsInert`, `TestValidateConfigRequiresActivationFields` — `internal/domain/config_test.go`<br>`TestRuntimeStartRecoversWithCorruptConfigWithoutActivatingSchedule` — `internal/app/restart_test.go`<br>`TestManagementDispatchEndToEndPreservesLifecycleAndSecretBoundaries` — `integration_test.go` | Local Go suite in Go 1.24 Docker; CI repeats the Go and integration suites. |
| 2 | The scheduler produces deterministic account occurrences and never catches up missed work. | `TestPlanDayUsesPreheatFormulaAndDeterministicSlots`, `TestOccurrenceIDUsesLocalDatePeriodAndAccount` — `internal/schedule/planner_test.go`<br>`TestStartupMarksEveryUnexecutedPastPlanMissed`, `TestFutureOccurrenceFiresExactlyOnce` — `internal/schedule/scheduler_test.go`<br>`TestRuntimeStartMarksPersistedPreheatMissed` — `internal/app/restart_test.go` | Local Go suite in Go 1.24 Docker; CI repeats the scheduler tests. |
| 3 | A known low Long Window blocks preheating until a successful refresh proves recovery; unknown quota without a hold proceeds and is auditable. | `TestQuotaEvaluateFailsOpenWithoutKnownHold`, `TestQuotaGuardrailHoldPersistsAcrossFailureStalenessAndRestart`, `TestQuotaGuardrailHoldClearsOnlyAfterEveryLongWindowRecovers` — `internal/quota/service_test.go`<br>`TestPreheatGuardrailAndSufficientDecisionsSkipProbe`, `TestPreheatQuotaUnknownFailsOpenAndRecordsIndependentOutcomes` — `internal/app/preheat_test.go` | Local Go suite in Go 1.24 Docker; CI repeats the quota and preheat tests. |
| 4 | Manual and scheduled Probe Requests disclose quota side effects and keep request/window outcomes separate. | `TestExecuteBuildsConfigurableModelRequest` — `internal/probe/client_test.go`<br>`TestManualProbeAllowsUnavailableOverrideButRejectsDisabled` — `internal/app/manual_test.go`<br>`TestClassifyWindowOutcome` — `internal/app/outcomes_test.go`<br>`TestPreheatQuotaUnknownFailsOpenAndRecordsIndependentOutcomes` — `internal/app/preheat_test.go`<br>`unavailable accounts require an explicit override before Health Probe`, `ordinary Health Probe payload never inherits an unavailable override` — `web/tests/fix-round-2.test.mjs` | Local Go and Node suites; CI repeats both suites. The UI disclosure is asserted by the explicit acknowledgement payload tests. |
| 5 | A successful but unverified preheat is not retried. | `TestPreheatCompensationOnlyForEligibleAutomaticFailures` (subtest `success unverified`) — `internal/app/preheat_test.go`<br>`TestClassifyWindowOutcome` (subcases `missing after`, `request failed`) — `internal/app/outcomes_test.go` | Local Go suite in Go 1.24 Docker; CI repeats the preheat tests. |
| 6 | A Quota Reset can target only one account, requires confirmation, uses server-side single-flight and durable idempotency, and produces a Reset Audit. | `TestResetPersistsPendingBeforeConsumeAndReplaysOutcome`, `TestResetRejectsIdempotencyReuseForDifferentAccount`, `TestResetReturnsAccountBusyForDistinctSimultaneousKey`, `TestResetSameKeyWaitsForCurrentFlightAndReloadsFinalRecord`, `TestResetSameKeyWaitCancellationReturnsStableBusyError`, `TestResetRecoversPendingOnStartupWithoutConsuming` — `internal/app/reset_test.go`<br>`TestResetAuditDuplicateKeyReturnsConflict`, `TestResetAuditReplaceUpdatesExistingRecord`, `TestResetAuditRetentionKeepsExactCutoffAndRecoversOnlyExplicitly` — `internal/store/repositories_test.go`<br>`TestManagementRejectsUnknownJSONFieldsAndPassesExactAuditConfirmation` — `internal/management/contract_test.go`<br>`TestManagementDispatchEndToEndPreservesLifecycleAndSecretBoundaries` — `integration_test.go` | Local Go suite in Go 1.24 Docker; CI repeats the management and reset suites. |
| 7 | Ordinary history deletion cannot remove configuration, credentials, runtime state, or reset audit. | `TestHistoryClearOnlyClearsHistoryDocument`, `TestOrdinaryClearCannotDeleteAuditOrRuntimeState`, `TestResetAuditClearOnlyClearsAuditDocument` — `internal/store/repositories_test.go`<br>`TestClearResetAuditRequiresExactPhrase` — `internal/app/reset_test.go`<br>`TestManagementDispatchEndToEndPreservesLifecycleAndSecretBoundaries` — `integration_test.go` | Local Go suite in Go 1.24 Docker; CI repeats the persistence and integration suites. |
| 8 | The panel never owns a CLIProxyAPI management key or receives a Codex access token. | `TestAccountProjectionNeverContainsToken`, `TestAccountAndErrorJSONNeverExposeIdentityOrCredentialFixtures` — `internal/accounts/service_test.go`<br>`TestResetAuditDoesNotPersistSecrets` — `internal/app/reset_test.go`<br>`TestManagementResponsesAndAssetsContainNoCredentialMaterial`, `TestManagementCredentialScanUsesProductionBoundary` — `internal/management/contract_test.go`<br>`TestStructuredLoggerAcceptsOnlySanitizedOutcomeFields`, `TestManagementDispatchEndToEndPreservesLifecycleAndSecretBoundaries` — `integration_test.go`<br>`does not own authentication storage`, `requests use same-origin credentials and dispatch host authentication failures`, `all production asset imports resolve and forbidden credential fallbacks are absent` — `web/tests/api.test.mjs`, `web/tests/markup.test.mjs` | Local Go and Node suites plus the explicit `rg` secret scan; CI repeats the test suites. Documentation matches are limited to README’s allowed statement that the plugin does not ask for or save the management key. |
| 9 | Desktop and mobile layouts preserve all essential state without page-level horizontal scrolling. | `panel IDs are unique and tab panels are linked`, `static buttons have accessible names and inputs have labels`, `module exports and design tokens stay unique and exact` — `web/tests/markup.test.mjs`<br>Responsive CSS source: `web/styles.css`; panel source: `web/panel.html` | Local Node suite and source scan. The checked static contract covers the 767px breakpoint, contained layout primitives, labels, focus states, and 44px controls; no browser viewport automation is present. |
| 10 | The complete verification suite passes for both supported release architectures. | `TestDispatchRegistersManagementAndDynamicResources`, `TestExportedCallAllocatesAndFreesPluginBuffer`, `TestDispatchAcceptsAllResourceBasePathFieldCasing` — `main_test.go`<br>`TestManagementResourcesServeEveryRegisteredAsset`, `TestResourceRegistrationUsesExactContentTypes` — `internal/management/contract_test.go`<br>Release layout assertions: `scripts/package_release_test.sh` | Architecture evidence is the local Go 1.24 Docker test/vet suite plus amd64 and arm64 C-shared builds. The amd64 artifact is inspected with `file` and `nm -D`; CI remains the release gate for the same two architectures. |

## Commands and results

Dates below use the execution date in the environment (`2026-09-10`). Go
commands run in a Go 1.24 Docker container because the host has no Go
toolchain. The container uses the available cross compiler for arm64 and does
not execute the arm64 artifact. The container reported `go version go1.24.13
linux/arm64`.

The commands below were run fresh against the final reviewed base. Exit codes
and concise output summaries are recorded rather than inferred from prior
reports.

| Date | Command | Outcome |
| --- | --- | --- |
| 2026-09-10 | `gofmt -w $(find . -name '*.go' -not -path './.git/*')` | Exit 0 in Go 1.24 Docker; no Go-file diff remained afterward. |
| 2026-09-10 | `go vet ./...` | Exit 0 in Go 1.24 Docker; no diagnostics. |
| 2026-09-10 | `go test -count=1 ./...` | Exit 0 in Go 1.24 Docker; all root and tested internal packages reported `ok`; `internal/testabi` reported no test files. |
| 2026-09-10 | `go test -count=1 -race ./...` | Exit 0 in Go 1.24 Docker; all root and tested internal packages reported `ok`; `internal/testabi` reported no test files. |
| 2026-09-10 | `node --check web/modules/api.js && node --check web/modules/state.js && node --check web/modules/accounts.js && node --check web/modules/schedule.js && node --check web/modules/simulator.js && node --check web/modules/history.js && node --check web/modules/main.js` | Exit 0 on the host Node runtime. |
| 2026-09-10 | `node --test web/tests/*.test.mjs` | Exit 0; Node reported 5 tests, 5 passed, 0 failed, 0 skipped. |
| 2026-09-10 | Required secret scan (exact command below) | Exit 0 because it found only the allowed README management-key wording and test-only fixture/assertion strings. The production-only boundary scan below returned no matches; no test was weakened. |
| 2026-09-10 | Required asset scan (exact command below) | Exit 1 with no matches; no wildcard asset registration was found. |
| 2026-09-10 | `CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -trimpath -buildmode=c-shared -o /tmp/codex-window-reset.so .` | Exit 0 in Go 1.24 Docker. The native arm64 container exported `CC=x86_64-linux-gnu-gcc` before this exact command. |
| 2026-09-10 | amd64 `file`/`nm -D` symbol inspection (exact command below) | Exit 0; `file` reported an ELF 64-bit LSB x86-64 shared object, and `nm -D` showed all four required ABI symbols. |
| 2026-09-10 | `CC=aarch64-linux-gnu-gcc CGO_ENABLED=1 GOOS=linux GOARCH=arm64 go build -trimpath -buildmode=c-shared -o /tmp/codex-window-reset-arm64.so .` | Exit 0 in Go 1.24 Docker; `file` reported an ELF 64-bit LSB ARM aarch64 shared object. This local arm64 cross-build means the architecture row is not CI-pending. |
| 2026-09-10 | `scripts/package_release_test.sh /out 0.0.1` from `/out` after Docker-built libraries were packaged with `SOURCE_DATE_EPOCH=0 scripts/package-release.sh 0.0.1 /out` | Exit 0 in Go 1.24 Docker with `zip` and `unzip`; all four checksum entries reported `OK`, and the seven named assets were present. |
| 2026-09-10 | `git diff --check` and tracked-SDD-artifact inspection | Recorded after the final documentation and index cleanup below; no whitespace errors, no Task 8 report remains tracked, and no new unexpected SDD artifact was introduced. Pre-existing tracked Task 5 and Task 6 reports remain unchanged. |

The exact broad scans and amd64 symbol inspection were:

```text
rg -n 'access_token|Authorization: Bearer|management[_ -]?key|localStorage|sessionStorage' web internal/management README.md
rg -n '/panel/assets/|Resources:.*\*|resource.*\*' . --glob '*.go' --glob '*.js' --glob '*.html'
file /tmp/codex-window-reset.so && nm -D /tmp/codex-window-reset.so | rg 'cliproxy_plugin_init|cliproxyPluginCall|cliproxyPluginFree|cliproxyPluginShutdown'
```

The required secret scan also had this production-boundary follow-up:

```text
rg -n 'access_token|Authorization: Bearer|management[_ -]?key|localStorage|sessionStorage' web/modules web/panel.html web/styles.css internal/management --glob '*.go' --glob '*.js' --glob '*.html' --glob '!*_test.go'
```

It exited 1 with no matches. The README matches from the required broad scan
are documentation of the host-owned authentication boundary, not production
storage, logging, transport, or a plugin-owned prompt. The broad scan's other
matches are deliberately test-only fixture/assertion text in
`web/tests/` and `internal/management/contract_test.go`.

An initial diagnostic package-smoke invocation from `/src` exited 1 because
the smoke script verifies basename-only checksum entries from its release
directory working directory. A host retry was discarded because the host has
no `zip`. The successful command above used the required `/out` working
directory and installed `zip` and `unzip` in the Go 1.24 container; no source
file was changed for either environment issue.

## Tracking cleanup

`.superpowers/sdd/2026-09-09-codex-window-reset/task-8-report.md` is a
disposable ignored report. It is intentionally kept in the local workspace
but removed from Git tracking in the Task 14 commit. The current Task 14
workspace and report remain ignored and available for final review.

The final `git ls-files -- .superpowers/**` inspection still shows the two
pre-existing tracked Task 5 and Task 6 reports; they are outside this task's
requested cleanup and were not modified. The Task 8 report is absent from the
index while its local file remains present.

# Release verification evidence

## 批量额度刷新与账号列表加载的性能修复（当前修复）

2026-09-13：复查“页面首次启动、模拟器首次启动、各种调用”的性能问题，发现两处
真实的冗余账号发现调用，以及一处冗余前端重绘：

- `internal/accounts.Service` 按设计不缓存（每次 `List`/`Find` 都重新调用宿主
  `ListAuthFiles`），但 `RefreshQuotas` 里每个账号的 worker 各自调用一次
  `Accounts.Find`（内部等价于整表 `List`），把一次 N 账号的批量刷新变成 N 次
  整表账号发现——账号越多越明显。修复为整批共享一次 `Accounts.List`，按 key
  在内存里查找，不再逐账号重新发现；已改用 `TestRefreshQuotasDiscoversAccountsOnceForTheWholeBatch`
  验证批量刷新只调用一次 `List`、零次 `Find`。
- `Runtime.ListAccounts`（`GET /accounts` 使用）已经有 5 秒内存缓存，但
  `ListQuota`（面板首次加载调用的 `GET /quota`）绕开了这个缓存，直接调用
  `Accounts.List`，导致短时间内重复加载面板或轮询状态时反复触发整表账号发现。
  改为复用 `ListAccounts` 的缓存；`TestListQuotaReusesAccountDiscoveryCache`
  验证 5 秒窗口内连续两次 `ListQuota` 只触发一次账号发现。手动检测
  （`StartManualProbes`）与窗口重置仍然保持绕开缓存，因为它们在执行真实操作前
  需要最新的禁用/不可用状态，不适合和展示型读取共用短期缓存。
- 额度快照写入插件内存的修复把批量刷新从“逐账号流式到达”改成“一次请求拿到
  整批结果”，但 `main.js` 的 `onUpdate` 回调仍在循环里对每个账号各调用一次
  `renderAccounts()`（重建整张账号表，且每行都要对 `state.history` 做一次
  filter+sort）。数据其实是同一时刻一起到达的，循环内的中间重绘完全是浪费——
  `withQuotaRefresh` 结束时已经会统一重绘一次。改为循环内只合并数据，重绘只在
  最外层做一次。

两处后端修复都先在临时改回旧实现后确认对应新测试会失败，再验证修复后通过，
避免测试形同虚设；`internal/accounts.Service` 的“不缓存”设计本身未改动，只是
避免在同一次调用里为同一批数据重复触发它。

## 补齐手动检测与窗口重置的额度刷新超时（当前修复）

2026-09-13：额度快照写入插件内存的修复只给批量“刷新额度”接口加了 15 秒上限，
手动健康检测（`executeManual`）探测前后各一次的额度刷新、以及窗口重置
（`ResetQuota`）消费额度前后各一次的额度刷新仍直接复用调用方 `ctx`，没有独立
超时——与 v0.0.18 已经修复的自动预热路径是同一类根因，但只覆盖了三条调用路径
中的一条。上游额度接口卡住时，手动检测或重置请求会无限期挂起。

现在这两条路径的额度刷新都套用与批量刷新相同的 `quotaRefreshTimeout`（15 秒）。

回归证据：`TestManualProbeSurvivesStalledQuotaRefresh` 验证卡住的探测前/后
额度刷新不会阻塞探测本身，运行在限定时间内完成并写入历史记录；
`TestResetQuotaBoundsStalledPreConsumeRefresh` 验证消费额度前的刷新卡住时，
重置请求会在限定时间内失败，且不会误触发上游消费调用。两个测试都先在移除
超时包装的情况下确认会挂起超时失败，再验证修复后通过。

## 批量额度刷新迁移到插件缓存路由后修复浏览器回归测试（当前修复）

2026-09-13：额度快照写入插件内存的修复把 `refreshAccountQuotas` 从“逐账号并发
调用宿主 `/api-call`”改成“一次性调用插件 `POST /quota/refresh`”，但配套的
`web/tests/browser.test.mjs` 夹具服务器和 `audit-refresh*` 断言仍假设旧的按
`auth_index` 逐个请求、且额外有“先返回用量再返回重置次数”的两阶段进度状态。
四个浏览器回归测试（`audit-refresh`、`audit-refresh-empty`、
`audit-refresh-failure`、`audit-refresh-progress`）因此失败：夹具的
`/quota/refresh` 处理器忽略请求体、恒定返回同一个成功快照，从不模拟失败或按
账号区分结果。

修复：夹具按真实契约解析 `account_keys` 并按 `failAuthIndexes` 逐账号返回
成功或失败视图；`audit-refresh-progress` 改为验证单次请求期间面板保持不可用、
不会再出现已废弃的“获取重置次数中”两阶段状态。同时补上一个真实产品缺陷——
`main.js` 渲染“额度更新时间”列时只看 `stale` 字段，而新的单次刷新失败视图
不再触发前端自造的 `stale: true`，导致失败账号不会显示“已过期”；现在同时检查
`refresh_error_code`。

## 额度快照写入插件内存（当前修复）

2026-09-13：发现面板的手动刷新虽然显示了新额度，却直接从浏览器调用宿主管理
`/api-call`，结果只存在页面状态；重新打开插件时只能看到空额度。现在手动刷新
统一调用插件 `POST /quota/refresh`，由 Runtime 写入进程内快照，打开面板的
`GET /quota` 只读该缓存；表格显示快照实际获取时间。停用账号仍包含在刷新请求中。
每个后台账号刷新增加 15 秒上限，避免上游卡住拖垮整批刷新。

回归证据：`web/tests/quota-refresh.test.mjs` 验证刷新只发插件缓存路由、包含停用
账号并逐账号报告失败；`TestRefreshQuotasBoundsStalledUpstreamRefresh` 验证卡住的
后台请求会在限定时间内返回失败视图。

## v0.0.15 额度刷新等待与错峰批次分组

2026-09-13：用户报告额度长时间无法刷新，同一轮随机错峰预热被拆为多批。修复纳入 v0.0.15；发布说明见 [v0.0.15](releases/v0.0.15.md)。

### 根因与修复边界

- 刷新逻辑先串行等待每个账号的用量和重置次数，再用 `Promise.allSettled` 等待所有账号；没有请求截止时间。一个慢请求可以阻止其他已成功用量显示。现在最多并发处理 3 个账号，用量一返回立即显示；单次用量请求 15 秒、重置详情 5 秒超时，失败账号保留旧快照并标记过期。详情失败不丢弃用量，也不开放重置按钮。
- 后台面板加载期间，刷新按钮原本被 `panelLoading` 静默拦截。现在明确刷新可独立执行，并用请求代次避免稍后返回的缓存覆盖新额度；历史、审计等各自就绪即显示。
- 账号发现仍通过宿主 `/auth-files`，但额度展示不再绕过 Runtime：`ListQuota` 只读取插件内存快照，显式刷新才进入 `RefreshQuotas`。
- 自动记录的 occurrence_id 是 `日期/p时段/账号`。按完整 ID 分组会把同一时段的账号拆开。现在按 `日期/p时段` 分组，失败补偿归入原计划批次；手动任务按 run_id，不同日期、时段、手动任务不混合。无法识别批次的旧记录单独保留，不靠执行时间相近来猜测。

### 防复发与证据

- `quota-refresh.test.mjs` 覆盖响应体卡住时超时并中止、先显示用量、详情超时保留成功结果、并发上限和禁用账号排除。
- `history-batches.test.mjs` 覆盖 08:15 / 08:20 错峰请求与 08:25 补偿合并为两个账号三次操作，以及跨日期/时段/手动任务隔离。
- 浏览器以延迟重置详情和延迟后台快照模拟卡顿，验证逐项显示及新数据不被覆盖；移动端验证批次展开和刷新后保留展开状态。
- 相关单元测试 19 项通过；源码页面和真实 Go 嵌入页面各 8 项关键交互通过，真实页面另通过未保存草稿保护检查。`npm run check` 通过。未进行真实账号或上游请求，也未声称已部署到用户插件。

## v0.0.14 发布失败修复记录

2026-09-13：v0.0.13 的 CI / Release 因模拟器浏览器断言仍要求两个预热标记而失败。
已将静态数据和断言同步为 06:30、11:30、16:30 三个时间点。
同一失败用例分别使用源码页面、Go 实际嵌入页面及模拟 JSON 执行，均通过（每种模式包含 375px、1440px 明暗主题，0 跳过）。本地未追加全量测试；发布流水线继续执行已有门禁。

详细根因、日志链接与防复发规则见 [v0.0.14 发布复盘](releases/v0.0.14.md)。

## 历史验证记录

This record covers the reviewed v0.0.2 implementation and the follow-up
contract corrections verified in the working tree on 2026-09-11.

The release contract is `v0.0.3` as a tag and `0.0.3` as the tag-stripped
version, with Linux `amd64` and `arm64` shared libraries. The native plugin ABI
must export `cliproxy_plugin_init`, `cliproxyPluginCall`, `cliproxyPluginFree`,
and `cliproxyPluginShutdown`.

The seven release assets are:

```text
codex-window-reset_0.0.3_linux_amd64.zip
codex-window-reset_0.0.3_linux_arm64.zip
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
| 6 | A Quota Reset can target only one account, requires confirmation, uses server-side single-flight and durable idempotency, and produces a Reset Audit. | `TestResetPersistsPendingBeforeConsumeAndReplaysOutcome`, `TestResetRejectsIdempotencyReuseForDifferentAccount`, `TestResetReturnsAccountBusyForDistinctSimultaneousKey`, `TestResetSameKeyWaitsForCurrentFlightAndReloadsFinalRecord`, `TestResetSameKeyWaitCancellationReturnsStableBusyError`, `TestResetRecoversPendingOnStartupWithoutConsuming` — `internal/app/reset_test.go`<br>`TestResetAuditDuplicateKeyReturnsConflict`, `TestResetAuditReplaceUpdatesExistingRecord`, `TestResetAuditRetentionKeepsExactCutoffAndRecoversOnlyExplicitly` — `internal/store/repositories_test.go`<br>`headless browser reset confirmation gates the request and sends the server contract` — `web/tests/browser.test.mjs`<br>`TestManagementDispatchEndToEndPreservesLifecycleAndSecretBoundaries` — `integration_test.go` | Go 1.24 Docker, real headless Chromium with CDP, and CI browser acceptance. The browser test opens and cancels the confirmation dialog with zero reset requests, then confirms one request containing exactly `account_key` and `idempotency_key`; the listed management confirmation test is intentionally not used as quota-reset confirmation evidence because it covers the separate reset-audit deletion phrase. |
| 7 | Ordinary history deletion cannot remove configuration, credentials, runtime state, or reset audit. | `TestHistoryClearOnlyClearsHistoryDocument`, `TestOrdinaryClearCannotDeleteAuditOrRuntimeState`, `TestResetAuditClearOnlyClearsAuditDocument` — `internal/store/repositories_test.go`<br>`TestClearResetAuditRequiresExactPhrase` — `internal/app/reset_test.go`<br>`TestManagementDispatchEndToEndPreservesLifecycleAndSecretBoundaries` — `integration_test.go` (after `DELETE /history`, asserts unchanged enabled configuration/revision, preserved reset audit, and a successful authenticated Health Probe using the host credential boundary) | Local Go suite in Go 1.24 Docker; CI repeats the persistence and integration suites. |
| 8 | The panel never owns a CLIProxyAPI management key or receives a Codex access token. | `TestAccountProjectionNeverContainsToken`, `TestAccountAndErrorJSONNeverExposeIdentityOrCredentialFixtures` — `internal/accounts/service_test.go`<br>`TestResetAuditDoesNotPersistSecrets` — `internal/app/reset_test.go`<br>`TestManagementResponsesAndAssetsContainNoCredentialMaterial`, `TestManagementCredentialScanUsesProductionBoundary` — `internal/management/contract_test.go`<br>`TestStructuredLoggerAcceptsOnlySanitizedOutcomeFields`, `TestManagementDispatchEndToEndPreservesLifecycleAndSecretBoundaries` — `integration_test.go`<br>`reads only the host-owned management key and never writes browser storage`, `host management requests use the canonical API and inherited authorization`, `all production asset imports resolve and forbidden credential fallbacks are absent` — `web/tests/api.test.mjs`, `web/tests/markup.test.mjs` | The browser transiently reads CLIProxyAPI's existing host-owned key solely to build Management API authorization; tests and scans prohibit all storage writes, plugin login UI, key responses, and Codex-token exposure. |
| 9 | Desktop and mobile layouts preserve all essential state without page-level horizontal scrolling. | `headless browser responsive viewports preserve essential state without page overflow` — `web/tests/browser.test.mjs`<br>`panel IDs are unique and the four monitoring regions are present together`, `static buttons have accessible names and inputs have labels`, `module exports and design tokens stay unique and exact` — `web/tests/markup.test.mjs`<br>Responsive CSS source: `web/styles.css`; panel source: `web/panel.html` | Real headless Chromium with CDP device metrics at 375, 768, 1024, and 1440 CSS pixels measures `document.documentElement.scrollWidth` and `document.body.scrollWidth`, verifies the summary, account identity/selection state, stacked workspace, and 44px controls remain present and usable. |
| 10 | The complete verification suite passes for both supported release architectures. | `TestDispatchRegistersManagementAndDynamicResources`, `TestExportedCallAllocatesAndFreesPluginBuffer`, `TestDispatchAcceptsAllResourceBasePathFieldCasing` — `main_test.go`<br>`TestManagementResourcesServeEveryRegisteredAsset`, `TestResourceRegistrationUsesExactContentTypes` — `internal/management/contract_test.go`<br>Release layout assertions: `scripts/package_release_test.sh`<br>Native architecture workflow: `verify-supported-architectures` in `.github/workflows/ci.yml` | Native arm64 Go 1.24 format/vet/normal/race suite and shared-library build passed locally on 2026-09-10. This host's amd64 binfmt execution was attempted but Go 1.24 crashed in QEMU's emulated address space before tests could run; amd64 is therefore **pending native CI evidence**, not marked passed. CI now has native `ubuntu-24.04` amd64 and `ubuntu-24.04-arm` arm64 jobs that run the full Go and Node/browser suite plus native shared-library build. |

## Commands and results

Dates below use the execution date in the environment (`2026-09-10`). Go
commands run in Go 1.24 Docker containers because the host has no Go toolchain.
The native container reported `go version go1.24.13 linux/arm64`; its normal,
race, vet, and arm64 shared-library checks executed successfully. Docker amd64
binfmt was enabled and attempted, but Go 1.24 crashed inside QEMU before the
amd64 tests could execute. The native amd64 CI matrix below is therefore the
authoritative pending evidence for that architecture.

The commands below were run fresh against the final reviewed base. Exit codes
and concise output summaries are recorded rather than inferred from prior
reports.

| Date | Command | Outcome |
| --- | --- | --- |
| 2026-09-10 | `docker run --rm -v "$PWD":/src -w /src golang:1.24-bookworm sh -lc '/usr/local/go/bin/go version; /usr/local/go/bin/gofmt -w $(find . -name "*.go" -not -path "./.git/*"); /usr/local/go/bin/go vet ./...; /usr/local/go/bin/go test -count=1 ./...; /usr/local/go/bin/go test -count=1 -race ./...'` | Exit 0 in native Linux arm64 Docker; `go version go1.24.13 linux/arm64`, no formatting diff, vet had no diagnostics, and all normal/race packages reported `ok`; `internal/testabi` had no test files. |
| 2026-09-10 | `docker run --rm --privileged --platform linux/amd64 ... golang:1.24-bookworm ...` with Docker amd64 binfmt | Exit nonzero before test completion: Go 1.24 `runtime` crashed in QEMU address-space handling (`lfstack.push`/`netpoll`). No amd64 suite result is claimed; native amd64 CI evidence is pending. |
| 2026-09-10 | `node --check web/modules/api.js && node --check web/modules/state.js && node --check web/modules/accounts.js && node --check web/modules/schedule.js && node --check web/modules/simulator.js && node --check web/modules/history.js && node --check web/modules/main.js` | Exit 0 on the host Node runtime. |
| 2026-09-10 | `BROWSER_DOCKER_IMAGE=lscr.io/linuxserver/chrome:latest node --test --test-reporter tap web/tests/*.test.mjs` | Historical v0.0.2 run: six test files produced 30 nested tests, 30 passed. Follow-up contract work is recorded in the 2026-09-11 row below. |
| 2026-09-10 | Required secret scan (exact command below) | Exit 0 because it found only the allowed README management-key wording and test-only fixture/assertion strings. The production-only boundary scan below returned no matches; no test was weakened. |
| 2026-09-10 | Required asset scan (exact command below) | Exit 1 with no matches; no wildcard asset registration was found. |
| 2026-09-10 | `CC=x86_64-linux-gnu-gcc CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -trimpath -buildmode=c-shared -o /tmp/codex-window-reset-amd64.so .` in native arm64 Go 1.24 Docker with `gcc-x86-64-linux-gnu libc6-dev-amd64-cross` | Exit 0; `readelf -h` reported ELF64 `Advanced Micro Devices X86-64`, and exact `nm -D` matching found all four ABI symbols. This is a cross-build only and does not close the pending native amd64 suite row. |
| 2026-09-10 | `CGO_ENABLED=1 GOOS=linux GOARCH=arm64 go build -trimpath -buildmode=c-shared -o /tmp/codex-window-reset-arm64.so .` in native arm64 Go 1.24 Docker | Exit 0; `readelf -h` reported ELF64 `AArch64`, and exact `nm -D` matching found all four ABI symbols. |
| 2026-09-10 | `scripts/package_release_test.sh /out 0.0.1` from `/out` after Docker-built libraries were packaged with `SOURCE_DATE_EPOCH=0 scripts/package-release.sh 0.0.1 /out` | Exit 0 in Go 1.24 Docker with `zip` and `unzip`; all four checksum entries reported `OK`, and the seven named assets were present. |
| 2026-09-10 | `git diff --check` and tracked-SDD-artifact inspection | Recorded after the final documentation and index cleanup below; no whitespace errors, no Task 8 report remains tracked, and no new unexpected SDD artifact was introduced. Pre-existing tracked Task 5 and Task 6 reports remain unchanged. |
| 2026-09-11 | `npm run check && BROWSER_BIN=/snap/bin/chromium node --test web/tests/*.test.mjs` | Exit 0; 46 Node tests passed, including canonical `/auth-files` normalization, host-owned authorization reads without storage writes, five operational summary counts, explicit Probe consent, Reset credit eligibility, server `/simulate` delegation, single-page responsive layout, and Reset Audit interactions. |
| 2026-09-11 | `docker run --rm -v /share/codeSpace/codex-window-reset/.worktrees/codex-window-reset:/src -w /src golang:1.24-bookworm ... go test -count=1 ./...; go vet ./...; go test -count=1 -race ./...` | Exit 0; all Go packages passed normal tests, vet, and race tests after the status/embedded-panel contract changes. |

The exact broad scans and amd64 symbol inspection were:

```text
rg -n 'access_token|localStorage\s*\.\s*setItem|sessionStorage\s*\.\s*setItem' web/modules web/panel.html web/styles.css internal/management
rg -n '/panel/assets/|Resources:.*\*|resource.*\*' . --glob '*.go' --glob '*.js' --glob '*.html'
readelf -h /tmp/codex-window-reset-amd64.so | rg 'Class:|Machine:'
nm -D /tmp/codex-window-reset-amd64.so | rg '(^| )cliproxy_plugin_init$|(^| )cliproxyPluginCall$|(^| )cliproxyPluginFree$|(^| )cliproxyPluginShutdown$'
```

The required production-boundary scan is:

```text
rg -n 'access_token|localStorage\s*\.\s*setItem|sessionStorage\s*\.\s*setItem' web/modules web/panel.html web/styles.css internal/management --glob '*.go' --glob '*.js' --glob '*.html' --glob '!*_test.go'
```

It exits 1 with no matches. The browser's `managementKey()` read path is
intentional and covered by `web/tests/api.test.mjs`; only storage writes,
access-token fields, plugin prompts, and credential responses are forbidden.

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

## 2026-09-11：模拟器与页面版本纠正（未发布）

### 已确认的原因

- `v0.0.8..v0.0.9` 只修改了空账户集合的配置校验及测试，没有包含模拟器 UI 改动。
- 下载的 v0.0.9 Linux amd64 发布库仍包含写死的 `v0.0.8` 页面徽标；这不是缓存推测。
- 旧渲染器把同一组顶层时间分段用于 A/B 两条轨道，且把起止相同的预热点拉长到午夜。
- 原浏览器样例使用空时间轴，未验证策略差异，也未从 Go 嵌入页面验证版本和静态资源打包。

### 本次实现与回归门禁

- 参考 `codex-health-monitor` 的参数侧栏、四项指标、策略卡、预热概览和时间轴布局；保留本项目服务端调度与模拟算法，不声称算法等同参考项目。
- Go 从与覆盖指标相同的区间生成独立 A/B 分段；预热点与持续时间区间分开渲染。
- 页面版本读取构建时的 `pluginVersion`；新增样式和模块继续内联，仅注册 `/panel` 资源。
- `TestEmbeddedPanelShowsTheBuildVersion` 检查嵌入页面的构建版本；`TestSimulationExposesDistinctStrategyCoverageForTheTimeline` 检查服务端 JSON 分段及覆盖分钟数。
- `scripts/verify-panel.sh` 导出真实 Go 嵌入页面和模拟响应，再运行浏览器测试；CI 和 Release 的所有浏览器检查入口均调用该脚本。
- 浏览器验证非空 A/B 时间轴、点标记、图例、键盘详情、明暗主题、375/1440px 模拟器、空账户配置保存及没有二次静态资源请求。完整布局测试另覆盖 375/768/1024/1440px。

### 本地验证及边界

- Go 1.24 Docker：`go vet ./...`、`go test -count=1 ./...`、`go test -count=1 -race ./...` 全部通过。
- 实际嵌入页面及 Go 模拟响应：`node --test --test-isolation=none web/tests/*.test.mjs`，51 项通过，0 失败，0 跳过。
- `npm run check`、`bash -n scripts/verify-panel.sh`、`actionlint` 和 `git diff --check` 通过。
- 本机仅构建验证了 Linux arm64 共享库，使用测试版本 `0.0.9-validation`；该版本不是 Release，未验证真实宿主部署，也没有进行真实账户的额度消费操作。
- 已检查桌面明暗主题及移动端截图。暗色新增样式仅作用于模拟器，不代表整页暗色主题已重做。
- 本次没有提交、推送、打标签、发版或更新插件商店；已安装的 v0.0.9 不会因这些本地修改而改变。

## 2026-09-12：修复窗口周期与策略 B 时间轴（v0.0.11 后，未发布）

### 原因与修正

- 旧模拟算法在每段工作时间开始时重新分配可用预算，忽略了窗口周期；策略 B 又从工作锚点推算，而没有使用实际计划预热时间。前端颜色映射并非根因，原回归样例也固化了错误结果。
- 现在 A 从首次工作开始、B 从上班前最早有效 `PlannedAt` 开始，按 `WindowHours` 推演周期窗口。每个窗口共享一份预计可用时长，只有工作时段消耗；午休不消耗、不额外恢复预算。
- 指标和时间轴使用同一组可用区间；窗口空闲仅统计窗口内非工作时间。多账户采用最早有效预热账户的单账户示例，不叠加账户预算；没有提前预热时 B 与 A 相同。后续预热请求不视为强制重置，页面明确说明这些假设。
- 实际调度器、预热请求和配额操作没有修改；这是与参考项目周期模型一致的模拟，不是上游真实额度或请求成功率保证。

### 手算与真实响应核对

Asia/Shanghai，2026-09-14，工作 09:00–12:00、13:30–19:00，窗口 5 小时、每窗口可用 60 分钟，示例预热 06:30：

| 策略 | 绿色可用区间 | 总计 |
| --- | --- | --- |
| A | 09:00–10:00、14:00–15:00 | 120 分钟 |
| B | 09:00–10:00、11:30–12:00、13:30–14:00、16:30–17:30 | 180 分钟 |

净收益 60 分钟。用 Node VM 执行参考项目 `windowSegments` / `simulateAvailability`，与本地 Go 导出的实际 JSON 逐段及总分钟数比较，全部一致。

### 验证证据

- 新增测试先复现失败，再验证修复：窗口周期 1/2/5/24 小时、午休预算延续、相邻工作段、多账户错峰不叠加、跳过预热、休息日、午夜裁剪和预算上限。
- 离线 Go 1.24 Linux arm64 Docker：`go vet ./...`、`go test -count=1 -race ./...`、带 `CWR_BROWSER_FIXTURE_DIR` 的全量 `go test -count=1` 均退出 0。
- 源码页面和 Go 实际嵌入页面/实际模拟 JSON 各运行一次 Chromium + Node 全套回归，均 63 项通过、0 失败、0 跳过。浏览器断言精确 A/B 绿色区间及收益，并保留零收益展示测试。
- `npm run check`、`git diff --check` 通过；人工检查 1440px 明暗主题和 375px 移动端截图。
- 本轮没有真实宿主部署或真实账户请求，没有提交、推送、升版本或发版；已安装的 v0.0.11 尚不包含这次修复。

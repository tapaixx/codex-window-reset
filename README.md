# Codex Window Reset

> CLIProxyAPI 原生插件 · Linux `amd64` / `arm64` · 当前版本 **v0.0.16**

[下载 Release](https://github.com/tapaixx/codex-window-reset/releases) ·
[插件商店](https://github.com/tapaixx/CLIProxyAPI-Plugins-Store) ·
[构建状态](https://github.com/tapaixx/codex-window-reset/actions)

Codex Window Reset 是一个面向 CLIProxyAPI 的窗口配额管理插件。它从宿主读取 Codex 账户，按固定工作窗口执行预热，提供手动健康检测与窗口额度重置，并在单页控制台中展示账户状态、24 小时时间轴和审计记录。

Automatic scheduling never spends Reset Credits. The panel is intentionally
single-page and reuses CLIProxyAPI's existing authentication and persistence.

## 功能概览

- **固定窗口配置**：时区、工作/午休时间、工作日、预热参数、健康阈值和跳过窗口。
- **账户集合管理**：自动预热账户可多选，也可以为空；空集合不会触发调度。
- **服务端模拟器**：对比“首次使用”和“已配置预热”两种策略，不发送真实请求。
- **安全操作**：手动检测需要确认额度影响；重置使用 single-flight（同一账户同一时刻只执行一个操作）与幂等键，防止同一请求重复消费。

## 运行要求与安全 / Requirements and security

插件不自建登录，不保存 management key，默认遮罩账户身份。账户来自
`/v0/management/auth-files`，配额查询通过 `/v0/management/api-call`。

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
authorization header. Reloading or closing the page discards page-memory
state, not persisted configuration or runtime records.

The plugin does not receive or display Codex access tokens in the panel.
Account identities are masked by default.

## 安装 / Install

下载与 Linux 主机架构匹配的 `.so`，放入 CLIProxyAPI 插件目录，按宿主
正常流程重载插件。无需设置插件专用密钥。

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

## 首次启动与配置 / First startup and activation

A new installation starts inert. Startup or plugin registration alone does not
activate preheating. An empty Scheduled Account collection is valid: the
schedule can be enabled and saved, but the scheduler creates no occurrences
until an account is added.

To activate automatic work:

1. Open the panel. Account metadata comes from CLIProxyAPI's
   `/v0/management/auth-files` endpoint.
2. Optionally select accounts for scheduled preheating. The persistent
   Scheduled toggle is separate from the temporary Action Selection checkboxes.
3. Set an IANA timezone, weekdays, fixed work/lunch times, window cycle,
   preheat lead/span, quota floors, skip windows, and probe settings.
4. Enable the schedule and save it. The server validates the revision; no
   Scheduled Account is required for saving.

The scheduler makes at most one Preheat Request per account in each bounded
preheat window and does not catch up missed occurrences. A Preheat Request is
a real minimal Codex request: it can consume ordinary quota and may begin a
Short Window, but it never consumes a Reset Credit.

### 固定窗口与账户集合（中文说明）

面板沿用 Codex Health Monitor 的固定窗口布局：

| 参数 | 默认值 |
| --- | --- |
| 时区 | `Asia/Shanghai` |
| 工作日 | 周一至周五 |
| 上午工作时间 | `09:00–12:00` |
| 下午工作时间 | `13:30–19:00` |
| 窗口周期 | `5` 小时 |
| 单窗口预计可用时长 | `60` 分钟 |
| 最小健康阈值 | `80%` |

预热提前和时长需要在启用前填写并保存，例如 `120` / `60` 分钟；
新建服务端配置不会自动保存这两个值。午休不计入工作时段，预热窗口
则按工作窗口锚点、提前量和时长另行推算，可能位于工作时间之外。
请以模拟器中的计算结果核对时间。

后续轮次也会检查实际预热时间是否仍在工作时段内，不会因为名义周期锚点
超过下班时间而漏掉。例如工作到 18:00、周期 5 小时、提前 120 分钟、
错峰时长 60 分钟，单账号计划为 06:30、11:30、16:30；最后一轮不是省略项。
下班时刻及之后的账号执行槽位不安排，也不提前挪动槽位来增加预热次数。

配置与账户是两个独立概念。自动预热账户可以为空；此时配置仍可保存，
但自动调度计划为空、不会发出请求。之后打开账户行的“自动预热”开关并
保存，才会为该账户生成计划。手动刷新、健康检测和重置仍需在操作时
明确选择账户。模拟器即使没有账户也可以预览策略和时间轴。

## 操作说明 / Safe operator actions

### 健康检测 / Health Probe

勾选目标账户，点击“立即检测”并确认额度影响。检测会发送真实的 Codex
模型请求，可能消耗普通配额并开启短窗口；不可用账户需要额外确认，
已停用账户不能检测。检测不等同于刷新配额，也不消耗 Reset Credit。

The Probe action is manual and requires explicit quota acknowledgement. It is
a real Codex model request, may consume ordinary quota, and may begin a Short
Window. It can be authorized for an unavailable account when the Operator
acknowledges that override; disabled accounts cannot be probed. Use the
explicit quota refresh action when current quota data is needed.

### 窗口重置 / Quota Reset

重置仅能手动执行，一次一个账户。先刷新配额，确认有可用的 Reset Credit
后再操作；确认重置会消费上游重置额度，不是普通刷新。插件记录独立审计，
并以同账户 single-flight 和同一操作的幂等键防止并发执行与重试重复消费。

Reset is manual only and accepts exactly one account. Before confirmation, the
panel requires a successful current quota and Reset Credit refresh. Confirming
the action calls the upstream reset-credit endpoint and consumes one applicable
Reset Credit. Treat the Reset Credit as scarce and irreversible: it is not a
refresh, a Probe, or an automatic scheduling action. The plugin records a
durable Reset Audit and uses an idempotency key so a retry cannot consume the
same credit twice.

### 配额时效与保护暂停 / Quota freshness and holds

页面不定时轮询配额；浏览器在明确刷新或重置后查询配额，运行时在检测、
预热与重置前后更新决策快照。快照五分钟后过期。长窗口剩余配额小于或
等于下限时，会进入持久化 Guardrail Hold（保护暂停）；只有后续成功刷新
并证明所有已识别长窗口均高于下限，才会解除。没有保护暂停且配额查询
结果未知时，自动预热仍可能执行，并记录 `quota_unknown_fail_open`。

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

## 数据与删除边界 / Persistence and deletion boundaries

插件保存配置、运行状态、最近 100 条操作记录和保留 365 天的重置审计。
它不替 CLIProxyAPI 保存登录密钥或账户凭据。清除普通历史不会删除配置、
运行状态和重置审计；删除审计是独立操作，必须输入 `DELETE AUDIT`。
配置损坏会停用调度并报告 `store_corrupt`，不会静默覆盖为默认配置。

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

## 升级与回滚 / Upgrade and rollback

升级前按宿主流程停用插件并备份数据目录，再替换对应架构的 `.so`。
重启后核对注册版本和面板，手动刷新配额后再启用调度。回滚先恢复已验证
的旧插件文件；只有持久化格式变化时才需要恢复数据备份。不要通过删除
配置或审计文件来“重置升级”。

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

## 验证 / Verification

以下文件名已对应 `v0.0.16` Release。下载后可校验 SHA-256；浏览器验收需要
Chromium，源码构建需要 Go 1.24、Node.js 和 C 编译器，交叉构建 arm64
还需要 `aarch64-linux-gnu-gcc`。

The current release is `v0.0.16`; scripts receive and store `0.0.16` without the
leading `v`. The GitHub repository is
<https://github.com/tapaixx/codex-window-reset>. A release contains these
seven named assets:

```text
codex-window-reset_0.0.16_linux_amd64.zip
codex-window-reset_0.0.16_linux_arm64.zip
checksums.txt
codex-window-reset-linux-amd64.so
codex-window-reset-linux-arm64.so
codex-window-reset-linux-amd64.so.sha256
codex-window-reset-linux-arm64.so.sha256
```

Verify downloaded assets from their directory with:

```bash
sha256sum --check checksums.txt
unzip -Z1 codex-window-reset_0.0.16_linux_amd64.zip \
  | diff -u - <(printf 'codex-window-reset.so\n')
unzip -Z1 codex-window-reset_0.0.16_linux_arm64.zip \
  | diff -u - <(printf 'codex-window-reset.so\n')
```

From a checkout, the release gates are:

```bash
bash -n scripts/package-release.sh scripts/package_release_test.sh
npm run check
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

## 许可证与致谢 / License and attribution

本项目采用 [MIT License](LICENSE)，参考了
[Codex Health Monitor](https://github.com/tapaixx/codex-health-monitor)
的协议行为与固定窗口设计，相关归属说明保留在 [NOTICE](NOTICE)。

Codex Window Reset is distributed under the MIT License. `NOTICE` preserves
the attribution for protocol behavior derived from
<https://github.com/tapaixx/codex-health-monitor>.

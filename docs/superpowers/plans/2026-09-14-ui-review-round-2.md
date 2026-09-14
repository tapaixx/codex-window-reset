# UI Review Round 2 — Findings Log

> Recorded 2026-09-14, before a second pass with the `impeccable` and
> `make-interfaces-feel-better` skills. This file is the durable record of the
> findings; execution authorization is separate and not granted by this file.

**Surface:** the single embedded panel (`web/panel.html` served as one
self-contained document; source split across `web/modules/*.js`, `web/styles.css`,
`web/simulator.css`).

**Mode:** Operate. The visitor completes a task — inspect account health, tune a
preheat schedule, trigger a manual probe. Scanability, consistency, and the real
usage scene outrank expression; brand lives in precise details.

**Scope discipline:** refinement of the incumbent implementation, not a
replacement visual world. The blue/white operations-console identity, the
Chinese-first copy, and every host API contract stay as they are.

## Findings

### F1 · Strategy A/B labels carry no information (evidence: real user confusion)

The simulator timeline lanes are labelled `策略 A · 首次使用` and
`策略 B · 配置预热`; the metric cards read `策略 A 可用` / `策略 B 可用` /
`预热后增益`. The letters require a mental lookup before the row means anything.

In this session the Operator read strategy A's `10:30–12:30 预计受限` segment as
their own 11:20 preheat plan and reported it as a bug. It was the no-preheat
control line behaving correctly.

Direction: drop the letters. Label the lanes by what they are — the control line
should say it is a control (e.g. `不预热（对照）` / `按计划预热`). Already
partially addressed: the segment detail table now lists strategy B only
(v0.0.20), but the lanes and metric cards still lead with bare letters.

### F2 · The accounts table carries 16 columns, ~5 without decision value

```
选择 账号 自动预热 AUTH-INDEX 账号前缀 套餐类型 状态 短窗口 长窗口
重置额度 请求结果 窗口结果 HTTP/耗时 额度更新时间 错误原因 操作
```

- `AUTH INDEX` (`58baed010f75dd6e`) and `账号前缀` (`acct_…`) are opaque
  identifiers used only when cross-referencing during triage, yet each holds a
  full column.
- `请求结果`, `窗口结果`, `HTTP / 耗时` all describe the same event: the last
  probe. Three columns for one fact.

Direction: fold the identifiers into the account cell's secondary line or a row
disclosure; collapse the three probe columns into one `上次检测` status with
detail on hover/expand. Target ≈10 columns.

### F3 · Snapshot freshness has no global presence

The panel deliberately never polls (correct, and load-bearing: ADR-0011 and the
"no polling timer" test enforce it). The cost is that a freshly opened panel can
show hours-old quota with the same visual confidence as live data.

Today freshness appears only as (a) a per-row `额度更新时间` cell and (b) a
`快照过期` count in the summary strip — a count of how many are stale, never how
old anything is.

Direction: one global freshness indicator near the summary (`额度快照：12 分钟前`),
degrading in colour past a threshold. This is the panel's highest-consequence
misread risk: the numbers look authoritative whether or not they are current.

### F4 · Guardrail Hold is averaged into the summary strip

Guardrail Hold is the only state that actually blocks automatic preheating, yet
it sits in the summary strip at the same weight as 健康/暂停/快照过期 and holds
its slot at zero.

Direction: recede at zero; at non-zero, promote the whole tile to a warning
treatment and make it a filter into the affected accounts.

### F5 · No guidance for "why is nothing happening"

A new installation is inert by design (spec acceptance criterion). The panel
explains this only inside the help dialog.

Direction: while the schedule is disabled, show a persistent dismissible
checklist under the summary — ① preheat lead/span ② scheduled accounts
③ save and enable — ticking items off as they are satisfied.

### F6 · Upstream-effecting actions look like local ones

`刷新面板` (reloads local panel data) and `刷新全部额度` (real upstream calls,
seconds long) share naming shape and button styling; `立即检测` sends a real
Codex model request.

Direction: separate the visual hierarchy of upstream-effecting actions
(刷新额度, 立即检测) from read-only/local ones (刷新面板, 显示身份).

## Non-UI note carried along

`withQuotaRefresh` captures `previous = controls.map(...)` before the action and
restores `control.disabled` in `finally`. Row-level refresh buttons
(`[data-row-action="refresh"]`) are destroyed and recreated by `renderAccounts()`
during and after the action, so that restore lands on detached nodes. It behaves
correctly today only because the following `renderAccounts()` recomputes
`disabled = Boolean(state.quotaBusy)` with the flag already cleared — correct by
ordering coincidence, not by design.

Direction: let row buttons derive disabled state solely from `renderAccounts()`
and keep them out of `previous`.

## Priority as proposed to the Operator

F1 → F3 → F2. F1 is the smallest change against a confirmed real misread; F3
guards decisions made on stale numbers; F2 is the largest effort and the largest
readability gain.

## Outcome (implemented 2026-09-14)

All six findings and the non-UI note are implemented.

- **F1** — lanes read `不预热 / 对照基准` and `按计划预热 / 已配置计划`; metric cards
  read `不预热可用` / `按计划预热可用`. The 达标/未达标 verdict badge and its
  `health_threshold_percent` argument are gone; strategy cards state
  `覆盖工作时段 N%` instead. `health_threshold_percent` was never read by the
  scheduler, so the field was also removed from the config form.
- **F2** — 16 → 12 columns. `AUTH INDEX` and `账号前缀` fold into the account
  cell's secondary line; `请求结果` / `窗口结果` / `HTTP / 耗时` collapse into one
  `上次检测` pill with the detail beside it.
- **F3** — `#quota-freshness` in the toolbar reports the age of the oldest
  snapshot and degrades fresh → aging (5 min) → stale (30 min).
- **F4** — the Guardrail Hold tile recedes at zero and takes the warning
  treatment above it.
- **F5** — `#first-run` renders a three-step checklist while the schedule is
  disabled, ticking items off as they are satisfied, dismissible for the session.
- **F6** — upstream-effecting actions carry `.upstream` and a leading dot;
  `刷新面板` is demoted to a quiet `重新载入` in the header.
- **Non-UI note** — row refresh buttons are excluded from `withQuotaRefresh`'s
  `previous` array and derive their disabled state solely from `renderAccounts()`.

Also fixed from the measured pass: light-mode contrast on
`.simulation-assumptions` (4.42 → pass), the 36px-wide row action buttons
(now 40×40), and the accounts table's `min-width` (1420px → 1180px, which was
forcing a horizontal scrollbar inside the wrapper at 1440px).

Deliberately not changed: `.preheat-point` stays 24px wide. A 40px hit box makes
a point marker indistinguishable from a short duration band on the same lane,
which is the exact confusion the marker exists to prevent; the 76px height and
the hover/focus tooltip carry targetability instead.

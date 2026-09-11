import { createCodexApiCall, hostManagementRequest, normalizeHostAuthFiles, request, requestErrorMessage } from './api.js';
import { buildWindowStrategy, groupRunHistory, projectAccountRow, summarizeAccounts } from './dashboard.js';

export function syncHostTheme({ root = globalThis.document?.documentElement, parentRoot, parentDocument, windowRef = globalThis.window, observe = true } = {}) {
  if (!root) return () => {};
  let source = parentRoot;
  try { source ||= (parentDocument || windowRef?.parent?.document)?.documentElement; } catch { source = null; }
  const media = windowRef?.matchMedia?.('(prefers-color-scheme: dark)');
  const apply = () => {
    const parentTheme = source?.getAttribute?.('data-theme') || (source?.classList?.contains?.('dark') ? 'dark' : '');
    root.setAttribute('data-theme', parentTheme === 'dark' || parentTheme === 'light' ? parentTheme : (media?.matches ? 'dark' : 'light'));
  };
  apply();
  const Observer = windowRef?.MutationObserver || globalThis.MutationObserver;
  const observer = observe && source && Observer ? new Observer(apply) : null;
  observer?.observe(source, { attributes: true, attributeFilter: ['data-theme', 'class'] });
  if (observe) media?.addEventListener?.('change', apply);
  return () => { observer?.disconnect(); media?.removeEventListener?.('change', apply); };
}

const RESET_FRESHNESS_MS = 5 * 60 * 1000;
export function isResetQuotaEligible(account = {}, quota = {}, now = Date.now()) {
  const capturedAt = Date.parse(quota?.snapshot?.captured_at || '');
  const current = now instanceof Date ? now.getTime() : Number(now);
  return Boolean(!account.disabled && !account.unavailable && !quota?.refresh_error_code && !quota?.stale
    && quota?.snapshot?.reset_info_complete && Number.isFinite(capturedAt) && current >= capturedAt && current - capturedAt <= RESET_FRESHNESS_MS);
}

export function prepareProbeRequest({ accounts = [], selectedAccountKeys = [], unavailableAcknowledged = false } = {}) {
  const keys = [...new Set(selectedAccountKeys.map((key) => String(key || '').trim()).filter(Boolean))];
  if (!keys.length) return { ok: false, errorCode: 'selection_required', body: null };
  const selected = accounts.filter((account) => keys.includes(account.account_key));
  if (selected.some((account) => account.disabled)) return { ok: false, errorCode: 'account_disabled', body: null };
  const unavailable = selected.some((account) => account.unavailable);
  if (unavailable && !unavailableAcknowledged) return { ok: false, errorCode: 'account_unavailable', body: null };
  return { ok: true, errorCode: null, body: { account_keys: keys, acknowledge_quota_effect: true, allow_unavailable: unavailable } };
}

const WEEKDAYS = [['1', '一'], ['2', '二'], ['3', '三'], ['4', '四'], ['5', '五'], ['6', '六'], ['7', '日']];
const statusLabels = { healthy: '健康', warning: '警告', disabled: '已停用' };
const triggerLabels = { health_probe: '手动检测', preheat: '自动预热', compensation: '失败补偿' };
const $ = (selector) => document.querySelector(selector);
const $$ = (selector) => [...document.querySelectorAll(selector)];
const text = (selector, value) => { const element = $(selector); if (element) element.textContent = String(value ?? ''); };
const formatDate = (value, fallback = '--') => {
  const date = new Date(value || '');
  return Number.isNaN(date.getTime()) ? fallback : date.toLocaleString('zh-CN', { hour12: false });
};
const formatDuration = (milliseconds) => milliseconds >= 1000 ? `${(milliseconds / 1000).toFixed(1)} 秒` : `${milliseconds || 0} ms`;

function createCell(label, content, className = '') {
  const cell = document.createElement('td');
  cell.dataset.label = label;
  if (className) cell.className = className;
  if (content instanceof Node) cell.append(content); else cell.textContent = String(content ?? '--');
  return cell;
}

function bootPanel() {
  const state = { status: {}, accounts: [], schedule: {}, quota: [], history: [], selected: new Set(), hidden: false, skipTimes: [], resetIntent: null };
  syncHostTheme();

  const quotaIndex = () => Object.fromEntries(state.quota.map((item) => [item?.snapshot?.account_key || item?.account_key, item]).filter(([key]) => key));
  const notify = (message) => { text('#feedback', message); clearTimeout(notify.timer); notify.timer = setTimeout(() => text('#feedback', ''), 3600); };
  const showError = (selector, error) => text(selector, requestErrorMessage(error));
  const nextRun = () => Object.values(state.status?.next_runs || {}).filter(Boolean).sort((a, b) => Date.parse(a) - Date.parse(b))[0];

  function renderSummary() {
    const summary = summarizeAccounts(state.accounts, quotaIndex());
    text('#summary-total', summary.total); text('#summary-healthy', summary.healthy); text('#summary-warning', summary.warning); text('#summary-disabled', summary.disabled);
    text('#summary-next', nextRun() ? formatDate(nextRun()) : '未计划');
    text('#selection-count', `已选择 ${state.selected.size} 个账号`);
    const allSelectable = state.accounts.filter((account) => !account.disabled);
    const all = $('#select-all-checkbox');
    all.checked = allSelectable.length > 0 && allSelectable.every((account) => state.selected.has(account.account_key));
    all.indeterminate = state.selected.size > 0 && !all.checked;
  }

  function healthNode(value, threshold) {
    const wrapper = document.createElement('span'); wrapper.className = 'health-meter';
    const track = document.createElement('span'); track.className = 'health-track';
    const fill = document.createElement('span'); fill.className = `health-fill ${value < threshold ? (value < threshold / 2 ? 'danger' : 'warning') : ''}`; fill.style.width = `${value}%`; track.append(fill);
    const label = document.createElement('b'); label.textContent = `${value}%`; wrapper.append(track, label); return wrapper;
  }

  function renderAccounts() {
    const body = $('#accounts-table'); body.replaceChildren();
    const quota = quotaIndex();
    if (!state.accounts.length) { const row = document.createElement('tr'); const cell = createCell('', '未发现 Codex 账号', 'empty-row'); cell.colSpan = 16; row.append(cell); body.append(row); renderSummary(); return; }
    for (const account of state.accounts) {
      const model = projectAccountRow(account, { quota: quota[account.account_key], history: state.history, hidden: state.hidden });
      const row = document.createElement('tr'); row.dataset.accountKey = model.key;
      const checkbox = document.createElement('input'); checkbox.type = 'checkbox'; checkbox.checked = state.selected.has(model.key); checkbox.disabled = account.disabled; checkbox.dataset.accountSelection = model.key; checkbox.setAttribute('aria-label', `选择 ${model.email}`);
      checkbox.addEventListener('change', () => { checkbox.checked ? state.selected.add(model.key) : state.selected.delete(model.key); renderSummary(); });
      const name = document.createElement('div'); name.className = 'account-name'; const strong = document.createElement('strong'); strong.textContent = model.email || account.masked_identity || model.key; const small = document.createElement('small'); small.textContent = model.key; name.append(strong, small);
      const status = document.createElement('span'); status.className = `status-pill status-${model.status}`; status.textContent = statusLabels[model.status];
      const actions = document.createElement('div'); actions.className = 'row-actions';
      const refresh = document.createElement('button'); refresh.className = 'secondary-button'; refresh.type = 'button'; refresh.textContent = '刷新'; refresh.dataset.rowAction = 'refresh'; refresh.addEventListener('click', () => refreshQuota([model.key], refresh));
      const reset = document.createElement('button'); reset.className = 'secondary-button'; reset.type = 'button'; reset.textContent = '重置'; reset.dataset.rowAction = 'reset'; reset.disabled = !isResetQuotaEligible(account, quota[model.key]); reset.addEventListener('click', () => openReset(account, quota[model.key])); actions.append(refresh, reset);
      row.append(createCell('选择', checkbox), createCell('账号', name), createCell('AUTH INDEX', model.authIndex, 'mono'), createCell('账号前缀', model.accountPrefix, 'mono'), createCell('套餐类型', model.plan), createCell('状态', status), createCell('健康度', healthNode(model.health, state.schedule.health_threshold_percent || 80)), createCell('配额', `${model.remaining}%`), createCell('已使用', `${model.used}%`), createCell('重置窗口', formatDate(model.resetAt)), createCell('HTTP', model.httpStatus || '--'), createCell('耗时', model.latencyMs ? `${model.latencyMs} ms` : '--'), createCell('最后检测', formatDate(model.lastCheckedAt)), createCell('配置更新时间', formatDate(model.configurationUpdatedAt)), createCell('错误原因', model.error || '--'), createCell('操作', actions));
      body.append(row);
    }
    renderSummary();
  }

  function setField(name, value) { const input = $(`#schedule-form [name="${name}"]`); if (input) input.value = value ?? ''; }
  function renderWeekdays(values = []) {
    const selected = new Set(values.map(String)); const root = $('#weekdays'); root.replaceChildren();
    for (const [value, label] of WEEKDAYS) { const chip = document.createElement('label'); chip.className = 'weekday-chip'; const input = document.createElement('input'); input.type = 'checkbox'; input.value = value; input.checked = selected.has(value); input.addEventListener('change', renderSimulation); chip.append(input, document.createTextNode(label)); root.append(chip); }
  }
  function renderSkipTimes() {
    const root = $('#skip-window-chips'); root.replaceChildren();
    for (const value of state.skipTimes) { const chip = document.createElement('span'); chip.className = 'skip-chip'; chip.append(document.createTextNode(value)); const remove = document.createElement('button'); remove.type = 'button'; remove.textContent = '×'; remove.setAttribute('aria-label', `删除跳过窗口 ${value}`); remove.addEventListener('click', () => { state.skipTimes = state.skipTimes.filter((item) => item !== value); renderSkipTimes(); renderSimulation(); }); chip.append(remove); root.append(chip); }
  }
  function applySchedule(schedule) {
    state.schedule = structuredClone(schedule || {}); $('#schedule-enabled').checked = Boolean(schedule.enabled); text('#schedule-revision', schedule.revision ?? '--');
    const suggested = { preheat_lead_minutes: 30, preheat_span_minutes: 15 };
    for (const name of ['timezone', 'window_hours', 'productivity_minutes', 'health_threshold_percent', 'preheat_lead_minutes', 'preheat_span_minutes', 'remaining_quota_floor_percent', 'remaining_window_floor_minutes', 'long_window_floor_percent', 'probe_model', 'probe_timeout_seconds']) setField(name, schedule[name] ?? suggested[name]);
    const periods = schedule.work_periods || []; setField('work_start', periods[0]?.start || '09:00'); setField('lunch_start', periods[0]?.end || '12:00'); setField('lunch_end', periods[1]?.start || '13:30'); setField('work_end', periods[1]?.end || periods[0]?.end || '19:00');
    renderWeekdays(schedule.weekdays || [1, 2, 3, 4, 5]); state.skipTimes = [...(schedule.skip_window_times || [])]; renderSkipTimes(); renderSimulation(); renderAccounts();
  }
  function readSchedule() {
    const form = $('#schedule-form'); const value = (name) => form.elements[name]?.value || ''; const integer = (name, fallback = 0) => Number.parseInt(value(name), 10) || fallback;
    return { ...state.schedule, enabled: $('#schedule-enabled').checked, timezone: value('timezone'), window_hours: integer('window_hours', 5), productivity_minutes: integer('productivity_minutes', 60), health_threshold_percent: integer('health_threshold_percent', 80), preheat_lead_minutes: integer('preheat_lead_minutes', 30), preheat_span_minutes: integer('preheat_span_minutes', 15), remaining_quota_floor_percent: integer('remaining_quota_floor_percent', 20), remaining_window_floor_minutes: integer('remaining_window_floor_minutes', 60), long_window_floor_percent: integer('long_window_floor_percent', 10), probe_model: value('probe_model'), probe_timeout_seconds: integer('probe_timeout_seconds', 30), weekdays: $$('#weekdays input:checked').map((input) => Number(input.value)), work_periods: [{ start: value('work_start'), end: value('lunch_start') }, { start: value('lunch_end'), end: value('work_end') }], blackout_periods: state.schedule.blackout_periods || [], skip_window_times: [...state.skipTimes], scheduled_account_keys: state.accounts.filter((account) => !account.disabled).map((account) => account.account_key) };
  }

  function renderSimulation() {
    const config = readSchedule(); const result = buildWindowStrategy(config); const root = $('#simulation-output');
    const metric = (label, value, extra = '') => `<div class="sim-metric ${extra}"><span>${label}</span><strong>${value}</strong></div>`;
    const band = (item, kind) => `<span class="timeline-band ${kind}" style="--start:${item.startMinute / 14.4}%;--width:${(item.endMinute - item.startMinute) / 14.4}%" title="${item.label}"><i>${item.label}</i></span>`;
    const markers = result.windows.map((window, index) => `<span class="window-marker" style="--at:${window.anchorMinute / 14.4}%" title="窗口 ${index + 1} 于 ${window.workStart} 开始"><i>${window.workStart}</i></span>`).join('');
    const ticks = [0, 3, 6, 9, 12, 15, 18, 21, 24].map((hour) => `<span style="--at:${hour / 24 * 100}%">${String(hour).padStart(2, '0')}:00</span>`).join('');
    const windowSteps = result.windows.map((window, index) => `<li><b>窗口 ${index + 1}</b><span><em>${window.preheatAt}</em> 预热</span><span>${window.workStart} 开始</span><span>${window.expiresAt} 到期</span></li>`).join('');
    root.innerHTML = `<div class="sim-metrics">${metric('当天总工作时长', `${(result.workMinutes / 60).toFixed(1)} 小时`)}${metric('普通检测可用', `${result.normalAvailableMinutes} 分钟`)}${metric('预热策略可用', `${result.preheatedAvailableMinutes} 分钟`)}${metric('预热后提升', `+${result.gainMinutes} 分钟`, 'gain')}</div><div class="strategy-compare"><article class="strategy-card"><h3>策略 A · 普通检测</h3><p>到工作时间才触发窗口，中间空档不补偿。</p><strong>${result.normalAvailableMinutes} 分钟可用</strong></article><article class="strategy-card recommended"><h3>策略 B · 提前预热</h3><p>提前 ${config.preheat_lead_minutes} 分钟触发，并按 ${config.window_hours} 小时周期接续。</p><strong>${result.preheatedAvailableMinutes} 分钟可用</strong></article></div><div class="timeline-block"><div class="timeline-title"><strong>一天内什么时候工作、什么时候预热</strong><span class="timeline-legend"><i class="legend-work"></i>工作时段<i class="legend-normal"></i>普通窗口<i class="legend-preheat"></i>预热窗口</span></div><div class="timeline-chart" role="img" aria-label="24 小时策略对比：普通检测 ${result.normalAvailableMinutes} 分钟，提前预热 ${result.preheatedAvailableMinutes} 分钟"><div class="timeline-ruler">${ticks}</div><div class="timeline-row"><b>工作安排</b><div class="timeline-lane">${result.workBands.map((item) => band(item, 'work-band')).join('')}${result.breakBands.map((item) => band(item, 'break-band')).join('')}</div></div><div class="timeline-row"><b>策略 A</b><div class="timeline-lane">${result.normalBands.map((item) => band(item, 'normal-band')).join('')}${markers}</div></div><div class="timeline-row emphasized"><b>策略 B</b><div class="timeline-lane">${result.preheatBands.map((item) => band(item, 'preheat-band')).join('')}${markers}</div></div></div><ol class="window-steps">${windowSteps || '<li>当前工作时段内没有可执行窗口</li>'}</ol></div><p class="recommendation"><strong>结论：</strong> 在 ${result.windows.map((window) => window.preheatAt).join('、') || '无'} 预热，预计比普通检测多覆盖 <b>${result.gainMinutes} 分钟</b>。当前最小健康阈值为 ${config.health_threshold_percent}%。</p>`;
  }

  function renderHistory() {
    const body = $('#history-output'); body.replaceChildren(); const runs = groupRunHistory(state.history);
    if (!runs.length) { const row = document.createElement('tr'); const cell = createCell('', '暂无检测历史', 'empty-row'); cell.colSpan = 9; row.append(cell); body.append(row); return; }
    runs.forEach((run, index) => { const row = document.createElement('tr'); const result = document.createElement('span'); result.className = `result-pill result-${run.result}`; result.textContent = run.result === 'success' ? '全部成功' : run.result === 'partial' ? '部分成功' : '检测失败'; row.append(createCell('序号', index + 1), createCell('检测时间', formatDate(run.startedAt)), createCell('触发方式', triggerLabels[run.trigger] || run.trigger), createCell('账号数', run.accounts), createCell('成功', run.succeeded), createCell('失败', run.failed), createCell('耗时', formatDuration(run.durationMs)), createCell('主要信息', run.message), createCell('操作结果', result)); body.append(row); });
  }

  async function loadPanel() {
    $('#connection').dataset.state = 'loading'; text('#connection', '正在加载数据…');
    const [status, authFiles, schedule, quota, history] = await Promise.all([request('/status'), hostManagementRequest('/auth-files'), request('/schedule'), request('/quota'), request('/history')]);
    state.status = status || {}; state.accounts = normalizeHostAuthFiles(authFiles); state.quota = quota || []; state.history = history || []; state.selected = new Set([...state.selected].filter((key) => state.accounts.some((account) => account.account_key === key && !account.disabled)));
    applySchedule(schedule || {}); renderHistory(); $('#connection').dataset.state = 'ready'; text('#connection', '');
  }

  async function refreshQuota(keys, button) {
    if (!keys.length) { text('#account-feedback', '请先选择账号。'); return; }
    button && (button.disabled = true); text('#account-feedback', '');
    try {
      const refreshed = await Promise.all(keys.map(async (key) => {
        const account = state.accounts.find((item) => item.account_key === key);
        if (!account?.auth_index) throw new Error('账号缺少 auth_index');
        const result = await hostManagementRequest('/api-call', { method: 'POST', body: createCodexApiCall({ authIndex: account.auth_index, accountId: account.account_id, method: 'GET', url: 'https://chatgpt.com/backend-api/wham/usage', headers: { Accept: 'application/json' } }) });
        const statusCode = Number(result?.status_code || result?.statusCode || 0);
        if (statusCode < 200 || statusCode >= 300) throw new Error(`额度接口 HTTP ${statusCode || '-'}`);
        const body = typeof result?.body === 'string' ? result.body : result?.data ?? result;
        const usage = typeof body === 'string' ? JSON.parse(body) : body;
        const windows = Object.entries(usage?.rate_limit || {}).filter(([name, value]) => value && typeof value === 'object' && value.limit_window_seconds).map(([name, value]) => ({ short: Number(value.limit_window_seconds) <= 18_000, remaining_percent: Math.max(0, Math.min(100, 100 - Number(value.used_percent || 0))), reset_at: value.reset_at ? new Date(Number(value.reset_at) * 1000).toISOString() : '' }));
        if (!windows.length) throw new Error('额度接口未返回可识别窗口');
        let resetCount = null;
        try {
          const detail = await hostManagementRequest('/api-call', { method: 'POST', body: createCodexApiCall({ authIndex: account.auth_index, accountId: account.account_id, method: 'GET', url: 'https://chatgpt.com/backend-api/wham/rate-limit-reset-credits', headers: { Accept: 'application/json', 'OpenAI-Beta': 'codex-1', Originator: 'Codex Desktop' } }) });
          const detailBody = typeof detail?.body === 'string' ? JSON.parse(detail.body) : detail?.body ?? detail?.data ?? detail;
          resetCount = Number(detailBody?.applicable_available_count ?? detailBody?.available_count ?? detailBody?.reset_credits_available);
          if (!Number.isFinite(resetCount)) resetCount = null;
        } catch { /* usage remains useful when reset detail is unavailable */ }
        return { stale: false, refresh_error_code: '', snapshot: { account_key: key, captured_at: new Date().toISOString(), windows, reset_info_complete: resetCount !== null, reset_credits: [], reset_applicable_count: resetCount } };
      }));
      const merged = quotaIndex(); for (const view of refreshed) merged[view.snapshot.account_key] = view; state.quota = Object.values(merged); renderAccounts(); notify('配额快照已刷新');
    } catch (error) { showError('#account-feedback', error); } finally { button && (button.disabled = false); }
  }

  async function runProbe(button) {
    const prepared = prepareProbeRequest({ accounts: state.accounts, selectedAccountKeys: [...state.selected] });
    if (!prepared.ok) { text('#account-feedback', prepared.errorCode === 'selection_required' ? '请先选择需要检测的账号。' : '所选账号已停用或当前不可用。'); return; }
    button.disabled = true; text('#account-feedback', '正在提交检测任务…');
    try { const result = await request('/probes', { method: 'POST', body: prepared.body }); state.status = { ...state.status, ...result }; text('#account-feedback', ''); notify(`检测任务 ${result?.run_id || ''} 已开始；完成后的配额与结果可点击刷新查看。`); } catch (error) { showError('#account-feedback', error); } finally { button.disabled = false; }
  }

  function openReset(account, quota) {
    const prior = state.resetIntent; const idempotencyKey = prior?.accountKey === account.account_key && prior?.status !== 'completed' ? prior.idempotencyKey : crypto.randomUUID(); state.resetIntent = { accountKey: account.account_key, idempotencyKey, status: 'ready' }; text('#reset-account', account.email || account.masked_identity || account.account_key); text('#reset-credits', quota?.snapshot?.reset_applicable_count ?? '--'); text('#reset-feedback', ''); $('#reset-dialog').showModal();
  }
  async function submitReset() {
    const intent = state.resetIntent; if (!intent || intent.status === 'submitting') return; intent.status = 'submitting'; $('#reset-confirm').disabled = true;
    try { await request('/quota/reset', { method: 'POST', body: { account_key: intent.accountKey, idempotency_key: intent.idempotencyKey } }); intent.status = 'completed'; await refreshQuota([intent.accountKey]); $('#reset-dialog').close(); notify('窗口重置完成'); } catch (error) { intent.status = 'failed'; showError('#reset-feedback', error); } finally { $('#reset-confirm').disabled = false; }
  }

  $('[data-action="refresh-panel"]').addEventListener('click', (event) => loadPanel().then(() => notify('面板已刷新')).catch((error) => { $('#connection').dataset.state = 'error'; text('#connection', requestErrorMessage(error)); }));
  $('[data-action="focus-settings"]').addEventListener('click', () => $('#settings').scrollIntoView({ behavior: 'smooth', block: 'start' }));
  $('[data-action="show-help"]').addEventListener('click', () => $('#help-dialog').showModal());
  $('[data-action="select-all"]').addEventListener('click', () => { state.selected = new Set(state.accounts.filter((account) => !account.disabled).map((account) => account.account_key)); renderAccounts(); });
  $('[data-action="select-none"]').addEventListener('click', () => { state.selected.clear(); renderAccounts(); });
  $('#select-all-checkbox').addEventListener('change', (event) => { state.selected = event.target.checked ? new Set(state.accounts.filter((account) => !account.disabled).map((account) => account.account_key)) : new Set(); renderAccounts(); });
  $('[data-action="toggle-identities"]').addEventListener('click', (event) => { state.hidden = !state.hidden; event.currentTarget.querySelector('span').textContent = state.hidden ? '显示' : '隐藏'; renderAccounts(); });
  $('[data-action="refresh-quota"]').addEventListener('click', (event) => refreshQuota([...state.selected], event.currentTarget));
  $('[data-action="run-probe"]').addEventListener('click', (event) => runProbe(event.currentTarget));
  $('[data-action="restore-schedule"]').addEventListener('click', () => applySchedule(state.schedule));
  $('[data-action="add-skip-window"]').addEventListener('click', () => { const input = $('#skip-window-input'); if (input.value && !state.skipTimes.includes(input.value)) { state.skipTimes.push(input.value); state.skipTimes.sort(); input.value = ''; renderSkipTimes(); renderSimulation(); } });
  $('#schedule-enabled').addEventListener('change', renderSimulation); $('#schedule-form').addEventListener('input', renderSimulation);
  $('#schedule-form').addEventListener('submit', async (event) => { event.preventDefault(); const button = event.submitter; button.disabled = true; text('#schedule-feedback', ''); try { const saved = await request('/schedule', { method: 'PUT', headers: { 'If-Match': String(state.schedule.revision ?? 0) }, body: readSchedule() }); applySchedule(saved); notify('自动检测配置已保存'); } catch (error) { showError('#schedule-feedback', error); } finally { button.disabled = false; } });
  $('[data-action="load-records"]').addEventListener('click', async () => { try { state.history = await request('/history'); renderHistory(); notify('检测历史已刷新'); } catch (error) { notify(requestErrorMessage(error)); } });
  $('[data-action="clear-data"]').addEventListener('click', () => $('#clear-dialog').showModal());
  $$('[data-action="close-clear"]').forEach((button) => button.addEventListener('click', () => $('#clear-dialog').close()));
  $('#clear-form').addEventListener('submit', async (event) => { event.preventDefault(); try { await request('/history', { method: 'DELETE' }); state.history = []; renderAccounts(); renderHistory(); $('#clear-dialog').close(); notify('检测历史已删除'); } catch (error) { notify(requestErrorMessage(error)); } });
  $$('[data-action="close-reset"]').forEach((button) => button.addEventListener('click', () => $('#reset-dialog').close())); $('#reset-form').addEventListener('submit', (event) => { event.preventDefault(); submitReset(); });
  loadPanel().catch((error) => { $('#connection').dataset.state = 'error'; text('#connection', requestErrorMessage(error)); });
}

if (typeof document !== 'undefined') {
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', bootPanel, { once: true }); else bootPanel();
}

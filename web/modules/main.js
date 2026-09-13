import { renderSimulationComparison } from './timeline.js';
import { createCodexApiCall, hostManagementRequest, normalizeCodexQuota, normalizeHostAuthFiles, request, requestErrorMessage } from './api.js';
import { projectAccountRow, summarizeOperations } from './dashboard.js';

export function syncHostTheme({ root = globalThis.document?.documentElement, parentRoot, parentDocument, windowRef = globalThis.window, observe = true } = {}) {
  if (!root) return () => {};
  let source = parentRoot;
  try { source ||= (parentDocument || windowRef?.parent?.document)?.documentElement; } catch { source = null; }
  const media = windowRef?.matchMedia?.('(prefers-color-scheme: dark)');
  const apply = () => {
    const parentTheme = source?.getAttribute?.('data-theme') || (source?.classList?.contains?.('dark') ? 'dark' : '');
    root.setAttribute('data-theme', parentTheme === 'dark' || parentTheme === 'light' ? parentTheme : (media?.matches ? 'dark' : 'light'));
    const themeColor = root.ownerDocument?.querySelector('meta[name="theme-color"]');
    if (themeColor) themeColor.content = root.getAttribute('data-theme') === 'dark' ? '#101722' : '#f4f7fb';
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
    && quota?.snapshot?.reset_info_complete && Number(quota?.snapshot?.reset_applicable_count) > 0
    && Number.isFinite(capturedAt) && current >= capturedAt && current - capturedAt <= RESET_FRESHNESS_MS);
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
  const state = { status: {}, accounts: [], schedule: {}, quota: [], history: [], audit: [], selected: new Set(), scheduledKeys: new Set(), hidden: true, skipTimes: [], resetIntent: null, probeIntent: null, simulationTimer: null };
  text('#simulation-output', '正在准备模拟器…');
  syncHostTheme();

  const quotaIndex = () => Object.fromEntries(state.quota.map((item) => [item?.snapshot?.account_key || item?.account_key, item]).filter(([key]) => key));
  const notify = (message) => { text('#feedback', message); clearTimeout(notify.timer); notify.timer = setTimeout(() => text('#feedback', ''), 3600); };
  const showError = (selector, error) => text(selector, requestErrorMessage(error));
  function renderSummary() {
    const summary = summarizeOperations({ accounts: state.accounts, quotaByAccount: quotaIndex(), scheduledKeys: state.scheduledKeys, guardrailHoldCount: state.status?.guardrail_hold_count });
    text('#summary-scheduled', summary.scheduled); text('#summary-healthy', summary.healthy); text('#summary-paused', summary.paused); text('#summary-guardrail', summary.guardrail); text('#summary-stale', summary.stale);
    text('#selection-count', `已选择 ${state.selected.size} 个账号`);
    const allSelectable = state.accounts.filter((account) => !account.disabled);
    const all = $('#select-all-checkbox');
    all.checked = allSelectable.length > 0 && allSelectable.every((account) => state.selected.has(account.account_key));
    all.indeterminate = state.selected.size > 0 && !all.checked;
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
      const name = document.createElement('div'); name.className = 'account-name'; const strong = document.createElement('strong'); strong.textContent = model.email || account.masked_identity || '***'; const small = document.createElement('small'); small.textContent = state.hidden ? '身份已遮罩' : model.key; name.append(strong, small);
      const scheduled = document.createElement('label'); scheduled.className = 'switch row-switch'; const scheduledInput = document.createElement('input'); scheduledInput.type = 'checkbox'; scheduledInput.checked = state.scheduledKeys.has(model.key); scheduledInput.disabled = account.disabled; scheduledInput.dataset.accountScheduled = model.key; scheduledInput.setAttribute('aria-label', `自动预热 ${model.email}`); const scheduledTrack = document.createElement('span'); const scheduledLabel = document.createElement('em'); scheduledLabel.textContent = scheduledInput.checked ? '已计划' : '仅手动'; scheduledInput.addEventListener('change', () => { scheduledInput.checked ? state.scheduledKeys.add(model.key) : state.scheduledKeys.delete(model.key); scheduledLabel.textContent = scheduledInput.checked ? '已计划' : '仅手动'; queueSimulation(); }); scheduled.append(scheduledInput, scheduledTrack, scheduledLabel);
      const status = document.createElement('span'); status.className = `status-pill status-${model.status}`; status.textContent = statusLabels[model.status];
      const actions = document.createElement('div'); actions.className = 'row-actions';
      const refresh = document.createElement('button'); refresh.className = 'secondary-button'; refresh.type = 'button'; refresh.textContent = '刷新'; refresh.dataset.rowAction = 'refresh'; refresh.disabled = account.disabled || Boolean(state.quotaBusy); refresh.addEventListener('click', () => refreshQuota([model.key], refresh));
      const reset = document.createElement('button'); reset.className = 'secondary-button'; reset.type = 'button'; reset.textContent = '重置'; reset.dataset.rowAction = 'reset'; reset.disabled = !isResetQuotaEligible(account, quota[model.key]); reset.addEventListener('click', () => openReset(account, quota[model.key])); actions.append(refresh, reset);
      const snapshot = quota[model.key]?.snapshot || {}; const windows = snapshot.windows || []; const short = windows.find((item) => item.short) || windows[0]; const long = windows.find((item) => !item.short); const record = state.history.filter((item) => item.account_key === model.key).sort((a, b) => Date.parse(b.finished_at || b.started_at || '') - Date.parse(a.finished_at || a.started_at || ''))[0] || {};
      const windowText = (item) => item ? (() => { const remaining = Math.max(0, Math.min(100, Number(item.remaining_percent ?? 0))); const bar = document.createElement('span'); bar.className = 'quota-inline'; const fill = document.createElement('i'); fill.style.width = `${remaining}%`; bar.append(fill); const label = document.createElement('span'); label.textContent = `${item.remaining_percent ?? '--'}% · ${formatDate(item.reset_at)}`; const wrap = document.createElement('span'); wrap.className = 'quota-inline-wrap'; wrap.append(bar, label); return wrap; })() : '--';
      row.append(createCell('选择', checkbox), createCell('账号', name), createCell('自动预热', scheduled), createCell('AUTH INDEX', model.authIndex, 'mono'), createCell('账号前缀', model.accountPrefix, 'mono'), createCell('套餐类型', model.plan), createCell('状态', status), createCell('短窗口', windowText(short)), createCell('长窗口', windowText(long)), createCell('重置额度', snapshot.reset_applicable_count ?? (snapshot.reset_info_complete ? (snapshot.reset_credits || []).length : '--')), createCell('请求结果', record.request_outcome || '--'), createCell('窗口结果', record.window_outcome || '--'), createCell('HTTP / 耗时', record.http_status ? `${record.http_status} / ${record.latency_ms || 0} ms` : '--'), createCell('快照时间', `${formatDate(snapshot.captured_at)}${quota[model.key]?.stale ? ' · 已过期' : ''}`), createCell('错误原因', model.error || '--'), createCell('操作', actions));
      body.append(row);
    }
    renderSummary();
  }

  function setField(name, value) { const input = $(`#schedule-form [name="${name}"]`); if (input) input.value = value ?? ''; }
  function renderWeekdays(values = []) {
    const selected = new Set(values.map(String)); const root = $('#weekdays'); root.replaceChildren();
    for (const [value, label] of WEEKDAYS) { const chip = document.createElement('label'); chip.className = 'weekday-chip'; const input = document.createElement('input'); input.type = 'checkbox'; input.value = value; input.checked = selected.has(value); input.addEventListener('change', queueSimulation); chip.append(input, document.createTextNode(label)); root.append(chip); }
  }
  function renderSkipTimes() {
    const root = $('#skip-window-chips'); root.replaceChildren();
    for (const value of state.skipTimes) { const chip = document.createElement('span'); chip.className = 'skip-chip'; chip.append(document.createTextNode(value)); const remove = document.createElement('button'); remove.type = 'button'; remove.textContent = '×'; remove.setAttribute('aria-label', `删除跳过窗口 ${value}`); remove.addEventListener('click', () => { state.skipTimes = state.skipTimes.filter((item) => item !== value); renderSkipTimes(); queueSimulation(); }); chip.append(remove); root.append(chip); }
  }
  function renderPeriods(rootSelector, periods = []) {
    const root = $(rootSelector); root.replaceChildren();
    const kind = root.id === 'work-periods' ? 'work' : 'blackout';
    const values = periods.length ? periods : (kind === 'work' ? [{ start: '', end: '' }] : []);
    values.forEach((period) => {
      const row = document.createElement('div'); row.className = 'period-row';
      const start = document.createElement('input'); start.type = 'time'; start.value = period.start || ''; start.setAttribute('aria-label', `${kind === 'work' ? '工作' : '禁用'}时段开始`);
      const separator = document.createElement('span'); separator.textContent = '至';
      const end = document.createElement('input'); end.type = 'time'; end.value = period.end || ''; end.setAttribute('aria-label', `${kind === 'work' ? '工作' : '禁用'}时段结束`);
      const remove = document.createElement('button'); remove.type = 'button'; remove.className = 'text-button'; remove.textContent = '删除'; remove.addEventListener('click', () => { row.remove(); if (kind === 'work' && !root.children.length) renderPeriods(rootSelector, []); queueSimulation(); });
      start.addEventListener('input', queueSimulation); end.addEventListener('input', queueSimulation); row.append(start, separator, end, remove); root.append(row);
    });
  }
  function readPeriods(rootSelector) {
    return [...$(rootSelector).querySelectorAll('.period-row')].map((row) => { const inputs = row.querySelectorAll('input'); return { start: inputs[0]?.value || '', end: inputs[1]?.value || '' }; }).filter((period) => period.start || period.end);
  }
  function addPeriod(rootSelector) {
    const periods = readPeriods(rootSelector); periods.push({ start: '', end: '' }); renderPeriods(rootSelector, periods); queueSimulation();
  }
  function draftSignature() {
    return JSON.stringify({
      fields: [...$('#schedule-form').querySelectorAll('input[name]')].map((input) => [input.name, input.value]),
      enabled: $('#schedule-enabled').checked,
      weekdays: $$('#weekdays input:checked').map((input) => input.value),
      skipTimes: state.skipTimes,
      scheduled: [...state.scheduledKeys].sort(),
    });
  }
  const isDraftDirty = () => state.savedDraftSignature !== undefined && draftSignature() !== state.savedDraftSignature;
  function updateDraftStatus() { text('#schedule-dirty', isDraftDirty() ? '有未保存的修改' : '已保存'); }
  function clearFieldErrors() {
    $$('#schedule-form .field-error').forEach((node) => node.remove());
    $$('#schedule-form [aria-invalid]').forEach((input) => { input.removeAttribute('aria-invalid'); input.removeAttribute('aria-describedby'); });
  }
  function validateScheduleForm({ showErrors = true } = {}) {
    const form = $('#schedule-form'); const errors = new Map();
    const field = (name) => form.elements[name];
    const add = (name, message) => { if (!errors.has(name)) errors.set(name, message); };
    for (const input of form.querySelectorAll('input[name]')) {
      if (!input.checkValidity()) {
        const label = input.closest('label')?.querySelector('span')?.textContent || '此项';
        add(input.name, input.validity.valueMissing ? `请填写${label}。` : input.type === 'number' ? `请输入${input.min || '有效'}${input.max ? `–${input.max}` : '以上'}的整数。` : `请填写有效的${label}。`);
      }
    }
    const timezone = field('timezone').value.trim();
    try { if (!timezone || timezone === 'Local') throw new Error(); new Intl.DateTimeFormat('zh-CN', { timeZone: timezone }); }
    catch { add('timezone', '请输入有效的 IANA 时区，例如 Asia/Shanghai。'); }
    if (!field('probe_model').value.trim()) add('probe_model', '请填写检测模型。');
    const work = field('work_start').value, lunch = field('lunch_start').value, resume = field('lunch_end').value, end = field('work_end').value;
    if (work && lunch && work >= lunch) add('lunch_start', '午休开始必须晚于工作开始。');
    if (lunch && resume && lunch > resume) add('lunch_end', '午休结束不能早于午休开始。');
    if (resume && end && resume >= end) add('work_end', '工作结束必须晚于午休结束。');
    const lead = field('preheat_lead_minutes').value, span = field('preheat_span_minutes').value;
    if ($('#schedule-enabled').checked || lead !== '' || span !== '') {
      if (!lead) add('preheat_lead_minutes', '请同时填写预热提前时间和预热时长。');
      if (!span) add('preheat_span_minutes', '请同时填写预热提前时间和预热时长。');
    }
    if (lead && span && work) {
      const [hours, minutes] = work.split(':').map(Number);
      if (Number(lead) + Number(span) > hours * 60 + minutes) add('preheat_lead_minutes', '提前时间与预热时长之和不能超过当天工作开始时间，预热不能跨到前一天。');
    }
    if (showErrors) {
      clearFieldErrors();
      for (const [name, message] of errors) {
        const input = field(name); const error = document.createElement('small');
        error.id = `field-error-${name}`; error.className = 'field-error'; error.textContent = message;
        input.setAttribute('aria-invalid', 'true'); input.setAttribute('aria-describedby', error.id); input.after(error);
      }
      const first = form.querySelector('[aria-invalid="true"]');
      if (first) { const details = first.closest('details'); if (details) details.open = true; first.focus(); }
      text('#schedule-feedback', errors.size ? `有 ${errors.size} 项参数需要修正，请查看字段下方提示。` : '');
    }
    return errors.size === 0;
  }
  function applySchedule(schedule) {
    state.schedule = structuredClone(schedule || {}); $('#schedule-enabled').checked = Boolean(schedule.enabled); text('#schedule-revision', schedule.revision ?? '--');
    for (const name of ['timezone', 'window_hours', 'productivity_minutes', 'health_threshold_percent', 'preheat_lead_minutes', 'preheat_span_minutes', 'remaining_quota_floor_percent', 'remaining_window_floor_minutes', 'long_window_floor_percent', 'probe_model', 'probe_timeout_seconds']) setField(name, schedule[name] ?? '');
    state.scheduledKeys = new Set(schedule.scheduled_account_keys || []);
    const periods = schedule.work_periods || [];
    setField('work_start', periods[0]?.start || '09:00'); setField('lunch_start', periods[0]?.end || '12:00');
    setField('lunch_end', periods[1]?.start || '13:30'); setField('work_end', periods[1]?.end || '19:00');
    renderPeriods('#work-periods', periods); renderPeriods('#blackout-periods', schedule.blackout_periods || []);
    renderWeekdays(schedule.weekdays || [1, 2, 3, 4, 5]); state.skipTimes = [...(schedule.skip_window_times || [])]; renderSkipTimes();
    clearFieldErrors(); text('#schedule-feedback', ''); state.savedDraftSignature = draftSignature(); queueSimulation(); renderAccounts();
  }
  function readSchedule() {
    const form = $('#schedule-form'); const value = (name) => form.elements[name]?.value || ''; const integer = (name, fallback = 0) => value(name) === '' ? fallback : Number(value(name)); const optionalInteger = (name) => value(name) === '' ? null : Number(value(name));
    const fixedPeriods = [{ start: value('work_start'), end: value('lunch_start') }, { start: value('lunch_end'), end: value('work_end') }];
    const workPeriods = fixedPeriods.every((period) => period.start && period.end) ? fixedPeriods : readPeriods('#work-periods');
    return { ...state.schedule, enabled: $('#schedule-enabled').checked, timezone: value('timezone'), window_hours: integer('window_hours', 5), productivity_minutes: integer('productivity_minutes', 60), health_threshold_percent: integer('health_threshold_percent', 80), preheat_lead_minutes: optionalInteger('preheat_lead_minutes'), preheat_span_minutes: optionalInteger('preheat_span_minutes'), remaining_quota_floor_percent: integer('remaining_quota_floor_percent', 20), remaining_window_floor_minutes: integer('remaining_window_floor_minutes', 60), long_window_floor_percent: integer('long_window_floor_percent', 10), probe_model: value('probe_model'), probe_timeout_seconds: integer('probe_timeout_seconds', 30), weekdays: $$('#weekdays input:checked').map((input) => Number(input.value)), work_periods: workPeriods, blackout_periods: readPeriods('#blackout-periods'), skip_window_times: [...state.skipTimes], scheduled_account_keys: [...state.scheduledKeys] };
  }

  async function runSimulation() {
    if (!validateScheduleForm({ showErrors: false })) { text('#simulation-output', '请补全有效参数后查看模拟结果；错误详情可点击“保存配置”定位。'); return; }
    const config = readSchedule(); try { const result = await request('/simulate', { method: 'POST', body: config }); renderSimulationComparison($('#simulation-output'), result, config); } catch (error) { text('#simulation-output', requestErrorMessage(error)); }
  }
  function queueSimulation() {
    updateDraftStatus();
    clearTimeout(state.simulationTimer); state.simulationTimer = setTimeout(runSimulation, 180);
  }

  function renderHistory() {
    const body = $('#history-output'); body.replaceChildren();
    if (!state.history.length) { const row = document.createElement('tr'); const cell = createCell('', '暂无检测历史', 'empty-row'); cell.colSpan = 8; row.append(cell); body.append(row); return; }
    const batches = new Map();
    state.history.slice(0, 100).forEach((record) => { const key = record.run_id || record.occurrence_id || record.correlation_id || record.id; if (!batches.has(key)) batches.set(key, []); batches.get(key).push(record); });
    for (const [key, records] of batches) {
      const first = records[0]; const summary = document.createElement('tr'); const cell = document.createElement('td'); cell.colSpan = 8;
      const details = document.createElement('details'); const summaryNode = document.createElement('summary'); summaryNode.textContent = `${formatDate(first.finished_at || first.started_at)} · ${triggerLabels[first.trigger] || first.trigger} · ${records.length} 个账号`;
      const list = document.createElement('div'); list.className = 'history-batch-details';
      records.forEach((record) => { const item = document.createElement('div'); item.className = 'history-batch-item'; item.textContent = `${record.masked_identity || record.account_key} · ${record.request_outcome || '--'} · ${record.window_outcome || '--'} · ${record.http_status || '--'} · ${formatDuration(record.latency_ms || 0)}${record.error_code ? ` · ${record.error_code}` : ''}`; list.append(item); });
      details.append(summaryNode, list); cell.append(details); summary.append(cell); body.append(summary);
    }
  }

  function renderAudit() {
    const body = $('#reset-audit-output'); body.replaceChildren();
    if (!state.audit.length) { const row = document.createElement('tr'); const cell = createCell('', '暂无重置审计', 'empty-row'); cell.colSpan = 6; row.append(cell); body.append(row); return; }
    state.audit.forEach((record) => { const row = document.createElement('tr'); row.append(createCell('请求时间', formatDate(record.requested_at)), createCell('账号', record.masked_identity || record.account_key), createCell('重置前额度', record.prior_applicable_credits ?? '--'), createCell('结果', record.outcome || '--'), createCell('HTTP 分类', record.http_category || '--'), createCell('关联 ID', record.correlation_id || '--', 'mono')); body.append(row); });
  }

  async function loadPanel() {
    if (state.panelLoading || state.quotaBusy) return false;
    state.panelLoading = true;
    try {
      $('#connection').dataset.state = 'loading'; text('#connection', '正在加载数据…');
      const statusJob = request('/status');
      const accountsJob = hostManagementRequest('/auth-files').then((files) => { state.accounts = normalizeHostAuthFiles(files); renderAccounts(); return files; });
      const scheduleJob = request('/schedule').then((schedule) => { if (!isDraftDirty()) { applySchedule(schedule || {}); queueSimulation(); } return schedule; });
      const quotaJob = request('/quota'); const historyJob = request('/history'); const auditJob = request('/reset-audit');
      const jobs = await Promise.allSettled([statusJob, accountsJob, scheduleJob, quotaJob, historyJob, auditJob]);
      const value = (index, fallback) => jobs[index]?.status === 'fulfilled' ? jobs[index].value : fallback;
      state.status = value(0, state.status); if (jobs[1].status === 'fulfilled') state.accounts = normalizeHostAuthFiles(jobs[1].value);
      state.quota = value(3, state.quota); state.history = value(4, state.history); state.audit = value(5, state.audit); state.selected = new Set([...state.selected].filter((key) => state.accounts.some((account) => account.account_key === key && !account.disabled)));
      if (jobs[2].status === 'fulfilled' && !isDraftDirty()) applySchedule(jobs[2].value || {});
      renderAccounts(); renderHistory(); renderAudit(); updateDraftStatus(); queueSimulation();
      const failed = jobs.find((job) => job.status === 'rejected');
      $('#connection').dataset.state = failed ? 'error' : 'ready';
      text('#connection', failed ? `部分数据加载失败，已保留原数据。${requestErrorMessage(failed.reason)}` : '');
      return !failed;
    } finally { state.panelLoading = false; }
  }

  async function withQuotaRefresh(button, action) {
    if (state.quotaBusy || state.panelLoading) return;
    state.quotaBusy = true;
    const controls = $$('[data-action="refresh-quota"], [data-action="refresh-all-quota"], [data-row-action="refresh"], [data-action="refresh-panel"]');
    const previous = controls.map((control) => [control, control.disabled]);
    controls.forEach((control) => { control.disabled = true; });
    const label = button ? [...button.childNodes] : [];
    if (button) { button.textContent = '刷新中…'; button.setAttribute('aria-busy', 'true'); }
    text('#account-feedback', ''); text('#feedback', '');
    try {
      return await action();
    } catch (error) { showError('#account-feedback', error); }
    finally {
      state.quotaBusy = false;
      previous.forEach(([control, disabled]) => { control.disabled = disabled; });
      if (button) { button.replaceChildren(...label); button.removeAttribute('aria-busy'); }
      renderAccounts();
    }
  }

  async function refreshQuotaSnapshots(keys) {
    const capturedAt = Date.now();
    const refreshed = await Promise.allSettled(keys.map(async (key) => {
      const account = state.accounts.find((item) => item.account_key === key);
      if (!account?.auth_index) throw new Error('账号缺少 auth_index');
      const result = await hostManagementRequest('/api-call', { method: 'POST', body: createCodexApiCall({ authIndex: account.auth_index, accountId: account.account_id, method: 'GET', url: 'https://chatgpt.com/backend-api/wham/usage', headers: { Accept: 'application/json' } }) });
      const statusCode = Number(result?.status_code || result?.statusCode || 0);
      if (statusCode < 200 || statusCode >= 300) throw new Error(`额度接口 HTTP ${statusCode || '-'}`);
      const body = typeof result?.body === 'string' ? result.body : result?.data ?? result;
      const usage = typeof body === 'string' ? JSON.parse(body) : body;
      let resetPayload = null;
      try {
        const detail = await hostManagementRequest('/api-call', { method: 'POST', body: createCodexApiCall({ authIndex: account.auth_index, accountId: account.account_id, method: 'GET', url: 'https://chatgpt.com/backend-api/wham/rate-limit-reset-credits', headers: { Accept: 'application/json', 'OpenAI-Beta': 'codex-1', Originator: 'Codex Desktop' } }) });
        resetPayload = typeof detail?.body === 'string' ? JSON.parse(detail.body) : detail?.body ?? detail?.data ?? detail;
      } catch { /* usage remains useful when reset detail is unavailable */ }
      const normalized = normalizeCodexQuota(usage, { capturedAt, resetPayload });
      if (!normalized.windows.length) throw new Error('额度接口未返回可识别窗口');
      return { stale: false, refresh_error_code: '', snapshot: { account_key: key, captured_at: new Date(capturedAt).toISOString(), ...normalized } };
    }));
    const merged = quotaIndex(); let succeeded = 0;
    refreshed.forEach((outcome, index) => {
      const key = keys[index];
      if (outcome.status === 'fulfilled') { merged[key] = outcome.value; succeeded++; }
      else merged[key] = { ...merged[key], account_key: key, stale: true, refresh_error_code: requestErrorMessage(outcome.reason) };
    });
    state.quota = Object.values(merged); renderAccounts();
    return { succeeded, failed: keys.length - succeeded };
  }

  function reportQuotaRefresh(outcome, skipped = 0) {
    const message = `额度刷新：成功 ${outcome.succeeded}，失败 ${outcome.failed}，跳过 ${skipped}。`;
    if (outcome.failed) text('#account-feedback', `${message}失败账号保留旧快照并标记过期，请检查错误原因后重试。`);
    else notify(message);
  }

  async function refreshQuota(keys, button) {
    if (!keys.length) { text('#account-feedback', '请先选择账号，或使用“刷新全部额度”。'); return; }
    return withQuotaRefresh(button, async () => {
      const enabled = keys.filter((key) => state.accounts.some((account) => account.account_key === key && !account.disabled));
      if (!enabled.length) { text('#account-feedback', '没有可刷新的已启用账号。'); return; }
      const outcome = await refreshQuotaSnapshots(enabled);
      reportQuotaRefresh(outcome, keys.length - enabled.length);
      return outcome;
    });
  }

  async function refreshAllQuota(button) {
    return withQuotaRefresh(button, async () => {
      const files = normalizeHostAuthFiles(await hostManagementRequest('/auth-files'));
      const previous = new Set(state.selected);
      state.accounts = files;
      state.selected = new Set([...previous].filter((key) => files.some((account) => account.account_key === key && !account.disabled)));
      renderAccounts();
      const keys = files.filter((account) => !account.disabled).map((account) => account.account_key);
      if (!keys.length) { text('#account-feedback', `没有可刷新的已启用账号。已发现 ${files.length} 个账号，均未发送额度请求。`); return; }
      const outcome = await refreshQuotaSnapshots(keys);
      reportQuotaRefresh(outcome, files.length - keys.length);
      return outcome;
    });
  }

  function openProbe() {
    const keys = [...state.selected]; if (!keys.length) { text('#account-feedback', '请先选择需要检测的账号。'); return; }
    if (state.accounts.some((account) => keys.includes(account.account_key) && account.disabled)) { text('#account-feedback', '已停用账号不能执行检测。'); return; }
    const unavailable = state.accounts.some((account) => keys.includes(account.account_key) && account.unavailable); state.probeIntent = { keys, unavailable }; text('#probe-selection-summary', `将检测 ${keys.length} 个账号。`); $('#unavailable-confirm-row').hidden = !unavailable; $('#unavailable-confirm').checked = false; text('#probe-feedback', ''); $('#probe-dialog').showModal();
  }
  async function submitProbe(button) {
    const intent = state.probeIntent; if (!intent) return; const unavailableAcknowledged = !intent.unavailable || $('#unavailable-confirm').checked;
    const prepared = prepareProbeRequest({ accounts: state.accounts, selectedAccountKeys: intent.keys, unavailableAcknowledged });
    if (!prepared.ok) { text('#probe-feedback', prepared.errorCode === 'account_unavailable' ? '请确认仍要检测不可用账号。' : '当前选择不能执行检测。'); return; }
    button.disabled = true; text('#probe-feedback', '正在提交检测任务…');
    try { const result = await request('/probes', { method: 'POST', body: prepared.body }); state.status = { ...state.status, ...result }; $('#probe-dialog').close(); text('#account-feedback', ''); notify(`检测任务 ${result?.run_id || ''} 已开始；完成后的配额与结果可点击刷新查看。`); } catch (error) { showError('#probe-feedback', error); } finally { button.disabled = false; }
  }

  function openReset(account, quota) {
    const prior = state.resetIntent; const idempotencyKey = prior?.accountKey === account.account_key && prior?.status !== 'completed' ? prior.idempotencyKey : crypto.randomUUID(); state.resetIntent = { accountKey: account.account_key, idempotencyKey, status: 'ready' }; text('#reset-account', state.hidden ? (account.masked_identity || '***') : (account.email || account.account_key)); text('#reset-credits', quota?.snapshot?.reset_applicable_count ?? '--'); text('#reset-feedback', ''); $('#reset-dialog').showModal();
  }
  async function submitReset() {
    const intent = state.resetIntent; if (!intent || intent.status === 'submitting') return; intent.status = 'submitting'; $('#reset-confirm').disabled = true;
    try { await request('/quota/reset', { method: 'POST', body: { account_key: intent.accountKey, idempotency_key: intent.idempotencyKey } }); intent.status = 'completed'; await refreshQuota([intent.accountKey]); state.audit = await request('/reset-audit'); renderAudit(); $('#reset-dialog').close(); notify('窗口重置完成'); } catch (error) { intent.status = 'failed'; showError('#reset-feedback', error); } finally { $('#reset-confirm').disabled = false; }
  }

  $('[data-action="refresh-panel"]').addEventListener('click', () => loadPanel().then((ok) => { if (ok) notify(isDraftDirty() ? '面板已刷新，未保存的配置已保留。' : '面板已刷新'); }).catch((error) => { $('#connection').dataset.state = 'error'; text('#connection', requestErrorMessage(error)); }));
  $('[data-action="focus-settings"]').addEventListener('click', () => { $('#settings').scrollIntoView({ behavior: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'instant' : 'smooth', block: 'start' }); $('#settings').focus({ preventScroll: true }); });
  $('[data-action="show-help"]').addEventListener('click', () => $('#help-dialog').showModal());
  $('[data-action="select-all"]').addEventListener('click', () => { state.selected = new Set(state.accounts.filter((account) => !account.disabled).map((account) => account.account_key)); renderAccounts(); });
  $('[data-action="select-none"]').addEventListener('click', () => { state.selected.clear(); renderAccounts(); });
  $('#select-all-checkbox').addEventListener('change', (event) => { state.selected = event.target.checked ? new Set(state.accounts.filter((account) => !account.disabled).map((account) => account.account_key)) : new Set(); renderAccounts(); });
  $('[data-action="toggle-identities"]').addEventListener('click', (event) => { state.hidden = !state.hidden; event.currentTarget.querySelector('span').textContent = state.hidden ? '显示身份' : '隐藏身份'; renderAccounts(); });
  $('[data-action="refresh-quota"]').addEventListener('click', (event) => refreshQuota([...state.selected], event.currentTarget));
  $('[data-action="refresh-all-quota"]').addEventListener('click', (event) => refreshAllQuota(event.currentTarget));
  $('[data-action="run-probe"]').addEventListener('click', openProbe);
  $('[data-action="restore-schedule"]').addEventListener('click', () => applySchedule(state.schedule));
  $('[data-action="add-work-period"]')?.addEventListener('click', () => addPeriod('#work-periods'));
  $('[data-action="add-blackout-period"]')?.addEventListener('click', () => addPeriod('#blackout-periods'));
  $('[data-action="add-skip-window"]').addEventListener('click', () => { const input = $('#skip-window-input'); if (input.value && !state.skipTimes.includes(input.value)) { state.skipTimes.push(input.value); state.skipTimes.sort(); input.value = ''; renderSkipTimes(); queueSimulation(); } });
  $('#schedule-enabled').addEventListener('change', queueSimulation); $('#schedule-form').addEventListener('input', queueSimulation);
  $('#schedule-form').addEventListener('submit', async (event) => {
    event.preventDefault(); if (state.saving || !validateScheduleForm()) return;
    const button = event.submitter || $('#schedule-form button[type="submit"]');
    const submitted = draftSignature(); const config = readSchedule();
    state.saving = true; button.disabled = true; button.textContent = '保存中…'; button.setAttribute('aria-busy', 'true');
    try {
      const saved = await request('/schedule', { method: 'PUT', headers: { 'If-Match': String(state.schedule.revision ?? 0) }, body: config });
      if (draftSignature() === submitted) applySchedule(saved);
      else { state.schedule = structuredClone(saved); state.savedDraftSignature = submitted; text('#schedule-revision', saved.revision); updateDraftStatus(); }
      notify(isDraftDirty() ? '提交的配置已保存，后续修改仍未保存。' : '自动检测配置已保存');
    } catch (error) { showError('#schedule-feedback', error); }
    finally { state.saving = false; button.disabled = false; button.textContent = '保存配置'; button.removeAttribute('aria-busy'); }
  });
  window.addEventListener('beforeunload', (event) => { if (isDraftDirty()) { event.preventDefault(); event.returnValue = ''; } });
  for (const input of $$('#schedule-form input[type="number"]')) { input.step = '1'; input.required = !['preheat_lead_minutes', 'preheat_span_minutes'].includes(input.name); }
  for (const name of ['preheat_lead_minutes', 'preheat_span_minutes']) $(`[name="${name}"]`).max = '1440';
  $('[data-action="load-records"]').addEventListener('click', async () => { try { state.history = await request('/history'); renderHistory(); notify('检测历史已刷新'); } catch (error) { notify(requestErrorMessage(error)); } });
  $('[data-action="load-audit"]').addEventListener('click', async () => { try { state.audit = await request('/reset-audit'); renderAudit(); notify('重置审计已刷新'); } catch (error) { notify(requestErrorMessage(error)); } });
  $('[data-action="clear-audit"]').addEventListener('click', () => { $('#audit-confirmation').value = ''; text('#audit-feedback', ''); $('#clear-audit-dialog').showModal(); });
  $$('[data-action="close-audit"]').forEach((button) => button.addEventListener('click', () => $('#clear-audit-dialog').close()));
  $('#clear-audit-form').addEventListener('submit', async (event) => { event.preventDefault(); const confirmation = $('#audit-confirmation').value; if (confirmation !== 'DELETE AUDIT') { text('#audit-feedback', '请输入完整确认短语 DELETE AUDIT。'); return; } try { await request('/reset-audit', { method: 'DELETE', headers: { 'X-Confirmation': confirmation } }); state.audit = []; renderAudit(); $('#clear-audit-dialog').close(); notify('重置审计已清除'); } catch (error) { showError('#audit-feedback', error); } });
  $('[data-action="clear-data"]').addEventListener('click', () => $('#clear-dialog').showModal());
  $$('[data-action="close-clear"]').forEach((button) => button.addEventListener('click', () => $('#clear-dialog').close()));
  $('#clear-form').addEventListener('submit', async (event) => { event.preventDefault(); try { await request('/history', { method: 'DELETE' }); state.history = []; renderAccounts(); renderHistory(); $('#clear-dialog').close(); notify('检测历史已删除'); } catch (error) { notify(requestErrorMessage(error)); } });
  $$('[data-action="close-probe"]').forEach((button) => button.addEventListener('click', () => $('#probe-dialog').close())); $('#probe-form').addEventListener('submit', (event) => { event.preventDefault(); submitProbe($('#probe-confirm')); });
  $$('[data-action="close-reset"]').forEach((button) => button.addEventListener('click', () => $('#reset-dialog').close())); $('#reset-form').addEventListener('submit', (event) => { event.preventDefault(); submitReset(); });
  loadPanel().catch((error) => { $('#connection').dataset.state = 'error'; text('#connection', requestErrorMessage(error)); });
}

if (typeof document !== 'undefined') {
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', bootPanel, { once: true }); else bootPanel();
}

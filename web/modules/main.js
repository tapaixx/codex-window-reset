import { renderSimulationComparison } from './timeline.js';
import { hostManagementRequest, normalizeHostAuthFiles, refreshAccountQuotas, request, requestErrorMessage } from './api.js';
import { accountStatusDetail, describeNextRun, describeOperation, groupHistoryBatches, quotaAvailabilityNote, resetCreditSummary, projectAccountRow, projectUpcomingBatches, summarizeOperations } from './dashboard.js';

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
const statusLabels = { healthy: '健康', unknown: '未刷新', refresh_failed: '刷新失败', unavailable: '不可用', disabled: '已停用' };
const triggerLabels = { health_probe: '手动检测', preheat: '自动预热', compensation: '失败补偿' };
const $ = (selector) => document.querySelector(selector);
const $$ = (selector) => [...document.querySelectorAll(selector)];
const text = (selector, value) => { const element = $(selector); if (element) element.textContent = String(value ?? ''); };
const formatDate = (value, fallback = '--') => {
  const date = new Date(value || '');
  return Number.isNaN(date.getTime()) ? fallback : date.toLocaleString('zh-CN', { hour12: false });
};
const formatDuration = (milliseconds) => milliseconds >= 1000 ? `${(milliseconds / 1000).toFixed(1)} 秒` : `${milliseconds || 0} ms`;

const probeOutcomeLabels = { succeeded: '成功', unauthorized: '未授权', forbidden: '被拒绝', payment_required: '需付费', rate_limited: '被限流', upstream_error: '上游错误', network_error: '网络错误', timeout: '超时', response_error: '响应异常', unexpected_output: '输出异常', credential_error: '凭据错误', disabled: '已停用' };
const decisionLabels = { proceed: '判定需要预热', window_active: '窗口已在运行，无法再开新窗口', sufficient_window: '窗口已在运行（旧记录：额度充足）', guardrail_hold: 'Guardrail Hold：长窗口余量过低', quota_unknown_fail_open: '额度未知，按放行处理' };
const windowOutcomeLabels = { verified_started: '已开窗', already_active: '窗口已在', unchanged: '窗口未变', unverified: '未验证', not_observed: '未观察' };

// One probe, one cell: the request outcome carries the colour, the window
// outcome and HTTP/latency ride along as the detail nobody scans for.
export function lastProbeCell(record = {}) {
  if (!record.request_outcome) return '--';
  const wrap = document.createElement('span'); wrap.className = 'probe-cell';
  const pill = document.createElement('span');
  pill.className = `probe-pill ${record.request_outcome === 'succeeded' ? 'ok' : 'bad'}`;
  pill.textContent = probeOutcomeLabels[record.request_outcome] || record.request_outcome;
  const detail = [windowOutcomeLabels[record.window_outcome] || record.window_outcome, record.http_status ? `HTTP ${record.http_status}` : '', record.latency_ms ? `${record.latency_ms} ms` : ''].filter(Boolean).join(' · ');
  const note = document.createElement('small'); note.className = 'probe-detail'; note.textContent = detail || '--';
  wrap.title = [pill.textContent, detail].filter(Boolean).join(' · ');
  wrap.append(pill, note);
  return wrap;
}

function createCell(label, content, className = '') {
  const cell = document.createElement('td');
  cell.dataset.label = label;
  if (className) cell.className = className;
  if (content instanceof Node) cell.append(content); else cell.textContent = String(content ?? '--');
  return cell;
}

function bootPanel() {
  const state = { status: {}, upcoming: null, accounts: [], schedule: {}, quota: [], history: [], selected: new Set(), scheduledKeys: new Set(), hidden: true, skipTimes: [], probeIntent: null, simulationTimer: null, simulationGeneration: 0 };
  text('#simulation-output', '正在准备模拟器…');
  syncHostTheme();

  const quotaIndex = () => Object.fromEntries(state.quota.map((item) => [item?.snapshot?.account_key || item?.account_key, item]).filter(([key]) => key));
  const notify = (message) => { text('#feedback', message); clearTimeout(notify.timer); notify.timer = setTimeout(() => text('#feedback', ''), 3600); };
  const showError = (selector, error) => text(selector, requestErrorMessage(error));
  function renderSummary() {
    const summary = summarizeOperations({ accounts: state.accounts, quotaByAccount: quotaIndex(), scheduledKeys: state.scheduledKeys, guardrailHoldCount: state.status?.guardrail_hold_count });
    text('#summary-scheduled', summary.scheduled); text('#summary-healthy', summary.healthy); text('#summary-paused', summary.paused); text('#summary-guardrail', summary.guardrail); text('#summary-stale', summary.stale);
    // Guardrail Hold is the only state that actually blocks preheating. At zero
    // it is noise holding a slot; above zero it outranks everything beside it.
    const guardrail = $('#summary-guardrail')?.closest('span');
    if (guardrail) guardrail.dataset.state = summary.guardrail > 0 ? 'active' : 'idle';
    text('#selection-count', `已选择 ${state.selected.size} 个账号`);
    renderFreshness();
    renderNextRun();
    renderUpcoming();
    renderFirstRun();
    const allSelectable = state.accounts.filter((account) => !account.disabled);
    const all = $('#select-all-checkbox');
    all.checked = allSelectable.length > 0 && allSelectable.every((account) => state.selected.has(account.account_key));
    all.indeterminate = state.selected.size > 0 && !all.checked;
  }

  // The panel never polls, so an hours-old snapshot renders with exactly the
  // authority of a live one. State the age of the oldest snapshot once, near
  // the controls that change it, instead of leaving it to a per-row timestamp.
  function renderFreshness() {
    const node = $('#quota-freshness'); if (!node) return;
    const stamps = state.quota.map((view) => Date.parse(view?.snapshot?.captured_at || '')).filter(Number.isFinite);
    if (!stamps.length) { node.dataset.state = 'none'; node.textContent = '额度快照 · 尚未刷新'; return; }
    const age = Math.max(0, Date.now() - Math.min(...stamps));
    const minutes = Math.floor(age / 60000);
    node.dataset.state = minutes >= 30 ? 'stale' : minutes >= 5 ? 'aging' : 'fresh';
    node.textContent = `额度快照 · ${minutes < 1 ? '刚刚' : minutes < 60 ? `${minutes} 分钟前` : `${Math.floor(minutes / 60)} 小时前`}`;
    if (stamps.length < state.accounts.length) node.textContent += `（${state.accounts.length - stamps.length} 个账号尚无快照）`;
  }

  function resetCreditCell(snapshot) {
    const summary = resetCreditSummary(snapshot);
    if (!summary.expiries.length) return summary.text;
    const wrap = document.createElement('span'); wrap.className = 'reset-cell'; wrap.title = summary.title;
    wrap.textContent = summary.text;
    return wrap;
  }

  function renderUpcoming() {
    const body = $('#upcoming-output'); if (!body) return;
    const note = $('#upcoming-note');
    const expanded = new Set([...body.querySelectorAll('details[open]')].map((details) => details.dataset.batchKey));
    body.replaceChildren();
    const view = state.upcoming || { enabled: state.schedule?.enabled, store_error_code: state.status?.store_error_code, batches: [] };
    const all = projectUpcomingBatches(view, { accounts: state.accounts, hidden: state.hidden, now: Date.now() });
    const batches = state.fullHorizon ? all : all.filter((batch) => batch.near);
    const emptyState = describeNextRun(view, Date.now());
    if (!batches.length) {
      const row = document.createElement('tr');
      // The empty reading must never be a neutral "nothing yet": an enabled
      // schedule with nothing armed is a fault, and it is the shape a stalled
      // scheduler leaves behind.
      const hiddenByFilter = all.length > 0;
      const message = hiddenByFilter ? `今天与明天没有预热批次；完整计划视野中还有 ${all.length} 批` : emptyState.text.replace('下一次预热 · ', '');
      const cell = createCell('', message, 'empty-row');
      cell.colSpan = 8; row.append(cell); body.append(row);
    }
    for (const batch of batches) {
      const row = document.createElement('tr'); const cell = document.createElement('td'); cell.colSpan = 8;
      const details = document.createElement('details'); details.dataset.batchKey = batch.key; details.open = expanded.has(batch.key);
      const summaryNode = document.createElement('summary');
      summaryNode.textContent = `${batch.dayLabel} ${batch.windowLabel} · ${batch.periodLabel} · ${batch.accountCount} 个账号 · ${batch.relative}`;
      const list = document.createElement('div'); list.className = 'history-batch-details';
      for (const slot of batch.occurrences) {
        const item = document.createElement('div'); item.className = 'history-batch-item upcoming-item';
        const who = document.createElement('span'); who.textContent = slot.email;
        const when = document.createElement('b'); when.textContent = slot.time;
        item.append(who, when);
        if (slot.blocked) { const blocked = document.createElement('em'); blocked.className = 'upcoming-blocked'; blocked.textContent = slot.blocked; item.append(blocked); }
        list.append(item);
      }
      details.append(summaryNode, list); cell.append(details); row.append(cell); body.append(row);
    }
    if (note) {
      const hiddenCount = all.length - batches.length;
      note.hidden = hiddenCount <= 0;
      note.textContent = hiddenCount > 0 ? `另有 ${hiddenCount} 批在今天与明天之后；打开「完整计划视野」查看。` : '';
    }
  }

  function renderNextRun() {
    const node = $('#next-run'); if (!node) return;
    const view = state.upcoming || { enabled: state.schedule?.enabled, store_error_code: state.status?.store_error_code, batches: [] };
    const described = describeNextRun(view, Date.now());
    node.dataset.state = described.state;
    node.textContent = described.text;
  }

  // A fresh install is inert by design, which reads as "nothing is happening".
  // Name the three things that have to be true, and retire the whole block once
  // the schedule is enabled.
  function renderFirstRun() {
    const node = $('#first-run'); if (!node) return;
    const schedule = state.schedule || {};
    if (schedule.enabled || state.firstRunDismissed) { node.hidden = true; return; }
    const steps = [
      ['配置预热提前量与时长', Number(schedule.preheat_lead_minutes) > 0 && Number(schedule.preheat_span_minutes) > 0],
      ['勾选账号并加入预热计划', state.scheduledKeys.size > 0],
      ['保存并启用计划', Boolean(schedule.enabled)],
    ];
    node.hidden = false;
    const list = node.querySelector('.first-run-steps');
    list.replaceChildren(...steps.map(([label, done], index) => {
      const item = document.createElement('li');
      item.dataset.done = String(done);
      const mark = document.createElement('b'); mark.textContent = done ? '✓' : String(index + 1);
      const name = document.createElement('span'); name.textContent = label;
      item.append(mark, name);
      return item;
    }));
  }

  function renderAccounts() {
    const body = $('#accounts-table'); body.replaceChildren();
    const quota = quotaIndex();
    if (!state.accounts.length) { const row = document.createElement('tr'); const cell = createCell('', '未发现 Codex 账号', 'empty-row'); cell.colSpan = 12; row.append(cell); body.append(row); renderSummary(); return; }
    for (const account of state.accounts) {
      const model = projectAccountRow(account, { quota: quota[account.account_key], history: state.history, hidden: state.hidden });
      const row = document.createElement('tr'); row.dataset.accountKey = model.key;
      const checkbox = document.createElement('input'); checkbox.type = 'checkbox'; checkbox.checked = state.selected.has(model.key); checkbox.disabled = account.disabled; checkbox.dataset.accountSelection = model.key; checkbox.setAttribute('aria-label', `选择 ${model.email}`);
      checkbox.addEventListener('change', () => { checkbox.checked ? state.selected.add(model.key) : state.selected.delete(model.key); renderSummary(); });
      // Identifiers live in the account cell instead of holding two columns of
      // their own: they are only ever read when cross-referencing during triage.
      const name = document.createElement('div'); name.className = 'account-name'; const strong = document.createElement('strong'); strong.textContent = model.email || account.masked_identity || '***';
      const small = document.createElement('small'); small.textContent = state.hidden ? '身份已遮罩' : model.key;
      const ids = document.createElement('small'); ids.className = 'account-ids mono'; ids.textContent = `${model.authIndex || '--'} · ${model.accountPrefix || '--'}`; ids.title = `AUTH INDEX ${model.authIndex || '--'} · 账号前缀 ${model.accountPrefix || '--'}`;
      name.append(strong, small, ids);
      const scheduled = document.createElement('label'); scheduled.className = 'switch row-switch'; const scheduledInput = document.createElement('input'); scheduledInput.type = 'checkbox'; scheduledInput.checked = state.scheduledKeys.has(model.key); scheduledInput.disabled = account.disabled; scheduledInput.dataset.accountScheduled = model.key; scheduledInput.setAttribute('aria-label', `自动预热 ${model.email}`); const scheduledTrack = document.createElement('span'); const scheduledLabel = document.createElement('em'); scheduledLabel.textContent = scheduledInput.checked ? '已计划' : '仅手动'; scheduledInput.addEventListener('change', () => { scheduledInput.checked ? state.scheduledKeys.add(model.key) : state.scheduledKeys.delete(model.key); scheduledLabel.textContent = scheduledInput.checked ? '已计划' : '仅手动'; queueSimulation(); }); scheduled.append(scheduledInput, scheduledTrack, scheduledLabel);
      const status = document.createElement('span'); status.className = `status-pill status-${model.status}`; status.textContent = statusLabels[model.status] || model.status; status.title = accountStatusDetail(model.status);
      const actions = document.createElement('div'); actions.className = 'row-actions';
      const refresh = document.createElement('button'); refresh.className = 'secondary-button'; refresh.type = 'button'; refresh.textContent = '刷新'; refresh.dataset.rowAction = 'refresh'; refresh.disabled = Boolean(state.quotaBusy); refresh.addEventListener('click', () => refreshQuota([model.key], refresh)); actions.append(refresh);
      const snapshot = quota[model.key]?.snapshot || {}; const windows = snapshot.windows || []; const short = windows.find((item) => item.short) || windows[0]; const long = windows.find((item) => !item.short); const record = state.history.filter((item) => item.account_key === model.key).sort((a, b) => Date.parse(b.finished_at || b.started_at || '') - Date.parse(a.finished_at || a.started_at || ''))[0] || {};
      const windowText = (item) => item ? (() => { const remaining = Math.max(0, Math.min(100, Number(item.remaining_percent ?? 0))); const bar = document.createElement('span'); bar.className = 'quota-inline'; const fill = document.createElement('i'); fill.style.width = `${remaining}%`; bar.append(fill); const label = document.createElement('span'); label.textContent = `${item.remaining_percent ?? '--'}% · ${formatDate(item.reset_at)}`; const wrap = document.createElement('span'); wrap.className = 'quota-inline-wrap'; wrap.append(bar, label); const note = quotaAvailabilityNote(snapshot, item); if (note.text) { const tag = document.createElement('em'); tag.className = `quota-note quota-note-${note.state}`; tag.textContent = note.text; wrap.append(tag); } return wrap; })() : '--';
      // One probe produces one fact. Request outcome, window outcome and the
      // HTTP/latency pair described it across three columns; they are one cell
      // with the detail on hover.
      row.append(createCell('选择', checkbox), createCell('账号', name), createCell('自动预热', scheduled), createCell('套餐类型', model.plan), createCell('状态', status), createCell('短窗口', windowText(short)), createCell('长窗口', windowText(long)), createCell('重置额度', resetCreditCell(snapshot)), createCell('上次检测', lastProbeCell(record)), createCell('额度更新时间', `${formatDate(snapshot.captured_at)}${quota[model.key]?.refresh_error_code ? ' · 刷新失败，保留上次快照' : ''}`), createCell('错误原因', model.error || '--'), createCell('操作', actions));
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
    for (const name of ['timezone', 'window_hours', 'productivity_minutes', 'preheat_lead_minutes', 'preheat_span_minutes', 'long_window_floor_percent', 'probe_model', 'probe_timeout_seconds']) setField(name, schedule[name] ?? '');
    state.scheduledKeys = new Set(schedule.scheduled_account_keys || []);
    const periods = schedule.work_periods || [];
    setField('work_start', periods[0]?.start || '09:00'); setField('lunch_start', periods[0]?.end || '12:00');
    setField('lunch_end', periods[1]?.start || '13:30'); setField('work_end', periods[1]?.end || '19:00');
    renderPeriods('#work-periods', periods); renderPeriods('#blackout-periods', schedule.blackout_periods || []);
    renderWeekdays(schedule.weekdays || [1, 2, 3, 4, 5]); state.skipTimes = [...(schedule.skip_window_times || [])]; renderSkipTimes();
    clearFieldErrors(); text('#schedule-feedback', ''); state.savedDraftSignature = draftSignature(); queueSimulation(); renderAccounts();
  }
  // remaining_quota_floor_percent and remaining_window_floor_minutes are no
  // longer read from the form: no decision uses them. The spread of
  // state.schedule carries the stored values through unchanged.
  function readSchedule() {
    const form = $('#schedule-form'); const value = (name) => form.elements[name]?.value || ''; const integer = (name, fallback = 0) => value(name) === '' ? fallback : Number(value(name)); const optionalInteger = (name) => value(name) === '' ? null : Number(value(name));
    const fixedPeriods = [{ start: value('work_start'), end: value('lunch_start') }, { start: value('lunch_end'), end: value('work_end') }];
    const workPeriods = fixedPeriods.every((period) => period.start && period.end) ? fixedPeriods : readPeriods('#work-periods');
    return { ...state.schedule, enabled: $('#schedule-enabled').checked, timezone: value('timezone'), window_hours: integer('window_hours', 5), productivity_minutes: integer('productivity_minutes', 60), preheat_lead_minutes: optionalInteger('preheat_lead_minutes'), preheat_span_minutes: optionalInteger('preheat_span_minutes'), long_window_floor_percent: integer('long_window_floor_percent', 10), probe_model: value('probe_model'), probe_timeout_seconds: integer('probe_timeout_seconds', 30), weekdays: $$('#weekdays input:checked').map((input) => Number(input.value)), work_periods: workPeriods, blackout_periods: readPeriods('#blackout-periods'), skip_window_times: [...state.skipTimes], scheduled_account_keys: [...state.scheduledKeys] };
  }

  async function runSimulation() {
    const generation = ++state.simulationGeneration;
    if (!validateScheduleForm({ showErrors: false })) { text('#simulation-output', '请补全有效参数后查看模拟结果；错误详情可点击“保存配置”定位。'); return; }
    const config = readSchedule(); try { const result = await request('/simulate', { method: 'POST', body: config }); if (generation !== state.simulationGeneration) return; renderSimulationComparison($('#simulation-output'), result, config); } catch (error) { if (generation === state.simulationGeneration) text('#simulation-output', requestErrorMessage(error)); }
  }
  function queueSimulation() {
    updateDraftStatus();
    clearTimeout(state.simulationTimer); state.simulationTimer = setTimeout(runSimulation, 180);
  }

  function renderHistory() {
    const body = $('#history-output');
    const expanded = new Set([...body.querySelectorAll('details[open]')].map((details) => details.dataset.batchKey));
    body.replaceChildren();
    if (!state.history.length) { const row = document.createElement('tr'); const cell = createCell('', '暂无检测历史', 'empty-row'); cell.colSpan = 8; row.append(cell); body.append(row); return; }
    for (const batch of groupHistoryBatches(state.history)) {
      const { key, records } = batch;
      const summary = document.createElement('tr'); const cell = document.createElement('td'); cell.colSpan = 8;
      const details = document.createElement('details'); details.dataset.batchKey = key; details.open = expanded.has(key);
      const summaryNode = document.createElement('summary');
      const period = batch.period ? ` · 计划时段 ${Number(batch.period.split('/p')[1]) + 1}` : '';
      const skipped = batch.skipped ? ` · ${batch.skipped === records.length ? '全部跳过，未发送请求' : `${batch.skipped} 次跳过`}` : '';
      summaryNode.textContent = `${triggerLabels[batch.trigger] || batch.trigger}${period} · ${batch.accountCount} 个账号 · ${records.length} 次操作${skipped} · ${formatDate(batch.startedAt)} — ${formatDate(batch.finishedAt)}`;
      const list = document.createElement('div'); list.className = 'history-batch-details';
      records.forEach((record) => {
        const item = document.createElement('div'); item.className = 'history-batch-item';
        const detail = describeOperation(record, { probeOutcomes: probeOutcomeLabels, windowOutcomes: windowOutcomeLabels, decisions: decisionLabels });
        const head = [record.masked_identity || record.account_key, formatDate(record.started_at), triggerLabels[record.trigger] || record.trigger];
        // Latency is only meaningful for a request that was actually sent.
        const tail = record.latency_ms ? [formatDuration(record.latency_ms)] : [];
        item.textContent = [...head, ...detail, ...tail].join(' · ');
        list.append(item);
      });
      details.append(summaryNode, list); cell.append(details); summary.append(cell); body.append(summary);
    }
  }

  async function loadPanel() {
    if (state.panelLoading || state.quotaBusy) return false;
    state.panelLoading = true;
    const quotaGeneration = state.quotaGeneration || 0;
    const accountsGeneration = state.accountsGeneration || 0;
    try {
      $('#connection').dataset.state = 'loading'; text('#connection', '正在加载数据…');
      const statusJob = request('/status').then((status) => { state.status = status; renderSummary(); });
      const upcomingJob = request('/upcoming').then((upcoming) => { state.upcoming = upcoming; renderSummary(); });
      const accountsJob = hostManagementRequest('/auth-files').then((files) => {
        if ((state.accountsGeneration || 0) !== accountsGeneration) return;
        state.accounts = normalizeHostAuthFiles(files);
        state.selected = new Set([...state.selected].filter((key) => state.accounts.some((account) => account.account_key === key && !account.disabled)));
        renderAccounts();
      });
      const scheduleJob = request('/schedule').then((schedule) => { if (!isDraftDirty()) { applySchedule(schedule || {}); queueSimulation(); } return schedule; });
      const quotaJob = request('/quota-snapshot').then((quota) => {
        if ((state.quotaGeneration || 0) === quotaGeneration) { state.quota = quota; renderAccounts(); }
      });
      const historyJob = request('/history').then((history) => { state.history = history; renderHistory(); renderAccounts(); });
      const jobs = await Promise.allSettled([statusJob, upcomingJob, accountsJob, scheduleJob, quotaJob, historyJob]);
      updateDraftStatus();
      const failed = jobs.find((job) => job.status === 'rejected');
      $('#connection').dataset.state = failed ? 'error' : 'ready';
      text('#connection', failed ? `部分数据加载失败，已保留原数据。${requestErrorMessage(failed.reason)}` : '');
      return !failed;
    } finally { state.panelLoading = false; }
  }

  async function withQuotaRefresh(button, action) {
    if (state.quotaBusy) return;
    state.quotaBusy = true;
    state.quotaGeneration = (state.quotaGeneration || 0) + 1;
    // Row buttons are destroyed and rebuilt by renderAccounts(), which derives
    // their disabled state from state.quotaBusy; restoring them here would write
    // to detached nodes. Only the toolbar controls survive the action.
    const controls = $$('[data-action="refresh-quota"], [data-action="refresh-all-quota"], [data-action="refresh-panel"]');
    const previous = controls.map((control) => [control, control.disabled]);
    [...controls, ...$$('[data-row-action="refresh"]')].forEach((control) => { control.disabled = true; });
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
    const accounts = keys.map((key) => state.accounts.find((account) => account.account_key === key)).filter(Boolean);
    text('#account-feedback', keys.length ? `正在刷新 ${accounts.length} 个账号；结果返回后写入插件缓存。` : '正在刷新全部账号；结果返回后写入插件缓存。');
    // The plugin now returns every account's result in one response, so
    // onUpdate fires synchronously for the whole batch rather than trickling
    // in over time. Merge into state without re-rendering the account table
    // on every iteration; withQuotaRefresh renders once after this resolves.
    const merged = quotaIndex();
    const unknownKeys = [];
    return refreshAccountQuotas(accounts, {
      onUpdate: ({ accountKey, view }) => {
        merged[accountKey] = view;
        if (!state.accounts.some((account) => account.account_key === accountKey)) unknownKeys.push(accountKey);
      },
      onProgress: ({ succeeded, failed, total }) => text('#account-feedback', `额度刷新 ${succeeded + failed}/${total} · 成功 ${succeeded}，失败 ${failed}`),
    }).then((outcome) => { state.quota = Object.values(merged); return { ...outcome, unknownKeys }; });
  }

  function reportQuotaRefresh(outcome, skipped = 0) {
    const message = `额度刷新：成功 ${outcome.succeeded}，失败 ${outcome.failed}，跳过 ${skipped}。`;
    if (outcome.failed) text('#account-feedback', `${message}失败账号保留旧快照并标记过期，请检查错误原因后重试。`);
    else { text('#account-feedback', ''); notify(message); }
  }

  async function refreshQuota(keys, button) {
    if (!keys.length) { text('#account-feedback', '请先选择账号，或使用“刷新全部额度”。'); return; }
    return withQuotaRefresh(button, async () => {
      const accounts = keys.filter((key) => state.accounts.some((account) => account.account_key === key));
      if (!accounts.length) { text('#account-feedback', '未找到可刷新的账号。'); return; }
      const outcome = await refreshQuotaSnapshots(accounts);
      reportQuotaRefresh(outcome, 0);
      return outcome;
    });
  }

  async function refreshAllQuota(button) {
    return withQuotaRefresh(button, async () => {
      // The plugin discovers accounts server-side for every batch, so a
      // refresh-all sends no keys instead of fetching the credential list here
      // and echoing it back. That second round trip duplicated work the server
      // already does and could stall the whole action on its own.
      const outcome = await refreshQuotaSnapshots([]);
      if (!outcome.succeeded && !outcome.failed) { text('#account-feedback', '未发现可刷新的账号。'); return outcome; }
      // Only a credential added or re-enabled since the panel loaded produces a
      // snapshot without a row; fetch identities just for that rare case.
      if (outcome.unknownKeys?.length) await reloadAccountRows();
      reportQuotaRefresh(outcome, 0);
      return outcome;
    });
  }

  async function reloadAccountRows() {
    state.accountsGeneration = (state.accountsGeneration || 0) + 1;
    const generation = state.accountsGeneration;
    try {
      const files = normalizeHostAuthFiles(await hostManagementRequest('/auth-files'));
      if (state.accountsGeneration !== generation) return;
      const previous = new Set(state.selected);
      state.accounts = files;
      state.selected = new Set([...previous].filter((key) => files.some((account) => account.account_key === key && !account.disabled)));
      renderAccounts();
    } catch {
      // Identity metadata is presentation only. Keep the rows already shown
      // rather than failing a refresh whose quota results already arrived.
    }
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
  $('#upcoming-horizon').addEventListener('change', (event) => { state.fullHorizon = event.target.checked; renderUpcoming(); });
  $('[data-action="dismiss-first-run"]').addEventListener('click', () => { state.firstRunDismissed = true; renderFirstRun(); });
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
  $('[data-action="clear-data"]').addEventListener('click', () => $('#clear-dialog').showModal());
  $$('[data-action="close-clear"]').forEach((button) => button.addEventListener('click', () => $('#clear-dialog').close()));
  $('#clear-form').addEventListener('submit', async (event) => { event.preventDefault(); try { await request('/history', { method: 'DELETE' }); state.history = []; renderAccounts(); renderHistory(); $('#clear-dialog').close(); notify('检测历史已删除'); } catch (error) { notify(requestErrorMessage(error)); } });
  $$('[data-action="close-probe"]').forEach((button) => button.addEventListener('click', () => $('#probe-dialog').close())); $('#probe-form').addEventListener('submit', (event) => { event.preventDefault(); submitProbe($('#probe-confirm')); });
  loadPanel().catch((error) => { $('#connection').dataset.state = 'error'; text('#connection', requestErrorMessage(error)); });
}

if (typeof document !== 'undefined') {
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', bootPanel, { once: true }); else bootPanel();
}

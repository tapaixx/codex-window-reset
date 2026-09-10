import { request, requestErrorMessage, setRequestDispatcher } from './api.js';
import { createStore } from './state.js';
import { renderAccounts } from './accounts.js';
import { readScheduleDraft, renderSchedule, validateScheduleDraft } from './schedule.js';
import { renderSimulation } from './simulator.js';
import { renderHistory, renderResetAudit } from './history.js';

export function syncHostTheme({ root = globalThis.document?.documentElement, parentRoot, parentDocument, windowRef = globalThis.window, observe = true } = {}) {
  if (!root) return () => {};
  let sourceRoot = parentRoot;
  if (!sourceRoot) {
    try {
      const parent = parentDocument || windowRef?.parent?.document;
      sourceRoot = parent?.documentElement;
    } catch {
      sourceRoot = null;
    }
  }

  const parentTheme = () => {
    if (!sourceRoot) return null;
    const attribute = sourceRoot.getAttribute?.('data-theme') || sourceRoot.dataset?.theme;
    if (attribute === 'dark' || attribute === 'light') return attribute;
    if (sourceRoot.classList?.contains?.('dark')) return 'dark';
    return null;
  };
  const media = windowRef?.matchMedia?.('(prefers-color-scheme: dark)');
  const apply = () => {
    const theme = parentTheme() || (media?.matches ? 'dark' : 'light');
    root.setAttribute?.('data-theme', theme);
  };
  apply();

  let observer;
  let mediaListener;
  const Observer = windowRef?.MutationObserver || globalThis.MutationObserver;
  if (observe && sourceRoot && Observer) {
    observer = new Observer(apply);
    observer.observe(sourceRoot, { attributes: true, attributeFilter: ['data-theme', 'class'] });
  }
  if (observe && !parentTheme() && media) {
    mediaListener = apply;
    if (media.addEventListener) media.addEventListener('change', mediaListener);
    else media.addListener?.(mediaListener);
  }
  return () => {
    observer?.disconnect();
    if (mediaListener) {
      if (media.removeEventListener) media.removeEventListener('change', mediaListener);
      else media.removeListener?.(mediaListener);
    }
  };
}

function node(id) {
  return document.getElementById(id);
}

function formatDate(value) {
  if (!value) return 'None';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? 'None' : date.toLocaleString(undefined, { dateStyle: 'short', timeStyle: 'short' });
}

function earliestNextRun(nextRuns = {}) {
  const values = Object.values(nextRuns).filter(Boolean).sort((left, right) => new Date(left) - new Date(right));
  return values[0] || null;
}

function setText(element, value) {
  if (element) element.textContent = String(value ?? '');
}

function showAreaError(id, message = '') {
  const element = node(id);
  if (element) element.textContent = message;
}

function scheduleErrorMessage(errors) {
  const messages = {
    timezone: '请填写有效时区。',
    probe_model: '请填写探测模型。',
    preheat_lead_minutes: '启用调度前必须填写预热提前分钟数。',
    preheat_span_minutes: '启用调度前必须填写预热持续分钟数。',
    work_periods: '启用调度前至少需要一个工作时段。',
    scheduled_account_keys: '启用调度前至少需要一个已安排账户。',
  };
  return errors.map((error) => messages[error.field] || '请检查调度配置。').join(' ');
}

function selectedKeys(state) {
  return [...(state.selectedAccountKeys || [])];
}

function setButtonBusy(button, busy, busyLabel = 'Working...') {
  if (!button) return;
  if (busy) {
    button.dataset.idleLabel = button.textContent;
    button.textContent = busyLabel;
    button.disabled = true;
    button.setAttribute('aria-busy', 'true');
  } else {
    button.textContent = button.dataset.idleLabel || button.textContent;
    button.disabled = false;
    button.removeAttribute('aria-busy');
  }
}

async function withButton(button, busyLabel, operation) {
  setButtonBusy(button, true, busyLabel);
  try {
    return await operation();
  } finally {
    setButtonBusy(button, false);
  }
}

function bootPanel() {
  const store = createStore({ loading: true, accounts: [], history: [], resetAudit: [], schedule: {} });
  const elements = {
    connection: node('connection'),
    summaryCards: {
      schedule: node('summary-schedule'), accounts: node('summary-accounts'), quota: node('summary-quota'),
      run: node('summary-run'), next: node('summary-next'),
    },
    accounts: node('accounts-table'),
    selectionCount: node('selection-count'),
    schedule: node('schedule-form'),
    scheduleRevision: node('schedule-revision'),
    simulation: node('simulation-output'),
    history: node('history-output'),
    audit: node('audit-output'),
    feedback: node('feedback'),
    hostLogin: node('host-login'),
    runStatus: node('run-status'),
    resetDialog: node('reset-dialog'),
    resetForm: node('reset-form'),
    resetAccount: node('reset-account'),
    resetCredits: node('reset-credits'),
    resetStatus: node('reset-status'),
    resetFeedback: node('reset-feedback'),
    resetConfirm: node('reset-confirm'),
    auditDialog: node('audit-dialog'),
    auditForm: node('audit-form'),
    auditConfirmation: node('audit-confirmation'),
    auditFeedback: node('audit-feedback'),
  };
  const tabs = [...document.querySelectorAll('[data-workspace]')];
  const panels = [...document.querySelectorAll('[data-panel]')];
  let lastRenderedSchedule = false;
  let lastRenderedSimulation = false;
  let lastRenderedHistory = false;
  let lastRenderedAudit = false;

  function renderCard(card, value, detail) {
    if (!card) return;
    setText(card.querySelector('.summary-value'), value);
    setText(card.querySelector('.summary-detail'), detail);
  }

  function renderWorkspaceTabs(state) {
    const active = state.activeWorkspace || 'schedule';
    tabs.forEach((tab) => {
      const selected = tab.dataset.workspace === active;
      tab.setAttribute('aria-selected', String(selected));
      tab.tabIndex = selected ? 0 : -1;
      tab.classList.toggle('is-active', selected);
    });
    panels.forEach((panel) => { panel.hidden = panel.dataset.panel !== active; });
  }

  function render(state, action = { type: 'initial' }) {
    const status = state.status || {};
    const run = state.currentRun || status;
    const freshCount = state.quota.filter((item) => !item.stale).length;
    const next = earliestNextRun(status.next_runs);
    renderCard(elements.summaryCards.schedule, status.enabled ? 'Enabled' : 'Paused', status.store_error_code ? 'Store error' : 'Automatic preheat');
    renderCard(elements.summaryCards.accounts, state.accounts.length, 'Discovered');
    renderCard(elements.summaryCards.quota, `${freshCount}/${state.quota.length}`, 'Fresh snapshots');
    renderCard(elements.summaryCards.run, run.run_id ? `${run.run_completed || 0}/${run.run_total || 0}` : 'Idle', run.run_id ? `Run ${run.run_id}` : 'No active operation');
    renderCard(elements.summaryCards.next, formatDate(next), next ? 'Next planned occurrence' : 'Not scheduled');
    setText(elements.scheduleRevision, state.serverSchedule?.revision ?? '-');
    setText(elements.selectionCount, `${state.selectedAccountKeys.length} selected`);
    setText(elements.runStatus, run.run_id ? `Run ${run.run_id}: ${run.run_completed || 0} of ${run.run_total || 0} complete.` : 'No operation is running.');
    setText(elements.feedback, state.feedback || '');
    if (elements.connection) {
      if (state.authRequired) {
        elements.connection.dataset.state = 'error';
        elements.connection.textContent = 'Host login required';
      } else if (state.loading) {
        elements.connection.dataset.state = 'loading';
        elements.connection.textContent = 'Connecting...';
      } else {
        elements.connection.dataset.state = 'ready';
        elements.connection.textContent = 'Connected';
      }
    }
    if (elements.hostLogin) elements.hostLogin.hidden = !state.authRequired;
    const resetButton = document.querySelector('[data-action="open-reset"]');
    const selected = selectedKeys(state);
    const selectedQuota = selected.length === 1 ? state.quotaByAccount[selected[0]] : null;
    if (resetButton) resetButton.disabled = selected.length !== 1 || !selectedQuota || Boolean(selectedQuota.stale) || !selectedQuota.snapshot?.reset_info_complete;

    const renderAccountActions = new Set(['initial', 'accounts-loaded', 'schedule-loaded', 'schedule-saved', 'restore-schedule', 'quota-refreshed', 'identity-revealed', 'history-loaded']);
    if (renderAccountActions.has(action.type)) {
      renderAccounts(elements.accounts, state.accounts, {
        scheduledAccountKeys: state.draftSchedule?.scheduled_account_keys,
        selectedAccountKeys: state.selectedAccountKeys,
        identityRevealKeys: state.identityRevealKeys,
        quotaByAccount: state.quotaByAccount,
        history: state.history,
        nextRuns: status.next_runs,
        onSelectionChange: (accountKey, selected) => store.dispatch({ type: 'toggle-action-selection', accountKey, selected }),
        onScheduleChange: (accountKey, scheduled) => store.dispatch({ type: 'set-scheduled-membership', accountKey, scheduled }),
        onReveal: (accountKey) => store.dispatch({ type: 'identity-revealed', accountKey, revealed: !state.identityRevealKeys.has(accountKey) }),
      });
    }
    const renderScheduleActions = new Set(['initial', 'schedule-loaded', 'schedule-saved', 'restore-schedule']);
    if (!lastRenderedSchedule || renderScheduleActions.has(action.type)) {
      renderSchedule(elements.schedule, state.draftSchedule, (name, value) => store.dispatch({ type: 'edit-schedule', patch: { [name]: value } }));
      lastRenderedSchedule = true;
    }
    if (elements.simulation && (!lastRenderedSimulation || action.type === 'simulation-loaded' || action.type === 'quota-refreshed')) {
      renderSimulation(elements.simulation, state.simulation, state.quota);
      lastRenderedSimulation = true;
    }
    if (elements.history && (!lastRenderedHistory || action.type === 'history-loaded')) {
      renderHistory(elements.history, state.history);
      lastRenderedHistory = true;
    }
    if (elements.audit && (!lastRenderedAudit || action.type === 'audit-loaded' || action.type === 'audit-cleared')) {
      renderResetAudit(elements.audit, state.resetAudit);
      lastRenderedAudit = true;
    }
    renderWorkspaceTabs(state);
    renderResetDialog(state);
  }

  function dispatchFeedback(message) {
    store.dispatch({ type: 'feedback', value: message });
  }

  function dispatchError(area, error) {
    const message = requestErrorMessage(error);
    store.dispatch({ type: 'error', error: error?.code || 'request_failed' });
    showAreaError(area, message);
  }

  function currentDraft() {
    const stateDraft = store.getState().draftSchedule || {};
    const draft = readScheduleDraft(elements.schedule, stateDraft);
    if (Array.isArray(stateDraft.scheduled_account_keys)) draft.scheduled_account_keys = [...stateDraft.scheduled_account_keys];
    return draft;
  }

  async function loadPanel(button) {
    await withButton(button, 'Loading...', async () => {
      try {
        const [status, accounts, schedule] = await Promise.all([request('/status'), request('/accounts'), request('/schedule')]);
        store.dispatch({ type: 'status-loaded', value: status });
        store.dispatch({ type: 'accounts-loaded', value: accounts });
        store.dispatch({ type: 'schedule-loaded', value: schedule });
        dispatchFeedback('Status and schedule refreshed.');
      } catch (error) {
        dispatchError('feedback', error);
      }
    });
  }

  async function refreshQuota(button) {
    const keys = selectedKeys(store.getState());
    if (!keys.length) {
      showAreaError('account-feedback', '请先选择至少一个账户。');
      return;
    }
    await withButton(button, 'Refreshing...', async () => {
      store.dispatch({ type: 'quota-refresh-started', keys });
      try {
        const value = await request('/quota/refresh', { method: 'POST', body: { account_keys: keys } });
        store.dispatch({ type: 'quota-refreshed', value });
        showAreaError('account-feedback', '');
        dispatchFeedback('Quota snapshots refreshed.');
      } catch (error) {
        dispatchError('account-feedback', error);
        store.dispatch({ type: 'quota-refreshed', value: [] });
      }
    });
  }

  async function runProbe(button) {
    const keys = selectedKeys(store.getState());
    if (!keys.length) {
      showAreaError('account-feedback', '请先选择至少一个账户。');
      return;
    }
    if (!node('probe-acknowledgement')?.checked) {
      showAreaError('account-feedback', '请先确认真实 Codex 请求可能消耗普通配额或开始 Short Window。');
      return;
    }
    await withButton(button, 'Starting...', async () => {
      try {
        const value = await request('/probes', { method: 'POST', body: { account_keys: keys, acknowledge_quota_effect: true, allow_unavailable: false } });
        store.dispatch({ type: 'run-started', value });
        dispatchFeedback(`Health probe run ${value?.run_id || 'started'} accepted.`);
        const status = await request('/status');
        store.dispatch({ type: 'status-loaded', value: status });
      } catch (error) {
        dispatchError('account-feedback', error);
      }
    });
  }

  async function saveSchedule(button) {
    const draft = currentDraft();
    const errors = validateScheduleDraft(draft);
    if (errors.length) {
      showAreaError('schedule-feedback', scheduleErrorMessage(errors));
      return;
    }
    await withButton(button, 'Saving...', async () => {
      store.dispatch({ type: 'schedule-save-started' });
      try {
        const value = await request('/schedule', { method: 'PUT', body: draft });
        store.dispatch({ type: 'schedule-saved', value });
        showAreaError('schedule-feedback', '');
        dispatchFeedback('Schedule saved.');
      } catch (error) {
        dispatchError('schedule-feedback', error);
      }
    });
  }

  async function simulate(button) {
    const draft = currentDraft();
    const errors = validateScheduleDraft(draft);
    if (errors.length && draft.enabled) {
      showAreaError('simulation-feedback', scheduleErrorMessage(errors));
      return;
    }
    await withButton(button, 'Simulating...', async () => {
      try {
        const value = await request('/simulate', { method: 'POST', body: draft });
        store.dispatch({ type: 'simulation-loaded', value });
        store.dispatch({ type: 'workspace-selected', value: 'simulator' });
        showAreaError('simulation-feedback', '');
        dispatchFeedback('Simulation complete.');
      } catch (error) {
        dispatchError('simulation-feedback', error);
      }
    });
  }

  async function loadRecords(button) {
    await withButton(button, 'Loading...', async () => {
      try {
        const [history, audit] = await Promise.all([request('/history'), request('/reset-audit')]);
        store.dispatch({ type: 'history-loaded', value: history });
        store.dispatch({ type: 'audit-loaded', value: audit });
        showAreaError('records-feedback', '');
        dispatchFeedback('History and reset audit refreshed.');
      } catch (error) {
        dispatchError('records-feedback', error);
      }
    });
  }

  async function clearHistory(button) {
    if (!globalThis.confirm?.('Clear the latest 100 operation records?')) return;
    await withButton(button, 'Deleting...', async () => {
      try {
        await request('/history', { method: 'DELETE' });
        store.dispatch({ type: 'history-loaded', value: [] });
        dispatchFeedback('Operation history cleared.');
      } catch (error) {
        dispatchError('records-feedback', error);
      }
    });
  }

  function renderResetDialog(state) {
    const intent = state.resetIntent;
    if (!intent) return;
    const account = state.accounts.find((item) => item.account_key === intent.accountKey);
    const quota = state.quotaByAccount[intent.accountKey];
    setText(elements.resetAccount, account?.masked_identity || intent.accountKey);
    setText(elements.resetCredits, quota?.snapshot?.reset_applicable_count ?? 'Unknown');
    const status = intent.status === 'submitting' ? 'Submitting reset...' : intent.status === 'completed' ? 'Reset completed.' : intent.status === 'failed' ? 'Reset failed. The same request can be retried.' : '';
    setText(elements.resetStatus, status);
    showAreaError('reset-feedback', intent.error ? requestErrorMessage({ code: intent.error }) : '');
    if (elements.resetConfirm) {
      elements.resetConfirm.disabled = intent.status === 'submitting' || intent.status === 'completed';
      elements.resetConfirm.textContent = intent.status === 'failed' ? 'Retry reset' : 'Confirm reset';
    }
  }

  function openReset() {
    const keys = selectedKeys(store.getState());
    if (keys.length !== 1) {
      showAreaError('account-feedback', '重置操作必须且只能选择一个账户。');
      return;
    }
    const quota = store.getState().quotaByAccount[keys[0]];
    if (!quota || quota.stale || !quota.snapshot?.reset_info_complete) {
      showAreaError('account-feedback', '请先对该账户执行一次成功的当前配额刷新。');
      return;
    }
    store.dispatch({ type: 'open-reset-intent', accountKey: keys[0] });
    elements.resetDialog?.showModal?.();
  }

  async function submitReset() {
    const intent = store.getState().resetIntent;
    if (!intent || intent.status === 'submitting' || intent.status === 'completed') return;
    store.dispatch({ type: 'reset-submitted' });
    try {
      const result = await request('/quota/reset', { method: 'POST', body: { account_key: intent.accountKey, idempotency_key: intent.idempotencyKey } });
      store.dispatch({ type: 'reset-finished', value: result });
      dispatchFeedback('Quota reset completed.');
      try {
        const refreshed = await request('/quota/refresh', { method: 'POST', body: { account_keys: [intent.accountKey] } });
        store.dispatch({ type: 'quota-refreshed', value: refreshed });
      } catch (error) {
        dispatchFeedback(`Reset completed, but the follow-up quota refresh failed: ${requestErrorMessage(error)}`);
      }
    } catch (error) {
      store.dispatch({ type: 'reset-failed', error: error?.code || 'reset_outcome_unknown' });
    }
  }

  async function clearAudit() {
    if (elements.auditConfirmation.value !== 'DELETE AUDIT') {
      setText(elements.auditFeedback, '请输入 DELETE AUDIT 以确认。');
      return;
    }
    const button = elements.auditForm.querySelector('button[type="submit"]');
    await withButton(button, 'Deleting...', async () => {
      try {
        await request('/reset-audit', { method: 'DELETE', headers: { 'X-Confirmation': 'DELETE AUDIT' } });
        store.dispatch({ type: 'audit-cleared' });
        elements.auditDialog.close();
        dispatchFeedback('Reset audit cleared.');
      } catch (error) {
        setText(elements.auditFeedback, requestErrorMessage(error));
      }
    });
  }

  function activateWorkspace(value) {
    store.dispatch({ type: 'workspace-selected', value });
  }

  document.querySelectorAll('[data-action]').forEach((button) => {
    button.addEventListener('click', () => {
      const action = button.dataset.action;
      if (action === 'refresh-panel') void loadPanel(button);
      if (action === 'refresh-quota') void refreshQuota(button);
      if (action === 'run-probe') void runProbe(button);
      if (action === 'save-schedule') void saveSchedule(button);
      if (action === 'restore-schedule') store.dispatch({ type: 'restore-schedule' });
      if (action === 'simulate') void simulate(button);
      if (action === 'load-records') void loadRecords(button);
      if (action === 'clear-history') void clearHistory(button);
      if (action === 'open-reset') openReset();
      if (action === 'close-reset') { store.dispatch({ type: 'reset-intent-cleared' }); elements.resetDialog.close(); }
      if (action === 'open-audit-clear') { elements.auditConfirmation.value = ''; setText(elements.auditFeedback, ''); elements.auditDialog.showModal(); }
      if (action === 'close-audit') elements.auditDialog.close();
    });
  });

  tabs.forEach((tab, index) => {
    tab.addEventListener('click', () => activateWorkspace(tab.dataset.workspace));
    tab.addEventListener('keydown', (event) => {
      if (!['ArrowRight', 'ArrowLeft', 'Home', 'End'].includes(event.key)) return;
      event.preventDefault();
      const next = event.key === 'Home' ? 0 : event.key === 'End' ? tabs.length - 1 : (index + (event.key === 'ArrowRight' ? 1 : -1) + tabs.length) % tabs.length;
      tabs[next].focus();
      activateWorkspace(tabs[next].dataset.workspace);
    });
  });

  elements.resetForm.addEventListener('submit', (event) => { event.preventDefault(); void submitReset(); });
  elements.auditForm.addEventListener('submit', (event) => { event.preventDefault(); void clearAudit(); });
  elements.resetDialog.addEventListener('cancel', () => store.dispatch({ type: 'reset-intent-cleared' }));
  elements.auditDialog.addEventListener('cancel', () => { setText(elements.auditFeedback, ''); });

  store.subscribe(render);
  setRequestDispatcher((action) => store.dispatch(action));
  syncHostTheme();
  render(store.getState());
  void loadPanel();
}

if (typeof document !== 'undefined' && document.getElementById('main-content')) bootPanel();

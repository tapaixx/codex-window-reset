function clone(value) {
  if (value === undefined) return undefined;
  if (typeof structuredClone === 'function') return structuredClone(value);
  return JSON.parse(JSON.stringify(value));
}

function uniqueKeys(keys) {
  return [...new Set((Array.isArray(keys) ? keys : []).map((key) => String(key).trim()).filter(Boolean))];
}

function quotaIndex(value = []) {
  return Object.fromEntries(value.map((item) => [item?.snapshot?.account_key || item?.account_key, item]).filter(([key]) => key));
}

export function createResetIntent(accountKey, currentIntent = null, suppliedKey = '') {
  const key = String(accountKey || '').trim();
  if (!key) return null;
  if (currentIntent?.accountKey === key && currentIntent.idempotencyKey && currentIntent.status !== 'completed') {
    return currentIntent;
  }
  const idempotencyKey = suppliedKey || globalThis.crypto.randomUUID();
  return { accountKey: key, idempotencyKey, status: 'ready', error: null, result: null };
}

export function createStore(initialState = {}) {
  const serverSchedule = clone(initialState.serverSchedule ?? initialState.schedule ?? {});
  const draftSchedule = clone(initialState.draftSchedule ?? initialState.draft ?? serverSchedule);
  let state = {
    loading: true,
    status: {},
    accounts: [],
    serverSchedule,
    schedule: clone(serverSchedule),
    draftSchedule,
    draft: clone(draftSchedule),
    selectedAccountKeys: uniqueKeys(initialState.selectedAccountKeys),
    identityRevealKeys: new Set(initialState.identityRevealKeys || []),
    quota: [],
    quotaByAccount: {},
    quotaRefresh: { loading: false, keys: [] },
    history: [],
    resetAudit: [],
    currentRun: null,
    lastError: null,
    feedback: '',
    authRequired: false,
    resetIntent: null,
    simulation: null,
    activeWorkspace: 'schedule',
    ...clone(initialState),
  };
  state.serverSchedule = clone(initialState.serverSchedule ?? initialState.schedule ?? state.serverSchedule);
  state.schedule = clone(state.serverSchedule);
  state.draftSchedule = clone(initialState.draftSchedule ?? initialState.draft ?? state.serverSchedule);
  state.draft = clone(state.draftSchedule);
  state.selectedAccountKeys = uniqueKeys(initialState.selectedAccountKeys ?? state.selectedAccountKeys);
  state.identityRevealKeys = new Set(initialState.identityRevealKeys || []);
  state.quotaByAccount = quotaIndex(state.quota);
  const listeners = new Set();

  return {
    getState: () => state,
    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    dispatch(action) {
      state = reduceState(state, action);
      listeners.forEach((listener) => listener(state, action));
      return state;
    },
  };
}

export function reduceState(state, action) {
  switch (action.type) {
    case 'status-loaded': {
      const value = clone(action.value || {});
      const currentRun = state.currentRun ? { ...state.currentRun, ...value } : state.currentRun;
      return { ...state, status: value, currentRun, loading: false };
    }
    case 'accounts-loaded':
      return { ...state, accounts: clone(action.value || []), loading: false };
    case 'schedule-loaded': {
      const value = clone(action.value || {});
      return { ...state, serverSchedule: value, schedule: clone(value), draftSchedule: clone(value), draft: clone(value), scheduleSaving: false, loading: false };
    }
    case 'schedule-save-started':
      return { ...state, scheduleSaving: true, lastError: null };
    case 'schedule-saved': {
      const value = clone(action.value || {});
      return { ...state, serverSchedule: value, schedule: clone(value), draftSchedule: clone(value), draft: clone(value), scheduleSaving: false, lastError: null };
    }
    case 'edit-schedule': {
      const draft = { ...clone(state.draftSchedule || {}), ...clone(action.patch || {}) };
      return { ...state, draftSchedule: draft, draft: clone(draft), lastError: null };
    }
    case 'restore-schedule': {
      const draft = clone(state.serverSchedule || state.schedule || {});
      return { ...state, draftSchedule: draft, draft: clone(draft), scheduleSaving: false, lastError: null };
    }
    case 'set-action-selection': {
      const selectedAccountKeys = uniqueKeys(action.keys);
      return { ...state, selectedAccountKeys, actionSelection: selectedAccountKeys };
    }
    case 'toggle-action-selection': {
      const key = String(action.accountKey || '').trim();
      const selected = new Set(state.selectedAccountKeys || []);
      if (action.selected) selected.add(key);
      else selected.delete(key);
      const selectedAccountKeys = [...selected].filter(Boolean);
      return { ...state, selectedAccountKeys, actionSelection: selectedAccountKeys };
    }
    case 'set-scheduled-membership': {
      const existing = new Set(state.draftSchedule?.scheduled_account_keys || []);
      if (action.scheduled) existing.add(action.accountKey);
      else existing.delete(action.accountKey);
      const draft = { ...clone(state.draftSchedule || {}), scheduled_account_keys: [...existing].filter(Boolean) };
      return { ...state, draftSchedule: draft, draft: clone(draft) };
    }
    case 'quota-refresh-started':
      return { ...state, quotaRefresh: { loading: true, keys: uniqueKeys(action.keys) }, lastError: null };
    case 'quota-refreshed': {
      const value = clone(action.value || []);
      const merged = { ...state.quotaByAccount, ...quotaIndex(value) };
      return { ...state, quota: Object.values(merged), quotaByAccount: merged, quotaRefresh: { loading: false, keys: [] }, lastError: null };
    }
    case 'quota-refresh-failed': {
      const errorCode = String(action.error || 'quota_refresh_failed');
      const failedKeys = uniqueKeys(action.keys);
      const marked = { ...state.quotaByAccount };
      for (const key of failedKeys) {
        if (marked[key]) marked[key] = { ...marked[key], refresh_error_code: errorCode };
      }
      return {
        ...state,
        quota: Object.values(marked),
        quotaByAccount: marked,
        quotaRefresh: { loading: false, keys: [] },
        lastError: errorCode,
      };
    }
    case 'run-started':
      return { ...state, currentRun: { ...(action.value || {}), loading: true }, lastError: null };
    case 'run-updated':
      return { ...state, currentRun: { ...(state.currentRun || {}), ...(action.value || {}) } };
    case 'run-finished':
      return { ...state, currentRun: { ...(state.currentRun || {}), ...(action.value || {}), loading: false } };
    case 'history-loaded':
      return { ...state, history: clone(action.value || []).slice(0, 100), lastError: null };
    case 'audit-loaded':
      return { ...state, resetAudit: clone(action.value || []), lastError: null };
    case 'audit-cleared':
      return { ...state, resetAudit: [], lastError: null };
    case 'simulation-loaded':
      return { ...state, simulation: clone(action.value || {}), lastError: null };
    case 'identity-revealed': {
      const identityRevealKeys = new Set(state.identityRevealKeys || []);
      if (action.revealed === false) identityRevealKeys.delete(action.accountKey);
      else identityRevealKeys.add(action.accountKey);
      return { ...state, identityRevealKeys };
    }
    case 'open-reset-intent': {
      const resetIntent = createResetIntent(action.accountKey, state.resetIntent, action.idempotencyKey);
      return { ...state, resetIntent, lastError: null };
    }
    case 'reset-retry':
      return state.resetIntent ? { ...state, resetIntent: { ...state.resetIntent, status: 'ready', error: null } } : state;
    case 'reset-submitted':
      return state.resetIntent ? { ...state, resetIntent: { ...state.resetIntent, status: 'submitting', error: null } } : state;
    case 'reset-finished':
      return state.resetIntent ? { ...state, resetIntent: { ...state.resetIntent, status: 'completed', result: clone(action.value), error: null }, lastError: null } : state;
    case 'reset-failed':
      return state.resetIntent ? { ...state, resetIntent: { ...state.resetIntent, status: 'failed', error: action.error || 'reset_failed' }, lastError: action.error || 'reset_failed' } : state;
    case 'reset-intent-cleared':
      return { ...state, resetIntent: null };
    case 'host-auth-required':
      return { ...state, authRequired: true, lastError: 'host-auth-required' };
    case 'feedback':
      return { ...state, feedback: String(action.value || ''), lastError: null };
    case 'error':
      return { ...state, lastError: action.error || 'request_failed', feedback: '' };
    case 'workspace-selected':
      return { ...state, activeWorkspace: action.value };
    default:
      return state;
  }
}

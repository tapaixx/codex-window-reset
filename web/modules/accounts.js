const OUTCOME_LABELS = {
  succeeded: 'Succeeded',
  unauthorized: 'Unauthorized',
  forbidden: 'Forbidden',
  payment_required: 'Payment required',
  rate_limited: 'Rate limited',
  upstream_error: 'Upstream error',
  network_error: 'Network error',
  timeout: 'Timeout',
  response_error: 'Response error',
  unexpected_output: 'Unexpected output',
  credential_error: 'Credential error',
  disabled: 'Disabled',
  verified_started: 'Verified started',
  already_active: 'Already active',
  unchanged: 'Unchanged',
  unverified: 'Unverified',
  not_observed: 'Not observed',
};

function node(tag, text = '', className = '') {
  const element = document.createElement(tag);
  if (className) element.className = className;
  if (text !== '') element.textContent = text;
  return element;
}

function actionButton(label, key, action, pressed = false) {
  const button = node('button', label, 'text-button');
  button.type = 'button';
  button.dataset.accountAction = action;
  button.dataset.accountKey = key;
  if (pressed) button.setAttribute('aria-pressed', 'true');
  button.setAttribute('aria-label', `${label}: ${key}`);
  return button;
}

export function bindAccountRevealControl(button, accountKey, onReveal) {
  button.addEventListener('click', () => onReveal?.(accountKey));
  return button;
}

function maskIdentity(value, fallback) {
  const text = String(value || fallback || 'Account');
  if (text.includes('*')) return text;
  const at = text.indexOf('@');
  if (at > 0) return `${text[0]}***${text.slice(at)}`;
  return `${text.slice(0, 1)}***`;
}

export function safeAccountIdentityProjection(account = {}, revealed = false) {
  const identity = maskIdentity(account.masked_identity, account.account_key);
  return {
    identity,
    accountKey: revealed ? String(account.account_key || '') : '',
    fingerprint: revealed ? String(account.fingerprint || '') : '',
  };
}

function dateLabel(value) {
  if (!value) return 'Not captured';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? 'Not captured' : date.toLocaleString(undefined, { dateStyle: 'short', timeStyle: 'short' });
}

function remainingWindow(windows, short) {
  const candidates = (Array.isArray(windows) ? windows : []).filter((window) => Boolean(window.short) === short);
  const window = candidates[0];
  return window ? Math.max(0, Math.min(100, Number(window.remaining_percent) || 0)) : null;
}

function metricMeter(label, value) {
  const wrapper = node('div', '', 'quota-meter');
  const meter = document.createElement('meter');
  meter.min = 0;
  meter.max = 100;
  meter.value = value ?? 0;
  meter.setAttribute('aria-label', `${label} remaining`);
  const text = node('span', value === null ? 'No snapshot' : `${value}% remaining`, 'meter-text');
  wrapper.append(meter, text);
  return wrapper;
}

export function accountStatusValue(account, quota) {
  if (account.disabled) return ['Disabled', 'status-danger'];
  if (account.unavailable) return ['Unavailable', 'status-warning'];
  if (!quota) return ['Awaiting refresh', 'status-neutral'];
  if (quota.refresh_error_code) return ['Refresh failed', 'status-danger'];
  if (quota.stale) return ['Stale snapshot', 'status-warning'];
  return ['Healthy', 'status-success'];
}

function statusNode(account, quota) {
  const [label, tone] = accountStatusValue(account, quota);
  const wrapper = node('span', '', `status-inline ${tone}`);
  const marker = node('span', '', 'status-marker');
  marker.setAttribute('aria-hidden', 'true');
  wrapper.append(marker, node('span', label));
  return wrapper;
}

function cell(label, content, header = false) {
  const element = node(header ? 'th' : 'td', '', header ? '' : 'account-cell');
  if (header) element.scope = 'col';
  else element.dataset.label = label;
  if (typeof content === 'string') element.textContent = content;
  else if (content) element.append(content);
  return element;
}

export function latestAccountHistoryRecord(history, key) {
  let latest = null;
  let latestTime = Number.NaN;
  for (const record of Array.isArray(history) ? history : []) {
    if (record?.account_key !== key) continue;
    if (!latest) {
      latest = record;
      latestTime = Date.parse(record.finished_at || record.started_at || '');
      continue;
    }
    const currentTime = Date.parse(record.finished_at || record.started_at || '');
    if (
      (Number.isFinite(currentTime) && !Number.isFinite(latestTime))
      || (Number.isFinite(currentTime) && currentTime >= latestTime)
      || (!Number.isFinite(currentTime) && !Number.isFinite(latestTime))
    ) {
      latest = record;
      latestTime = currentTime;
    }
  }
  return latest;
}

export function renderAccounts(container, accounts = [], options = {}) {
  container.replaceChildren();
  const scheduledKeys = new Set(options.scheduledAccountKeys || []);
  const selectedKeys = new Set(options.selectedAccountKeys || []);
  const revealedKeys = options.identityRevealKeys instanceof Set ? options.identityRevealKeys : new Set(options.identityRevealKeys || []);
  const quotaByAccount = options.quotaByAccount || {};
  const history = options.history || [];

  if (!accounts.length) {
    const row = node('tr');
    const empty = cell('', 'No accounts discovered.');
    empty.colSpan = 13;
    row.append(empty);
    container.append(row);
    return;
  }

  for (const account of accounts) {
    const key = String(account.account_key || '').trim();
    const quota = quotaByAccount[key];
    const snapshot = quota?.snapshot || {};
    const latest = latestAccountHistoryRecord(history, key);
    const row = node('tr');
    row.dataset.accountKey = key;

    const revealed = revealedKeys.has(key);
    const projection = safeAccountIdentityProjection(account, revealed);
    const identity = node('div', '', 'identity-cell');
    const identityLine = node('div', '', 'identity-line');
    const identityText = node('span', projection.identity, 'identity-value');
    identityText.dataset.identityState = revealed ? 'revealed' : 'masked';
    identityLine.append(identityText);
    const revealButton = actionButton(revealed ? 'Mask' : 'Reveal', key, 'reveal-identity', revealed);
    bindAccountRevealControl(revealButton, key, options.onReveal);
    identityLine.append(revealButton);
    identity.append(identityLine);
    if (revealed) {
      const safeDetails = node('span', '', 'account-safe-details');
      if (projection.accountKey) safeDetails.append(node('span', `Account key: ${projection.accountKey}`, 'account-key'));
      if (projection.fingerprint) safeDetails.append(node('span', `Fingerprint: ${projection.fingerprint}`, 'account-fingerprint'));
      identity.append(safeDetails);
    }
    const actionLabel = node('label', '', 'selection-control');
    const actionCheckbox = document.createElement('input');
    actionCheckbox.type = 'checkbox';
    actionCheckbox.checked = selectedKeys.has(key);
    actionCheckbox.disabled = Boolean(account.unavailable || account.disabled);
    actionCheckbox.dataset.accountSelection = key;
    actionCheckbox.setAttribute('aria-label', `Select account ${maskIdentity(account.masked_identity, key)} for an action`);
    actionCheckbox.addEventListener('change', () => options.onSelectionChange?.(key, actionCheckbox.checked));
    actionLabel.append(actionCheckbox, node('span', 'Action selection'));
    identity.append(actionLabel);
    row.append(cell('Account', identity, true));

    row.append(cell('Plan', account.plan_label || 'Plan not reported'));
    row.append(cell('Health', statusNode(account, quota)));

    const scheduleLabel = node('label', '', 'toggle-control');
    const scheduleToggle = document.createElement('input');
    scheduleToggle.type = 'checkbox';
    scheduleToggle.checked = scheduledKeys.has(key);
    scheduleToggle.dataset.accountScheduled = key;
    scheduleToggle.setAttribute('aria-label', `Schedule account ${maskIdentity(account.masked_identity, key)}`);
    scheduleToggle.addEventListener('change', () => options.onScheduleChange?.(key, scheduleToggle.checked));
    scheduleLabel.append(scheduleToggle, node('span', scheduleToggle.checked ? 'Scheduled' : 'Manual only'));
    row.append(cell('Scheduled', scheduleLabel));

    const windows = snapshot.windows || [];
    row.append(cell('Short window', metricMeter('Short window', remainingWindow(windows, true))));
    row.append(cell('Long window', metricMeter('Long window', remainingWindow(windows, false))));

    const credits = snapshot.reset_applicable_count ?? (snapshot.reset_info_complete ? (snapshot.reset_credits || []).length : null);
    row.append(cell('Reset credits', credits === null ? 'Not refreshed' : String(credits)));
    row.append(cell('Next occurrence', options.nextRuns?.[key] ? dateLabel(options.nextRuns[key]) : account.next_occurrence ? dateLabel(account.next_occurrence) : 'Not scheduled'));

    const requestOutcome = latest?.request_outcome || account.request_outcome || 'No operation';
    const windowOutcome = latest?.window_outcome || account.window_outcome || 'No operation';
    row.append(cell('Request Outcome', OUTCOME_LABELS[requestOutcome] || requestOutcome));
    row.append(cell('Window Outcome', OUTCOME_LABELS[windowOutcome] || windowOutcome));

    const transport = latest ? `${latest.http_status || '-'} / ${latest.latency_ms || 0} ms` : 'No request';
    row.append(cell('HTTP / latency', transport));

    const capture = node('div', '', 'capture-stack');
    capture.append(node('span', dateLabel(snapshot.captured_at), 'capture-time'));
    if (quota?.stale) capture.append(node('span', 'Stale', 'stale-label'));
    row.append(cell('Capture', capture));

    const errorCode = quota?.refresh_error_code || latest?.error_code || account.error_code;
    row.append(cell('Error', errorCode ? String(errorCode) : 'None'));
    container.append(row);
  }
}

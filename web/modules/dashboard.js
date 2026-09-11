const SUCCESS_OUTCOMES = new Set(['succeeded', 'verified_started', 'already_active', 'unchanged']);

function number(value, fallback = 0) {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : fallback;
}

function clockMinutes(value) {
  const match = /^(\d{2}):(\d{2})$/u.exec(String(value || ''));
  if (!match) return null;
  const hours = Number(match[1]);
  const minutes = Number(match[2]);
  return hours < 24 && minutes < 60 ? hours * 60 + minutes : null;
}

function clockLabel(minutes) {
  const normalized = ((Math.round(minutes) % 1440) + 1440) % 1440;
  return `${String(Math.floor(normalized / 60)).padStart(2, '0')}:${String(normalized % 60).padStart(2, '0')}`;
}

function latestRecord(history, key) {
  return (Array.isArray(history) ? history : [])
    .filter((record) => record?.account_key === key)
    .sort((a, b) => Date.parse(b.finished_at || b.started_at || '') - Date.parse(a.finished_at || a.started_at || ''))[0] || {};
}

function shortWindow(quota) {
  return (quota?.snapshot?.windows || []).find((window) => window?.short) || (quota?.snapshot?.windows || [])[0] || {};
}

export function maskOperationalIdentity(identity = {}) {
  const email = String(identity.email || '');
  const at = email.indexOf('@');
  const maskedEmail = at > 0 ? `${email[0]}***${email.slice(at)}` : '***';
  const authIndex = String(identity.authIndex || '');
  const prefix = String(identity.accountPrefix || '');
  return {
    email: maskedEmail,
    authIndex: authIndex ? '*'.repeat(Math.min(2, authIndex.length)) : '',
    accountPrefix: prefix.length > 4 ? `${prefix.slice(0, Math.min(5, prefix.length - 4))}***${prefix.slice(-4)}` : '***',
  };
}

export function accountStatus(account = {}, quota = {}) {
  if (account.disabled) return 'disabled';
  if (account.unavailable || !quota?.snapshot || quota?.refresh_error_code || quota?.stale) return 'warning';
  return 'healthy';
}

export function summarizeAccounts(accounts = [], quotaByAccount = {}) {
  const summary = { total: accounts.length, healthy: 0, warning: 0, disabled: 0 };
  for (const account of accounts) summary[accountStatus(account, quotaByAccount[account.account_key])] += 1;
  return summary;
}

export function projectAccountRow(account = {}, { quota = {}, history = [], hidden = false } = {}) {
  const record = latestRecord(history, account.account_key);
  const window = shortWindow(quota);
  const remaining = Math.max(0, Math.min(100, number(window.remaining_percent, 0)));
  const rawIdentity = {
    email: account.email || account.masked_identity || '',
    authIndex: account.auth_index || '',
    accountPrefix: account.account_prefix || account.account_key || '',
  };
  const identity = hidden ? maskOperationalIdentity(rawIdentity) : rawIdentity;
  return {
    key: String(account.account_key || ''),
    email: identity.email,
    authIndex: identity.authIndex,
    accountPrefix: identity.accountPrefix,
    plan: String(account.plan_label || account.account_type || '-'),
    status: accountStatus(account, quota),
    health: remaining,
    remaining,
    used: 100 - remaining,
    resetAt: String(window.reset_at || ''),
    httpStatus: number(record.http_status, 0),
    latencyMs: number(record.latency_ms, 0),
    lastCheckedAt: String(record.finished_at || record.started_at || ''),
    configurationUpdatedAt: String(account.configuration_updated_at || ''),
    error: String(quota?.refresh_error_code || record.error_code || account.status_message || ''),
  };
}

export function groupRunHistory(records = []) {
  const groups = new Map();
  for (const record of Array.isArray(records) ? records : []) {
    const key = record.run_id || record.correlation_id || record.id;
    if (!key) continue;
    const group = groups.get(key) || { records: [], runId: key };
    group.records.push(record);
    groups.set(key, group);
  }
  return [...groups.values()].map(({ records: items, runId }) => {
    const starts = items.map((item) => ({ raw: item.started_at || '', value: Date.parse(item.started_at || '') })).filter((item) => Number.isFinite(item.value));
    const finishes = items.map((item) => Date.parse(item.finished_at || item.started_at || '')).filter(Number.isFinite);
    const succeeded = items.filter((item) => SUCCESS_OUTCOMES.has(item.request_outcome)).length;
    const errors = items.map((item) => item.error_code).filter(Boolean);
    return {
      runId,
      startedAt: starts.length ? starts.reduce((earliest, item) => item.value < earliest.value ? item : earliest).raw : '',
      trigger: items[0]?.trigger || '',
      accounts: new Set(items.map((item) => item.account_key).filter(Boolean)).size,
      succeeded,
      failed: items.length - succeeded,
      durationMs: starts.length && finishes.length ? Math.max(...finishes) - Math.min(...starts.map((item) => item.value)) : 0,
      message: errors[0] || (succeeded === items.length ? '检测完成' : '未记录结果'),
      result: succeeded === items.length ? 'success' : succeeded ? 'partial' : 'failed',
    };
  }).sort((a, b) => Date.parse(b.startedAt || '') - Date.parse(a.startedAt || ''));
}

export function buildWindowStrategy(config = {}) {
  const periods = (config.work_periods || []).map((period) => ({ start: clockMinutes(period.start), end: clockMinutes(period.end) }))
    .filter((period) => period.start !== null && period.end !== null && period.end > period.start);
  const workMinutes = periods.reduce((sum, period) => sum + period.end - period.start, 0);
  const windowMinutes = Math.max(60, number(config.window_hours, 5) * 60);
  const usableMinutes = Math.max(1, number(config.productivity_minutes, 60));
  const lead = Math.max(0, number(config.preheat_lead_minutes, 0));
  const skip = new Set(config.skip_window_times || []);
  const windows = [];
  if (periods.length) {
    const finalEnd = periods.at(-1).end;
    for (let cursor = periods[0].start; cursor < finalEnd; cursor += windowMinutes) {
      const containingPeriod = periods.find((period) => cursor >= period.start && cursor < period.end);
      if (!containingPeriod) continue;
      const workStart = clockLabel(cursor);
      if (skip.has(workStart)) continue;
      windows.push({ workStart, preheatAt: clockLabel(cursor - lead), expiresAt: clockLabel(cursor + windowMinutes) });
    }
  }
  const normalAvailableMinutes = Math.min(workMinutes, windows.length * usableMinutes);
  const preheatedAvailableMinutes = Math.min(workMinutes, normalAvailableMinutes + Math.max(0, windows.length - 1) * Math.min(lead, usableMinutes));
  const timeline = Array.from({ length: 24 }, (_, hour) => ({ hour, active: periods.some((period) => hour * 60 < period.end && (hour + 1) * 60 > period.start) }));
  return { workMinutes, windowMinutes, windows, normalAvailableMinutes, preheatedAvailableMinutes, gainMinutes: preheatedAvailableMinutes - normalAvailableMinutes, timeline };
}

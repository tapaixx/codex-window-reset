function number(value, fallback = 0) {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : fallback;
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

export function summarizeOperations({ accounts = [], quotaByAccount = {}, scheduledKeys = new Set(), guardrailHoldCount = 0 } = {}) {
  const scheduled = scheduledKeys instanceof Set ? scheduledKeys : new Set(scheduledKeys || []);
  return {
    scheduled: accounts.filter((account) => scheduled.has(account.account_key)).length,
    healthy: accounts.filter((account) => accountStatus(account, quotaByAccount[account.account_key]) === 'healthy').length,
    paused: accounts.filter((account) => account.disabled || account.unavailable).length,
    guardrail: Math.max(0, Number(guardrailHoldCount) || 0),
    stale: Object.values(quotaByAccount).filter((view) => view?.stale).length,
  };
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

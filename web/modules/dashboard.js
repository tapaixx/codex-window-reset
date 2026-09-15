function number(value, fallback = 0) {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : fallback;
}

export function groupHistoryBatches(history = []) {
  const groups = new Map();
  for (const [index, record] of history.slice(0, 100).entries()) {
    const automatic = record.trigger === 'preheat' || record.trigger === 'compensation';
    const occurrence = automatic && /^(\d{4}-\d{2}-\d{2}\/p\d+)\/.+$/.exec(record.occurrence_id || '');
    const key = occurrence ? `scheduled/${occurrence[1]}` : record.run_id ? `manual/${record.run_id}` : `record/${record.id || index}`;
    if (!groups.has(key)) groups.set(key, { key, trigger: occurrence ? 'preheat' : record.trigger, period: occurrence?.[1] || '', records: [] });
    groups.get(key).records.push(record);
  }
  return [...groups.values()].map((batch) => {
    const starts = batch.records.map((record) => record.started_at || record.finished_at).filter((date) => Number.isFinite(Date.parse(date))).sort((a, b) => Date.parse(a) - Date.parse(b));
    const finishes = batch.records.map((record) => record.finished_at || record.started_at).filter((date) => Number.isFinite(Date.parse(date))).sort((a, b) => Date.parse(b) - Date.parse(a));
    return { ...batch, accountCount: new Set(batch.records.map((record) => record.account_key || record.id)).size, startedAt: starts[0], finishedAt: finishes[0] };
  }).sort((a, b) => (Date.parse(b.finishedAt) || 0) - (Date.parse(a.finishedAt) || 0));
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

// The panel's highest-consequence silent failure is automatic preheating that
// has stopped without anything on screen changing. describeNextRun turns the
// upcoming view into one line that always states which of those situations is
// true, so "nothing scheduled" can never be mistaken for "not yet due".
export function describeNextRun(view = {}, now = Date.now()) {
  if (view.store_error_code) {
    return { state: 'error', text: `下一次预热 · 读取计划状态失败（${view.store_error_code}）` };
  }
  if (!view.enabled) {
    return { state: 'off', text: '下一次预热 · 计划未启用' };
  }
  const at = Date.parse(view.next_run_at || '');
  if (!Number.isFinite(at)) {
    return { state: 'alarm', text: '下一次预热 · 计划已启用，但没有任何待执行的预热' };
  }
  const batch = (view.batches || []).find((item) => (item.occurrences || []).some((slot) => Date.parse(slot.planned_at || '') === at));
  const accounts = batch ? (batch.occurrences || []).length : 0;
  const minutes = Math.round((at - now) / 60000);
  const clock = new Date(at).toLocaleTimeString('zh-CN', { hour12: false, hour: '2-digit', minute: '2-digit' });
  const day = dayPrefix(at, now);
  const parts = [`下一次预热 · ${day}${clock}`];
  if (accounts > 0) parts.push(`${accounts} 个账号`);
  parts.push(relativeFromNow(minutes));
  // A horizon that is armed but a day out is the shape a stalled scheduler
  // leaves behind, so it is surfaced rather than read as a normal quiet day.
  return { state: minutes > 24 * 60 ? 'distant' : 'ready', text: parts.join(' · ') };
}

function dayPrefix(at, now) {
  const start = (value) => { const date = new Date(value); date.setHours(0, 0, 0, 0); return date.getTime(); };
  const days = Math.round((start(at) - start(now)) / 86400000);
  if (days <= 0) return '今天 ';
  if (days === 1) return '明天 ';
  return `${new Date(at).toLocaleDateString('zh-CN', { month: '2-digit', day: '2-digit' })} `;
}

function relativeFromNow(minutes) {
  if (minutes <= 0) return '即将执行';
  if (minutes < 60) return `${minutes} 分钟后`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} 小时后`;
  return `${Math.floor(hours / 24)} 天后`;
}

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

// "No snapshot yet" is the normal state of a freshly opened panel, because the
// panel deliberately never polls (ADR-0011). Reporting it as a warning made
// every account look broken on every load and taught the Operator to ignore
// the column. Missing data is its own state, and each real warning names the
// condition instead of showing a bare 警告.
export function accountStatus(account = {}, quota = {}) {
  if (account.disabled) return 'disabled';
  if (account.unavailable) return 'unavailable';
  if (quota?.refresh_error_code) return 'refresh_failed';
  if (!quota?.snapshot) return 'unknown';
  // A merely old snapshot is not an account problem. Every preheat refreshes
  // quota immediately before deciding, so age never blocks one; the five-minute
  // staleness boundary exists for decisions, not for this column. Flagging it
  // here turned every row amber five minutes after each load and said nothing
  // the timestamp column and the global freshness indicator do not already say.
  return 'healthy';
}

const STATUS_DETAIL = {
  healthy: '额度快照已获取且在有效期内。',
  unknown: '尚未获取额度快照。面板不会自动轮询，点击「刷新已选额度」或「刷新全部额度」后显示。',
  refresh_failed: '最近一次额度刷新失败，保留了上一次的快照。错误原因见同行的错误列。',
  unavailable: '主机报告该凭证当前不可用，自动预热会跳过它。',
  disabled: '该凭证已停用，不参与自动预热或手动检测。',
};

export function accountStatusDetail(status) {
  return STATUS_DETAIL[status] || '';
}

export function summarizeAccounts(accounts = [], quotaByAccount = {}) {
  const summary = { total: accounts.length, healthy: 0, unknown: 0, warning: 0, disabled: 0 };
  for (const account of accounts) {
    const status = accountStatus(account, quotaByAccount[account.account_key]);
    if (status === 'disabled' || status === 'healthy' || status === 'unknown') summary[status] += 1;
    else summary.warning += 1;
  }
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
    plan: planDisplayLabel(account.plan_label || account.plan_type),
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
  const day = `${dayLabel(at, now)} `;
  const parts = [`下一次预热 · ${day}${clock}`];
  if (accounts > 0) parts.push(`${accounts} 个账号`);
  parts.push(relativeFromNow(minutes));
  // A horizon that is armed but a day out is the shape a stalled scheduler
  // leaves behind, so it is surfaced rather than read as a normal quiet day.
  return { state: minutes > 24 * 60 ? 'distant' : 'ready', text: parts.join(' · ') };
}

function relativeFromNow(minutes) {
  if (minutes <= 0) return '即将执行';
  if (minutes < 60) return `${minutes} 分钟后`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} 小时后`;
  return `${Math.floor(hours / 24)} 天后`;
}

const clockLabel = (value) => {
  const at = Date.parse(value || '');
  return Number.isFinite(at) ? new Date(at).toLocaleTimeString('zh-CN', { hour12: false, hour: '2-digit', minute: '2-digit' }) : '--';
};

// projectUpcomingBatches renders what the scheduler has actually armed. It
// mirrors the history panel's batch grouping so past and future batches read
// the same way, and it only ever states conditions that are already true: a
// quota decision depends on the snapshot at execution time and is not guessed
// here. Accounts are joined client-side because the panel already holds the
// account list together with the Operator's identity-masking preference.
export function projectUpcomingBatches(view = {}, { accounts = [], hidden = false, now = Date.now() } = {}) {
  const byKey = new Map(accounts.map((account) => [account.account_key, account]));
  return (view.batches || []).map((batch) => {
    const first = Date.parse(batch.occurrences?.[0]?.planned_at || '');
    const startsToday = dayOffset(first, now);
    return {
      key: `${batch.local_date}/p${batch.period_index}`,
      dayLabel: dayLabel(first, now),
      windowLabel: `${clockLabel(batch.window_start)}–${clockLabel(batch.window_end)}`,
      periodLabel: `计划时段 ${number(batch.period_index, 0) + 1}`,
      accountCount: (batch.occurrences || []).length,
      relative: Number.isFinite(first) ? relativeFromNow(Math.round((first - now) / 60000)) : '--',
      near: Number.isFinite(startsToday) && startsToday <= 1,
      occurrences: (batch.occurrences || []).map((slot) => {
        const account = byKey.get(slot.account_key);
        const rawIdentity = { email: account?.email || account?.masked_identity || slot.account_key, authIndex: '', accountPrefix: slot.account_key };
        return {
          key: slot.account_key,
          email: (hidden ? maskOperationalIdentity(rawIdentity) : rawIdentity).email,
          time: clockLabel(slot.planned_at),
          blocked: blockedLabel(slot, account),
        };
      }),
    };
  });
}

function blockedLabel(slot = {}, account) {
  if (slot.blocked_reason === 'guardrail_hold') return 'Guardrail Hold · 届时将跳过';
  if (account?.disabled) return '账号已停用 · 届时将跳过';
  if (account?.unavailable) return '账号不可用 · 届时将跳过';
  return '';
}

function dayOffset(at, now) {
  if (!Number.isFinite(at)) return NaN;
  const start = (value) => { const date = new Date(value); date.setHours(0, 0, 0, 0); return date.getTime(); };
  return Math.round((start(at) - start(now)) / 86400000);
}

function dayLabel(at, now) {
  const days = dayOffset(at, now);
  if (!Number.isFinite(days)) return '';
  if (days <= 0) return '今天';
  if (days === 1) return '明天';
  return new Date(at).toLocaleDateString('zh-CN', { month: '2-digit', day: '2-digit' });
}

// The host's account_type is the credential type ("oauth"), not a subscription
// tier; the tier travels in the OAuth id_token as plan_type. Known tiers get a
// cased label and anything unrecognised is passed through rather than hidden,
// so a new tier degrades to its raw name instead of disappearing.
const PLAN_LABELS = { free: 'Free', go: 'Go', plus: 'Plus', pro: 'Pro', team: 'Team', business: 'Business', enterprise: 'Enterprise', edu: 'Edu' };

export function planDisplayLabel(value) {
  const raw = String(value || '').trim();
  if (!raw) return '-';
  const known = PLAN_LABELS[raw.toLowerCase()];
  if (known) return known;
  return raw.length > 1 ? raw[0].toUpperCase() + raw.slice(1) : raw.toUpperCase();
}

// The reset-credit count alone does not say how long the credits last. Credits
// expire unused, so the soonest expiry is the part that can prompt an action;
// it is stated plainly rather than styled as a warning, because the panel has
// one alarm channel already and this is not a fault.
export function resetCreditSummary(snapshot = {}) {
  const complete = Boolean(snapshot.reset_info_complete);
  const credits = Array.isArray(snapshot.reset_credits) ? snapshot.reset_credits : [];
  const count = snapshot.reset_applicable_count ?? (complete ? credits.length : null);
  if (count === null || count === undefined) return { count: '--', expiry: '', title: '' };
  const soonest = credits
    .map((credit) => Date.parse(credit?.expires_at || ''))
    .filter(Number.isFinite)
    .sort((left, right) => left - right)[0];
  if (!Number.isFinite(soonest) || Number(count) <= 0) return { count: String(count), expiry: '', title: '' };
  const date = new Date(soonest);
  return {
    count: String(count),
    expiry: `最早 ${date.toLocaleDateString('zh-CN', { month: '2-digit', day: '2-digit' })} 到期`,
    title: `最早到期的重置额度：${date.toLocaleString('zh-CN', { hour12: false })}${credits.length > 1 ? `（共 ${credits.length} 张，按到期时间排序）` : ''}`,
  };
}

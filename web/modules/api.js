const ERROR_MESSAGES = {
  config_invalid: '配置无效，请检查必填字段和值域。',
  revision_conflict: '配置已被其他操作更新，请恢复服务器版本后重试。',
  run_in_progress: '已有操作正在执行，请等待当前运行结束。',
  account_busy: '该账户正在执行其他操作，请稍后重试。',
  account_disabled: '该账户已停用，无法执行此操作。',
  account_unavailable: '该账户当前不可用，请先刷新账户状态。',
  guardrail_hold: '长窗口余额低于安全阈值，操作已暂停。',
  quota_refresh_failed: '配额刷新失败，请检查宿主连接后重试。',
  probe_failed: '健康探测失败，请查看操作记录后重试。',
  window_unverified: '窗口结果未能验证，请查看操作记录。',
  idempotency_conflict: '此重置请求标识已用于其他账户。',
  reset_outcome_unknown: '重置结果未知，请查看重置审计后再决定是否操作。',
  store_corrupt: '持久化数据不可用，调度已暂停但诊断仍可继续。',
  unauthorized: 'CLIProxyAPI 管理密钥无效或已变更，请刷新宿主管理面板后重试。',
  forbidden: 'CLIProxyAPI 拒绝了此管理请求，请检查管理密钥权限。',
};

const HOST_MANAGEMENT_BASE = '/v0/management';

let requestDispatcher = null;

function decodeManagerStorage(value) {
  if (!value || !value.startsWith('enc::v1::')) return value;
  try {
    const encoded = atob(value.slice(9));
    const key = new TextEncoder().encode(`cli-proxy-api-webui::secure-storage|${location.host}|${navigator.userAgent}`);
    const decoded = new Uint8Array(encoded.length);
    for (let index = 0; index < encoded.length; index += 1) decoded[index] = encoded.charCodeAt(index) ^ key[index % key.length];
    return new TextDecoder().decode(decoded);
  } catch { return ''; }
}

function extractManagementKey(value) {
  let current = decodeManagerStorage(value);
  for (let depth = 0; depth < 3 && typeof current === 'string'; depth += 1) {
    try { current = JSON.parse(current); } catch { break; }
  }
  if (typeof current === 'string') return current;
  if (!current || typeof current !== 'object') return '';
  return current.managementKey || current.state?.managementKey || (typeof current.value === 'string' ? current.value : '');
}

export function managementKey() {
  for (const name of ['cli-proxy-auth', 'managementKey']) {
    const key = extractManagementKey(globalThis.localStorage?.getItem?.(name) || '');
    if (key) return key;
  }
  return '';
}

function authenticatedHeaders(headers = {}) {
  const key = managementKey();
  return { ...(key ? { Authorization: `Bearer ${key}` } : {}), ...headers };
}

function defaultResourcePath() {
  const metadata = globalThis.document?.querySelector?.('meta[name="resource-base-path"], meta[name="resource_base_path"]');
  return metadata?.content || globalThis.location?.pathname || '';
}

function stripPathDecorations(value) {
  return String(value || '').split(/[?#]/u, 1)[0];
}

function validPluginID(value) {
  return /^[A-Za-z0-9._-]+$/u.test(value) && !/[\s/\\]/u.test(value);
}

export function deriveManagementBase(resourcePath = defaultResourcePath()) {
  const path = stripPathDecorations(resourcePath);
  const resourceMarker = '/resource/plugins/';
  const resourceIndex = path.indexOf(resourceMarker);
  if (resourceIndex >= 0) {
    const prefix = path.slice(0, resourceIndex);
    const plugin = path.slice(resourceIndex + resourceMarker.length).split('/')[0];
    if (validPluginID(plugin)) return `${prefix}/management/plugins/${plugin}`;
  }

  const managementMarker = '/management/plugins/';
  const managementIndex = path.indexOf(managementMarker);
  if (managementIndex >= 0) {
    const prefix = path.slice(0, managementIndex);
    const plugin = path.slice(managementIndex + managementMarker.length).split('/')[0];
    if (validPluginID(plugin)) return `${prefix}/management/plugins/${plugin}`;
  }

  return '/v0/management/plugins';
}

export function setRequestDispatcher(dispatcher) {
  requestDispatcher = typeof dispatcher === 'function' ? dispatcher : null;
  return () => {
    if (requestDispatcher === dispatcher) requestDispatcher = null;
  };
}

export async function request(path, options = {}) {
  const {
    base,
    dispatch,
    onAuthRequired,
    body,
    method = 'GET',
    headers = {},
    ...fetchOptions
  } = options;
  const endpoint = String(path || '').startsWith('/') ? path : `/${path}`;
  const init = {
    ...fetchOptions,
    method,
    credentials: 'same-origin',
    headers: {
      'Content-Type': 'application/json',
      Accept: 'application/json',
      ...authenticatedHeaders(headers),
    },
  };
  if (body !== undefined) init.body = typeof body === 'string' ? body : JSON.stringify(body);

  const response = await fetch(`${base || deriveManagementBase()}${endpoint}`, init);
  let envelope;
  try {
    envelope = await response.json();
  } catch {
    envelope = { ok: false, error: { code: 'store_corrupt', message: 'Invalid response' } };
  }

  if (response.status === 401 || response.status === 403) {
    const authAction = { type: 'host-auth-required', status: response.status };
    (typeof dispatch === 'function' ? dispatch : requestDispatcher)?.(authAction);
    onAuthRequired?.(authAction);
  }

  if (!response.ok || !envelope?.ok) {
    const details = envelope?.error || {};
    const error = Object.assign(
      new Error(details.message || `HTTP ${response.status}`),
      details,
      { status: response.status },
    );
    throw error;
  }
  return envelope.result;
}

// Host-owned management data is intentionally fetched from CLIProxyAPI's
// canonical management surface. It is not a plugin route and must not be
// derived from the resource URL.
export async function hostManagementRequest(path, options = {}) {
  const endpoint = String(path || '').startsWith('/') ? path : `/${path}`;
  const { body, method = 'GET', headers = {}, ...fetchOptions } = options;
  const init = {
    ...fetchOptions,
    method,
    credentials: 'same-origin',
    headers: { Accept: 'application/json', 'Content-Type': 'application/json', ...authenticatedHeaders(headers) },
  };
  if (body !== undefined) init.body = typeof body === 'string' ? body : JSON.stringify(body);
  const response = await fetch(`${HOST_MANAGEMENT_BASE}${endpoint}`, init);
  let envelope = {};
  try { envelope = await response.json(); } catch { /* handled below */ }
  if (!response.ok) {
    const details = envelope?.error || {};
    const error = Object.assign(new Error(details.message || `HTTP ${response.status}`), details, { status: response.status });
    throw error;
  }
  return envelope?.result ?? envelope;
}

export function createCodexApiCall({ authIndex, accountId = '', method = 'GET', url, headers = {}, data } = {}) {
  const header = { Authorization: 'Bearer $TOKEN$', 'Content-Type': 'application/json', 'User-Agent': 'codex-tui', ...headers };
  if (accountId) header['Chatgpt-Account-Id'] = String(accountId).trim();
  const payload = { auth_index: String(authIndex || '').trim(), method, url, header };
  if (data !== undefined) payload.data = typeof data === 'string' ? data : JSON.stringify(data);
  return payload;
}

function record(value) {
  return value && typeof value === 'object' && !Array.isArray(value) ? value : null;
}

function finiteNumber(value) {
  if (value === null || value === undefined || value === '') return null;
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : null;
}

function epochMillis(value) {
  if (value === null || value === undefined || value === '') return null;
  if (typeof value === 'number' && Number.isFinite(value)) return value > 1e12 ? value : value * 1000;
  const text = String(value).trim();
  if (/^\d+(?:\.\d+)?$/u.test(text)) { const parsed = Number(text); return parsed > 1e12 ? parsed : parsed * 1000; }
  const parsed = Date.parse(text); return Number.isNaN(parsed) ? null : parsed;
}

function resetCreditInfo(payload) {
  const value = record(payload);
  if (!value) return { count: null, credits: [], valid: false };
  const credits = Array.isArray(value.credits) ? value.credits.filter((item) => record(item)).map((item) => ({ id: String(item.id || ''), expires_at: String(item.expires_at ?? item.expiresAt ?? '') })) : [];
  const count = finiteNumber(value.applicable_available_count ?? value.applicableAvailableCount ?? value.available_count ?? value.availableCount);
  return { count: count === null ? (credits.length || null) : count, credits, valid: ['credits', 'applicable_available_count', 'applicableAvailableCount', 'available_count', 'availableCount'].some((key) => Object.hasOwn(value, key)) };
}

export function normalizeCodexQuota(payload, { capturedAt = Date.now(), resetPayload = null } = {}) {
  const root = record(payload) || {};
  const rate = record(root.rate_limit ?? root.rateLimit) || {};
  const rawWindows = [record(rate.primary_window ?? rate.primaryWindow), record(rate.secondary_window ?? rate.secondaryWindow)].filter(Boolean);
  if (Array.isArray(rate.windows)) rawWindows.push(...rate.windows.filter((item) => record(item)));
  const windows = rawWindows.map((window) => {
    const seconds = finiteNumber(window.limit_window_seconds ?? window.limitWindowSeconds);
    const minutes = finiteNumber(window.window_minutes ?? window.windowMinutes) ?? (seconds === null ? null : seconds / 60);
    let used = finiteNumber(window.used_percent ?? window.usedPercent ?? window.used_fraction ?? window.usedFraction);
    if (used !== null && used >= 0 && used <= 1) used *= 100;
    const remaining = finiteNumber(window.remaining_percent ?? window.remainingPercent);
    const resetDirect = epochMillis(window.reset_at ?? window.resetAt);
    const resetAfter = finiteNumber(window.reset_after_seconds ?? window.resetAfterSeconds);
    const resetAt = resetDirect ?? (resetAfter !== null && resetAfter >= 0 ? Number(capturedAt) + resetAfter * 1000 : null);
    if (minutes === null || minutes <= 0 || (used === null && remaining === null)) return null;
    return { duration_minutes: Math.round(minutes), remaining_percent: Math.max(0, Math.min(100, remaining ?? (100 - used))), reset_at: resetAt === null ? '' : new Date(resetAt).toISOString() };
  }).filter(Boolean).sort((left, right) => left.duration_minutes - right.duration_minutes).filter((window, index, all) => index === 0 || window.duration_minutes !== all[index - 1].duration_minutes);
  windows.forEach((window, index) => { window.short = index === 0; });
  const embedded = resetCreditInfo(root.rate_limit_reset_credits ?? root.rateLimitResetCredits);
  const detail = resetCreditInfo(resetPayload);
  return { windows, reset_credits: detail.credits.length ? detail.credits : embedded.credits, reset_applicable_count: detail.count ?? embedded.count, reset_info_complete: detail.valid };
}

export function normalizeHostAuthFiles(payload) {
  const files = Array.isArray(payload) ? payload : payload?.files;
  return (files || []).filter((file) => String(file?.provider || file?.type || file?.credential_type || '').toLowerCase() === 'codex')
    .map((file) => {
      const authIndex = String(file?.auth_index || file?.authIndex || file?.index || '').trim();
      const accountID = String(file?.account_id || file?.accountId || file?.account || file?.id || '').trim();
      return {
        account_key: `acct-${authIndex}`,
        auth_index: authIndex,
        email: String(file?.email || '').trim(),
        account_prefix: accountID,
        masked_identity: String(file?.email || `acct-${authIndex}`).replace(/^(.).*(@.*)$/u, '$1***$2'),
        account_id: accountID,
        plan_label: String(file?.plan_label || file?.plan || file?.account_type || '').trim(),
        disabled: Boolean(file?.disabled),
        unavailable: Boolean(file?.unavailable),
        fingerprint: '',
        configuration_updated_at: String(file?.updated_at || file?.updatedAt || '').trim(),
      };
    }).filter((file) => file.auth_index);
}

export function requestErrorMessage(error) {
  if (error?.status === 401 || error?.status === 403) return ERROR_MESSAGES[error.status === 401 ? 'unauthorized' : 'forbidden'];
  return ERROR_MESSAGES[error?.code] || '操作失败，请稍后重试。';
}

export function localizeManagementError(error) {
  return requestErrorMessage(error);
}

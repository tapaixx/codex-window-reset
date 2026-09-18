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
  store_corrupt: '持久化数据不可用，调度已暂停但诊断仍可继续。',
  unauthorized: 'CLIProxyAPI 管理密钥无效或已变更，请刷新宿主管理面板后重试。',
  forbidden: 'CLIProxyAPI 拒绝了此管理请求，请检查管理密钥权限。',
};

const HOST_MANAGEMENT_BASE = '/v0/management';

let requestDispatcher = null;

const MANAGER_STORAGE_NAMESPACE = 'cli-proxy-api-webui::secure-storage';
const MANAGER_ENVELOPE = /^enc::(v\d+)::/u;

// The host manager obfuscates what it puts in localStorage and stamps the
// scheme into the value. Each version derives its key differently, so a value
// written by a scheme this panel does not know cannot be read at all — and must
// never be forwarded as if it were the key itself.
function managerObfuscationKey(version) {
  const host = globalThis.location?.host || '';
  const agent = globalThis.navigator?.userAgent || '';
  if (version === 'v2') return host ? `${MANAGER_STORAGE_NAMESPACE}|v2|${host}` : `${MANAGER_STORAGE_NAMESPACE}|v2`;
  return host || agent ? `${MANAGER_STORAGE_NAMESPACE}|${host}|${agent}` : MANAGER_STORAGE_NAMESPACE;
}

export function managerEnvelopeVersion(value) {
  const match = typeof value === 'string' ? MANAGER_ENVELOPE.exec(value) : null;
  return match ? match[1] : '';
}

function decodeManagerStorage(value) {
  const version = managerEnvelopeVersion(value);
  if (!version) return value;
  if (version !== 'v1' && version !== 'v2') return '';
  try {
    const encoded = atob(value.slice(9));
    const key = new TextEncoder().encode(managerObfuscationKey(version));
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
  // An undecoded envelope is not a credential. Forwarding one produced
  // "Authorization: Bearer enc::v2::..." and a 401 that looked like the
  // Operator's key had changed when it had not.
  if (typeof current === 'string') return managerEnvelopeVersion(current) ? '' : current;
  if (!current || typeof current !== 'object') return '';
  return current.managementKey || current.state?.managementKey || (typeof current.value === 'string' ? current.value : '');
}

// managementKeyProblem names why no key could be read, so a 401 can say
// something the Operator can act on instead of blaming their key.
export function managementKeyProblem() {
  let envelope = '';
  for (const name of ['cli-proxy-auth', 'managementKey']) {
    const raw = globalThis.localStorage?.getItem?.(name) || '';
    if (!raw) continue;
    if (extractManagementKey(raw)) return '';
    const version = managerEnvelopeVersion(raw);
    if (version) envelope = version;
  }
  return envelope ? `unsupported_storage_${envelope}` : '';
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

// Keep the deadline active until the response body has been consumed too.
async function fetchJSONWithDeadline(url, init, timeoutMs) {
  const controller = new AbortController();
  const callerSignal = init.signal;
  const abort = () => controller.abort(callerSignal.reason);
  if (callerSignal?.aborted) abort();
  else callerSignal?.addEventListener('abort', abort, { once: true });
  let timer;
  try {
    return await Promise.race([
      (async () => {
        const response = await fetch(url, { ...init, signal: controller.signal });
        let envelope;
        try { envelope = await response.json(); }
        catch (error) {
          if (controller.signal.aborted) throw error;
          envelope = { ok: false, error: { code: 'store_corrupt', message: 'Invalid response' } };
        }
        return { response, envelope };
      })(),
      new Promise((_, reject) => {
        timer = setTimeout(() => {
          reject(Object.assign(new Error('请求超时，请稍后重试。'), { code: 'request_timeout' }));
          controller.abort();
        }, timeoutMs);
      }),
    ]);
  } finally {
    clearTimeout(timer);
    callerSignal?.removeEventListener('abort', abort);
  }
}

export async function request(path, options = {}) {
  const {
    base,
    dispatch,
    onAuthRequired,
    body,
    method = 'GET',
    timeoutMs = method === 'GET' ? 15000 : 150000,
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

  const { response, envelope } = await fetchJSONWithDeadline(`${base || deriveManagementBase()}${endpoint}`, init, timeoutMs);

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
  const { body, method = 'GET', headers = {}, timeoutMs = 15000, ...fetchOptions } = options;
  const init = {
    ...fetchOptions,
    method,
    credentials: 'same-origin',
    headers: { Accept: 'application/json', 'Content-Type': 'application/json', ...authenticatedHeaders(headers) },
  };
  if (body !== undefined) init.body = typeof body === 'string' ? body : JSON.stringify(body);
  const { response, envelope } = await fetchJSONWithDeadline(`${HOST_MANAGEMENT_BASE}${endpoint}`, init, timeoutMs);
  if (!response.ok) {
    const details = envelope?.error || {};
    const error = Object.assign(new Error(details.message || `HTTP ${response.status}`), details, { status: response.status });
    throw error;
  }
  return envelope?.result ?? envelope;
}

function quotaCallBody(result) {
  const status = Number(result?.status_code ?? result?.statusCode ?? 0);
  if (status < 200 || status >= 300) throw new Error(`额度接口 HTTP ${status || '-'}`);
  const body = result?.body ?? result?.data ?? result;
  return typeof body === 'string' ? JSON.parse(body) : body;
}

// Explicitly refresh quota through the plugin Runtime so the result is kept
// in process memory and ordinary panel loads remain read-only.
export async function refreshAccountQuotas(accounts, { onUpdate = () => {}, onProgress = () => {}, timeoutMs = 150000 } = {}) {
  // Quota refresh is an explicit user action. Route it through the plugin so
  // the Runtime owns the snapshot in its process-memory cache and subsequent
  // panel loads can read it without contacting the upstream API again.
  const queue = [...new Map((Array.isArray(accounts) ? accounts : []).map((account) => [account.account_key, account])).values()]
    .filter((account) => String(account?.account_key || '').trim());
  const requested = queue.map((account) => account.account_key);
  const views = await request('/quota-refresh', {
    method: 'POST',
    timeoutMs,
    body: { account_keys: requested },
  });
  const byKey = new Map((Array.isArray(views) ? views : []).map((view) => [view?.snapshot?.account_key || view?.account_key, view]));
  // An empty request asks the plugin to refresh every account it can discover,
  // so the response defines the batch. An explicit request stays driven by the
  // requested keys, where a missing response is that account's failure.
  const batch = requested.length ? requested : [...byKey.keys()].filter(Boolean);
  const counts = { succeeded: 0, failed: 0 };
  for (const accountKey of batch) {
    const view = byKey.get(accountKey) || { account_key: accountKey, refresh_error_code: 'quota_refresh_failed', snapshot: { account_key: accountKey } };
    if (view.refresh_error_code) counts.failed++;
    else counts.succeeded++;
    onUpdate({ accountKey, phase: 'complete', view });
    onProgress({ ...counts, total: batch.length });
  }
  return counts;
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
    // The unit belongs to the field name. Scaling any value in [0,1] by 100
    // cannot tell one percent from a full window, and a live response carrying
    // used_percent 1 was read as a fully consumed window.
    const usedPercent = finiteNumber(window.used_percent ?? window.usedPercent);
    const usedFraction = finiteNumber(window.used_fraction ?? window.usedFraction);
    const used = usedPercent ?? (usedFraction === null ? null : usedFraction * 100);
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
        plan_label: String(file?.plan_label || file?.plan || file?.id_token?.plan_type || file?.idToken?.plan_type || '').trim(),
        disabled: Boolean(file?.disabled),
        unavailable: Boolean(file?.unavailable),
        fingerprint: '',
        configuration_updated_at: String(file?.updated_at || file?.updatedAt || '').trim(),
      };
    }).filter((file) => file.auth_index);
}

export function requestErrorMessage(error) {
  if (error?.status === 401 || error?.status === 403) {
    // Blaming the key is wrong when the key is there and simply cannot be read.
    const problem = managementKeyProblem();
    if (problem) return `宿主管理面板的密钥存储格式（${problem.replace('unsupported_storage_', '')}）本插件无法读取，请升级插件。`;
    return ERROR_MESSAGES[error.status === 401 ? 'unauthorized' : 'forbidden'];
  }
  // Preserve the server's actionable validation detail.  The previous
  // implementation replaced every config_invalid response with a generic
  // message, making it impossible to tell which field failed validation.
  if (error?.message && error.message !== ERROR_MESSAGES[error?.code]) return String(error.message);
  return ERROR_MESSAGES[error?.code] || error?.message || '操作失败，请稍后重试。';
}

export function localizeManagementError(error) {
  return requestErrorMessage(error);
}

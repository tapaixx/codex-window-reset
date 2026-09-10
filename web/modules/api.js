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
  unauthorized: '宿主登录状态已失效，请返回宿主登录后重试。',
  forbidden: '宿主拒绝了此操作，请返回宿主登录后重试。',
};

let requestDispatcher = null;

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
      ...headers,
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

export function requestErrorMessage(error) {
  if (error?.status === 401 || error?.status === 403) return ERROR_MESSAGES[error.status === 401 ? 'unauthorized' : 'forbidden'];
  return ERROR_MESSAGES[error?.code] || '操作失败，请稍后重试。';
}

export function localizeManagementError(error) {
  return requestErrorMessage(error);
}

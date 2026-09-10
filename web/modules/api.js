export function deriveManagementBase(resourcePath = globalThis.location?.pathname ?? '') {
  const marker = '/resource/plugins/';
  const index = resourcePath.indexOf(marker);
  if (index < 0) return '/v0/management/plugins';
  const prefix = resourcePath.slice(0, index);
  const remainder = resourcePath.slice(index + marker.length);
  const plugin = remainder.split('/')[0];
  if (!plugin || plugin.includes('/') || /[\s?]/u.test(plugin)) return '/v0/management/plugins';
  return `${prefix}/management/plugins/${plugin}`;
}

export async function request(endpoint, options = {}) {
  const base = options.base ?? deriveManagementBase();
  const init = {
    method: options.method ?? 'GET',
    credentials: 'same-origin',
    headers: { Accept: 'application/json', ...(options.headers ?? {}) },
  };
  if (options.body !== undefined) {
    init.headers['Content-Type'] = 'application/json';
    init.body = JSON.stringify(options.body);
  }
  const response = await fetch(`${base}${endpoint}`, init);
  const payload = await response.json();
  if (!response.ok || payload.ok === false) {
    const error = new Error(payload.error?.message ?? 'Request failed');
    error.code = payload.error?.code ?? 'request_failed';
    error.retryable = Boolean(payload.error?.retryable);
    error.correlationId = payload.error?.correlation_id ?? '';
    throw error;
  }
  return payload.result;
}

export function requestErrorMessage(error) {
  if (error?.code === 'unauthorized' || error?.code === 'forbidden') return 'Host authentication is required.';
  return error?.message ?? 'Request failed.';
}

import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { createCodexApiCall, deriveManagementBase, hostManagementRequest, normalizeCodexQuota, normalizeHostAuthFiles, request, requestErrorMessage } from '../modules/api.js';

test('surfaces actionable server validation details for invalid configuration', () => {
  assert.equal(requestErrorMessage({ code: 'config_invalid', message: 'preheat lead and span are required when scheduling is enabled' }), 'preheat lead and span are required when scheduling is enabled');
});

test('derives management path from a suffixed resource path', () => {
  assert.equal(
    deriveManagementBase('/v0/resource/plugins/codex-window-reset-linux-arm64/panel'),
    '/v0/management/plugins/codex-window-reset-linux-arm64',
  );
});

test('derives management path from bootstrap resource metadata', () => {
  assert.equal(
    deriveManagementBase('/resource/plugins/window-reset/panel'),
    '/management/plugins/window-reset',
  );
});

test('reuses the CLIProxyAPI management key without creating plugin storage', async () => {
  const source = await readFile(new URL('../modules/api.js', import.meta.url), 'utf8');
  assert.match(source, /localStorage/);
  assert.doesNotMatch(source, /localStorage\?\.setItem|sessionStorage\?\.setItem/);
});

test('host management requests use the canonical API and inherited authorization', async () => {
  const originalFetch = globalThis.fetch;
  const originalStorage = globalThis.localStorage;
  const calls = [];
  globalThis.localStorage = { getItem: (name) => name === 'cli-proxy-auth' ? JSON.stringify({ managementKey: 'host-key' }) : null };
  globalThis.fetch = async (url, options) => {
    calls.push({ url, options });
    return { ok: true, status: 200, async json() { return { files: [] }; } };
  };
  try {
    await hostManagementRequest('/auth-files');
  } finally {
    globalThis.fetch = originalFetch;
    if (originalStorage === undefined) delete globalThis.localStorage;
    else globalThis.localStorage = originalStorage;
  }
  assert.equal(calls[0].url, '/v0/management/auth-files');
  assert.equal(calls[0].options.headers.Authorization, 'Bearer host-key');
});

test('normalizes the auth-files response shape used by CLIProxyAPI', () => {
  const accounts = normalizeHostAuthFiles({ files: [{ type: 'codex', auth_index: '7', email: 'alice@example.com', account_id: 'account-7' }] });
  assert.deepEqual(accounts.map(({ account_key, auth_index, account_id }) => ({ account_key, auth_index, account_id })), [
    { account_key: 'acct-7', auth_index: '7', account_id: 'account-7' },
  ]);
});

test('api-call delegates credential substitution to CLIProxyAPI', () => {
  const payload = createCodexApiCall({ authIndex: '7', accountId: 'acct-1', url: 'https://chatgpt.com/backend-api/wham/usage' });
  assert.equal(payload.auth_index, '7');
  assert.equal(payload.header.Authorization, 'Bearer $TOKEN$');
  assert.equal(payload.header['Chatgpt-Account-Id'], 'acct-1');
});

test('normalizes the quota payload variants accepted by the reference plugin', () => {
  const capturedAt = Date.parse('2026-09-11T12:00:00Z');
  const quota = normalizeCodexQuota({
    rateLimit: {
      primaryWindow: { limitWindowSeconds: 18_000, usedPercent: 25, resetAfterSeconds: 60 },
      secondaryWindow: { limitWindowSeconds: 604_800, usedPercent: 80, resetAt: '2026-09-18T12:00:00Z' },
    },
    rateLimitResetCredits: { applicableAvailableCount: 3, credits: [] },
  }, { capturedAt });
  assert.deepEqual(quota.windows.map((window) => [window.short, window.duration_minutes, window.remaining_percent]), [[true, 300, 75], [false, 10080, 20]]);
  assert.equal(quota.windows[0].reset_at, '2026-09-11T12:01:00.000Z');
  assert.equal(quota.reset_applicable_count, 3);
});

test('requests use same-origin credentials and dispatch host authentication failures', async () => {
  const originalFetch = globalThis.fetch;
  const actions = [];
  const calls = [];
  globalThis.fetch = async (url, options) => {
    calls.push({ url, options });
    return {
      ok: false,
      status: 401,
      async json() {
        return { ok: false, error: { code: 'unauthorized', message: 'unauthorized' } };
      },
    };
  };
  try {
    await assert.rejects(
      request('/status', {
        base: '/v0/management/plugins/window-reset',
        dispatch: (action) => actions.push(action),
      }),
      (error) => error.status === 401,
    );
  } finally {
    globalThis.fetch = originalFetch;
  }
  assert.equal(calls[0].url, '/v0/management/plugins/window-reset/status');
  assert.equal(calls[0].options.credentials, 'same-origin');
  assert.equal(calls[0].options.headers['Content-Type'], 'application/json');
  assert.equal(actions[0].type, 'host-auth-required');
  assert.equal(actions[0].status, 401);
});

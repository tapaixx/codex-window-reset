import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { createCodexApiCall, deriveManagementBase, request } from '../modules/api.js';

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

test('api-call delegates credential substitution to CLIProxyAPI', () => {
  const payload = createCodexApiCall({ authIndex: '7', accountId: 'acct-1', url: 'https://chatgpt.com/backend-api/wham/usage' });
  assert.equal(payload.auth_index, '7');
  assert.equal(payload.header.Authorization, 'Bearer $TOKEN$');
  assert.equal(payload.header['Chatgpt-Account-Id'], 'acct-1');
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

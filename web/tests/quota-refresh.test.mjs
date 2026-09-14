import test from 'node:test';
import assert from 'node:assert/strict';
import * as api from '../modules/api.js';

const account = (key) => ({ account_key: key, auth_index: key });

test('host request deadline covers a stalled response body and aborts the fetch', async (t) => {
  let signal;
  t.mock.method(globalThis, 'fetch', async (_url, options) => {
    signal = options.signal;
    return { ok: true, status: 200, json: () => new Promise(() => {}) };
  });
  await assert.rejects(api.hostManagementRequest('/api-call', { timeoutMs: 20 }), { code: 'request_timeout' });
  assert.equal(signal.aborted, true);
});

test('manual refresh sends all accounts, including disabled ones, to the plugin cache endpoint', async (t) => {
  const calls = [];
  t.mock.method(globalThis, 'fetch', async (url, options) => {
    calls.push({ url, options });
    return { ok: true, status: 200, json: async () => ({ ok: true, result: [
      { stale: false, snapshot: { account_key: 'enabled', captured_at: new Date().toISOString() } },
      { stale: false, snapshot: { account_key: 'disabled', captured_at: new Date().toISOString() } },
    ] }) };
  });
  const result = await api.refreshAccountQuotas([account('enabled'), { ...account('disabled'), disabled: true }]);
  assert.deepEqual(result, { succeeded: 2, failed: 0, resetFailed: 0 });
  assert.equal(calls.length, 1);
  assert.match(calls[0].url, /quota\/refresh$/u);
  assert.deepEqual(JSON.parse(calls[0].options.body), { account_keys: ['enabled', 'disabled'] });
});

test('plugin refresh errors are reported per account while preserving successful snapshots', async (t) => {
  const updates = [];
  t.mock.method(globalThis, 'fetch', async () => ({ ok: true, status: 200, json: async () => ({ ok: true, result: [
    { stale: false, snapshot: { account_key: 'a', captured_at: new Date().toISOString() } },
    { stale: true, refresh_error_code: 'quota_refresh_failed', snapshot: { account_key: 'b', captured_at: '2026-09-13T12:00:00.000Z' } },
  ] }) }));
  const result = await api.refreshAccountQuotas([account('a'), account('b')], { onUpdate: (update) => updates.push(update) });
  assert.deepEqual(result, { succeeded: 1, failed: 1, resetFailed: 0 });
  assert.equal(updates.length, 2);
  assert.equal(updates[0].view.snapshot.account_key, 'a');
  assert.equal(updates[1].view.refresh_error_code, 'quota_refresh_failed');
});

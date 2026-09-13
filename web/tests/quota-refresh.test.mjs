import test from 'node:test';
import assert from 'node:assert/strict';
import * as api from '../modules/api.js';

const usage = { rate_limit: { primary_window: { limit_window_seconds: 18000, used_percent: 20 } } };
const reply = (body, status = 200) => ({ ok: true, status: 200, json: async () => ({ status_code: status, body }) });
const account = (key) => ({ account_key: key, auth_index: key });
const deferred = () => { let resolve; const promise = new Promise((done) => { resolve = done; }); return { promise, resolve }; };

test('host request deadline covers a stalled response body and aborts the fetch', async (t) => {
  let signal;
  t.mock.method(globalThis, 'fetch', async (_url, options) => {
    signal = options.signal;
    return { ok: true, status: 200, json: () => new Promise(() => {}) };
  });
  await assert.rejects(api.hostManagementRequest('/api-call', { timeoutMs: 20 }), { code: 'request_timeout' });
  assert.equal(signal.aborted, true);
});

test('usage becomes visible before reset details or another account finish', async (t) => {
  const resetGate = deferred(), slowGate = deferred(), usageVisible = deferred();
  const updates = [];
  t.mock.method(globalThis, 'fetch', async (_url, options) => {
    const call = JSON.parse(options.body);
    if (call.auth_index === 'slow') return slowGate.promise;
    if (call.url.endsWith('/usage')) return reply(usage);
    return resetGate.promise;
  });
  const refreshing = api.refreshAccountQuotas([account('fast'), account('slow')], {
    onUpdate: (update) => { updates.push(update); if (update.phase === 'usage') usageVisible.resolve(); },
  });
  try {
    await usageVisible.promise;
    assert.equal(updates.length, 1);
    assert.equal(updates[0].accountKey, 'fast');
    assert.equal(updates[0].view.snapshot.windows[0].remaining_percent, 80);
    assert.equal(updates[0].view.snapshot.reset_info_complete, false);
  } finally {
    slowGate.resolve(reply({}, 503));
    resetGate.resolve(reply({ applicable_available_count: 2 }));
  }
  assert.deepEqual(await refreshing, { succeeded: 1, failed: 1, resetFailed: 0 });
  assert.equal(updates.find((update) => update.phase === 'complete').view.snapshot.reset_applicable_count, 2);
});

test('unavailable reset details do not discard successfully refreshed usage', async (t) => {
  const updates = [];
  t.mock.method(globalThis, 'fetch', async (_url, options) => {
    const call = JSON.parse(options.body);
    return call.url.endsWith('/usage') ? reply(JSON.stringify(usage)) : new Promise(() => {});
  });
  const result = await api.refreshAccountQuotas([account('a')], { resetTimeoutMs: 20, onUpdate: (update) => updates.push(update) });
  assert.deepEqual(result, { succeeded: 1, failed: 0, resetFailed: 1 });
  assert.equal(updates.at(-1).view.stale, false);
  assert.equal(updates.at(-1).view.snapshot.reset_info_complete, false);
  assert.match(updates.at(-1).view.reset_refresh_error, /超时/);
});

test('quota requests use bounded concurrency and include disabled accounts', async (t) => {
  let active = 0, peak = 0;
  const started = [], gates = [];
  t.mock.method(globalThis, 'fetch', async (_url, options) => {
    const call = JSON.parse(options.body);
    started.push(call.auth_index);
    active++; peak = Math.max(peak, active);
    const gate = deferred(); gates.push(gate);
    setImmediate(() => gate.resolve());
    await gate.promise;
    active--;
    return call.url.endsWith('/usage') ? reply(usage) : reply({ applicable_available_count: 0 });
  });
  const result = await api.refreshAccountQuotas([...Array.from({ length: 8 }, (_, i) => account(String(i))), { ...account('disabled'), disabled: true }]);
  assert.ok(peak <= 3, `concurrent requests: ${peak}`);
  assert.equal(result.succeeded, 9);
  assert.ok(started.includes('disabled'));
});

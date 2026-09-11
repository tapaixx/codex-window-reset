import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { createStore } from '../modules/state.js';

test('restore changes returns to the last server revision', () => {
  const store = createStore({ schedule: { revision: 7, enabled: false } });
  store.dispatch({ type: 'edit-schedule', patch: { enabled: true } });
  store.dispatch({ type: 'restore-schedule' });
  assert.deepEqual(store.getState().draftSchedule, { revision: 7, enabled: false });
});

test('Action Selection does not mutate Scheduled Account membership', () => {
  const store = createStore({
    schedule: { revision: 3, scheduled_account_keys: ['acct-scheduled'] },
  });
  store.dispatch({ type: 'set-action-selection', keys: ['acct-action'] });
  assert.deepEqual(store.getState().selectedAccountKeys, ['acct-action']);
  assert.deepEqual(store.getState().draftSchedule.scheduled_account_keys, ['acct-scheduled']);
});

test('quota refresh is an explicit state action and production code has no polling timer', async () => {
  const store = createStore();
  store.dispatch({ type: 'quota-refresh-started', keys: ['acct-a'] });
  assert.deepEqual(store.getState().quotaRefresh, { loading: true, keys: ['acct-a'] });
  store.dispatch({ type: 'quota-refreshed', value: [] });
  assert.equal(store.getState().quotaRefresh.loading, false);

  const modules = await Promise.all([
    'api.js', 'state.js', 'accounts.js', 'schedule.js', 'simulator.js', 'history.js', 'main.js',
  ].map((name) => readFile(new URL(`../modules/${name}`, import.meta.url), 'utf8')));
  assert.equal(modules.some((source) => /\bsetInterval\s*\(/.test(source)), false);
});

test('reset submission generates one UUID and preserves it on a retry', () => {
  const originalCrypto = globalThis.crypto;
  const originalRandomUUID = globalThis.crypto.randomUUID;
  let uuidCalls = 0;
  globalThis.crypto.randomUUID = () => {
    uuidCalls += 1;
    return '11111111-1111-4111-8111-111111111111';
  };
  try {
    const store = createStore();
    store.dispatch({ type: 'open-reset-intent', accountKey: 'acct-a' });
    const firstKey = store.getState().resetIntent.idempotencyKey;
    store.dispatch({ type: 'reset-retry' });
    store.dispatch({ type: 'reset-submitted' });
    assert.equal(uuidCalls, 1);
    assert.equal(store.getState().resetIntent.idempotencyKey, firstKey);
  } finally {
    globalThis.crypto.randomUUID = originalRandomUUID;
    assert.equal(globalThis.crypto, originalCrypto);
  }
});

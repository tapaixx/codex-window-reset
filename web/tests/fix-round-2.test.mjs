import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { prepareProbeRequest } from '../modules/main.js';

test('scheduled membership is never inferred from every enabled account', async () => {
  const source = await readFile(new URL('../modules/main.js', import.meta.url), 'utf8');
  assert.doesNotMatch(source, /scheduled_account_keys:\s*state\.accounts\.filter/u);
  assert.match(source, /data\.accountScheduled|accountScheduled|scheduledKeys/u);
});

test('panel defaults to masked identities and delegates simulation to the server', async () => {
  const source = await readFile(new URL('../modules/main.js', import.meta.url), 'utf8');
  assert.match(source, /hidden:\s*true/u);
  assert.match(source, /request\(['"]\/simulate['"]/u);
  assert.match(source, /result\.assumptions/u);
  assert.doesNotMatch(source, /buildWindowStrategy\(/u);
});

const accounts = [
  { account_key: 'acct-disabled', disabled: true, unavailable: false },
  { account_key: 'acct-unavailable', disabled: false, unavailable: true },
  { account_key: 'acct-healthy', disabled: false, unavailable: false },
];

test('disabled accounts cannot be selected for a Health Probe', () => {
  const result = prepareProbeRequest({
    accounts,
    selectedAccountKeys: ['acct-disabled'],
    quotaAcknowledged: true,
    unavailableAcknowledged: true,
  });

  assert.equal(result.ok, false);
  assert.equal(result.errorCode, 'account_disabled');
  assert.equal(result.body, null);
});

test('unavailable accounts require an explicit override before Health Probe', () => {
  const blocked = prepareProbeRequest({
    accounts,
    selectedAccountKeys: ['acct-unavailable'],
    quotaAcknowledged: true,
    unavailableAcknowledged: false,
  });
  assert.equal(blocked.ok, false);
  assert.equal(blocked.errorCode, 'account_unavailable');
  assert.equal(blocked.body, null);

  const allowed = prepareProbeRequest({
    accounts,
    selectedAccountKeys: ['acct-unavailable'],
    quotaAcknowledged: true,
    unavailableAcknowledged: true,
  });
  assert.deepEqual(allowed, {
    ok: true,
    errorCode: null,
    body: {
      account_keys: ['acct-unavailable'],
      acknowledge_quota_effect: true,
      allow_unavailable: true,
    },
  });
});

test('ordinary Health Probe payload never inherits an unavailable override', () => {
  const result = prepareProbeRequest({
    accounts,
    selectedAccountKeys: ['acct-healthy'],
    quotaAcknowledged: true,
    unavailableAcknowledged: true,
  });

  assert.deepEqual(result.body, {
    account_keys: ['acct-healthy'],
    acknowledge_quota_effect: true,
    allow_unavailable: false,
  });
});

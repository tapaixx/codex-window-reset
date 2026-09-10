import test from 'node:test';
import assert from 'node:assert/strict';
import { prepareProbeRequest } from '../modules/main.js';

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
  assert.equal(blocked.errorCode, 'unavailable_acknowledgement_required');
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

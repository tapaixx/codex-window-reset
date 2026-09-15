import assert from 'node:assert/strict';
import test from 'node:test';
import { accountStatus, accountStatusDetail, planDisplayLabel, projectAccountRow } from '../modules/dashboard.js';
import { normalizeHostAuthFiles } from '../modules/api.js';

// Captured from a live CLIProxyAPI /v0/management/auth-files response.
const liveAuthFile = {
  auth_index: '58baed010f75dd6e',
  account: 'user@example.com',
  account_type: 'oauth',
  provider: 'codex',
  type: 'codex',
  status: 'active',
  email: 'user@example.com',
  account_id: 'acct_live',
  id_token: { chatgpt_account_id: '541773e7', plan_type: 'team' },
  disabled: false,
  unavailable: false,
};

test('the plan column reads the id_token tier, never the credential type', () => {
  const [file] = normalizeHostAuthFiles({ files: [liveAuthFile] });
  assert.equal(file.plan_label, 'team');
  assert.notEqual(file.plan_label, 'oauth');
  assert.equal(projectAccountRow(file, { quota: {}, history: [] }).plan, 'Team');
});

test('known tiers are cased and unknown ones are passed through, not hidden', () => {
  assert.equal(planDisplayLabel('team'), 'Team');
  assert.equal(planDisplayLabel('plus'), 'Plus');
  assert.equal(planDisplayLabel('go'), 'Go');
  assert.equal(planDisplayLabel('pro'), 'Pro');
  assert.equal(planDisplayLabel('Pro 20x'), 'Pro 20x');
  assert.equal(planDisplayLabel(''), '-');
});

// The panel never polls, so a freshly loaded account has no snapshot. Calling
// that a warning painted every row amber on every load.
test('an account with no snapshot is unknown, not a warning', () => {
  assert.equal(accountStatus({ account_key: 'a' }, undefined), 'unknown');
  assert.equal(accountStatus({ account_key: 'a' }, {}), 'unknown');
});

test('each real problem reports its own condition instead of a bare warning', () => {
  const snapshot = { snapshot: { windows: [] } };
  assert.equal(accountStatus({ disabled: true }, snapshot), 'disabled');
  assert.equal(accountStatus({ unavailable: true }, snapshot), 'unavailable');
  assert.equal(accountStatus({}, { ...snapshot, refresh_error_code: 'quota_refresh_failed' }), 'refresh_failed');
  assert.equal(accountStatus({}, { ...snapshot, stale: true }), 'stale');
  assert.equal(accountStatus({}, snapshot), 'healthy');
});

test('every status can explain itself', () => {
  for (const status of ['healthy', 'unknown', 'stale', 'refresh_failed', 'unavailable', 'disabled']) {
    assert.ok(accountStatusDetail(status).length > 0, `${status} has no explanation`);
  }
});

test('a failed refresh outranks the stale flag so the cause is named', () => {
  const view = { snapshot: { windows: [] }, stale: true, refresh_error_code: 'quota_refresh_failed' };
  assert.equal(accountStatus({}, view), 'refresh_failed');
});

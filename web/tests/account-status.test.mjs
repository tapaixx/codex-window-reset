import assert from 'node:assert/strict';
import test from 'node:test';
import { accountStatus, accountStatusDetail, planDisplayLabel, projectAccountRow, resetCreditSummary } from '../modules/dashboard.js';
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
  assert.equal(accountStatus({}, snapshot), 'healthy');
});

// Every preheat refreshes quota immediately before deciding, so snapshot age
// never blocks one. Flagging it in this column turned every row amber five
// minutes after each load while adding nothing to the timestamp column.
test('an aged snapshot is not an account fault', () => {
  const view = { snapshot: { windows: [] }, stale: true };
  assert.equal(accountStatus({}, view), 'healthy');
});

test('every status can explain itself', () => {
  for (const status of ['healthy', 'unknown', 'refresh_failed', 'unavailable', 'disabled']) {
    assert.ok(accountStatusDetail(status).length > 0, `${status} has no explanation`);
  }
});

test('a failed refresh outranks the stale flag so the cause is named', () => {
  const view = { snapshot: { windows: [] }, stale: true, refresh_error_code: 'quota_refresh_failed' };
  assert.equal(accountStatus({}, view), 'refresh_failed');
});

test('the reset column lists every expiry, oldest first', () => {
  const summary = resetCreditSummary({
    reset_info_complete: true,
    reset_applicable_count: 3,
    reset_credits: [
      { id: 'c2', expires_at: '2026-10-02T00:00:00Z' },
      { id: 'c1', expires_at: '2026-09-23T22:04:23Z' },
      { id: 'c3', expires_at: '2026-11-01T00:00:00Z' },
    ],
  });
  assert.equal(summary.count, '3');
  assert.equal(summary.expiries.length, 3, 'every credit must be listed, not only the soonest');
  assert.deepEqual(summary.expiries, [...summary.expiries].sort(), 'expiries are not oldest first');
  assert.equal(summary.text, `3 · ${summary.expiries.join('、')}`);
  assert.match(summary.title, /共 3 张/);
  assert.equal(summary.title.split('\n').length, 4, 'the title must carry a full timestamp per credit');
});

test('an unsorted credit list is still rendered oldest first', () => {
  const late = resetCreditSummary({ reset_info_complete: true, reset_credits: [{ expires_at: '2026-12-01T00:00:00Z' }, { expires_at: '2026-09-20T00:00:00Z' }] });
  const early = resetCreditSummary({ reset_info_complete: true, reset_credits: [{ expires_at: '2026-09-20T00:00:00Z' }, { expires_at: '2026-12-01T00:00:00Z' }] });
  assert.deepEqual(late.expiries, early.expiries);
  assert.equal(late.expiries.length, 2);
});

test('the reset column stays quiet when there is nothing to expire', () => {
  assert.deepEqual(resetCreditSummary({}), { count: '--', expiries: [], text: '--', title: '' });
  assert.deepEqual(resetCreditSummary({ reset_info_complete: true, reset_credits: [] }), { count: '0', expiries: [], text: '0', title: '' });
  const noExpiry = resetCreditSummary({ reset_info_complete: true, reset_applicable_count: 2, reset_credits: [{ id: 'c' }] });
  assert.equal(noExpiry.text, '2');
  assert.deepEqual(noExpiry.expiries, []);
});

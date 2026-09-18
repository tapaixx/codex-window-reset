import test from 'node:test';
import assert from 'node:assert/strict';

import {
  maskOperationalIdentity,
  projectAccountRow,
  summarizeAccounts,
  summarizeOperations,
} from '../modules/dashboard.js';

const accounts = [
  { account_key: 'acct-1', email: 'alice@example.com', auth_index: '1', account_prefix: 'acct_abc123', plan_label: 'Pro' },
  { account_key: 'acct-2', email: 'bob@example.com', auth_index: '2', account_prefix: 'acct_def456', unavailable: true },
  { account_key: 'acct-3', email: 'carol@example.com', auth_index: '3', account_prefix: 'acct_xyz789', disabled: true },
];

test('account summary and row projection match the monitoring table semantics', () => {
  const history = [{
    account_key: 'acct-1', request_outcome: 'succeeded', http_status: 200, latency_ms: 428,
    finished_at: '2026-09-11T01:02:03Z', window_outcome: 'already_active',
  }];
  const quota = {
    'acct-1': { snapshot: { windows: [{ short: true, remaining_percent: 83, reset_at: '2026-09-11T06:00:00Z' }] } },
  };
  assert.deepEqual(summarizeAccounts(accounts, quota), { total: 3, healthy: 1, unknown: 0, warning: 1, disabled: 1 });
  assert.deepEqual(projectAccountRow(accounts[0], { quota: quota['acct-1'], history, hidden: false }), {
    key: 'acct-1', email: 'alice@example.com', authIndex: '1', accountPrefix: 'acct_abc123', plan: 'Pro',
    status: 'healthy', health: 83, remaining: 83, used: 17, resetAt: '2026-09-11T06:00:00Z',
    httpStatus: 200, latencyMs: 428, lastCheckedAt: '2026-09-11T01:02:03Z', configurationUpdatedAt: '', error: '',
  });
});

// The panel never polls, so "no snapshot yet" is the normal state on load. It
// must not be counted as healthy, and it must not be counted as a warning
// either: painting every account amber on every load is how the column stopped
// carrying information.
test('an account without a cached quota snapshot is unknown, neither healthy nor a warning', () => {
  assert.deepEqual(summarizeAccounts([accounts[0]], {}), { total: 1, healthy: 0, unknown: 1, warning: 0, disabled: 0 });
});

test('operations summary separates scheduled, paused, guardrail, and stale counts', () => {
  const quota = {
    'acct-1': { snapshot: { windows: [{ short: true, remaining_percent: 83 }] }, stale: false },
    'acct-2': { snapshot: { windows: [{ short: true, remaining_percent: 61 }] }, stale: true },
  };
  assert.deepEqual(summarizeOperations({
    accounts,
    quotaByAccount: quota,
    scheduledKeys: new Set(['acct-1', 'acct-3', 'acct-missing']),
    guardrailHoldCount: 2,
  }), { scheduled: 2, healthy: 1, paused: 2, guardrail: 2, stale: 1 });
});

test('identity hiding is session-only and consistently masks all operational identifiers', () => {
  // A short identifier is masked outright: revealing a head and a tail of one
  // gives back most of it, which is how "alex_***.com" beside "a***@qq.com"
  // reassembled a whole address.
  assert.deepEqual(maskOperationalIdentity({ email: 'alice@example.com', authIndex: '17', accountPrefix: 'acct_abcdef' }), {
    email: 'a***@example.com', authIndex: '**', accountPrefix: '***',
  });
  // A long opaque identifier keeps enough to cross-reference during triage.
  assert.deepEqual(maskOperationalIdentity({ email: 'alice@example.com', authIndex: '17', accountPrefix: '541773e7-d581-469c-87fe-eabbca968d01' }), {
    email: 'a***@example.com', authIndex: '**', accountPrefix: '5417***8d01',
  });
});

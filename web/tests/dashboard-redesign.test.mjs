import test from 'node:test';
import assert from 'node:assert/strict';

import {
  buildWindowStrategy,
  groupRunHistory,
  maskOperationalIdentity,
  projectAccountRow,
  summarizeAccounts,
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
  assert.deepEqual(summarizeAccounts(accounts, quota), { total: 3, healthy: 1, warning: 1, disabled: 1 });
  assert.deepEqual(projectAccountRow(accounts[0], { quota: quota['acct-1'], history, hidden: false }), {
    key: 'acct-1', email: 'alice@example.com', authIndex: '1', accountPrefix: 'acct_abc123', plan: 'Pro',
    status: 'healthy', health: 83, remaining: 83, used: 17, resetAt: '2026-09-11T06:00:00Z',
    httpStatus: 200, latencyMs: 428, lastCheckedAt: '2026-09-11T01:02:03Z', configurationUpdatedAt: '', error: '',
  });
});

test('an account without a cached quota snapshot is warning, not falsely healthy', () => {
  assert.deepEqual(summarizeAccounts([accounts[0]], {}), { total: 1, healthy: 0, warning: 1, disabled: 0 });
});

test('identity hiding is session-only and consistently masks all operational identifiers', () => {
  assert.deepEqual(maskOperationalIdentity({ email: 'alice@example.com', authIndex: '17', accountPrefix: 'acct_abcdef' }), {
    email: 'a***@example.com', authIndex: '**', accountPrefix: 'acct_***cdef',
  });
});

test('history records are grouped into one detection run with aggregate outcomes', () => {
  const grouped = groupRunHistory([
    { id: '2', run_id: 'run-9', trigger: 'health_probe', account_key: 'acct-2', started_at: '2026-09-11T01:00:01Z', finished_at: '2026-09-11T01:00:04Z', request_outcome: 'timeout', error_code: 'probe_failed' },
    { id: '1', run_id: 'run-9', trigger: 'health_probe', account_key: 'acct-1', started_at: '2026-09-11T01:00:00Z', finished_at: '2026-09-11T01:00:02Z', request_outcome: 'succeeded' },
  ]);
  assert.equal(grouped.length, 1);
  assert.deepEqual(grouped[0], {
    runId: 'run-9', startedAt: '2026-09-11T01:00:00Z', trigger: 'health_probe', accounts: 2,
    succeeded: 1, failed: 1, durationMs: 4000, message: 'probe_failed', result: 'partial',
  });
});

test('window strategy models repeated five-hour windows, lunch and preheat gain', () => {
  const result = buildWindowStrategy({
    window_hours: 5,
    productivity_minutes: 60,
    preheat_lead_minutes: 30,
    preheat_span_minutes: 15,
    work_periods: [{ start: '09:00', end: '12:00' }, { start: '13:30', end: '19:00' }],
    skip_window_times: [],
  });
  assert.equal(result.workMinutes, 510);
  assert.equal(result.windowMinutes, 300);
  assert.equal(result.windows.length, 2);
  assert.deepEqual(result.windows.map((window) => window.workStart), ['09:00', '14:00']);
  assert.deepEqual(result.windows.map((window) => window.preheatAt), ['08:30', '13:30']);
  assert.ok(result.preheatedAvailableMinutes > result.normalAvailableMinutes);
  assert.equal(result.timeline.length, 24);
});

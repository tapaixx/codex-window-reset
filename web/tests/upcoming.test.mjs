// The panel formats instants in the viewer's local timezone, exactly as
// formatDate does elsewhere, so the day prefix is only meaningful against a
// fixed zone. Pin one before anything formats a date.
process.env.TZ = 'Asia/Shanghai';

import assert from 'node:assert/strict';
import test from 'node:test';
import { describeNextRun } from '../modules/dashboard.js';

const at = (iso) => new Date(iso).getTime();
const now = at('2026-09-15T02:00:00Z');
const batch = (plannedAt, count = 1) => ({
  local_date: '2026-09-15', period_index: 1,
  occurrences: Array.from({ length: count }, (_, index) => ({ account_key: `acct-${index}`, planned_at: new Date(at(plannedAt) + index * 200000).toISOString() })),
});

test('a disabled schedule says so instead of reporting an empty plan', () => {
  const got = describeNextRun({ enabled: false, batches: [] }, now);
  assert.equal(got.state, 'off');
  assert.match(got.text, /计划未启用/);
});

test('enabled with nothing armed is an alarm, not a quiet day', () => {
  const got = describeNextRun({ enabled: true, batches: [] }, now);
  assert.equal(got.state, 'alarm');
  assert.match(got.text, /没有任何待执行的预热/);
});

test('unreadable state never renders as "nothing scheduled"', () => {
  const got = describeNextRun({ enabled: true, store_error_code: 'store_corrupt', batches: [] }, now);
  assert.equal(got.state, 'error');
  assert.match(got.text, /store_corrupt/);
  assert.doesNotMatch(got.text, /没有任何待执行/);
});

test('the next run reports its clock time, batch size and distance', () => {
  const view = { enabled: true, next_run_at: '2026-09-15T02:31:40Z', batches: [batch('2026-09-15T02:31:40Z', 3)] };
  const got = describeNextRun(view, now);
  assert.equal(got.state, 'ready');
  assert.match(got.text, /10:31/);
  assert.match(got.text, /3 个账号/);
  assert.match(got.text, /32 分钟后/);
});

// The shape the v0.0.22 restart defect left behind: timers existed, but the
// soonest was seven days out. A plain "ready" reading would have hidden it.
test('a next run more than a day away is flagged rather than read as normal', () => {
  const view = { enabled: true, next_run_at: '2026-09-21T21:31:40Z', batches: [batch('2026-09-21T21:31:40Z', 3)] };
  const got = describeNextRun(view, now);
  assert.equal(got.state, 'distant');
  assert.match(got.text, /6 天后/);
});

test('same-day and next-day runs are labelled without a full date', () => {
  const today = describeNextRun({ enabled: true, next_run_at: '2026-09-15T09:00:00Z', batches: [batch('2026-09-15T09:00:00Z')] }, now);
  const tomorrow = describeNextRun({ enabled: true, next_run_at: '2026-09-16T09:00:00Z', batches: [batch('2026-09-16T09:00:00Z')] }, now);
  assert.match(today.text, /今天/);
  assert.match(tomorrow.text, /明天/);
});

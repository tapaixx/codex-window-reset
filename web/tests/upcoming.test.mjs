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

// A hot-loaded plugin build leaves the previous instance alive with timers it
// will never act on. Reporting a next run there would be a lie.
test('a superseded instance says so instead of reporting a next run', () => {
  const view = { enabled: true, superseded: true, next_run_at: '2026-09-15T02:31:40Z', batches: [batch('2026-09-15T02:31:40Z', 3)] };
  const got = describeNextRun(view, now);
  assert.equal(got.state, 'error');
  assert.match(got.text, /另一个插件实例已接管调度/);
  assert.doesNotMatch(got.text, /分钟后|小时后/);
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

import { projectUpcomingBatches } from '../modules/dashboard.js';

const accountsFixture = [
  { account_key: 'acct-a', email: 'alpha@example.com', disabled: false },
  { account_key: 'acct-b', email: 'bravo@example.com', disabled: true },
];
const viewFixture = {
  enabled: true,
  next_run_at: '2026-09-15T02:31:40Z',
  batches: [
    { local_date: '2026-09-15', period_index: 1, window_start: '2026-09-15T02:30:00Z', window_end: '2026-09-15T02:40:00Z',
      occurrences: [
        { account_key: 'acct-a', planned_at: '2026-09-15T02:31:40Z' },
        { account_key: 'acct-b', planned_at: '2026-09-15T02:35:00Z', blocked_reason: 'guardrail_hold' },
      ] },
    { local_date: '2026-09-18', period_index: 0, window_start: '2026-09-18T02:30:00Z', window_end: '2026-09-18T02:40:00Z',
      occurrences: [{ account_key: 'acct-a', planned_at: '2026-09-18T02:31:40Z' }] },
  ],
};

test('a batch header carries the window, period, size and distance', () => {
  const [batch] = projectUpcomingBatches(viewFixture, { accounts: accountsFixture, now });
  assert.equal(batch.dayLabel, '今天');
  assert.equal(batch.windowLabel, '10:30–10:40');
  assert.equal(batch.periodLabel, '计划时段 2');
  assert.equal(batch.accountCount, 2);
  assert.match(batch.relative, /分钟后|小时后/);
});

test('account rows carry the staggered time and follow the identity toggle', () => {
  const shown = projectUpcomingBatches(viewFixture, { accounts: accountsFixture, hidden: false, now });
  const masked = projectUpcomingBatches(viewFixture, { accounts: accountsFixture, hidden: true, now });
  assert.equal(shown[0].occurrences[0].email, 'alpha@example.com');
  assert.equal(masked[0].occurrences[0].email, 'a***@example.com');
  assert.equal(shown[0].occurrences[0].time, '10:31');
  assert.equal(shown[0].occurrences[1].time, '10:35');
});

test('only conditions that are already true are reported as blocking', () => {
  const [batch] = projectUpcomingBatches(viewFixture, { accounts: accountsFixture, now });
  assert.equal(batch.occurrences[0].blocked, '');
  assert.match(batch.occurrences[1].blocked, /Guardrail Hold/);
  const noHold = { ...viewFixture, batches: [{ ...viewFixture.batches[0], occurrences: [{ account_key: 'acct-b', planned_at: '2026-09-15T02:35:00Z' }] }] };
  assert.match(projectUpcomingBatches(noHold, { accounts: accountsFixture, now })[0].occurrences[0].blocked, /账号已停用/);
});

test('batches beyond tomorrow are marked so the default view can fold them', () => {
  const [near, far] = projectUpcomingBatches(viewFixture, { accounts: accountsFixture, now });
  assert.equal(near.near, true);
  assert.equal(far.near, false);
  assert.equal(far.dayLabel, '09/18'); // zh-CN separator, matching formatDate elsewhere in the panel
});

test('an unknown account key still renders a row rather than vanishing', () => {
  const orphan = { enabled: true, batches: [{ local_date: '2026-09-15', period_index: 0, window_start: '2026-09-15T02:30:00Z', window_end: '2026-09-15T02:40:00Z', occurrences: [{ account_key: 'acct-gone', planned_at: '2026-09-15T02:31:40Z' }] }] };
  const [batch] = projectUpcomingBatches(orphan, { accounts: accountsFixture, now });
  assert.equal(batch.occurrences.length, 1);
  assert.equal(batch.occurrences[0].email, 'acct-gone');
});

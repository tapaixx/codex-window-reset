import test from 'node:test';
import assert from 'node:assert/strict';
import { createStore } from '../modules/state.js';
import { serializeScheduleDraft, validateScheduleDraft } from '../modules/schedule.js';
import { isResetQuotaEligible } from '../modules/main.js';
import {
  accountStatusValue,
  bindAccountRevealControl,
  latestAccountHistoryRecord,
  safeAccountIdentityProjection,
} from '../modules/accounts.js';
import { formatObservedQuotaLine, timelineSegmentClock } from '../modules/simulator.js';

const CONFIG_KEYS = [
  'schema_version', 'revision', 'enabled', 'timezone', 'weekdays', 'work_periods',
  'preheat_lead_minutes', 'preheat_span_minutes', 'productivity_minutes',
  'remaining_quota_floor_percent', 'remaining_window_floor_minutes',
  'long_window_floor_percent', 'blackout_periods', 'probe_model',
  'probe_timeout_seconds', 'scheduled_account_keys',
];

const capturedAt = Date.parse('2026-09-10T12:00:00.000Z');

test('schedule serialization emits only Config keys and nested period arrays', () => {
  const serialized = serializeScheduleDraft({
    schema_version: 1,
    revision: 7,
    enabled: true,
    timezone: 'Asia/Shanghai',
    weekdays: [1, 2, 3, 4, 5],
    work_periods: [{ start: '09:00', end: '12:00' }],
    'work_periods[0].start': 'synthetic',
    'blackout_periods[0].end': 'synthetic',
    preheat_lead_minutes: 20,
    preheat_span_minutes: 10,
    productivity_minutes: 60,
    remaining_quota_floor_percent: 20,
    remaining_window_floor_minutes: 60,
    long_window_floor_percent: 10,
    blackout_periods: [{ start: '12:00', end: '13:00' }],
    probe_model: 'gpt-5.6-luna',
    probe_timeout_seconds: 30,
    scheduled_account_keys: ['acct-a'],
    extra_field: 'must be omitted',
  });

  assert.deepEqual(Object.keys(serialized).sort(), [...CONFIG_KEYS].sort());
  assert.deepEqual(serialized.work_periods, [{ start: '09:00', end: '12:00' }]);
  assert.deepEqual(serialized.blackout_periods, [{ start: '12:00', end: '13:00' }]);
  assert.equal(Object.keys(serialized).some((key) => /(?:work|blackout)_periods\[\d+\]\./u.test(key)), false);
});

test('schedule validation allows an enabled schedule with no selected accounts', () => {
  const errors = validateScheduleDraft({
    enabled: true,
    timezone: 'Asia/Shanghai',
    probe_model: 'gpt-5.6-luna',
    preheat_lead_minutes: 120,
    preheat_span_minutes: 60,
    work_periods: [{ start: '09:00', end: '12:00' }, { start: '13:30', end: '19:00' }],
    scheduled_account_keys: [],
  });
  assert.deepEqual(errors, []);
});

test('reset eligibility requires a successful snapshot no older than five minutes', () => {
  const account = { account_key: 'acct-a', unavailable: false, disabled: false };
  const base = {
    stale: false,
    snapshot: { captured_at: new Date(capturedAt - 4 * 60 * 1000).toISOString(), reset_info_complete: true, reset_applicable_count: 1 },
  };

  assert.equal(isResetQuotaEligible(account, base, capturedAt), true);
  assert.equal(isResetQuotaEligible(account, null, capturedAt), false);
  assert.equal(isResetQuotaEligible(account, { ...base, refresh_error_code: 'quota_refresh_failed' }, capturedAt), false);
  assert.equal(isResetQuotaEligible(account, { ...base, stale: true }, capturedAt), false);
  assert.equal(
    isResetQuotaEligible(account, { ...base, snapshot: { ...base.snapshot, captured_at: new Date(capturedAt - 5 * 60 * 1000 - 1).toISOString() } }, capturedAt),
    false,
  );
  assert.equal(
    isResetQuotaEligible(account, { ...base, snapshot: { ...base.snapshot, captured_at: new Date(capturedAt + 1).toISOString() } }, capturedAt),
    false,
  );
  assert.equal(isResetQuotaEligible({ ...account, unavailable: true }, base, capturedAt), false);
  assert.equal(isResetQuotaEligible(account, { ...base, snapshot: { ...base.snapshot, reset_applicable_count: 0 } }, capturedAt), false);
});

test('failed quota refresh marks the preserved snapshot ineligible', () => {
  const store = createStore({ quota: [{ snapshot: { account_key: 'acct-a', reset_info_complete: true } }] });
  store.dispatch({ type: 'quota-refresh-failed', keys: ['acct-a'], error: 'quota_refresh_failed' });
  assert.equal(store.getState().quotaByAccount['acct-a'].refresh_error_code, 'quota_refresh_failed');
});

test('Reveal/Mask invokes the session callback and reveals only safe stable metadata', () => {
  const listeners = [];
  const button = { addEventListener(type, listener) { if (type === 'click') listeners.push(listener); } };
  const calls = [];
  bindAccountRevealControl(button, 'acct-safe', (key) => calls.push(key));
  listeners[0]();
  assert.deepEqual(calls, ['acct-safe']);

  const projection = safeAccountIdentityProjection({
    account_key: 'acct-safe',
    masked_identity: 's***@example.com',
    fingerprint: 'a1b2c3d4e5f6',
    identity: 'raw@example.com',
  }, true);
  assert.equal(projection.identity, 's***@example.com');
  assert.equal(projection.accountKey, 'acct-safe');
  assert.equal(projection.fingerprint, 'a1b2c3d4e5f6');
  assert.doesNotMatch(projection.identity, /raw@example\.com/u);
});

test('refreshed run status is merged into the current run', () => {
  const store = createStore();
  store.dispatch({ type: 'run-started', value: { run_id: 'run-1' } });
  store.dispatch({ type: 'status-loaded', value: { run_id: 'run-1', run_total: 4, run_completed: 2 } });
  assert.deepEqual(store.getState().currentRun, {
    run_id: 'run-1',
    run_total: 4,
    run_completed: 2,
    loading: true,
  });
});

test('unavailable accounts are explicit and never reset-eligible', () => {
  const quota = {
    stale: false,
    snapshot: { captured_at: new Date(capturedAt - 60 * 1000).toISOString(), reset_info_complete: true },
  };
  assert.equal(accountStatusValue({ unavailable: true }, quota)[0], 'Unavailable');
  assert.notEqual(accountStatusValue({ unavailable: true }, quota)[0], 'Healthy');
  assert.equal(isResetQuotaEligible({ account_key: 'acct-a', unavailable: true }, quota, capturedAt), false);
});

test('latest history selection uses the newest matching record', () => {
  const oldRecord = { account_key: 'acct-a', finished_at: '2026-09-10T10:00:00Z', request_outcome: 'timeout' };
  const newestRecord = { account_key: 'acct-a', finished_at: '2026-09-10T11:00:00Z', request_outcome: 'succeeded' };
  assert.equal(latestAccountHistoryRecord([oldRecord, newestRecord], 'acct-a'), newestRecord);
  const appendedWithoutDates = [{ account_key: 'acct-a', request_outcome: 'timeout' }, { account_key: 'acct-a', request_outcome: 'succeeded' }];
  assert.equal(latestAccountHistoryRecord(appendedWithoutDates, 'acct-a'), appendedWithoutDates[1]);
});

test('Asia/Shanghai timeline placement and labels use the configured timezone', () => {
  const clock = timelineSegmentClock({ start: '2026-09-10T01:00:00Z', end: '2026-09-10T02:30:00Z' }, 'Asia/Shanghai');
  assert.equal(clock.startMinutes, 9 * 60);
  assert.equal(clock.endMinutes, 10 * 60 + 30);
  assert.equal(clock.startLabel, '09:00');
  assert.equal(clock.endLabel, '10:30');
  const overnight = timelineSegmentClock({ start: '2026-09-10T15:30:00Z', end: '2026-09-10T16:30:00Z' }, 'Asia/Shanghai');
  assert.equal(overnight.startMinutes, 23 * 60 + 30);
  assert.equal(overnight.endMinutes, 24 * 60 + 30);
});

test('observed quota includes window values and applicable reset credits', () => {
  const line = formatObservedQuotaLine({
    stale: false,
    snapshot: {
      account_key: 'acct-a',
      windows: [
        { short: true, duration_minutes: 60, remaining_percent: 81 },
        { short: false, duration_minutes: 180, remaining_percent: 42 },
      ],
      reset_applicable_count: 2,
    },
  });
  assert.match(line, /Short 60 min: 81%/u);
  assert.match(line, /Long 180 min: 42%/u);
  assert.match(line, /applicable reset credits: 2/u);
});

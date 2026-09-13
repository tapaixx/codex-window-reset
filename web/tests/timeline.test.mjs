import test from 'node:test';
import assert from 'node:assert/strict';
import { simulationRange } from '../modules/timeline.js';
import * as timeline from '../modules/timeline.js';
import { simulationFixture } from './simulation-fixture.mjs';

test('focused axis includes work and preheat with hour padding instead of an entire day', () => {
  assert.equal(typeof timeline.simulationDomain, 'function');
  assert.deepEqual(timeline.simulationDomain(simulationFixture, { timezone: 'Asia/Shanghai', work_periods: [{ start: '09:00', end: '19:00' }] }), { start: 300, end: 1200 });
});

test('focused axis stays inside the local day and has a safe empty fallback', () => {
  assert.equal(typeof timeline.simulationDomain, 'function');
  assert.deepEqual(timeline.simulationDomain({}, { work_periods: [{ start: '00:15', end: '23:45' }] }), { start: 0, end: 1440 });
  assert.deepEqual(timeline.simulationDomain({}, {}), { start: 0, end: 1440 });
});

test('a zero-duration preheat instant is never stretched into a band ending at midnight', () => {
  assert.equal(simulationRange('2026-09-14T06:30:00+08:00', '2026-09-14T06:30:00+08:00', 'Asia/Shanghai'), null);
});

test('positive intervals reaching local midnight end at 24:00', () => {
  const range = simulationRange('2026-09-14T15:00:00Z', '2026-09-14T16:00:00Z', 'Asia/Shanghai');
  assert.equal(range.label, '23:00–24:00');
  assert.ok(Math.abs(range.width - 100 / 24) < 0.000001);
  assert.ok(Math.abs(range.left + range.width - 100) < 0.000001);
});

test('invalid and reversed intervals do not corrupt the time axis', () => {
  for (const [start, end] of [[null, null], ['bad', 'bad'], ['2026-09-14T10:00Z', '2026-09-14T09:00Z']]) {
    assert.equal(simulationRange(start, end, 'Asia/Shanghai'), null);
  }
});

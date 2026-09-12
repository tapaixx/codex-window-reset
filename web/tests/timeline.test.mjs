import test from 'node:test';
import assert from 'node:assert/strict';
import { simulationRange } from '../modules/timeline.js';

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

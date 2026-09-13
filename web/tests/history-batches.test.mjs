import test from 'node:test';
import assert from 'node:assert/strict';
import * as dashboard from '../modules/dashboard.js';

test('staggered preheats and compensation share the scheduled date and period batch', () => {
  const batches = dashboard.groupHistoryBatches([
    { id: 'a', trigger: 'preheat', occurrence_id: '2026-09-14/p0/acct-a', account_key: 'a', started_at: '2026-09-14T08:15:00+08:00' },
    { id: 'b', trigger: 'preheat', occurrence_id: '2026-09-14/p0/acct-b', account_key: 'b', started_at: '2026-09-14T08:20:00+08:00' },
    { id: 'c', trigger: 'compensation', occurrence_id: '2026-09-14/p0/acct-a', account_key: 'a', started_at: '2026-09-14T08:25:00+08:00' },
  ]);
  assert.equal(batches.length, 1);
  assert.equal(batches[0].records.length, 3);
  assert.equal(batches[0].accountCount, 2);
  assert.equal(batches[0].startedAt, '2026-09-14T08:15:00+08:00');
});

test('different periods, dates and manual runs stay separate even at identical execution times', () => {
  const batches = dashboard.groupHistoryBatches([
    { id: 'a', trigger: 'preheat', occurrence_id: '2026-09-14/p0/acct-a' },
    { id: 'b', trigger: 'preheat', occurrence_id: '2026-09-14/p1/acct-b' },
    { id: 'c', trigger: 'preheat', occurrence_id: '2026-09-15/p0/acct-c' },
    { id: 'd', trigger: 'health_probe', run_id: 'manual-1' },
    { id: 'e', trigger: 'health_probe', run_id: 'manual-1' },
    { id: 'f', trigger: 'health_probe', run_id: 'manual-2' },
    { id: 'g', trigger: 'preheat' }, { id: 'h', trigger: 'preheat' },
  ]);
  assert.equal(batches.length, 7);
  assert.equal(batches.find((batch) => batch.key === 'manual/manual-1').records.length, 2);
});

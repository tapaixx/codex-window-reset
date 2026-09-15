import assert from 'node:assert/strict';
import test from 'node:test';
import { describeOperation, groupHistoryBatches, requestWasSent } from '../modules/dashboard.js';

const labels = {
  probeOutcomes: { succeeded: '成功', timeout: '超时', disabled: '已停用' },
  windowOutcomes: { verified_started: '已开窗', not_observed: '未观察' },
  decisions: { proceed: '判定需要预热', window_active: '窗口已在运行，无法再开新窗口', sufficient_window: '窗口已在运行（旧记录：额度充足）', guardrail_hold: 'Guardrail Hold：长窗口余量过低' },
};

// Captured verbatim from a live deployment: the scheduler fired on time and
// every account was skipped on purpose, but the panel rendered it as
// "请求：disabled · 窗口：not_observed · HTTP -- · 0 ms" and dropped the one
// field that explained it.
const skippedRecord = {
  trigger: 'preheat',
  occurrence_id: '2026-09-15/p1/acct-4264646886b92e66',
  account_key: 'acct-4264646886b92e66',
  masked_identity: 'c***@gmail.com',
  started_at: '2026-09-15T02:31:40.033437089Z',
  finished_at: '2026-09-15T02:31:41.423035602Z',
  request_outcome: 'disabled',
  window_outcome: 'not_observed',
  decision: 'sufficient_window',
  latency_ms: 0,
};

test('a skipped operation says no request was sent and why', () => {
  const parts = describeOperation(skippedRecord, labels);
  assert.deepEqual(parts, ['未发送请求', '窗口已在运行（旧记录：额度充足）']);
  assert.equal(requestWasSent(skippedRecord), false);
});

test('a skip never shows a request outcome, window outcome or HTTP status', () => {
  const text = describeOperation(skippedRecord, labels).join(' · ');
  for (const leak of ['disabled', 'not_observed', 'HTTP', '已停用', '未观察']) {
    assert.ok(!text.includes(leak), `${leak} leaked into a skipped operation: ${text}`);
  }
});

test('an executed operation still reports outcome, window and status', () => {
  const executed = { ...skippedRecord, request_outcome: 'succeeded', window_outcome: 'verified_started', decision: 'proceed', http_status: 200, latency_ms: 1240 };
  assert.deepEqual(describeOperation(executed, labels), ['请求：成功', '窗口：已开窗', 'HTTP 200']);
  assert.equal(requestWasSent(executed), true);
});

test('a guardrail hold is stated once, not as a decision plus an identical code', () => {
  const held = { ...skippedRecord, decision: 'guardrail_hold', error_code: 'guardrail_hold' };
  assert.deepEqual(describeOperation(held, labels), ['未发送请求', 'Guardrail Hold：长窗口余量过低']);
});

test('an error code that adds information is kept', () => {
  const failed = { ...skippedRecord, request_outcome: 'timeout', window_outcome: 'not_observed', decision: 'proceed', error_code: 'probe_failed' };
  assert.ok(describeOperation(failed, labels).includes('probe_failed'));
});

test('a batch counts its skipped operations so it cannot be read as a failure', () => {
  const [batch] = groupHistoryBatches([skippedRecord, { ...skippedRecord, account_key: 'acct-two', masked_identity: 'a***@qq.com' }]);
  assert.equal(batch.records.length, 2);
  assert.equal(batch.skipped, 2);
  const [mixed] = groupHistoryBatches([skippedRecord, { ...skippedRecord, account_key: 'acct-two', request_outcome: 'succeeded', decision: 'proceed' }]);
  assert.equal(mixed.skipped, 1);
});

// History written before the decision was renamed still has to read sensibly.
test('the retired sufficient_window value is still labelled', () => {
  const parts = describeOperation({ ...skippedRecord, decision: 'sufficient_window' }, labels);
  assert.ok(parts[1].includes('窗口已在运行'), parts.join(' · '));
});

test('a window-active skip names the reason a request would be useless', () => {
  const parts = describeOperation({ ...skippedRecord, decision: 'window_active' }, labels);
  assert.deepEqual(parts, ['未发送请求', '窗口已在运行，无法再开新窗口']);
});

test('an unknown decision falls back to its raw value rather than disappearing', () => {
  const future = { ...skippedRecord, decision: 'some_new_decision' };
  assert.ok(describeOperation(future, labels).includes('some_new_decision'));
});

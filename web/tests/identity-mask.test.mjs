import assert from 'node:assert/strict';
import test from 'node:test';
import { maskOperationalIdentity, projectAccountRow } from '../modules/dashboard.js';
import { normalizeHostAuthFiles } from '../modules/api.js';

// Captured from a live /v0/management/auth-files response. There is no
// account_id at the top level; account is the address itself and id embeds it.
const liveAuthFile = {
  provider: 'codex',
  auth_index: '58baed010f75dd6e',
  email: 'alex_nnn@qq.com',
  account: 'alex_nnn@qq.com',
  id: 'codex-03c103fb-alex_nnn@qq.com-team.json',
  id_token: { chatgpt_account_id: '541773e7-d581-469c-87fe-eabbca968d01', plan_type: 'team' },
  disabled: false,
  unavailable: false,
};

test('the account identifier comes from the id_token, never from account or id', () => {
  const [file] = normalizeHostAuthFiles({ files: [liveAuthFile] });
  assert.equal(file.account_prefix, '541773e7-d581-469c-87fe-eabbca968d01');
  assert.ok(!file.account_prefix.includes('@'), 'an address reached the account identifier');
  assert.ok(!file.account_prefix.includes('alex'), 'the local part reached the account identifier');
});

// The cell showed "a***@qq.com" above "** · alex_***.com": the head and tail of
// the prefix mask gave back the local part and the domain, so the masked
// address beside it could be reassembled and the masking meant nothing.
test('a masked row never reveals enough to reassemble the address', () => {
  const [file] = normalizeHostAuthFiles({ files: [liveAuthFile] });
  const row = projectAccountRow(file, { quota: {}, history: [], hidden: true });
  const shown = `${row.email} ${row.authIndex} ${row.accountPrefix}`;
  assert.equal(row.email, 'a***@qq.com');
  for (const secret of ['alex_nnn', 'alex_', 'nnn', '.com']) {
    assert.ok(!row.accountPrefix.includes(secret), `account prefix leaked ${secret}: ${row.accountPrefix}`);
  }
  assert.ok(!shown.includes('alex'), `masked row leaked the local part: ${shown}`);
});

test('an address that still reaches the identifier is masked as an address', () => {
  const masked = maskOperationalIdentity({ email: 'alex_nnn@qq.com', authIndex: '58baed', accountPrefix: 'alex_nnn@qq.com' });
  assert.equal(masked.accountPrefix, 'a***@qq.com');
  assert.ok(!masked.accountPrefix.includes('alex_'), 'the head of the address survived');
});

test('an opaque identifier stays useful for cross-referencing', () => {
  const masked = maskOperationalIdentity({ email: 'a@b.com', authIndex: '7', accountPrefix: '541773e7-d581-469c-87fe-eabbca968d01' });
  assert.match(masked.accountPrefix, /^5417\*\*\*/u);
  assert.ok(masked.accountPrefix.length < '541773e7-d581-469c-87fe-eabbca968d01'.length);
});

test('showing identities still shows the real values', () => {
  const [file] = normalizeHostAuthFiles({ files: [liveAuthFile] });
  const row = projectAccountRow(file, { quota: {}, history: [], hidden: false });
  assert.equal(row.email, 'alex_nnn@qq.com');
  assert.equal(row.accountPrefix, '541773e7-d581-469c-87fe-eabbca968d01');
});

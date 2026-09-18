import assert from 'node:assert/strict';
import test from 'node:test';

// The host manager derives its obfuscation key from the page's own host, so the
// panel has to stand in the same place to read it back.
globalThis.location = { host: 'cpa-plus.example.test', pathname: '/v0/resource/plugins/codex-window-reset/panel' };
Object.defineProperty(globalThis, 'navigator', { value: { userAgent: 'TestAgent/1.0' }, configurable: true });
globalThis.localStorage = {
  store: new Map(),
  getItem(name) { return this.store.has(name) ? this.store.get(name) : null; },
  setItem(name, value) { this.store.set(name, value); },
};

const { managementKey, managementKeyProblem, managerEnvelopeVersion, requestErrorMessage } = await import('../modules/api.js');

const NAMESPACE = 'cli-proxy-api-webui::secure-storage';
// Mirrors the manager's own writer: XOR against the versioned key, then base64.
function seal(version, plain) {
  const secret = version === 'v2'
    ? `${NAMESPACE}|v2|${globalThis.location.host}`
    : `${NAMESPACE}|${globalThis.location.host}|${globalThis.navigator.userAgent}`;
  const key = new TextEncoder().encode(secret);
  const body = new TextEncoder().encode(plain);
  let out = '';
  for (let i = 0; i < body.length; i += 1) out += String.fromCharCode(body[i] ^ key[i % key.length]);
  return `enc::${version}::${btoa(out)}`;
}

test('a v2 envelope is decoded, which is what the manager writes now', () => {
  globalThis.localStorage.store.clear();
  globalThis.localStorage.setItem('cli-proxy-auth', seal('v2', JSON.stringify({ managementKey: 'cpamp_live_key' })));
  assert.equal(managementKey(), 'cpamp_live_key');
  assert.equal(managementKeyProblem(), '');
});

test('the previously supported v1 envelope still works', () => {
  globalThis.localStorage.store.clear();
  globalThis.localStorage.setItem('cli-proxy-auth', seal('v1', JSON.stringify({ state: { managementKey: 'cpamp_v1_key' } })));
  assert.equal(managementKey(), 'cpamp_v1_key');
});

test('a plain stored key is still accepted', () => {
  globalThis.localStorage.store.clear();
  globalThis.localStorage.setItem('managementKey', 'cpamp_plain');
  assert.equal(managementKey(), 'cpamp_plain');
});

// Sending the envelope verbatim produced "Authorization: Bearer enc::v2::..."
// and a 401 that read as though the Operator's key had changed.
test('an envelope this panel cannot read is never sent as the key', () => {
  globalThis.localStorage.store.clear();
  globalThis.localStorage.setItem('cli-proxy-auth', 'enc::v9::c29tZXRoaW5nCg==');
  assert.equal(managementKey(), '');
  assert.equal(managerEnvelopeVersion('enc::v9::x'), 'v9');
  assert.equal(managementKeyProblem(), 'unsupported_storage_v9');
});

test('a 401 names the storage format instead of blaming the key', () => {
  globalThis.localStorage.store.clear();
  globalThis.localStorage.setItem('cli-proxy-auth', 'enc::v9::c29tZXRoaW5nCg==');
  const message = requestErrorMessage({ status: 401 });
  assert.match(message, /存储格式/);
  assert.match(message, /v9/);

  globalThis.localStorage.store.clear();
  globalThis.localStorage.setItem('cli-proxy-auth', seal('v2', JSON.stringify({ managementKey: 'ok' })));
  assert.match(requestErrorMessage({ status: 401 }), /管理密钥无效或已变更/);
});

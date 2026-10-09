import test from 'node:test';
import assert from 'node:assert/strict';
import { generateKeyPairSync } from 'node:crypto';
import { mkdtemp, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { parseArgs, privateKeyDetector, run, commitRecoveryGate, isExpiredRetainedProposal } from './browser.mjs';

test('browser smoke is read-only unless --publish is explicit', () => {
  const args = ['--dir', '/private/tmp/mobile-fixture', '--origin', 'https://phone.example'];
  assert.equal(parseArgs(args).publish, false);
  assert.equal(parseArgs([...args, '--publish']).publish, true);
  for (const origin of ['http://phone.example', 'https://evil@phone.example', 'https://phone.example/path', 'https://phone.example/#pair=secret']) {
    assert.throws(() => parseArgs(['--dir', '/private/tmp/mobile-fixture', '--origin', origin]));
  }
  assert.throws(() => parseArgs([...args, '--unknown']));
});

test('outbound inspection detects raw and encoded private keys without matching signatures', () => {
  const { privateKey } = generateKeyPairSync('ed25519');
  const pem = privateKey.export({ format: 'pem', type: 'pkcs8' });
  const der = privateKey.export({ format: 'der', type: 'pkcs8' });
  const seed = Buffer.from(privateKey.export({ format: 'jwk' }).d, 'base64url');
  const detects = privateKeyDetector(pem);
  for (const value of [pem, der, der.toString('base64'), seed, seed.toString('base64'), seed.toString('base64url'), seed.toString('hex'), JSON.stringify({ key: pem }), encodeURIComponent(pem)]) {
    assert.equal(detects(value), true, 'Known private-key transport encoding was missed');
  }
  assert.equal(detects(JSON.stringify({ id: 'public-operation', signature: 'only-a-signature' })), false);
  assert.equal(detects(null), false);
});

test('read-only service preflight never loads a key, browser, or mutating request', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'croptop-smoke-preflight-test-'));
  const origin = 'https://phone.example';
  // The key path intentionally does not exist. Preflight must not try to read it.
  await writeFile(join(dir, 'fixture.json'), JSON.stringify({ kind: 'croptop-isolated-mobile-smoke-v1', host: 'https://crop.top', ipns: 'public-test-identity', keyFile: join(dir, 'site-key.pem'), imageFile: join(dir, 'synthetic.png') }));
  const savedFetch = globalThis.fetch;
  let reads = 0;
  globalThis.fetch = async (url, options) => {
    assert.equal(url, origin + '/v0/mobile/config');
    assert.equal(options.method, undefined);
    assert.equal(options.body, undefined);
    reads++;
    return Response.json({ origin, host: 'crop.top', enabled: true });
  };
  try {
    const result = await run({ dir, origin, publish: false });
    assert.equal(result.mode, 'read-only-preflight');
    assert.equal(reads, 1);
    await assert.rejects(() => run({ dir, origin, publish: true }), /Publish the initial empty fixture/);
    assert.equal(reads, 2);
  } finally { globalThis.fetch = savedFetch; }
});

const origin = 'https://phone.example';
const operationID = '407651CE-0000-4000-8000-000000000001';
const commitURL = origin + '/v0/mobile/operations/' + operationID + '/commit';
const nextTurn = () => new Promise(resolve => setImmediate(resolve));
function routeFixture({ url = commitURL, method = 'POST', status = 202, fetch } = {}) {
  const events = [];
  const response = { status: () => status, dispose: async () => { events.push('dispose'); } };
  return {
    events, response,
    request: () => ({ method: () => method, url: () => url }),
    fetch: async options => {
      assert.equal(options.maxRedirects, 0, 'Authorization cannot follow redirects');
      events.push('dispatch');
      return fetch ? fetch(response) : response;
    },
    abort: async code => { assert.equal(code, 'failed'); events.push('withhold'); },
    fulfill: async options => { assert.equal(options.response, response); events.push('deliver'); },
  };
}

test('commit recovery waits for service acceptance, not local committing state or intercepted dispatch', async () => {
  const gate = commitRecoveryGate(origin, operationID);
  let received;
  const route = routeFixture({ fetch: response => new Promise(resolve => { received = () => resolve(response); }) });
  let readyToReload = false;
  gate.accepted.then(() => { readyToReload = true; });
  // This represents the page having durably saved "committing" before fetch.
  await nextTurn();
  assert.equal(readyToReload, false);
  const handling = gate.handle(route);
  await nextTurn();
  assert.deepEqual(route.events, ['dispatch']);
  assert.equal(readyToReload, false, 'Even dispatch is not proof the service accepted the request');
  received();
  await handling;
  assert.deepEqual(await gate.accepted, { status: 202, responseWithheld: true });
  assert.deepEqual(route.events, ['dispatch', 'withhold', 'dispose']);
  assert.equal(readyToReload, true);
});

test('commit recovery matches only the exact origin, retained operation and POST method', async () => {
  const gate = commitRecoveryGate(origin, operationID);
  for (const options of [{ method: 'GET' }, { url: commitURL.replace('phone.example', 'other.example') }, { url: commitURL.replace(operationID, 'another-operation') }, { url: commitURL + '?other=1' }]) {
    const route = routeFixture(options);
    assert.equal(await gate.handle(route), false);
    assert.deepEqual(route.events, []);
  }
  gate.close();
  await assert.rejects(gate.accepted, /interrupted/);
});

test('commit recovery allows one session refresh but withholds only accepted responses', async () => {
  const gate = commitRecoveryGate(origin, operationID);
  const expiredSession = routeFixture({ status: 401 });
  assert.equal(await gate.handle(expiredSession), true);
  assert.deepEqual(expiredSession.events, ['dispatch', 'deliver', 'dispose']);
  const acceptedCommit = routeFixture({ status: 200 });
  await gate.handle(acceptedCommit);
  assert.deepEqual(await gate.accepted, { status: 200, responseWithheld: true });
  assert.deepEqual(acceptedCommit.events, ['dispatch', 'withhold', 'dispose']);
});

test('rejected and uncertain commits never permit the reload-success path or expose response details', async () => {
  for (const status of [302, 403, 409, 500]) {
    const gate = commitRecoveryGate(origin, operationID);
    const route = routeFixture({ status });
    await gate.handle(route);
    await assert.rejects(gate.accepted, new RegExp('not accepted \\(HTTP ' + status + '\\)'));
    assert.deepEqual(route.events, ['dispatch', 'deliver', 'dispose']);
  }
  const gate = commitRecoveryGate(origin, operationID);
  const route = routeFixture({ fetch: () => { throw new Error('sensitive transport details that must never be printed'); } });
  await gate.handle(route);
  await assert.rejects(gate.accepted, error => {
    assert.equal(error.message, 'Commit transport is uncertain; retain the same directory/profile and check this operation before retrying');
    return true;
  });
});

test('startup permits only the exact retained expired proposal to use normal re-preparation', () => {
  const ipns = 'dedicated-smoke-site';
  const draft = { id: operationID, ipns, submitted: true, operation: { id: operationID, postID: operationID, ipns, state: 'needs_signature', proposal: { expiresAt: 1000 } } };
  assert.equal(isExpiredRetainedProposal(draft, operationID, ipns, 1000), true);
  assert.equal(isExpiredRetainedProposal(draft, operationID, ipns, 999), false);
  assert.equal(isExpiredRetainedProposal(draft, undefined, ipns, 1001), false);
  assert.equal(isExpiredRetainedProposal(draft, 'different-id', ipns, 1001), false);
  assert.equal(isExpiredRetainedProposal(draft, operationID, 'different-site', 1001), false);
  for (const change of [{ submitted: false }, { operation: { ...draft.operation, state: 'committing' } }, { operation: { ...draft.operation, id: 'different-id' } }, { operation: { ...draft.operation, proposal: { expiresAt: '1000' } } }]) {
    assert.equal(isExpiredRetainedProposal({ ...draft, ...change }, operationID, ipns, 1001), false);
  }
});

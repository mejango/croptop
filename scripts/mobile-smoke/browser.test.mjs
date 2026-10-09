import test from 'node:test';
import assert from 'node:assert/strict';
import { generateKeyPairSync } from 'node:crypto';
import { mkdtemp, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { parseArgs, privateKeyDetector, run } from './browser.mjs';

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

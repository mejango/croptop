import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { webcrypto, createECDH, createHash, hkdfSync, createCipheriv, randomBytes } from 'node:crypto';
import test from 'node:test';

if (!globalThis.crypto) Object.defineProperty(globalThis, 'crypto', { value: webcrypto });
const source = await readFile(new URL('./pairing.js', import.meta.url), 'utf8');
const { derivePairingKeys, pairingTranscript, receivePairing } = await import(`data:text/javascript;base64,${Buffer.from(source).toString('base64')}`);
const fixture = JSON.parse(await readFile(new URL('../../testdata/mobile-pairing-v1.json', import.meta.url), 'utf8'));
const bytes = value => Buffer.from(value, 'base64');

test('WebCrypto pairing uses the Go/Swift/Android transcript, code and authenticated envelope', async () => {
  const pub = bytes(fixture.info.receiverPublicKey);
  const key = await crypto.subtle.importKey('jwk', {
    kty: 'EC', crv: 'P-256', d: bytes(fixture.receiverPrivateKey).toString('base64url'),
    x: pub.subarray(1, 33).toString('base64url'), y: pub.subarray(33).toString('base64url'),
    ext: false, key_ops: ['deriveBits'],
  }, { name: 'ECDH', namedCurve: 'P-256' }, false, ['deriveBits']);
  assert.equal(new TextDecoder().decode(pairingTranscript(fixture.info)), fixture.transcript);
  const secrets = await derivePairingKeys(key, fixture.info);
  assert.equal(secrets.code, fixture.confirmationCode);
  assert.equal(secrets.key.extractable, false);
  const decrypt = info => crypto.subtle.decrypt({ name: 'AES-GCM', iv: bytes(fixture.nonce),
    additionalData: pairingTranscript(info), tagLength: 128 }, secrets.key, bytes(fixture.ciphertext));
  assert.equal(new TextDecoder().decode(await decrypt(fixture.info)), fixture.plaintext);
  await assert.rejects(decrypt({ ...fixture.info, origin: 'https://attacker.example' }));
  await assert.rejects(decrypt({ ...fixture.info, ipns: fixture.info.ipns + 'x' }));
});

test('phone receiver erases the capability URL, confirms before consuming, and receives only ciphertext', async () => {
  const previous = { location: globalThis.location, history: globalThis.history, fetch: globalThis.fetch };
  const id = 'B'.repeat(43), capability = 'C'.repeat(43), origin = 'https://app.crop.top';
  globalThis.location = { hash: `#pair=${id}.${capability}`, origin, pathname: '/', search: '' };
  globalThis.history = { replaceState(_state, _title, path) { assert.equal(path, '/'); globalThis.location.hash = ''; } };
  const sender = createECDH('prime256v1'); sender.generateKeys();
  let envelope, expectedCode, didConfirm = false, requestCount = 0;
  const value = JSON.parse(fixture.plaintext);
  globalThis.fetch = async (url, options) => {
    requestCount++;
    assert.equal(globalThis.location.hash, '');
    assert.equal(options.credentials, 'omit');
    assert.equal(options.cache, 'no-store');
    assert.equal(options.referrerPolicy, 'no-referrer');
    assert.equal(options.redirect, 'error');
    assert.equal(options.headers['X-Croptop-Pairing'], capability);
    assert.ok(!options.body.includes('PRIVATE KEY'));
    const input = JSON.parse(options.body);
    if (url.endsWith('/claim')) {
      assert.equal(didConfirm, false);
      const info = { id, origin, ipns: value.ipns, senderPublicKey: sender.getPublicKey().toString('base64'),
        receiverPublicKey: input.receiverPublicKey, expiresAt: Math.floor(Date.now() / 1000) + 600, state: 'claimed' };
      const transcript = pairingTranscript(info), secret = sender.computeSecret(bytes(info.receiverPublicKey));
      const salt = createHash('sha256').update(transcript).digest();
      const key = Buffer.from(hkdfSync('sha256', secret, salt, 'croptop-pairing-key-v1', 32));
      expectedCode = String(Buffer.from(hkdfSync('sha256', secret, salt, 'croptop-pairing-code-v1', 4)).readUInt32BE() % 100000000).padStart(8, '0');
      const nonce = randomBytes(12), cipher = createCipheriv('aes-256-gcm', key, nonce);
      cipher.setAAD(transcript);
      const ciphertext = Buffer.concat([cipher.update(JSON.stringify(value)), cipher.final(), cipher.getAuthTag()]);
      envelope = { ...info, nonce: nonce.toString('base64'), ciphertext: ciphertext.toString('base64'), state: 'consumed' };
      return new Response(JSON.stringify(info));
    }
    assert.ok(url.endsWith('/consume'));
    assert.equal(didConfirm, true);
    assert.deepEqual(input, { confirmed: true });
    return new Response(JSON.stringify(envelope));
  };
  try {
    const received = await receivePairing({ onConfirm: async info => {
      assert.equal(info.code, expectedCode);
      assert.equal(info.ipns, value.ipns);
      didConfirm = true;
      return true;
    } });
    assert.deepEqual(received, { pem: value.pem, ipns: value.ipns, name: value.name, origin });
    assert.equal(requestCount, 2);
  } finally {
    globalThis.location = previous.location; globalThis.history = previous.history; globalThis.fetch = previous.fetch;
  }
});

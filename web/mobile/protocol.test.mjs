import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { importSiteKey, signChallenge, validateProposal, base64, signProposal } from './protocol.js';

const fixture = JSON.parse(await readFile(new URL('../../docs/design/mobile-protocol-fixture.json', import.meta.url)));
const image = new Blob(['test normalized image'], { type: 'image/png' });
const draft = { id: 'C8DD533A-A87D-4C36-9719-253EA72C71CC', ipns: fixture.ipns, title: 'A thought', caption: 'Hello' };
const operation = {
  ...draft, postID: draft.id, mediaType: image.type,
  mediaSHA256: createHash('sha256').update(await image.bytes()).digest('hex'),
  proposal: { id: 'test-proposal', cid: fixture.cid, parent: fixture.cid, sequence: fixture.sequence, host: fixture.host, time: fixture.time, expiresAt: fixture.expiresAt, recordPayload: fixture.recordPayload, pushPayload: fixture.pushPayload },
};
const config = { host: fixture.host };
test('browser key import derives Go IPNS identity and leaves a non-exportable key', async () => {
  const connection = await importSiteKey(fixture.privateKeyPEM);
  assert.equal(connection.ipns, fixture.ipns);
  assert.equal(connection.key.extractable, false);
  await assert.rejects(crypto.subtle.exportKey('pkcs8', connection.key));
  assert.equal(await signChallenge(connection, fixture.sessionChallenge, fixture.origin, fixture.time), fixture.sessionChallenge.signature);
});
test('all signed bytes conform to the shared Go fixture', async () => {
  const connection = await importSiteKey(fixture.privateKeyPEM);
  const verified = await validateProposal(operation, draft, config, image, fixture.time);
  assert.equal(base64(await crypto.subtle.sign('Ed25519', connection.key, verified.record)), fixture.recordSignature);
  assert.equal(base64(await crypto.subtle.sign('Ed25519', connection.key, verified.push)), fixture.pushSignature);
});
test('rejects changed destination, draft, normalized media, host, expiry, sequence and push', async () => {
  for (const changed of [
    { ipns: 'other-site' }, { title: 'not my title' }, { postID: 'other-post' }, { mediaSHA256: '0'.repeat(64) },
    { proposal: { ...operation.proposal, host: 'evil.example' } },
    { proposal: { ...operation.proposal, expiresAt: fixture.time - 1 } },
    { proposal: { ...operation.proposal, sequence: '43' } },
    { proposal: { ...operation.proposal, sequence: '042' } },
    { proposal: { ...operation.proposal, pushPayload: base64(new TextEncoder().encode('sign anything')) } },
  ]) await assert.rejects(validateProposal({ ...operation, ...changed }, draft, config, image, fixture.time));
});
test('rejects record TTL/value/type changes, nonminimal, reordered, truncated and trailing CBOR', async () => {
  const payload = Buffer.from(fixture.recordPayload, 'base64');
  const variants = [
    Buffer.concat([payload, Buffer.from([0])]), payload.subarray(0, -1),
    Buffer.from(payload.toString('hex').replace('0df8475800', '0df8475801'), 'hex'),
    Buffer.from(payload.toString('hex').replace('6853657175656e6365182a', '6853657175656e636519002a'), 'hex'),
    Buffer.from(payload.toString('hex').replace('56616c69646974795479706500', '56616c69646974795479706501'), 'hex'),
    Buffer.from(payload.toString('hex').replace('2f697066732f', '2f69706e732f'), 'hex'),
  ];
  // Reorder the first two entries while keeping all the original values.
  variants.push(Buffer.concat([payload.subarray(0, 16), payload.subarray(29, 102), payload.subarray(16, 29), payload.subarray(102)]));
  for (const variant of variants) {
    assert.notDeepEqual(variant, payload, 'mutation must actually change bytes');
    await assert.rejects(validateProposal({ ...operation, proposal: { ...operation.proposal, recordPayload: variant.toString('base64') } }, draft, config, image, fixture.time));
  }
});
test('challenge audience and identity are checked before signing', async () => {
  const connection = await importSiteKey(fixture.privateKeyPEM);
  await assert.rejects(signChallenge(connection, fixture.sessionChallenge, 'https://other.example', fixture.time));
  await assert.rejects(signChallenge({ ...connection, ipns: 'other-site' }, fixture.sessionChallenge, fixture.origin, fixture.time));
  await assert.rejects(signChallenge(connection, fixture.sessionChallenge, fixture.origin, fixture.sessionChallenge.expiresAt));
});

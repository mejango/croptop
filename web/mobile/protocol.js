// The only browser module allowed to sign service requests. Every payload is
// reconstructed locally before signing; API data is never a signing oracle.
const encoder = new TextEncoder();
const decoder = new TextDecoder('utf-8', { fatal: true });
const RECORD_PREFIX = encoder.encode('ipns-signature:');
const MAX_SEQUENCE = (1n << 63n) - 1n;
export const bytes = value => encoder.encode(value);
export const base64 = value => btoa(String.fromCharCode(...new Uint8Array(value)));
export function unbase64(value) {
  if (typeof value !== 'string' || !/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/.test(value)) throw new Error('Invalid signing payload encoding.');
  return Uint8Array.from(atob(value), c => c.charCodeAt(0));
}
function equal(a, b) { return a.length === b.length && a.every((v, i) => v === b[i]); }
function join(...items) {
  const result = new Uint8Array(items.reduce((n, item) => n + item.length, 0));
  let offset = 0;
  for (const item of items) { result.set(item, offset); offset += item.length; }
  return result;
}
function ipnsForPublicKey(raw) {
  const identity = join(Uint8Array.of(1, 0x72, 0, 36, 8, 1, 18, 32), raw);
  let number = 0n;
  for (const byte of identity) number = number * 256n + BigInt(byte);
  return 'k' + number.toString(36);
}
export async function importSiteKey(pem) {
  if (!crypto.subtle) throw new Error('This browser cannot protect your site key. Open Croptop in an up-to-date Safari or Chrome over HTTPS.');
  if (typeof pem !== 'string' || pem.length > 16384) throw new Error('Choose an Ed25519 site-key PEM file.');
  const match = pem.trim().match(/^-----BEGIN PRIVATE KEY-----\s+([\sA-Za-z0-9+/=]+)\s+-----END PRIVATE KEY-----$/);
  if (!match) throw new Error('Choose the PRIVATE KEY PEM exported by your Croptop publisher.');
  const der = unbase64(match[1].replace(/\s/g, ''));
  try {
    // WebCrypto exposes the derived public x coordinate only on JWK export.
    // This transient key is never persisted; the stored key is non-exportable.
    const temporary = await crypto.subtle.importKey('pkcs8', der, 'Ed25519', true, ['sign']);
    const jwk = await crypto.subtle.exportKey('jwk', temporary);
    const publicBytes = Uint8Array.from(atob(jwk.x.replace(/-/g, '+').replace(/_/g, '/') + '='), c => c.charCodeAt(0));
    if (publicBytes.length !== 32 || jwk.crv !== 'Ed25519') throw new Error('Invalid Ed25519 key.');
    const key = await crypto.subtle.importKey('pkcs8', der, 'Ed25519', false, ['sign']);
    delete jwk.d;
    return { ipns: ipnsForPublicKey(publicBytes), key };
  } catch (error) {
    throw new Error('This key could not be opened. Use an Ed25519 Croptop site key and a current Safari or Chrome browser.', { cause: error });
  } finally { der.fill(0); }
}
export async function signChallenge(connection, challenge, origin, now = Math.floor(Date.now() / 1000)) {
  const parsed = new URL(origin);
  if (parsed.origin !== origin || (parsed.protocol !== 'https:' && !(parsed.protocol === 'http:' && ['localhost', '127.0.0.1', '[::1]'].includes(parsed.hostname)))) throw new Error('The publishing service must use HTTPS.');
  if (!challenge || !/^[A-Za-z0-9_-]{16,256}$/.test(challenge.id) || !Number.isSafeInteger(challenge.expiresAt) || challenge.expiresAt <= now || challenge.expiresAt > now + 600) throw new Error('The connection challenge expired or is invalid. Check your phone’s date and try again.');
  const expected = `croptop-mobile-session\n${origin}\n${connection.ipns}\n${challenge.id}\n${challenge.expiresAt}`;
  if (challenge.message !== expected) throw new Error('The connection challenge does not match this site and service.');
  return base64(await crypto.subtle.sign('Ed25519', connection.key, bytes(expected)));
}

// Minimal canonical DAG-CBOR for the five fields of an IPNS v2 record. Reject
// every other data type, duplicate field, indefinite length, or extra byte.
function header(type, value) {
  const n = BigInt(value);
  if (n < 24n) return Uint8Array.of((type << 5) | Number(n));
  const size = n <= 255n ? 1 : n <= 65535n ? 2 : n <= 4294967295n ? 4 : 8;
  const out = new Uint8Array(size + 1);
  out[0] = (type << 5) | ({ 1: 24, 2: 25, 4: 26, 8: 27 })[size];
  for (let i = size; i > 0; i--) out[i] = Number((n >> BigInt((size - i) * 8)) & 255n);
  return out;
}
const cborString = value => join(header(3, bytes(value).length), bytes(value));
const cborBytes = value => join(header(2, value.length), value);
function parseRecord(payload) {
  if (!equal(payload.slice(0, RECORD_PREFIX.length), RECORD_PREFIX)) throw new Error('Invalid IPNS signing domain.');
  let offset = RECORD_PREFIX.length;
  function readHeader(expected) {
    if (offset >= payload.length) throw new Error('Truncated IPNS record.');
    const first = payload[offset++];
    if (first >> 5 !== expected) throw new Error('Unexpected IPNS field type.');
    let length = BigInt(first & 31);
    if (length >= 24n) {
      const count = ({ 24: 1, 25: 2, 26: 4, 27: 8 })[Number(length)];
      if (!count || offset + count > payload.length) throw new Error('Invalid IPNS field length.');
      length = 0n;
      for (let i = 0; i < count; i++) length = (length << 8n) | BigInt(payload[offset++]);
    }
    return length;
  }
  function string(type) {
    const length = readHeader(type);
    if (length > 1024n || offset + Number(length) > payload.length) throw new Error('Invalid IPNS field length.');
    const data = payload.slice(offset, offset + Number(length));
    offset += Number(length);
    return type === 3 ? decoder.decode(data) : data;
  }
  if (readHeader(5) !== 5n) throw new Error('Unexpected IPNS fields.');
  const fields = Object.create(null);
  for (let i = 0; i < 5; i++) {
    const key = string(3);
    if (key in fields || !['TTL', 'Value', 'Sequence', 'Validity', 'ValidityType'].includes(key)) throw new Error('Unexpected IPNS field.');
    fields[key] = ['Value', 'Validity'].includes(key) ? string(2) : readHeader(0);
  }
  if (offset !== payload.length) throw new Error('Extra IPNS record data.');
  const canonical = join(RECORD_PREFIX, header(5, 5), ...['TTL', 'Value', 'Sequence', 'Validity', 'ValidityType'].flatMap(key => [cborString(key), fields[key] instanceof Uint8Array ? cborBytes(fields[key]) : header(0, fields[key])]));
  if (!equal(canonical, payload)) throw new Error('Noncanonical IPNS record.');
  return fields;
}
function validCID(value) {
  // Croptop publishes CIDv1 lowercase base32 roots. Decode the multiformat
  // envelope as well so a plausible-looking arbitrary string is not accepted.
  if (typeof value !== 'string' || !/^b[a-z2-7]{10,200}$/.test(value)) return false;
  let bits = 0, buffer = 0;
  const decoded = [];
  for (const c of value.slice(1)) {
    buffer = (buffer << 5) | 'abcdefghijklmnopqrstuvwxyz234567'.indexOf(c);
    bits += 5;
    if (bits >= 8) { bits -= 8; decoded.push((buffer >>> bits) & 255); }
  }
  if (bits && (buffer & ((1 << bits) - 1)) !== 0) return false;
  return decoded.length === 36 && decoded[0] === 1 && [0x55, 0x70, 0x71].includes(decoded[1]) && decoded[2] === 0x12 && decoded[3] === 32;
}
export async function validateProposal(operation, draft, config, normalizedImage, now = Math.floor(Date.now() / 1000)) {
  const proposal = operation?.proposal;
  if (operation?.id !== draft.id || operation.postID !== draft.id || operation.ipns !== draft.ipns || operation.title !== draft.title || operation.caption !== draft.caption) throw new Error('The prepared post does not match your saved draft. Nothing was signed.');
  if (!proposal || !validCID(proposal.cid) || !validCID(proposal.parent) || typeof proposal.id !== 'string' || !proposal.id || proposal.id.length > 256 || !/^(0|[1-9][0-9]{0,18})$/.test(proposal.sequence) || BigInt(proposal.sequence) > MAX_SEQUENCE) throw new Error('The publishing proposal is invalid. Nothing was signed.');
  if (!config.host || proposal.host !== config.host || /[\s/:]/.test(proposal.host)) throw new Error('The proposal points to an unexpected publishing host.');
  if (!Number.isSafeInteger(proposal.time) || !Number.isSafeInteger(proposal.expiresAt) || proposal.time > now + 60 || proposal.time < now - 600 || proposal.expiresAt <= now || proposal.expiresAt > proposal.time + 600) throw new Error('This publishing preview expired. Prepare it again.');
  if (!(normalizedImage instanceof Blob) || !['image/png', 'image/jpeg', 'image/webp'].includes(operation.mediaType) || normalizedImage.type.split(';')[0] !== operation.mediaType) throw new Error('The normalized preview is not a supported image.');
  const digest = new Uint8Array(await crypto.subtle.digest('SHA-256', await normalizedImage.arrayBuffer()));
  const hash = Array.from(digest, b => b.toString(16).padStart(2, '0')).join('');
  if (hash !== operation.mediaSHA256) throw new Error('The preview failed its integrity check. Nothing was signed.');
  const record = unbase64(proposal.recordPayload);
  const fields = parseRecord(record);
  const validity = decoder.decode(fields.Validity);
  const validityMS = Date.parse(validity);
  if (!equal(fields.Value, bytes('/ipfs/' + proposal.cid)) || fields.Sequence !== BigInt(proposal.sequence) || fields.ValidityType !== 0n || fields.TTL !== 60000000000n) throw new Error('The IPNS update does not match this post. Nothing was signed.');
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,9})?Z$/.test(validity) || !Number.isFinite(validityMS) || new Date(validityMS).toISOString().slice(0, 19) !== validity.slice(0, 19) || validityMS <= proposal.expiresAt * 1000 || validityMS > (proposal.time + 7200 * 3600 + 60) * 1000) throw new Error('The IPNS update has an invalid validity period.');
  const push = unbase64(proposal.pushPayload);
  const expectedPush = bytes(`croptop-push\n${proposal.host}\n${draft.ipns}\n${proposal.cid}\n${proposal.sequence}\n${proposal.time}`);
  if (!equal(push, expectedPush)) throw new Error('The host update does not match this post. Nothing was signed.');
  return { record, push };
}
export async function signProposal(connection, operation, draft, config, normalizedImage) {
  if (connection.ipns !== draft.ipns) throw new Error('Reconnect the original site before publishing this draft.');
  const { record, push } = await validateProposal(operation, draft, config, normalizedImage);
  return {
    proposalId: operation.proposal.id,
    recordSignature: base64(await crypto.subtle.sign('Ed25519', connection.key, record)),
    pushSignature: base64(await crypto.subtle.sign('Ed25519', connection.key, push)),
  };
}

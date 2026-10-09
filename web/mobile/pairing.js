// Cross-client pairing contract: docs/design/mobile-pairing.md.
const encoder = new TextEncoder();
const encode64 = bytes => btoa(String.fromCharCode(...new Uint8Array(bytes)));
const decode64 = text => Uint8Array.from(atob(text), c => c.charCodeAt(0));

export function hasPairingLink() {
  return /^#pair=[A-Za-z0-9_-]{43}\.[A-Za-z0-9_-]{43}$/.test(location.hash);
}

export function pairingTranscript(info) {
  return encoder.encode(['croptop-pairing-v1', info.origin, info.id, info.ipns,
    String(info.expiresAt), info.senderPublicKey, info.receiverPublicKey].join('\n'));
}

export async function derivePairingKeys(privateKey, info) {
  const pub = await crypto.subtle.importKey('raw', decode64(info.senderPublicKey),
    { name: 'ECDH', namedCurve: 'P-256' }, false, []);
  const secret = await crypto.subtle.deriveBits({ name: 'ECDH', public: pub }, privateKey, 256);
  const root = await crypto.subtle.importKey('raw', secret, 'HKDF', false, ['deriveBits', 'deriveKey']);
  const salt = await crypto.subtle.digest('SHA-256', pairingTranscript(info));
  const algorithm = infoText => ({ name: 'HKDF', hash: 'SHA-256', salt, info: encoder.encode(infoText) });
  const key = await crypto.subtle.deriveKey(algorithm('croptop-pairing-key-v1'), root,
    { name: 'AES-GCM', length: 256 }, false, ['decrypt']);
  const codeBytes = await crypto.subtle.deriveBits(algorithm('croptop-pairing-code-v1'), root, 32);
  const code = String(new DataView(codeBytes).getUint32(0) % 100000000).padStart(8, '0');
  return { key, code };
}

// onConfirm({code,ipns,origin,expiresAt}) must resolve true only after the person
// confirms the destination and matching code. No key import happens before it.
export async function receivePairing({ onStatus = () => {}, onConfirm, signal } = {}) {
  if (!hasPairingLink()) throw new Error('Open a new Connect phone link from your computer.');
  const [id, capability] = location.hash.slice(6).split('.');
  history.replaceState(null, '', location.pathname + location.search);
  if (typeof onConfirm !== 'function') throw new Error('Connection confirmation is required.');
  const origin = location.origin;
  if (!isTrustedPairingOrigin(origin)) throw new Error('Open Croptop using HTTPS.');
  const keys = await crypto.subtle.generateKey({ name: 'ECDH', namedCurve: 'P-256' }, false, ['deriveBits']);
  const receiverPublicKey = encode64(await crypto.subtle.exportKey('raw', keys.publicKey));
  const request = async (action, body) => {
    const response = await fetch(`${origin}/v0/mobile/pairings/${id}/${action}`, {
      method: 'POST', credentials: 'omit', cache: 'no-store', referrerPolicy: 'no-referrer', redirect: 'error', signal,
      headers: { 'Content-Type': 'application/json', 'X-Croptop-Pairing': capability },
      body: JSON.stringify(body),
    });
    const data = await response.json();
    if (!response.ok) {
      const error = new Error(data.error || 'Could not connect this device.');
      error.code = data.code;
      throw error;
    }
    return data;
  };
  onStatus('Connecting securely…');
  const info = await request('claim', { receiverPublicKey });
  validatePairingInfo(info, { origin, id, receiverPublicKey });
  const { key, code } = await derivePairingKeys(keys.privateKey, info);
  const confirmed = await onConfirm({ code, ipns: info.ipns, origin, expiresAt: info.expiresAt });
  if (!confirmed) throw new Error('Connection cancelled. Start a new link to try again.');
  onStatus('Confirm the matching code on your computer.');
  while (Date.now() < info.expiresAt * 1000) {
    signal?.throwIfAborted();
    let envelope;
    try {
      envelope = await request('consume', { confirmed: true });
    } catch (error) {
      if (error.code !== 'pairing_pending') throw error;
      await new Promise((resolve, reject) => {
        const finish = () => { signal?.removeEventListener('abort', abort); resolve(); };
        const timer = setTimeout(finish, 1500);
        const abort = () => { clearTimeout(timer); reject(signal.reason || new Error('Connection cancelled.')); };
        signal?.addEventListener('abort', abort, { once: true });
      });
      continue;
    }
    validatePairingInfo(envelope, { origin, id, receiverPublicKey });
    if (encode64(pairingTranscript(envelope)) !== encode64(pairingTranscript(info))) {
      throw new Error('Connection details changed. Start a new link.');
    }
    const nonce = decode64(envelope.nonce);
    if (nonce.length !== 12) throw new Error('Invalid encrypted connection.');
    const plaintext = await crypto.subtle.decrypt({ name: 'AES-GCM', iv: nonce,
      additionalData: pairingTranscript(info), tagLength: 128 }, key, decode64(envelope.ciphertext));
    const value = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(plaintext));
    if (value.version !== 1 || value.origin !== origin || value.ipns !== info.ipns ||
        typeof value.pem !== 'string' || value.pem.length > 1024 || typeof value.name !== 'string') {
      throw new Error('Connection key did not match this site.');
    }
    // The owning key-import module must derive and compare this expected IPNS
    // before storing its non-exportable key and marking the device connected.
    return { pem: value.pem, ipns: value.ipns, name: value.name, origin };
  }
  throw new Error('The connection expired. Start a new Connect phone link.');
}

function isTrustedPairingOrigin(origin) {
  const url = new URL(origin);
  return url.protocol === 'https:' || (url.protocol === 'http:' &&
    ['localhost', '127.0.0.1', '[::1]'].includes(url.hostname));
}

function validatePairingInfo(info, expected) {
  const now = Math.floor(Date.now() / 1000);
  if (info.origin !== expected.origin || info.id !== expected.id ||
      info.receiverPublicKey !== expected.receiverPublicKey || !/^k51[a-z0-9]+$/.test(info.ipns) ||
      !Number.isSafeInteger(info.expiresAt) || info.expiresAt <= now || info.expiresAt > now + 660 ||
      decode64(info.senderPublicKey).length !== 65) {
    throw new Error('This connection link is invalid or expired.');
  }
}

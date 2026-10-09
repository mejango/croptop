# Phone pairing v1

All requests use the service origin and `/v0/mobile/pairings`. HTTPS is required except loopback development. The existing publisher authenticates with its site session. The relay never receives the private key or ECDH secret. It keeps bounded, ephemeral entries in memory; a restart requires pairing again. Entries expire after ten minutes. Phone posting and drafts do not depend on retaining a pairing entry.

## Transport

All binary fields are standard padded base64. `id` and `capability` are independent random 32-byte base64url (unpadded) values. Only the capability SHA-256 is stored. Secrets stay out of URL query strings, logs and referrers.

1. Sender generates ephemeral P-256 ECDH and calls authenticated `POST /pairings` with `{senderPublicKey}`. The public key is 65-byte uncompressed SEC1. Response: `{id, capability, origin, ipns, senderPublicKey, expiresAt, state:"waiting"}`. The phone link is `<origin>/#pair=<id>.<capability>`. Native apps accept the same pasted link. An OS association may hand this link to a native app when configured.
2. Phone removes the fragment from browser history immediately, generates its own ephemeral P-256 key, then calls `POST /pairings/{id}/claim` with `X-Croptop-Pairing: <capability>` and `{receiverPublicKey}`. Response: `{id,origin,ipns,senderPublicKey,receiverPublicKey,expiresAt,state:"claimed"}`. The first valid receiver wins; retry with the same key is idempotent, a different key is rejected. Phone validates the exact configured origin, expiry, ID and both public keys.
3. Sender polls authenticated `GET /pairings/{id}` to obtain the same public fields. Both devices derive the eight-digit confirmation code below. Phone shows site identity and code. User enters the phone's code on the original publisher and explicitly confirms transferring this site's full-authority key. The original publisher compares the code locally before encrypting; no code is sent to the relay.
4. Sender calls authenticated `POST /pairings/{id}/complete` with `{nonce,ciphertext}`. This freezes the envelope; identical retry is allowed, different ciphertext is rejected. Response has `state:"ready"`.
5. After the phone user confirms the site and matching code, phone calls `POST /pairings/{id}/consume` with the capability header and `{confirmed:true}`. Before sender completion this returns `409 pairing_pending`; poll by retrying without discarding the ephemeral key. Once ready, response includes public fields and `{nonce,ciphertext,state:"consumed"}`. Ciphertext and capability are then discarded by the relay and replay returns `410 pairing_consumed`. A lost consumption response requires a fresh pairing. Never automatically report the phone connected before decrypting, checking the key identity, and safely storing the key.

Sender endpoints require a session for the exact pairing IPNS; another site's session cannot inspect or complete it. Receiver endpoints authorize only with the capability. Invalid/expired capability returns a generic error. All replies use `Cache-Control: no-store` and `Referrer-Policy: no-referrer`.

## Cryptography and canonical bytes

Construct this UTF-8 transcript by joining these seven lines with LF, **without a final LF**:

```
croptop-pairing-v1
<origin without trailing slash>
<id>
<ipns>
<expiresAt decimal Unix seconds>
<senderPublicKey standard padded base64>
<receiverPublicKey standard padded base64>
```

There are seven lines (the first is the protocol domain). `sharedSecret = ECDH-P256(localPrivateKey, peerPublicKey)` is 32 bytes. `salt = SHA256(transcript)`. Apply RFC5869 HKDF-SHA256 with that salt and shared secret:

- AES key: 32-byte output with UTF-8 info `croptop-pairing-key-v1`.
- Code bytes: 4-byte output with UTF-8 info `croptop-pairing-code-v1`. Interpret as unsigned big-endian integer, modulo 100,000,000, zero-pad to eight decimal digits. Display grouped as `1234 5678`, but compare ungrouped digits.

Encryption is AES-256-GCM with a fresh random 12-byte nonce and the full transcript as AAD. `ciphertext` includes the appended 16-byte authentication tag. Plaintext is UTF-8 JSON `{version:1,origin,ipns,name,pem}`, where `pem` is the existing PKCS8 Ed25519 site key. Receiver validates all fields and derives the IPNS identity from the imported key; it must match `ipns`. Imported key must stay on the receiver and must not be POSTed to an API. Browser storage uses a non-exportable Ed25519 CryptoKey after import.

Cross-client fixture: `testdata/mobile-pairing-v1.json`. Private keys in this fixture are deterministic **test-only raw 32-byte P-256 scalars**, not PKCS8. It includes exact transcript, AES key, confirmation code, nonce, ciphertext/tag and plaintext so each client can independently test the same bytes. Never use fixture keys outside tests.

## Local publisher adapter

`GET /mobile-connect#<site UUID>` opens the local setup page. `POST /v0/croptop/sites/{id}/phone` takes `{enableHosting:boolean}`. Without prior hosted consent, false returns `409 hosting_required`; true records the explicit storage choice and publishes the supported template/compatibility descriptor. The adapter verifies the service's readiness response, opts into the service, creates a pairing, and returns `{id,url,expiresAt,state,ipns,name}`. `GET /v0/croptop/sites/{id}/phone/{pairid}` returns state and locally derived code once claimed. `POST /v0/croptop/sites/{id}/phone/{pairid}/confirm` takes `{code}` and encrypts/transfers only after the code matches. Local endpoints enforce the console's existing same-origin protections. Local pending ECDH keys expire with their pairing and are never persisted.

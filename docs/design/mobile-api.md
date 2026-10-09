# Mobile publishing API v1

Implementation contract, 2026-10-09. All clients use the same HTTPS service origin and `/v0/mobile` prefix. A loopback HTTP origin is permitted only for local testing. No private site key is sent to this API. Sequence numbers are decimal strings. Byte strings/signatures are standard padded base64. Times are Unix seconds. JSON errors are `{ "error": "useful message", "code": "stable_code" }`.

## Connection

- `GET /config` → `{version:1, enabled, origin, host, maxImageBytes, maxImagePixels, maxTitleBytes, maxCaptionBytes, formats, maxImages:1}`. `host` is the fixed publishing host's hostname for push-signature validation. Text limits are UTF-8 byte counts, not character counts.
- `POST /challenge` JSON `{ipns}` → `{id, message, expiresAt}`. Message is UTF-8 `croptop-mobile-session\n<configured origin>\n<ipns>\n<id>\n<expiresAt>`. Client verifies origin/IPNS/ID/expiry before Ed25519 signing. Challenges are random, bounded, expire, and consumed once.
- `POST /session` JSON `{id, signature}` → `{token, expiresAt}`. Bearer token grants bounded service access, never IPNS signing authority; only its hash is retained by the service. Later endpoints require `Authorization: Bearer <token>`.
- `GET /site` → `{ipns,name,url,ready,enabled,reason?,cid,sequence}`. Validates the published hosted policy and the mobile/template compatibility descriptor. Sequence is a decimal string; clients compare proposals against the current head.
- `PUT /connection` JSON `{enabled:true|false}` → site response. Explicit service consent, separately from published hosting policy. Disable stops uncommitted work, not previously published posts.

Root-key import is PKCS8 Ed25519 PEM. Derive the public key locally and its existing k51 IPNS identity. Browser uses a non-exportable signing CryptoKey in IndexedDB; native clients use protected platform storage. Sessions can be refreshed by signing a new challenge with the local key. Key import must not automatically opt into service use.

## Post operations

- `POST /operations` multipart fields `id` (uppercase UUID generated and retained by client), `title`, `caption`, `image` (one still image). Returns operation, 202 if preparing. A repeated ID with different input is 409. The same ID and body resumes/returns that operation. The service persists intent and private upload before asynchronous preparation.
- `GET /operations/{id}` → operation. A session may access only its own site's operations. Reconciles uncertain commits against the published site.
- `GET /operations/{id}/image` → normalized image, authenticated and private with `Cache-Control: no-store`. Clients fetch a blob for previews; no public draft URLs.
- `POST /operations/{id}/prepare` JSON `{}` → operation. Reconcile first, then rebuild/re-sign a stale or failed attempt with the same logical post ID. Never duplicates an already committed post.
- `POST /operations/{id}/commit` JSON `{proposalId, recordSignature, pushSignature}` → operation. Validates the proposal, service consent, site policy, signatures and freshness; persists commit intent before host submission. May return 202 and complete asynchronously.

Operation shape:

```json
{
  "id": "UUID", "ipns": "k51...", "postID": "UUID", "state": "needs_signature",
  "title": "", "caption": "", "createdAt": 1791500000,
  "mediaSHA256": "lowercase hex", "mediaType": "image/png",
  "proposal": {
    "id": "opaque digest", "cid": "bafy...", "parent": "bafy...",
    "sequence": "12", "host": "crop.top", "time": 1791500000,
    "expiresAt": 1791500540,
    "recordPayload": "base64 bytes to Ed25519-sign",
    "pushPayload": "base64 bytes to Ed25519-sign"
  },
  "url": "", "error": "", "code": ""
}
```

States: `preparing`, `needs_signature`, `committing`, `published`, `failed`. A local draft remains local until POST. Clients retain original destination, ID, image/title/caption and last operation. A missing/expired session is reauthenticated; no new operation is created. GET failure is not proof of a failed commit. The published state provides `url`; read it before resetting draft state.

Definitive first-attempt input rejection leaves an unaccepted draft editable. A reconciled `draft_expired` tombstone, or `image_invalid`/`heif_unavailable` failure before any proposal or device signing, may offer an explicit corrected copy with a new UUID; preserve the old receipt and recheck authoritative status first. Transport failures, generic preparation errors, and uncertain commits never silently rotate the logical ID.

Before signing, clients check operation ID/IPNS/title/caption against the local draft, validate the normalized preview's SHA-256, verify `pushPayload` exactly equals UTF-8 `croptop-push\n<host>\n<ipns>\n<cid>\n<sequence>\n<time>`, check expiry, and ensure `recordPayload` is the IPNS v2 payload for that CID/sequence (a shared fixture defines its CBOR layout). No arbitrary bytes returned by an API may be signed without validation. The service constructs IPNS v2 using the existing boxo library and verifies the completed record independently.

## Pairing

Pairing is an encrypted single-use relay, not a server-held key or an independently revocable device credential. [The pairing contract](mobile-pairing.md) and `testdata/mobile-pairing-v1.json` define the transport and cryptographic transcript. No plaintext key may appear in the relay, URL, QR code, or logs. Expiry and both-device confirmation are required. Local PEM import remains available.

## Lifetime and deployment boundaries

Sessions last 12 hours and refresh through another device-signed challenge. Private unpublished drafts expire after seven days; uncertain commits are reconciled before deletion. Published operation receipts retain their IDs so replay cannot create duplicate posts. The service owns a private directory with exclusive process locking and bounded cleanup. Preparation/commit use temporary, offline, non-listening IPFS engines separate from the public gateway; removing a draft also removes its private staged blocks. No site signing key is imported by those engines.

Authentication issuance is bounded by actual socket source and IPNS identity; forwarded-address headers are not trusted. Configure edge abuse protection separately when a reverse proxy aggregates clients. Mobile requests have read deadlines, and request bodies are never decoded while holding the pairing relay's global lock.

The origin must be trusted for its entire history, not merely reserved by the new router. A legacy author-controlled service worker could survive new routing rules and compromise browser-held keys. Confirm the origin was never an author-content origin, or choose a fresh dedicated origin before enabling a release.

## Implementation ownership

- `internal/publish`: original CLI-compatible prepare/render/push and verified site/post inspection.
- `internal/ipfs`: IPNS record payload construction/completion; clients use conformance fixtures.
- `internal/mobile`: authentication, consent, persisted operation lifecycle, normalization, pairing relay and HTTP contract.
- `web/mobile`: browser client, local keys/drafts and static app assets.
- `apps/ios` and `apps/android`: native intake, platform key/draft storage and clients of this contract.
- CLI/host wiring and Worker trusted-origin dispatch remain thin adapters.

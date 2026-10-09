# Mobile composer

This app is served at the dedicated trusted app origin by the Go host or Worker.
It must never share an origin with author-controlled site HTML. Its only network
API is same-origin `/v0/mobile`; the service contract lives in
`docs/design/mobile-api.md`.

The page works before connection: selected images and text are retained in
IndexedDB. A Web Lock permits one active composer per origin, including an
installed app and browser tabs. The stored site key is a non-exportable Ed25519
CryptoKey. Image bytes use ArrayBuffer storage because WebKit cannot reliably
persist temporary file-backed File objects. Session tokens live only in memory.

An operation retains its UUID, destination and original input through retries,
reloads, authentication renewal and uncertain commits. The author reviews the
normalized image before signing. `protocol.js` owns validation of the challenge,
image hash, canonical IPNS payload and host authorization payload. Confirmed
expiry and explicitly unsigned image-normalization failures offer a new editable
draft only after a status recheck; the original receipt and image remain locally
archived. Removing the connection does not revoke another copy of the root key.

The service worker caches only the exact static assets listed in its SHA-256
manifest. It never caches APIs, image previews, keys, tokens or pairing links.
Every complete shell has its own content-derived cache name. Updates wait for
existing windows to close, then remove prior shell caches. After changing an
asset, update its digest in `sw.js`; the unit-test gate rejects stale digests.
Offline reopening preserves the draft; reconnect and reload to publish.

## Verification

With Node 24:

```sh
node --test web/mobile/protocol.test.mjs web/mobile/pairing.test.mjs web/mobile/sw-cache.test.mjs
```

Install Playwright 1.63.0 and its Chromium/WebKit engines in a development or CI
environment, then run:

```sh
node --test web/mobile/browser.test.mjs
```

For a task-local Playwright installation, set `PLAYWRIGHT_MODULE` to its
`index.mjs`. `PLAYWRIGHT_BROWSERS_PATH` and `CHROMIUM_EXECUTABLE` support local
engine installations. `MOBILE_BROWSERS=chromium` selects one engine;
`MOBILE_SCREENSHOTS=/absolute/path` writes the three mobile screenshots.

The browser suite runs a local fixture server and real WebCrypto. It covers
pre-connection capture, durable reload, unavailable-service reload from the
cached shell, explicit service consent, session renewal, normalized preview
integrity, verified signatures against the shared Go fixture, lost commit
responses, immutable operation identity, simultaneous tabs, byte-length limits,
rejected uploads and confirmed-expiry recovery. WebKit's Playwright offline
switch fails top-level navigation before the service worker runs, so the shared
offline test uses a real local-service outage instead.

Physical iPhone/Android photo pickers, screenshot HEIF variants, cellular posting
with the desktop asleep, Safari airplane-mode behavior, and real hosted-service
rollout still require the acceptance checks in the release scope. These fixture
tests do not establish production or physical-device readiness.

// Only our shipped app shell is cached. API requests, private images, keys,
// pairing capabilities and drafts never enter Cache Storage.
// sw-cache.test.mjs gates these hashes against source files. Changing an asset
// changes this worker, and therefore installs a new complete shell version.
const ASSET_DIGESTS = {
  "/": "0f73d240cef63fe37f4017e17226e9c8d046db8dc1ec2eab7994db24bc20d321",
  "/index.html": "0f73d240cef63fe37f4017e17226e9c8d046db8dc1ec2eab7994db24bc20d321",
  "/app.js": "d7e035dd71b4c59cb8146d96961a0031eaa532d36b90680c9fe4606eaeb7b8ce",
  "/protocol.js": "b73300818584399fb4d6c3add1a698617f372561d015dbbe4a1ccc1b785d18a9",
  "/pairing.js": "11f81ce49dbac529bb51f5a7d21f483d1c4d5a983998135886d3e4fffe472d32",
  "/storage.js": "b0d3818b48a88ef36e1547293b72aba0967c4bcb3416abc4dc0e40ba413c4375",
  "/style.css": "fa1eedc062899b925cc90f4729edb1339c97dcc98842b98956485462b0da6ff5",
  "/manifest.webmanifest": "a49c2c000ea4c58f5210f36ff0c65f9ff85cb1c083a3238714b9b64b18bb1949",
  "/icon.svg": "44a5b58f704312588bd0e299cb0451ea91c8b5d86e1a1f8a580008e1aeba57cf",
  "/fonts/SimplonNorm-Regular-WebXL.woff2": "3817b6d37af258364078193bab70803aa7518ccfb23263459c33b15507a1687d",
  "/fonts/SimplonNorm-Bold-WebXL.woff2": "37ef327ebd4c40ba8fffc63de7ca45d70444cd108c0b4e58a10da250ea87523b"
};
const PREFIX = 'croptop-mobile-shell-';
const digest = async data => Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', data)), byte => byte.toString(16).padStart(2, '0')).join('');
const cacheName = digest(new TextEncoder().encode(JSON.stringify(ASSET_DIGESTS))).then(hash => PREFIX + hash);
self.addEventListener('install', event => event.waitUntil((async () => {
  const cache = await caches.open(await cacheName);
  await Promise.all(Object.entries(ASSET_DIGESTS).map(async ([path, expected]) => {
    const response = await fetch(path, { cache: 'no-store', credentials: 'omit', redirect: 'error' });
    if (!response.ok || await digest(await response.clone().arrayBuffer()) !== expected) throw new Error('The app update is incomplete. Keep the installed version.');
    await cache.put(path, response);
  }));
  // Do not skipWaiting: an open composer keeps its complete version until all
  // existing windows close. A new worker cannot mix modules into an old page.
})()));
self.addEventListener('activate', event => event.waitUntil((async () => {
  const current = await cacheName;
  await Promise.all((await caches.keys()).filter(name => name.startsWith(PREFIX) && name !== current).map(name => caches.delete(name)));
  await self.clients.claim();
})()));
self.addEventListener('fetch', event => {
  const request = event.request;
  const url = new URL(request.url);
  if (request.method !== 'GET' || url.origin !== self.location.origin || url.search || !Object.hasOwn(ASSET_DIGESTS, url.pathname)) return;
  event.respondWith((async () => {
    const cache = await caches.open(await cacheName);
    const cached = await cache.match(request);
    if (cached) return cached;
    // Browser eviction may remove an entry. Restore only this exact version.
    const response = await fetch(request, { cache: 'no-store', redirect: 'error' });
    if (!response.ok || await digest(await response.clone().arrayBuffer()) !== ASSET_DIGESTS[url.pathname]) return new Response('Close and reopen Croptop to complete its update.', { status: 503 });
    await cache.put(request, response.clone());
    return response;
  })());
});

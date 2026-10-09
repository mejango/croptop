// Run with a locally installed Playwright: PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs
// node --test web/mobile/browser.test.mjs. No deployed service is contacted.
import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { readFile, mkdir } from 'node:fs/promises';
import { createHash, createPrivateKey, createPublicKey, verify } from 'node:crypto';
import { fileURLToPath } from 'node:url';
import { join } from 'node:path';

const playwright = await import(process.env.PLAYWRIGHT_MODULE || 'playwright');
const root = new URL('../../', import.meta.url);
const fixture = JSON.parse(await readFile(new URL('docs/design/mobile-protocol-fixture.json', root)));
const pairingFixture = JSON.parse(await readFile(new URL('testdata/mobile-pairing-v1.json', root)));
const image = await readFile(new URL('templates/croptop/dev/fixture/B2000000-0000-4000-8000-000000000002/cover.png', root));
const publicKey = createPublicKey(createPrivateKey(fixture.privateKeyPEM));

async function fixtureService({ corruptPreview = false, dropCommit = false, deferPreparation = false } = {}) {
  const state = { uploads: 0, commits: 0, enableCalls: 0, sessions: 0, signed: 0, pairingClaims: 0, requests: [], operation: null, enabled: false, invalidateSession: false };
  const send = (res, value, status = 200) => { res.writeHead(status, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' }); res.end(JSON.stringify(value)); };
  let origin;
  const server = createServer(async (req, res) => {
    try {
      if (state.offline) return req.socket.destroy();
      const url = new URL(req.url, origin);
      if (!url.pathname.startsWith('/v0/mobile')) {
        const path = url.pathname === '/' ? '/index.html' : url.pathname;
        if (!/^\/(?:[a-z.-]+|fonts\/SimplonNorm-(?:Regular|Bold)-WebXL\.woff2)$/.test(path)) return send(res, {}, 404);
        const asset = path.startsWith('/fonts/') ? new URL('templates/croptop/assets/' + path.split('/').at(-1), root) : new URL('web/mobile' + path, root);
        const data = await readFile(asset);
        const type = path.endsWith('.js') ? 'text/javascript' : path.endsWith('.css') ? 'text/css' : path.endsWith('.svg') ? 'image/svg+xml' : path.endsWith('.woff2') ? 'font/woff2' : path.endsWith('.webmanifest') ? 'application/manifest+json' : 'text/html';
        res.writeHead(200, { 'Content-Type': type, 'Content-Security-Policy': "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' blob:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'" });
        return res.end(data);
      }
      const bodyParts = [];
      for await (const part of req) bodyParts.push(part);
      const body = Buffer.concat(bodyParts);
      assert.ok(!body.toString().includes('PRIVATE KEY'), 'Private key must never enter requests');
      assert.ok(!body.toString().includes('MC4CAQAwBQYDK2Vw'), 'PKCS8 bytes must never enter requests');
      state.requests.push({ path: url.pathname, method: req.method });
      const path = url.pathname.slice('/v0/mobile'.length);
      const parsed = () => JSON.parse(body);
      const site = () => ({ ipns: fixture.ipns, name: 'Field notes', url: 'https://crop.top/ipns/' + fixture.ipns, ready: true, enabled: state.enabled });
      if (path === '/config') return send(res, { version: 1, enabled: true, origin, host: fixture.host, maxImageBytes: 20971520, maxImagePixels: 40000000, maxTitleBytes: 1000, maxCaptionBytes: 10000 });
      const claim = path.match(/^\/pairings\/([A-Za-z0-9_-]{43})\/claim$/);
      if (claim) {
        state.pairingClaims++;
        if (state.rejectPairing) return send(res, { code: 'pairing_expired', error: 'This connection expired. Start a new link.' }, 410);
        return send(res, { id: claim[1], origin, ipns: fixture.ipns, senderPublicKey: pairingFixture.info.senderPublicKey, receiverPublicKey: parsed().receiverPublicKey, expiresAt: fixture.time + 600, state: 'claimed' });
      }
      if (path === '/challenge') {
        assert.equal(parsed().ipns, fixture.ipns);
        state.challenge = { id: 'browser-fixture-challenge-00000001', expiresAt: fixture.time + 600 };
        state.challenge.message = `croptop-mobile-session\n${origin}\n${fixture.ipns}\n${state.challenge.id}\n${state.challenge.expiresAt}`;
        return send(res, state.challenge);
      }
      if (path === '/session') {
        assert.equal(parsed().id, state.challenge.id);
        assert.ok(verify(null, Buffer.from(state.challenge.message), publicKey, Buffer.from(parsed().signature, 'base64')));
        state.sessions++;
        return send(res, { token: 'session-' + state.sessions, expiresAt: fixture.time + 43200 });
      }
      if (state.invalidateSession) { state.invalidateSession = false; return send(res, { code: 'session_expired', error: 'Session expired.' }, 401); }
      assert.equal(req.headers.authorization, 'Bearer session-' + state.sessions);
      if (path === '/site') return send(res, site());
      if (path === '/connection') { state.enabled = parsed().enabled; state.enableCalls++; return send(res, site()); }
      if (path === '/operations' && req.method === 'POST') {
        if (state.rejectNextUpload) { state.rejectNextUpload = false; return send(res, { code: 'invalid_upload', error: 'This image could not be read. Choose it again.' }, 400); }
        const multipart = await new Request(origin + req.url, { method: 'POST', headers: req.headers, body }).formData();
        const id = multipart.get('id');
        assert.match(id, /^[0-9A-F-]{36}$/);
        assert.equal(state.enabled, true);
        if (state.operation) assert.equal(state.operation.id, id, 'Retries must retain original identity');
        state.uploads++;
        state.operation ||= { id, postID: id, ipns: fixture.ipns, title: multipart.get('title'), caption: multipart.get('caption'), state: 'needs_signature', mediaSHA256: createHash('sha256').update(image).digest('hex'), mediaType: 'image/png', proposal: { id: 'browser-proposal', cid: fixture.cid, parent: fixture.cid, sequence: fixture.sequence, host: fixture.host, time: fixture.time, expiresAt: fixture.expiresAt, recordPayload: fixture.recordPayload, pushPayload: fixture.pushPayload } };
        if (deferPreparation) state.operation = { ...state.operation, state: 'preparing', proposal: null };
        return send(res, state.operation, 202);
      }
      if (/^\/operations\/[^/]+\/image$/.test(path)) { res.writeHead(200, { 'Content-Type': 'image/png', 'Cache-Control': 'no-store' }); return res.end(corruptPreview ? Buffer.concat([image, Buffer.from('tampered')]) : image); }
      if (/^\/operations\/[^/]+\/commit$/.test(path)) {
        const signed = parsed();
        assert.equal(signed.proposalId, state.operation.proposal.id);
        assert.ok(verify(null, Buffer.from(fixture.recordPayload, 'base64'), publicKey, Buffer.from(signed.recordSignature, 'base64')), 'Record signature must verify against the shared Go payload and identity');
        assert.ok(verify(null, Buffer.from(fixture.pushPayload, 'base64'), publicKey, Buffer.from(signed.pushSignature, 'base64')), 'Host signature must verify against the shared Go payload and identity');
        // Chromium may resend an HTTP request after a socket drop. The service
        // contract still commits the operation once and returns its receipt.
        if (state.operation.state !== 'published') { state.signed++; state.commits++; }
        state.operation = { ...state.operation, state: 'published', url: 'https://crop.top/ipns/' + fixture.ipns + '/' + state.operation.id + '/' };
        if (dropCommit) return req.socket.destroy();
        return send(res, state.operation);
      }
      if (/^\/operations\/[^/]+$/.test(path)) {
        if (state.failNextOperationRead) { state.failNextOperationRead = false; return send(res, { code: 'temporarily_unavailable', error: 'Could not check image preparation. Try checking this saved post again.' }, 503); }
        return state.operation ? send(res, state.operation) : send(res, { error: 'Not found' }, 404);
      }
      return send(res, { error: 'Unsupported fixture request' }, 404);
    } catch (error) { state.serverError = error; send(res, { error: error.message }, 500); }
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  origin = 'http://127.0.0.1:' + server.address().port;
  return { origin, state, close: () => new Promise(resolve => server.close(resolve)) };
}
async function open(browser, origin) {
  const context = await browser.newContext({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 1, isMobile: true, hasTouch: true });
  await context.addInitScript(({ time }) => { Date.now = () => time * 1000; }, fixture);
  // Use the actual local HTTP stack. Request interception changes WebKit's
  // service-worker navigation behavior; no receipt links are followed here.
  const page = await context.newPage();
  const failures = [];
  page.on('pageerror', error => failures.push(error.message));
  await page.goto(origin);
  await page.locator('#image-help').filter({ hasText: '20 MB' }).waitFor();
  return { page, context, failures };
}
async function capture(page, browserName, name) {
  if (!process.env.MOBILE_SCREENSHOTS) return;
  await mkdir(process.env.MOBILE_SCREENSHOTS, { recursive: true });
  await page.screenshot({ path: join(process.env.MOBILE_SCREENSHOTS, browserName + '-' + name + '.png'), fullPage: true });
}
async function pick(page) {
  await page.locator('#image').setInputFiles({ name: 'a-day.png', mimeType: 'image/png', buffer: image });
  await page.waitForFunction(() => document.getElementById('draft-status').textContent.includes('Draft saved') || !document.getElementById('error').hidden);
  assert.equal(await page.locator('#error').isVisible(), false, await page.locator('#error').textContent());
}
async function connect(page) {
  await page.locator('#import-details').evaluate(element => { element.open = true; });
  await page.locator('#key-file').setInputFiles({ name: 'site.pem', mimeType: 'text/plain', buffer: Buffer.from(fixture.privateKeyPEM) });
  await page.locator('#enable').waitFor();
  await page.locator('#enable').click();
  await page.locator('#site-status').filter({ hasText: 'Your computer can sleep' }).waitFor();
}

for (const browserName of (process.env.MOBILE_BROWSERS || 'chromium,webkit').split(',')) {
  test(browserName + ': pairing links opened in the existing tab preserve drafts and respect one active receiver', { timeout: 90000 }, async t => {
    const service = await fixtureService();
    t.after(service.close);
    const options = browserName === 'chromium' && process.env.CHROMIUM_EXECUTABLE ? { executablePath: process.env.CHROMIUM_EXECUTABLE } : {};
    const browser = await playwright[browserName].launch({ headless: true, ...options });
    t.after(() => browser.close());
    const { page, context, failures } = await open(browser, service.origin);
    await pick(page);
    await page.locator('#caption').fill('Keep this while I connect.');
    await page.locator('#draft-status').filter({ hasText: 'Draft saved' }).waitFor();
    const original = await page.evaluate(async () => { window.sameComposerDocument = true; const draft = await (await import('./storage.js')).read('draft'); return { id: draft.id, caption: draft.caption, imageBytes: draft.image.size }; });
    const link = '#pair=' + 'B'.repeat(43) + '.' + 'C'.repeat(43);
    const anotherLink = '#pair=' + 'D'.repeat(43) + '.' + 'E'.repeat(43);
    await page.goto(service.origin + '/' + link);
    await page.locator('#confirm-pairing').waitFor();
    assert.equal(await page.evaluate(() => window.sameComposerDocument), true, 'Hash navigation must not require a page reload');
    assert.equal(await page.evaluate(() => location.hash), '', 'The existing receiver removes the capability from the URL');
    assert.equal(service.state.pairingClaims, 1);
    assert.match(await page.locator('#pairing-code').textContent(), /^\d{4} \d{4}$/);
    await page.goto(service.origin + '/' + anotherLink);
    await page.locator('#message').filter({ hasText: 'Finish or cancel' }).waitFor();
    assert.equal(service.state.pairingClaims, 1, 'Another hash cannot start a concurrent receiver');
    assert.equal(await page.evaluate(() => location.hash), '');
    const duplicate = await context.newPage();
    await duplicate.goto(service.origin);
    await duplicate.locator('#error').filter({ hasText: 'already open' }).waitFor();
    await duplicate.goto(service.origin + '/' + anotherLink);
    assert.equal(await duplicate.locator('#image').isDisabled(), true);
    assert.equal(service.state.pairingClaims, 1, 'An inactive tab cannot consume a connection link');
    await duplicate.close();
    await page.locator('#cancel-pairing').click();
    await page.locator('#pairing').waitFor({ state: 'hidden' });
    service.state.rejectPairing = true;
    await page.goto(service.origin + '/' + anotherLink);
    await page.locator('#error').filter({ hasText: 'connection expired' }).waitFor();
    assert.equal(service.state.pairingClaims, 2);
    assert.equal(await page.evaluate(() => location.hash), '');
    assert.equal(await page.locator('#pairing').isVisible(), false);
    service.state.rejectPairing = false;
    await connect(page);
    await page.goto(service.origin + '/' + link);
    await page.locator('#error').filter({ hasText: 'Remove the current site' }).waitFor();
    assert.equal(service.state.pairingClaims, 2, 'A connected site must not be silently replaced');
    assert.equal(await page.evaluate(() => location.hash), '');
    const retained = await page.evaluate(async () => { const draft = await (await import('./storage.js')).read('draft'); return { id: draft.id, caption: draft.caption, imageBytes: draft.image.size }; });
    assert.deepEqual(retained, original);
    assert.equal(service.state.uploads, 0);
    assert.equal(service.state.commits, 0);
    assert.deepEqual(failures, []);
    assert.ifError(service.state.serverError);
    await context.close();
  });
  test(browserName + ': first capture survives setup, signatures match Go, uncertain commit recovers after reload', { timeout: 90000 }, async t => {
    const service = await fixtureService({ dropCommit: true });
    t.after(service.close);
    const options = browserName === 'chromium' && process.env.CHROMIUM_EXECUTABLE ? { executablePath: process.env.CHROMIUM_EXECUTABLE } : {};
    const browser = await playwright[browserName].launch({ headless: true, ...options });
    t.after(() => browser.close());
    const { page, context, failures } = await open(browser, service.origin);
    await page.setViewportSize({ width: 360, height: 780 });
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, 'Onboarding fits a narrow phone screen');
    assert.equal(await page.locator('#desktop-download').getAttribute('href'), 'https://github.com/mejango/croptop/releases/tag/v0.13.22');
    assert.equal(await page.locator('#desktop-download').getAttribute('rel'), 'noopener noreferrer');
    assert.match(await page.locator('#setup').textContent(), /Croptop → Check for Updates…/);
    assert.match(await page.locator('#setup').textContent(), /0\.13\.22 or later/);
    assert.match(await page.locator('#setup').textContent(), /Open your site and choose Connect phone \(the phone button beside Settings\)/);
    assert.match(await page.locator('#setup').textContent(), /Setup stays in the Mac app\./);
    assert.match(await page.locator('#setup').textContent(), /Under Post from your phone, allow hosting if asked, then choose Connect\. Your saved hosting permission is reused\./);
    assert.match(await page.locator('#setup').textContent(), /Croptop checks the published site first\. If it needs an update, choose Publish and connect only when you’re ready to make all saved changes on this Mac public\./);
    assert.match(await page.locator('#setup').textContent(), /Then scan the QR code with your phone\./);
    assert.doesNotMatch(await page.locator('#setup').textContent(), /pilot|Create connection|0\.13\.21/i);
    assert.match(await page.locator('#setup').textContent(), /Confirm the connection on both devices\. If prompted, allow phone posting here\./);
    await capture(page, browserName, 'first-open');
    await pick(page);
    await page.locator('#caption').fill('A little moment, kept.');
    await page.locator('#draft-status').filter({ hasText: 'Draft saved' }).waitFor();
    const identity = await page.evaluate(async () => (await (await import('./storage.js')).read('draft')).id);
    await page.reload();
    await page.locator('#preview').waitFor();
    assert.equal(await page.locator('#caption').inputValue(), 'A little moment, kept.');
    await page.evaluate(async () => { await navigator.serviceWorker.ready; if (!navigator.serviceWorker.controller) await new Promise(resolve => navigator.serviceWorker.addEventListener('controllerchange', resolve, { once: true })); });
    // Simulate an unreachable service for both engines. WebKit's Playwright
    // offline switch aborts top-level navigation before dispatching its worker.
    service.state.offline = true;
    await page.reload();
    await page.locator('#preview').waitFor();
    assert.equal(await page.locator('#caption').inputValue(), 'A little moment, kept.', 'Cached app shell reopens an offline local draft');
    service.state.offline = false;
    await page.reload();
    await page.locator('#preview').waitFor();
    await connect(page);
    assert.equal(service.state.enableCalls, 1, 'Import must not automatically consent');
    const key = await page.evaluate(async () => { const key = (await (await import('./storage.js')).read('connection')).key; return { extractable: key.extractable, algorithm: key.algorithm.name }; });
    assert.deepEqual(key, { extractable: false, algorithm: 'Ed25519' });
    service.state.invalidateSession = true;
    await page.locator('#publish').click();
    await page.locator('#publish').filter({ hasText: 'Publish this preview' }).waitFor();
    assert.equal(service.state.operation.id, identity);
    assert.equal(service.state.sessions, 2, 'Expired session must reauthenticate without a new draft');
    assert.equal(service.state.commits, 0, 'Normalized preview requires user review');
    await capture(page, browserName, 'prepared-preview');
    await page.locator('#publish').click();
    await page.locator('#error').waitFor();
    assert.equal(service.state.signed, 1, await page.locator('#error').textContent());
    assert.equal(await page.locator('#message').isVisible(), false, 'A lost commit response must not leave an active Publishing message');
    assert.equal(await page.locator('#publish').innerText(), 'Check publication');
    assert.equal(await page.locator('#publish').isEnabled(), true);
    assert.match(await page.locator('#draft-status').innerText(), /Reopening keeps the same post/);
    assert.match(await page.locator('#publish-help').innerText(), /until publication is confirmed/);
    const uncertain = await page.evaluate(async () => {
      const saved = await (await import('./storage.js')).read('draft');
      return { id: saved.id, submitted: saved.submitted, signed: saved.signed, state: saved.operation.state };
    });
    assert.deepEqual(uncertain, { id: identity, submitted: true, signed: true, state: 'committing' });
    await page.reload();
    await page.locator('#receipt').waitFor();
    assert.equal(await page.locator('#post-link').getAttribute('href'), service.state.operation.url);
    assert.equal(service.state.uploads, 1);
    assert.equal(service.state.commits, 1);
    assert.equal((await page.evaluate(async () => (await (await import('./storage.js')).read('draft')))).id, identity);
    await capture(page, browserName, 'published');
    const cachePaths = await page.evaluate(async () => { const names = (await caches.keys()).filter(name => name.startsWith('croptop-mobile-shell-')); if (names.length !== 1) throw new Error('Keep one complete shell version'); const cache = await caches.open(names[0]); return (await cache.keys()).map(request => new URL(request.url).pathname); });
    assert.ok(cachePaths.includes('/app.js'));
    assert.ok(cachePaths.every(path => !path.startsWith('/v0/') && !path.includes('/operations/')), 'Private API responses are never cached');
    const duplicate = await context.newPage();
    await duplicate.goto(service.origin);
    await duplicate.locator('#error').filter({ hasText: 'already open' }).waitFor();
    assert.equal(await duplicate.locator('#image').isDisabled(), true);
    assert.equal(await duplicate.locator('#another').isDisabled(), true);
    await duplicate.locator('#another').evaluate(button => button.click());
    assert.equal((await page.evaluate(async () => (await (await import('./storage.js')).read('draft')))).id, identity, 'Inactive tabs cannot erase an active draft');
    await page.close();
    await duplicate.reload();
    await duplicate.locator('#receipt').waitFor();
    assert.equal((await duplicate.evaluate(async () => (await (await import('./storage.js')).read('draft')))).id, identity, 'Closing the active tab lets another recover the same receipt');
    assert.deepEqual(failures, []);
    assert.ifError(service.state.serverError);
    await context.close();
  });
  test(browserName + ': image preparation errors replace stale progress and preserve safe draft recovery', { timeout: 90000 }, async t => {
    const service = await fixtureService({ deferPreparation: true });
    t.after(service.close);
    const options = browserName === 'chromium' && process.env.CHROMIUM_EXECUTABLE ? { executablePath: process.env.CHROMIUM_EXECUTABLE } : {};
    const browser = await playwright[browserName].launch({ headless: true, ...options });
    t.after(() => browser.close());
    const { page, context, failures } = await open(browser, service.origin);
    await page.clock.install({ time: new Date(fixture.time * 1000) });
    await page.clock.pauseAt(new Date((fixture.time + 1) * 1000));
    await pick(page); await connect(page);
    await page.locator('#caption').fill('Keep the original image and words.');
    await page.locator('#publish').click();
    await page.locator('#message').filter({ hasText: 'Preparing your image and post' }).waitFor();
    await page.locator('#publish').filter({ hasText: 'Check publication' }).waitFor();
    const original = service.state.operation.id;
    const reads = () => service.state.requests.filter(request => request.method === 'GET' && request.path === '/v0/mobile/operations/' + original).length;

    // A transport error does not prove the operation failed or permit a new ID.
    service.state.failNextOperationRead = true;
    await page.locator('#publish').click();
    await page.locator('#error').filter({ hasText: 'Could not check image preparation' }).waitFor();
    assert.equal(await page.locator('#message').isVisible(), false);
    assert.equal(await page.locator('#publish').innerText(), 'Check publication');
    assert.equal(await page.locator('#publish').isEnabled(), true);
    assert.equal(await page.locator('#caption').isDisabled(), true);
    assert.match(await page.locator('#draft-status').innerText(), /Reopening keeps the same post/);
    assert.equal((await page.evaluate(async () => (await (await import('./storage.js')).read('draft')).id)), original);

    // Match the service's authoritative pre-signing image rejection. Trigger
    // a manual status check while the original automatic poll is still queued.
    const rejection = 'this image uses unsupported HDR or color encoding; export an SDR sRGB PNG or JPEG and try again';
    service.state.operation = { ...service.state.operation, state: 'failed', code: 'image_invalid', error: rejection, proposal: null };
    await page.evaluate(() => window.dispatchEvent(new Event('online')));
    await page.locator('#error').filter({ hasText: rejection }).waitFor();
    assert.equal(await page.locator('#message').isVisible(), false);
    assert.equal(await page.locator('#copy-expired').innerText(), 'Edit this draft');
    assert.equal(await page.locator('#copy-expired').isEnabled(), true);
    assert.equal(await page.locator('#publish-help').innerText(), 'This attempt stopped. Your image and words are still saved.');
    await page.setViewportSize({ width: 360, height: 780 });
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
    await capture(page, browserName, 'image-preparation-rejected');
    const failedReads = reads();
    await page.clock.runFor(2200);
    assert.equal(await page.locator('#error').innerText(), rejection);
    assert.equal(reads(), failedReads, 'A terminal failure cancels the previously queued automatic poll');
    const retained = await page.evaluate(async () => {
      const saved = await (await import('./storage.js')).read('draft');
      const sha256 = Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', await saved.image.arrayBuffer())), value => value.toString(16).padStart(2, '0')).join('');
      return { id: saved.id, caption: saved.caption, submitted: saved.submitted, signed: saved.signed, state: saved.operation.state, code: saved.operation.code, sha256 };
    });
    assert.deepEqual(retained, { id: original, caption: 'Keep the original image and words.', submitted: true, signed: false, state: 'failed', code: 'image_invalid', sha256: createHash('sha256').update(image).digest('hex') });
    assert.equal(service.state.uploads, 1); assert.equal(service.state.commits, 0); assert.equal(service.state.signed, 0);

    // The existing edit action rechecks that unsigned failure before archiving
    // it. Presentation changes must not silently resubmit or alter image bytes.
    await page.locator('#copy-expired').click();
    await page.locator('#message').filter({ hasText: 'This image could not be prepared and was not published' }).waitFor();
    const edited = await page.evaluate(async original => {
      const { read } = await import('./storage.js');
      const current = await read('draft'), archived = await read('draft:' + original);
      return { id: current.id, submitted: current.submitted, caption: current.caption, bytes: current.image.size, archivedID: archived.id, archivedCode: archived.operation.code };
    }, original);
    assert.notEqual(edited.id, original);
    assert.deepEqual({ ...edited, id: original }, { id: original, submitted: false, caption: retained.caption, bytes: image.length, archivedID: original, archivedCode: 'image_invalid' });
    assert.equal(service.state.uploads, 1); assert.equal(service.state.commits, 0);
    assert.equal(await page.locator('#caption').isEnabled(), true);
    assert.deepEqual(failures, []); assert.ifError(service.state.serverError);
    await context.close();
  });
  test(browserName + ': changed normalized media is never signed; original draft remains', { timeout: 90000 }, async t => {
    const service = await fixtureService({ corruptPreview: true });
    t.after(service.close);
    const options = browserName === 'chromium' && process.env.CHROMIUM_EXECUTABLE ? { executablePath: process.env.CHROMIUM_EXECUTABLE } : {};
    const browser = await playwright[browserName].launch({ headless: true, ...options });
    t.after(() => browser.close());
    const { page, context, failures } = await open(browser, service.origin);
    await pick(page); await connect(page);
    await page.locator('#publish').click();
    await page.locator('#error').filter({ hasText: 'integrity check' }).waitFor();
    assert.equal(service.state.commits, 0);
    assert.equal(await page.locator('#caption').isDisabled(), true);
    const draft = await page.evaluate(async () => { const draft = await (await import('./storage.js')).read('draft'); return { id: draft.id, bytes: draft.image.size, ipns: draft.ipns }; });
    assert.equal(draft.bytes, image.length);
    assert.equal(draft.ipns, fixture.ipns);
    assert.equal(draft.id, service.state.operation.id);
    assert.deepEqual(failures, []);
    assert.ifError(service.state.serverError);
    await context.close();
  });
  test(browserName + ': text/upload rejection stays editable and definitive expiry can safely copy a retained draft', { timeout: 90000 }, async t => {
    const service = await fixtureService();
    t.after(service.close);
    const options = browserName === 'chromium' && process.env.CHROMIUM_EXECUTABLE ? { executablePath: process.env.CHROMIUM_EXECUTABLE } : {};
    const browser = await playwright[browserName].launch({ headless: true, ...options });
    t.after(() => browser.close());
    const { page, context, failures } = await open(browser, service.origin);
    await pick(page); await connect(page);
    await page.locator('#caption').fill('é'.repeat(5001));
    await page.locator('#publish').click();
    await page.locator('#error').filter({ hasText: 'caption is too long' }).waitFor();
    assert.equal(service.state.uploads, 0, 'UTF8 limits are checked before locking or uploading');
    assert.equal(await page.locator('#caption').isDisabled(), false);
    await page.locator('#caption').fill('Keep this thought.');
    const original = await page.evaluate(async () => (await (await import('./storage.js')).read('draft')).id);
    service.state.rejectNextUpload = true;
    await page.locator('#publish').click();
    await page.locator('#error').filter({ hasText: 'image could not be read' }).waitFor();
    assert.equal(await page.locator('#image').isDisabled(), false, 'Definitive first-upload rejection must release the edit lock');
    assert.equal(await page.locator('#caption').isDisabled(), false);
    await page.locator('#publish').click();
    await page.locator('#publish').filter({ hasText: 'Publish this preview' }).waitFor();
    assert.equal(service.state.operation.id, original);
    service.state.operation = { ...service.state.operation, state: 'failed', code: 'draft_expired', error: 'This draft expired without publication.', proposal: null };
    await page.reload();
    await page.locator('#copy-expired').waitFor();
    await page.locator('#copy-expired').click();
    await page.locator('#message').filter({ hasText: 'ready in a new draft' }).waitFor();
    const saved = await page.evaluate(async oldID => {
      const { read } = await import('./storage.js');
      const current = await read('draft');
      const expired = await read('draft:' + oldID);
      return { id: current.id, title: current.title, caption: current.caption, imageBytes: current.image.size, ipns: current.ipns, submitted: current.submitted, old: expired.operation };
    }, original);
    assert.notEqual(saved.id, original);
    assert.equal(saved.caption, 'Keep this thought.');
    assert.equal(saved.imageBytes, image.length);
    assert.equal(saved.ipns, fixture.ipns);
    assert.equal(saved.submitted, false);
    assert.equal(saved.old.id, original);
    assert.equal(saved.old.code, 'draft_expired');
    assert.equal(service.state.commits, 0);
    assert.equal(service.state.uploads, 1, 'Copying never uploads or publishes the new draft');
    assert.deepEqual(failures, []);
    assert.ifError(service.state.serverError);
    await context.close();
  });
}

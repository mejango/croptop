// Local-only desktop setup regression tests. No site is published and no
// production pairing service is contacted. Run with PLAYWRIGHT_MODULE set.
import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';

const playwright = await import(process.env.PLAYWRIGHT_MODULE || 'playwright');
const siteID = '11111111-1111-4111-8111-111111111111';
const pairID = 'A'.repeat(43);
const capability = 'B'.repeat(42) + 'A';
const connection = () => ({ id: pairID, url: `https://phone.example/#pair=${pairID}.${capability}`, expiresAt: Math.floor(Date.now() / 1000) + 600, name: 'Test site', state: 'open' });
const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=', 'base64');
const deferred = () => { let resolve; const promise = new Promise(r => { resolve = r; }); return { promise, resolve }; };
async function waitFor(check) {
  const deadline = Date.now() + 6000;
  while (!check()) {
    assert.ok(Date.now() < deadline, 'Expected mock request did not arrive');
    await new Promise(resolve => setTimeout(resolve, 20));
  }
}

async function fixture(browser, hook, init) {
  const requests = [], errors = [], external = [];
  let snapshot;
  const send = (res, value, code = 200) => { res.writeHead(code, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' }); res.end(JSON.stringify(value)); };
  const server = createServer(async (req, res) => {
    try {
      if (req.url.startsWith('/v0/')) {
        assert.equal(req.headers['x-croptop-phone'], '1');
        assert.ok(!req.url.includes(capability));
        let raw = ''; for await (const part of req) raw += part;
        const request = { path: req.url.split('/phone')[1], method: req.method, body: raw ? JSON.parse(raw) : undefined };
        requests.push(request);
        if (request.path === '/preparations' && request.method === 'POST') {
          assert.match(request.body.id, /^[A-F0-9-]{36}$/);
          snapshot = { id: request.body.id, siteID, state: 'preparing', stage: 'checking', message: 'Checking the published site without publishing saved changes…', startedAt: Math.floor(Date.now() / 1000), deadline: Math.floor(Date.now() / 1000) + 300 };
        }
        if (await hook?.({ request, snapshot, send: (value, code) => send(res, value, code), res, requests })) return;
        if (request.path.endsWith('/qr')) { res.writeHead(200, { 'Content-Type': 'image/png' }); return res.end(png); }
        if (request.path.endsWith('/confirm')) return send(res, { state: 'ready' });
        if (request.path.startsWith('/preparations') && request.method === 'DELETE') return send(res, { ...snapshot, state: 'cancelled' });
        if (request.path.startsWith('/preparations')) return send(res, snapshot);
        if (request.path === `/${pairID}`) return send(res, { state: 'claimed' });
        return send(res, { error: 'Unknown mock request' }, 404);
      }
      const file = req.url === '/mobile-connect' ? 'mobile-connect.html' : req.url.slice(1);
      if (!/^mobile-connect\.(html|js|css)$/.test(file)) { res.writeHead(404); return res.end(); }
      const data = await readFile(new URL(file, import.meta.url));
      res.writeHead(200, { 'Content-Type': file.endsWith('.js') ? 'text/javascript' : file.endsWith('.css') ? 'text/css' : 'text/html', 'Content-Security-Policy': "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' blob:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'" });
      res.end(data);
    } catch (error) { errors.push(error.message); send(res, { error: 'Fixture failure' }, 500); }
  });
  await new Promise(r => server.listen(0, '127.0.0.1', r));
  const context = await browser.newContext({ viewport: { width: 390, height: 844 } });
  if (init) await context.addInitScript(init);
  const page = await context.newPage(); page.setDefaultTimeout(6000);
  page.on('pageerror', error => errors.push(error.message));
  await page.route('https://**', route => { external.push(route.request().url()); return route.abort(); });
  await page.goto(`http://127.0.0.1:${server.address().port}/mobile-connect#${siteID}`);
  return { page, requests, errors, close: async () => { await context.close(); server.closeAllConnections(); await new Promise(r => server.close(r)); assert.deepEqual(errors, []); assert.deepEqual(external, []); } };
}
async function stopped(page) { await page.getByText('Connection setup stopped. Published changes and any key already sent are not undone.', { exact: true }).waitFor(); }

for (const engine of ['chromium', 'webkit']) test(`${engine}: desktop phone preparation`, async t => {
  const options = engine === 'chromium' && process.env.CHROMIUM_EXECUTABLE ? { executablePath: process.env.CHROMIUM_EXECUTABLE } : {};
  const browser = await playwright[engine].launch({ headless: true, ...options });
  t.after(() => browser.close());
    await t.test('real stages, hosted-upload wait, deadline and cancellation acknowledgement', async () => {
      let stage = 'hosting', stopCalls = 0;
      const f = await fixture(browser, ({ request, snapshot, send }) => {
        if (request.method === 'GET' && request.path.startsWith('/preparations/')) { send({ ...snapshot, stage, message: 'Uploading and verifying the hosted site. Keep this computer awake…' }); return true; }
        if (request.method === 'DELETE') { send({ ...snapshot, state: ++stopCalls === 1 ? 'cancelling' : 'cancelled' }); return true; }
      });
      try {
        await f.page.locator('#start').click();
        await f.page.getByText('Uploading and verifying the hosted site. Keep this computer awake…', { exact: true }).waitFor();
        await f.page.screenshot({ path: `/tmp/croptop-phone-preparing-${engine}-390.png`, fullPage: true });
        await f.page.setViewportSize({ width: 1100, height: 900 });
        await f.page.screenshot({ path: `/tmp/croptop-phone-preparing-${engine}-desktop.png`, fullPage: true });
        assert.match(await f.page.locator('#timing').innerText(), /Time left for this attempt: [45]:/);
        assert.equal(await f.page.locator('#start').isDisabled(), true);
        await f.page.locator('#cancel').click();
        await stopped(f.page);
        assert.equal(stopCalls, 2);
        assert.equal(await f.page.locator('#start').isEnabled(), true);
        assert.equal(await f.page.locator('#connection').isVisible(), false);
        assert.equal(await f.page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
        const posts = f.requests.filter(r => r.method === 'POST'); assert.equal(posts.length, 1);
        assert.equal(posts[0].body.allowPublish, false); assert.equal(posts[0].body.enableHosting, false);
      } finally { await f.close(); }
    });

    await t.test('legacy P2P hosting permission is separate from explicit publication consent', async () => {
      const f = await fixture(browser, ({ request, snapshot, send }) => {
        if (request.path !== '/preparations') return;
        if (!request.body.enableHosting) send({ ...snapshot, state: 'failed', code: 'hosting_required', error: 'Enable hosting' });
        else if (!request.body.allowPublish) send({ ...snapshot, state: 'failed', code: 'publication_required', error: 'Old host metadata needs updating' });
        else send({ ...snapshot, state: 'ready', connection: connection() });
        return true;
      });
      try {
        await f.page.locator('#start').click(); await f.page.locator('#hosting:not([hidden])').waitFor();
        assert.equal(await f.page.locator('#error').isVisible(), false);
        await f.page.locator('#enable-hosting').check(); await f.page.locator('#start').click();
        await f.page.locator('#publication:not([hidden])').waitFor();
        assert.equal(await f.page.locator('#error').isVisible(), false);
        assert.match(await f.page.locator('#publication').innerText(), /all saved site changes public/);
        await f.page.screenshot({ path: `/tmp/croptop-phone-publication-consent-${engine}-390.png`, fullPage: true });
        await f.page.locator('#start').click();
        assert.equal(f.requests.filter(r => r.method === 'POST').length, 2);
        await f.page.locator('#allow-publish').check(); await f.page.locator('#start').click();
        await f.page.locator('#connection:not([hidden])').waitFor();
        assert.deepEqual(f.requests.filter(r => r.method === 'POST').map(r => [r.body.enableHosting, r.body.allowPublish]), [[false, false], [true, false], [true, true]]);
        assert.equal(new Set(f.requests.filter(r => r.method === 'POST').map(r => r.body.id)).size, 3);
        await f.page.screenshot({ path: `/tmp/croptop-phone-connect-${engine}-390.png`, fullPage: true });
      } finally { await f.close(); }
    });

    await t.test('lost POST / 500 recovers same UUID; Retry cannot overlap the automatic status request', async () => {
      const gate = deferred(); let reads = 0;
      const f = await fixture(browser, async ({ request, snapshot, send }) => {
        if (request.path === '/preparations') { send({ error: 'Temporary local response failure' }, 500); return true; }
        if (request.method === 'GET' && request.path.startsWith('/preparations/')) { reads++; await gate.promise; send({ ...snapshot, state: 'ready', connection: connection() }); return true; }
      });
      try {
        await f.page.locator('#start').click(); await f.page.locator('#retry:not([hidden])').waitFor();
        await f.page.waitForFunction(() => document.querySelector('#timing').textContent.includes('Time left'));
        await waitFor(() => reads === 1);
        await f.page.locator('#retry').click();
        assert.equal(reads, 1);
        gate.resolve(); await f.page.locator('#connection:not([hidden])').waitFor();
        assert.equal(f.requests.filter(r => r.method === 'POST').length, 1);
        assert.equal(await f.page.locator('#error').isVisible(), false);
        assert.equal(await f.page.evaluate(() => localStorage.length + sessionStorage.length), 0);
      } finally { gate.resolve(); await f.close(); }
    });

    await t.test('cancel before POST response tombstones UUID and late response cannot revive setup', async () => {
      const gate = deferred(); let oldID;
      const f = await fixture(browser, async ({ request, snapshot, send }) => {
        if (request.path === '/preparations' && !oldID) { oldID = request.body.id; await gate.promise; send({ ...snapshot, state: 'ready', connection: connection() }); return true; }
      }, () => { const fetch = window.fetch; window.fetch = (input, options) => fetch(input, { ...options, signal: undefined }); });
      try {
        await f.page.locator('#start').click(); await f.page.locator('#cancel').click(); await stopped(f.page);
        assert.ok(f.requests.some(r => r.method === 'DELETE' && r.path.endsWith(oldID)));
        await f.page.locator('#start').click();
        gate.resolve(); await f.page.waitForTimeout(150);
        assert.equal(await f.page.locator('#start').isDisabled(), true);
        assert.equal(await f.page.locator('#connection').isVisible(), false);
        assert.equal(await f.page.locator('#phone-link').getAttribute('href'), null);
      } finally { gate.resolve(); await f.close(); }
    });

    await t.test('late QR blob cannot replace a newer QR or resurrect a cancelled flow', async () => {
      const f = await fixture(browser, ({ request, snapshot, send }) => {
        if (request.path === '/preparations') { send({ ...snapshot, state: 'ready', connection: connection() }); return true; }
      }, () => {
        const blob = Response.prototype.blob; let first = true;
        Response.prototype.blob = async function () {
          const value = await blob.call(this);
          if (first && this.url.endsWith('/qr')) { first = false; window.qrWaiting = true; await new Promise(resolve => { window.releaseQR = resolve; }); }
          return value;
        };
      });
      try {
        await f.page.locator('#start').click(); await f.page.waitForFunction(() => window.qrWaiting);
        await f.page.locator('#cancel').click(); await stopped(f.page);
        await f.page.locator('#start').click(); await f.page.locator('#connection:not([hidden])').waitFor();
        const source = await f.page.locator('#qr').getAttribute('src');
        await f.page.evaluate(() => window.releaseQR()); await f.page.waitForTimeout(100);
        assert.equal(await f.page.locator('#qr').getAttribute('src'), source);
        assert.equal(await f.page.locator('#error').isVisible(), false);
      } finally { await f.close(); }
    });

    await t.test('late confirmation success cannot change a new attempt', async () => {
      const gate = deferred(); let attempts = 0, confirms = 0;
      const f = await fixture(browser, async ({ request, snapshot, send }) => {
        if (request.path === '/preparations') { if (++attempts === 1) send({ ...snapshot, state: 'ready', connection: connection() }); else send(snapshot); return true; }
        if (request.path.endsWith('/confirm')) { confirms++; await gate.promise; send({ state: 'ready' }); return true; }
      }, () => { const fetch = window.fetch; window.fetch = (input, options) => fetch(input, { ...options, signal: undefined }); });
      try {
        await f.page.locator('#start').click(); await f.page.locator('#confirm:not([hidden])').waitFor();
        await f.page.locator('#code').fill('1234 5678'); await f.page.locator('#confirm-button').click();
        await f.page.locator('#restart').click(); await stopped(f.page); await f.page.locator('#start').click();
        gate.resolve(); await f.page.waitForTimeout(150);
        assert.equal(confirms, 1); assert.equal(await f.page.locator('#confirm').isVisible(), false);
        assert.equal(await f.page.locator('#code').inputValue(), '');
        assert.equal(await f.page.locator('#start').isDisabled(), true);
        assert.doesNotMatch(await f.page.locator('#status').innerText(), /Finish connecting/);
      } finally { gate.resolve(); await f.close(); }
    });

    await t.test('a stale claimed poll cannot reopen confirmation after successful key delivery', async () => {
      const gate = deferred(); let reads = 0;
      const f = await fixture(browser, async ({ request, snapshot, send }) => {
        if (request.path === '/preparations') { send({ ...snapshot, state: 'ready', connection: connection() }); return true; }
        if (request.path === `/${pairID}` && request.method === 'GET' && ++reads === 2) { await gate.promise; send({ state: 'claimed' }); return true; }
      });
      try {
        await f.page.locator('#start').click(); await f.page.locator('#confirm:not([hidden])').waitFor();
        await waitFor(() => reads === 2);
        await f.page.locator('#code').fill('1234 5678'); await f.page.locator('#confirm-button').click();
        await f.page.getByText('Finish connecting on your phone.', { exact: true }).waitFor();
        gate.resolve(); await f.page.waitForTimeout(150);
        assert.equal(await f.page.locator('#confirm').isVisible(), false);
        assert.match(await f.page.locator('#status').innerText(), /Finish connecting/);
      } finally { gate.resolve(); await f.close(); }
    });

    await t.test('late poll failure cannot attach an old error to a new attempt', async () => {
      const gate = deferred(); let reads = 0, attempts = 0;
      const f = await fixture(browser, async ({ request, snapshot, send }) => {
        if (request.path === '/preparations') { send(++attempts === 1 ? { ...snapshot, state: 'ready', connection: connection() } : snapshot); return true; }
        if (request.path === `/${pairID}` && request.method === 'GET' && ++reads === 2) { await gate.promise; send({ error: 'Old connection failed' }, 500); return true; }
      }, () => { const fetch = window.fetch; window.fetch = (input, options) => fetch(input, { ...options, signal: undefined }); });
      try {
        await f.page.locator('#start').click(); await f.page.locator('#confirm:not([hidden])').waitFor();
        await waitFor(() => reads === 2);
        await f.page.locator('#restart').click(); await stopped(f.page); await f.page.locator('#start').click();
        gate.resolve(); await f.page.waitForTimeout(150);
        assert.equal(await f.page.locator('#error').isVisible(), false);
        assert.equal(await f.page.locator('#connection').isVisible(), false);
        assert.equal(await f.page.locator('#start').isDisabled(), true);
      } finally { gate.resolve(); await f.close(); }
    });

    await t.test('pairing expiry erases QR and capability even while status polling has failed', async () => {
      const f = await fixture(browser, ({ request, snapshot, send }) => {
        if (request.path === '/preparations') { send({ ...snapshot, state: 'ready', connection: { ...connection(), expiresAt: Math.floor(Date.now() / 1000) + 2 } }); return true; }
        if (request.path === `/${pairID}` && request.method === 'GET') { send({ error: 'Temporary status failure' }, 500); return true; }
      });
      try {
        await f.page.clock.install();
        await f.page.locator('#start').click(); await f.page.locator('#error:not([hidden])').waitFor();
        await f.page.clock.fastForward(3000); await stopped(f.page);
        assert.equal(await f.page.locator('#qr').getAttribute('src'), null);
        assert.equal(await f.page.locator('#phone-link').getAttribute('href'), null);
        assert.equal(await f.page.locator('#connection').isVisible(), false);
      } finally { await f.close(); }
    });

    await t.test('lost start response is bounded before any server deadline is received', async () => {
      const gate = deferred();
      const f = await fixture(browser, async ({ request, snapshot, send }) => {
        if (request.path === '/preparations') { await gate.promise; send(snapshot); return true; }
      });
      try {
        await f.page.clock.install();
        await f.page.locator('#start').click(); await waitFor(() => f.requests.some(request => request.method === 'POST'));
        await f.page.clock.fastForward(15001);
        await f.page.locator('#error:not([hidden])').waitFor();
        assert.match(await f.page.locator('#error').innerText(), /15 seconds/);
        await f.page.clock.fastForward(300001); await stopped(f.page);
        assert.equal(f.requests.filter(r => r.method === 'POST').length, 1);
        assert.equal(f.requests.filter(r => r.method === 'DELETE').length, 1);
      } finally { gate.resolve(); await f.close(); }
    });

    await t.test('non-JSON server failures show a recoverable error without raw HTML', async () => {
      let reads = 0;
      const f = await fixture(browser, ({ request, snapshot, send, res }) => {
        if (request.method === 'GET' && request.path.startsWith('/preparations/')) {
          if (++reads === 1) { res.writeHead(500, { 'Content-Type': 'text/html' }); res.end('<h1>Private debug details</h1>'); }
          else send({ ...snapshot, state: 'ready', connection: connection() });
          return true;
        }
      });
      try {
        await f.page.locator('#start').click(); await f.page.locator('#retry:not([hidden])').waitFor();
        assert.match(await f.page.locator('#error').innerText(), /unreadable response/);
        assert.doesNotMatch(await f.page.locator('body').innerText(), /Private debug details/);
        await f.page.locator('#retry').click(); await f.page.locator('#connection:not([hidden])').waitFor();
        assert.equal(f.requests.filter(r => r.method === 'POST').length, 1);
      } finally { await f.close(); }
    });

    await t.test('connection URLs bind the session and reject noncanonical capabilities or unsafe targets', async () => {
      let bad;
      const f = await fixture(browser, ({ request, snapshot, send }) => {
        if (request.path === '/preparations') { send({ ...snapshot, state: 'ready', connection: { ...connection(), ...bad } }); return true; }
      });
      try {
        const base = connection().url;
        for (bad of [
          { url: base.replace('https:', 'http:') },
          { url: base.replace('phone.example', 'user:secret@phone.example') },
          { url: base.replace('/#', '/unexpected#') },
          { url: base.replace('/#', '/?query=unexpected#') },
          { url: base.replace(`#pair=${pairID}`, `#pair=${capability}`) },
          { url: base.replace(capability, 'B'.repeat(43)) },
          { expiresAt: 0 },
          { expiresAt: Math.floor(Date.now() / 1000) + 9000 },
        ]) {
          await f.page.locator('#start').click(); await f.page.locator('#error:not([hidden])').waitFor();
          assert.equal(await f.page.locator('#phone-link').getAttribute('href'), null);
          assert.equal(await f.page.locator('#qr').getAttribute('src'), null);
          await f.page.locator('#cancel').click(); await stopped(f.page);
        }
        assert.equal(f.requests.filter(request => request.path.endsWith('/qr')).length, 0);
      } finally { await f.close(); }
    });

    await t.test('deadline clears pending work; restored page resets to safe idle state', async () => {
      const f = await fixture(browser, ({ request, snapshot, send }) => {
        if (request.path === '/preparations') { send({ ...snapshot, deadline: Math.floor(Date.now() / 1000) - 1 }); return true; }
      });
      try {
        await f.page.locator('#start').click(); await stopped(f.page);
        assert.equal(f.requests.filter(r => r.method === 'DELETE').length, 1);
        await f.page.evaluate(() => { window.dispatchEvent(new PageTransitionEvent('pagehide', { persisted: true })); window.dispatchEvent(new PageTransitionEvent('pageshow', { persisted: true })); });
        assert.equal(await f.page.locator('#start').isEnabled(), true);
        assert.equal(await f.page.locator('#setup').isVisible(), true);
        assert.equal(await f.page.locator('#phone-link').getAttribute('href'), null);
      } finally { await f.close(); }
    });
});

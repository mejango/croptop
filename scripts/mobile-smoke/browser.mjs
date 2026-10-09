// Default mode only reads public config. --publish explicitly authorizes one
// synthetic post on this harness's new site; reruns recover the same operation.
import assert from 'node:assert/strict';
import { createHash, createPrivateKey } from 'node:crypto';
import { chmod, mkdir, readFile, rename, stat, writeFile } from 'node:fs/promises';
import { resolve, join } from 'node:path';
import { pathToFileURL } from 'node:url';

const fixtureKind = 'croptop-isolated-mobile-smoke-v1';
const title = 'Mobile production smoke test';
const caption = 'Synthetic fixture. Published from the mobile website while the bootstrap publisher is stopped.';
const hash = value => createHash('sha256').update(value).digest('hex');

export function parseArgs(args) {
  const result = { publish: false, browser: 'chromium' };
  for (let i = 0; i < args.length; i++) {
    const arg = args[i];
    if (arg === '--publish') result.publish = true;
    else if (['--dir', '--origin', '--browser'].includes(arg)) {
      if (!args[i + 1] || args[i + 1].startsWith('--')) throw new Error('Missing value for ' + arg);
      result[arg.slice(2)] = args[++i];
    } else throw new Error('Unknown argument: ' + arg);
  }
  if (!result.dir || !result.origin) throw new Error('Provide --dir /absolute/fixture and --origin https://trusted-composer-origin');
  result.dir = resolve(result.dir);
  const origin = new URL(result.origin);
  if (origin.protocol !== 'https:' || origin.username || origin.password || origin.pathname !== '/' || origin.search || origin.hash) throw new Error('--origin must be a bare HTTPS origin');
  result.origin = origin.origin;
  if (!['chromium', 'webkit'].includes(result.browser)) throw new Error('--browser must be chromium or webkit');
  return result;
}

// Include raw and common transport encodings. Violations are reported without
// request URLs, bodies, headers, or key material; the offending request is blocked.
export function privateKeyDetector(pem) {
  const key = createPrivateKey(pem);
  const der = key.export({ format: 'der', type: 'pkcs8' });
  const seed = Buffer.from(key.export({ format: 'jwk' }).d, 'base64url');
  const needles = [Buffer.from('PRIVATE KEY'), Buffer.from(pem.trim()), der, seed,
    Buffer.from(der.toString('base64')), Buffer.from(seed.toString('base64')),
    Buffer.from(seed.toString('base64url')), Buffer.from(seed.toString('hex')),
    Buffer.from(JSON.stringify(pem).slice(1, -1)), Buffer.from(encodeURIComponent(pem))];
  return value => {
    const bytes = Buffer.isBuffer(value) ? value : Buffer.from(value || '');
    return needles.some(needle => needle.length && bytes.includes(needle));
  };
}

async function jsonFile(path) {
  try { return JSON.parse(await readFile(path, 'utf8')); }
  catch (error) { if (error.code === 'ENOENT') return null; throw error; }
}
async function saveJSON(path, value) {
  await writeFile(path + '.tmp', JSON.stringify(value, null, 2) + '\n', { mode: 0o600 });
  await rename(path + '.tmp', path);
}
async function publicResponse(url) {
  const response = await fetch(url, { redirect: 'error', cache: 'no-store', signal: AbortSignal.timeout(60000) });
  if (!response.ok) throw new Error('Public verification returned HTTP ' + response.status);
  return response;
}
const publicJSON = async url => (await publicResponse(url)).json();
const readDraft = page => page.evaluate(async () => {
  const value = await (await import('./storage.js')).read('draft');
  if (!value) return null;
  const { id, ipns, title, caption, submitted, operation } = value;
  return { id, ipns, title, caption, submitted, operation };
});

export async function run(o) {
  const fixture = await jsonFile(join(o.dir, 'fixture.json'));
  assert.ok(fixture?.kind === fixtureKind && fixture.host === 'https://crop.top', 'Use only a dedicated fixture created by this smoke harness');
  assert.equal(fixture.keyFile, join(o.dir, 'site-key.pem'));
  assert.equal(fixture.imageFile, join(o.dir, 'synthetic.png'));
  const config = await publicJSON(o.origin + '/v0/mobile/config');
  assert.equal(config.origin, o.origin, 'Composer configuration must bind the exact selected origin');
  assert.equal(config.host, 'crop.top', 'Composer must publish to the fixed production host');
  assert.equal(config.enabled, true, 'Mobile service is not enabled yet');
  if (!o.publish) return { mode: 'read-only-preflight', origin: o.origin, ipns: fixture.ipns, enabled: true, publishedFixture: !!fixture.publishedAt, next: 'Add --publish to explicitly authorize one synthetic test post.' };
  assert.ok(fixture.publishedAt, 'Publish the initial empty fixture using the guarded bootstrap before browser publishing');
  assert.equal((await stat(fixture.keyFile)).mode & 0o777, 0o600, 'Fixture key file must have mode 0600');
  const runPath = join(o.dir, 'browser-run.json');
  let runState = await jsonFile(runPath);
  const profile = join(o.dir, 'browser-profile');
  if (runState) {
    assert.equal(runState.origin, o.origin, 'Do not move a retained draft to a different service origin');
    assert.equal(runState.ipns, fixture.ipns);
    assert.equal(runState.browser, o.browser, 'Keep the same browser engine/profile to preserve the operation identity');
  }
  await mkdir(profile, { mode: 0o700, recursive: true });
  await chmod(profile, 0o700);
  const pem = await readFile(fixture.keyFile, 'utf8');
  const leaksKey = privateKeyDetector(pem);
  const playwright = await import(process.env.PLAYWRIGHT_MODULE || 'playwright');
  const launch = { headless: true, viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, serviceWorkers: 'block' };
  if (o.browser === 'chromium' && process.env.CHROMIUM_EXECUTABLE) launch.executablePath = process.env.CHROMIUM_EXECUTABLE;
  // Blocking service workers keeps every actual outbound request inspectable.
  // Offline shell behavior has its own browser tests; this smoke proves live
  // publishing, reload recovery, and keyless request bodies against production.
  const context = await playwright[o.browser].launchPersistentContext(profile, launch);
  let outboundRequests = 0;
  let keyViolation = false;
  let pageFailure = false;
  const safeError = message => new Error(message); // never echo server/key data
  try {
    await context.route('**/*', async route => {
      const request = route.request();
      const body = request.postDataBuffer();
      const headers = JSON.stringify(await request.allHeaders());
      if (leaksKey(request.url()) || leaksKey(headers) || leaksKey(body)) {
        keyViolation = true;
        await route.abort('blockedbyclient');
        return;
      }
      if (new URL(request.url()).origin !== o.origin) {
        pageFailure = true;
        await route.abort('blockedbyclient');
        return;
      }
      outboundRequests++;
      await route.continue();
    });
    const pages = context.pages();
    const page = pages[0] || await context.newPage();
    for (const extra of pages.slice(1)) await extra.close();
    page.on('pageerror', () => { pageFailure = true; });
    page.setDefaultTimeout(120000);
    await page.goto(o.origin, { waitUntil: 'domcontentloaded' });
    await page.locator('#image-help').filter({ hasText: '20 MB' }).waitFor();
    const connection = await page.evaluate(async () => {
      const connection = await (await import('./storage.js')).read('connection');
      return connection ? { ipns: connection.ipns, extractable: connection.key.extractable } : null;
    });
    if (!connection) {
      await page.locator('#import-details').evaluate(element => { element.open = true; });
      await page.locator('#key-file').setInputFiles(fixture.keyFile);
    } else {
      assert.equal(connection.ipns, fixture.ipns, 'Profile contains another site; refusing to touch it');
      assert.equal(connection.extractable, false);
    }
    await page.waitForFunction(() => {
      const status = document.getElementById('site-status').textContent;
      return status.includes('Your computer can sleep') || status.includes('Allow phone posting') || (!document.getElementById('error').hidden);
    });
    if (await page.locator('#error').isVisible()) throw safeError('Site connection failed; inspect the composer manually using this isolated browser profile');
    if (await page.locator('#enable').isVisible()) await page.locator('#enable').click();
    await page.locator('#site-status').filter({ hasText: 'Your computer can sleep' }).waitFor();
    const savedConnection = await page.evaluate(async () => {
      const value = await (await import('./storage.js')).read('connection');
      return { ipns: value.ipns, extractable: value.key.extractable };
    });
    assert.deepEqual(savedConnection, { ipns: fixture.ipns, extractable: false });

    let draft = await readDraft(page);
    if (runState?.operationID && !draft) throw safeError('Retained operation exists but browser draft is missing; refusing to create a duplicate');
    if (!draft) {
      await page.locator('#image').setInputFiles(fixture.imageFile);
      await page.locator('#draft-status').filter({ hasText: 'Draft saved' }).waitFor();
      await page.locator('#caption').fill(caption);
      await page.locator('details.more').evaluate(element => { element.open = true; });
      await page.locator('#title').fill(title);
      await page.locator('#draft-status').filter({ hasText: 'Draft saved' }).waitFor();
      draft = await readDraft(page);
    }
    assert.ok(draft?.id, 'Composer must retain a draft identity');
    if (runState?.operationID) assert.equal(draft.id, runState.operationID, 'Refusing to submit a different operation on rerun');
    assert.equal(draft.title, title, 'Retained draft is not the synthetic smoke fixture');
    assert.equal(draft.caption, caption);
    assert.ok(!draft.ipns || draft.ipns === fixture.ipns);
    runState = { ...runState, version: 1, origin: o.origin, ipns: fixture.ipns, browser: o.browser, operationID: draft.id };
    await saveJSON(runPath, runState); // durable identity BEFORE any upload

    if (draft.operation?.state !== 'published') {
      if ((await page.locator('#publish').textContent()) !== 'Publish this preview') await page.locator('#publish').click();
      await page.waitForFunction(() => !document.getElementById('receipt').hidden || document.getElementById('publish').textContent === 'Publish this preview' || !document.getElementById('error').hidden);
      if (await page.locator('#error').isVisible()) throw safeError('Preparation failed; rerun with the same directory/profile after investigating service health');
      if (!(await page.locator('#receipt').isVisible())) {
        await page.locator('#preview-notice').waitFor();
        assert.equal((await readDraft(page)).id, runState.operationID);
        await page.screenshot({ path: join(o.dir, 'prepared-preview.png'), fullPage: true });
        // This second, explicit UI action signs only the reviewed preparation.
        await page.locator('#publish').filter({ hasText: 'Publish this preview' }).click();
        // Do not reload while the asynchronous click handler is still signing
        // locally. Wait until its commit is in flight/acknowledged or uncertain.
        await page.waitForFunction(async () => {
          const saved = await (await import('./storage.js')).read('draft');
          return ['committing', 'published'].includes(saved?.operation?.state) || !document.getElementById('error').hidden;
        }, null, { timeout: 180000 });
      }
    }
    // Reload whether the commit response arrives or is lost. The application
    // must recover the same public receipt; no second identity is allocated.
    await page.reload({ waitUntil: 'domcontentloaded' });
    await page.locator('#receipt').waitFor({ timeout: 180000 });
    const finalDraft = await readDraft(page);
    assert.equal(finalDraft.id, runState.operationID);
    assert.equal(finalDraft.operation.state, 'published');
    assert.equal(finalDraft.operation.postID, runState.operationID);
    assert.equal(finalDraft.operation.ipns, fixture.ipns);
    assert.equal(await page.locator('#post-link').getAttribute('href'), finalDraft.operation.url);
    await page.screenshot({ path: join(o.dir, 'published-receipt.png'), fullPage: true });
    assert.equal(keyViolation, false, 'A private-key request was blocked');
    assert.equal(pageFailure, false, 'Composer encountered a browser error or cross-origin request');

    const head = await publicJSON(fixture.host + '/v0/host/keys/' + fixture.ipns + '?smoke=' + Date.now());
    assert.notEqual(head.cid, fixture.initialCID, 'Mobile post must advance the hosted head');
    const base = fixture.host + '/ipfs/' + head.cid + '/';
    const planet = await publicJSON(base + 'planet.json');
    assert.equal(planet.ipns, fixture.ipns);
    assert.equal(planet.articles.length, 1, 'Dedicated smoke site must contain exactly one post, including after reruns');
    assert.equal(planet.articles.filter(post => post.id === runState.operationID).length, 1);
    const article = await publicJSON(base + runState.operationID + '/article.json');
    assert.equal(article.id, runState.operationID);
    assert.equal(article.title, title);
    assert.equal(article.content, caption);
    assert.match(article.heroImageFilename, /^[a-f0-9]{64}\.png$/);
    const image = Buffer.from(await (await publicResponse(base + runState.operationID + '/' + article.heroImageFilename)).arrayBuffer());
    assert.equal(hash(image), finalDraft.operation.mediaSHA256, 'Public image must match the normalized preview hash');
    const receipt = new URL(finalDraft.operation.url);
    assert.equal(receipt.protocol, 'https:');
    assert.equal(receipt.hostname, fixture.ipns + '.crop.top');
    assert.equal(receipt.pathname, '/' + runState.operationID + '/');
    const html = await (await publicResponse(receipt.href)).text();
    assert.ok(html.includes(title), 'Public receipt page must contain the synthetic post title');
    runState = { ...runState, status: 'verified', url: receipt.href, cid: head.cid, sequence: String(head.sequence), outboundRequests, verifiedAt: new Date().toISOString(), privateKeyAbsentFromOutboundRequests: true, exactlyOnePost: true };
    await saveJSON(runPath, runState);
    return runState;
  } finally {
    await context.close();
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  try { process.stdout.write(JSON.stringify(await run(parseArgs(process.argv.slice(2))), null, 2) + '\n'); }
  catch (error) {
    // Assertion messages contain only public fixture fields. Never emit browser
    // network dumps, PEM content, API sessions, or a persistent profile archive.
    process.stderr.write('Mobile smoke failed: ' + error.message + '\n');
    process.exitCode = 1;
  }
}

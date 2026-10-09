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

// A locally saved "committing" state precedes network dispatch. This gate
// proves the exact commit was accepted by the service before simulating a lost
// response, so navigation cannot accidentally cancel the only commit request.
export function commitRecoveryGate(origin, operationID, { timeout = 180000 } = {}) {
  const target = new URL('/v0/mobile/operations/' + encodeURIComponent(operationID) + '/commit', origin).href;
  let resolve, reject, settled = false, unauthorized = 0;
  const accepted = new Promise((yes, no) => { resolve = yes; reject = no; });
  accepted.catch(() => {}); // A route can fail before the click promise finishes.
  const finish = (error, value) => {
    if (settled) return;
    settled = true;
    clearTimeout(timer);
    if (error) reject(error); else resolve(value);
  };
  const timer = setTimeout(() => finish(new Error('Commit acceptance was not observed; keep the same directory/profile and operation before retrying')), timeout);
  return {
    accepted,
    async handle(route) {
      const request = route.request();
      if (request.method() !== 'POST' || request.url() !== target) return false;
      if (settled) { await route.abort('failed'); return true; }
      let response;
      try {
        // The caller already inspected this request for private-key material.
        // Do not forward its authorization through any HTTP redirect.
        response = await route.fetch({ maxRedirects: 0, timeout });
        const status = response.status();
        if (status === 401 && unauthorized++ === 0) {
          // Preserve the app's single session-renewal retry on the same ID.
          await route.fulfill({ response });
          return true;
        }
        if (status !== 200 && status !== 202) {
          await route.fulfill({ response });
          finish(new Error('Commit was not accepted (HTTP ' + status + '); retain the same directory/profile before retrying'));
          return true;
        }
        // A service response proves dispatch and durable acceptance. Withhold
        // it from the page deliberately; the subsequent reload must use GET.
        await route.abort('failed');
        finish(null, { status, responseWithheld: true });
      } catch {
        await route.abort('failed').catch(() => {});
        finish(new Error('Commit transport is uncertain; retain the same directory/profile and check this operation before retrying'));
      } finally {
        await response?.dispose().catch(() => {});
      }
      return true;
    },
    close() { finish(new Error('Commit recovery check interrupted; retain the same directory/profile')); },
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

export function isExpiredRetainedProposal(draft, expectedID, expectedIPNS, now = Math.floor(Date.now() / 1000)) {
  const operation = draft?.operation;
  return !!expectedID && draft?.id === expectedID && draft.ipns === expectedIPNS && draft.submitted === true &&
    operation?.id === expectedID && operation.postID === expectedID && operation.ipns === expectedIPNS &&
    operation.state === 'needs_signature' && Number.isSafeInteger(operation.proposal?.expiresAt) &&
    operation.proposal.expiresAt > 0 && operation.proposal.expiresAt <= now;
}

export async function waitForComposerConfig(page) {
  // A retained published receipt hides the composer, including this text.
  await page.locator('#image-help').filter({ hasText: '20 MB' }).waitFor({ state: 'attached' });
}

export async function prepareWithoutSigning(page) {
  await page.waitForFunction(() => !document.getElementById('receipt').hidden || !document.getElementById('publish').disabled);
  const publish = page.locator('#publish');
  if (await page.locator('#receipt').isVisible() || await publish.textContent() === 'Publish this preview') return;
  try {
    // If the label changes during Playwright's actionability wait, this locator
    // stops matching. The preparation click can never turn into a signing click.
    await publish.filter({ hasNotText: /^Publish this preview$/ }).click({ timeout: 10000 });
  } catch {
    if (!(await page.locator('#receipt').isVisible()) && await publish.textContent() !== 'Publish this preview') {
      throw new Error('Preparation did not become ready; retain the same directory/profile before retrying');
    }
  }
}

export function assertCommitRecoveryEvidence(initialState, acceptance) {
  if (!['published', 'committing'].includes(initialState)) {
    assert.equal(acceptance?.responseWithheld, true, 'A new commit must prove service acceptance and withheld response before claiming recovery');
  }
}

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
  let commitGate;
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
      if (commitGate && await commitGate.handle(route)) return;
      await route.continue();
    });
    const pages = context.pages();
    const page = pages[0] || await context.newPage();
    for (const extra of pages.slice(1)) await extra.close();
    page.on('pageerror', () => { pageFailure = true; });
    page.setDefaultTimeout(120000);
    await page.goto(o.origin, { waitUntil: 'domcontentloaded' });
    await waitForComposerConfig(page);
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
    // Startup also reconciles the saved operation. Wait until that finishes so
    // a late preview-expiry message cannot be mistaken for a connection error.
    await page.waitForFunction(() => !document.getElementById('refresh-site').disabled);
    if (await page.locator('#error').isVisible()) {
      const siteReady = (await page.locator('#site-status').textContent()).includes('Your computer can sleep');
      const canRefresh = isExpiredRetainedProposal(await readDraft(page), runState?.operationID, fixture.ipns);
      if (!siteReady || !canRefresh) throw safeError('Site connection failed; inspect the composer manually using this isolated browser profile');
      // The normal Publish/Check publication action below re-prepares this
      // expired proposal under the same verified draft identity before review.
    }
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

    let acceptance;
    if (draft.operation?.state !== 'published') {
      await prepareWithoutSigning(page);
      await page.waitForFunction(() => !document.getElementById('receipt').hidden || document.getElementById('publish').textContent === 'Publish this preview' || !document.getElementById('error').hidden);
      if (await page.locator('#error').isVisible()) throw safeError('Preparation failed; rerun with the same directory/profile after investigating service health');
      if (!(await page.locator('#receipt').isVisible())) {
        await page.locator('#preview-notice').waitFor();
        assert.equal((await readDraft(page)).id, runState.operationID);
        await page.screenshot({ path: join(o.dir, 'prepared-preview.png'), fullPage: true });
        // This second, explicit UI action signs only the reviewed preparation.
        commitGate = commitRecoveryGate(o.origin, runState.operationID);
        await page.locator('#publish').filter({ hasText: 'Publish this preview' }).click();
        acceptance = await commitGate.accepted;
        runState = { ...runState, commitResponseWithheld: acceptance.responseWithheld };
        await saveJSON(runPath, runState);
      }
    }
    assertCommitRecoveryEvidence(draft.operation?.state, acceptance);
    // The commit was accepted before its response was withheld (or an earlier
    // run already published). Reload must recover the same public receipt.
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
    commitGate?.close();
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

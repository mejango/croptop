// The console's DOM and requests are real; IPFS and hosting APIs are fixtures.
import {test} from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import {fileURLToPath} from 'node:url';
const {chromium} = await import(process.env.PLAYWRIGHT_MODULE || 'playwright');
const root = fileURLToPath(new URL('../', import.meta.url));
const id = '11111111-1111-1111-1111-111111111111';

test('Storage requires saved opt-in and preserves settings across tabs and P2P switches', async () => {
  const server = http.createServer(async (req, res) => {
    const path = req.url === '/' ? 'web/index.html' : req.url.startsWith('/fonts/') ? 'templates/croptop/assets/' + req.url.slice(7) : 'web' + req.url;
    try {
      const body = await readFile(root + path);
      res.setHeader('Content-Type', path.endsWith('.js') ? 'text/javascript' : path.endsWith('.css') ? 'text/css' : path.endsWith('.woff2') ? 'font/woff2' : 'text/html');
      res.end(body);
    } catch { res.statusCode = 404; res.end(); }
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  let browser;
  try {
    browser = await chromium.launch({headless: true});
    for (const fixture of [{storage: undefined, host: '', gateway: 'crop.top'}, {storage: 'invalid', host: 'https://peer.example', gateway: 'crop.top'}, {storage: 'p2p', host: 'https://peer.example', gateway: 'sucks'}]) {
      const {storage, host, gateway} = fixture;
      const context = await browser.newContext({viewport: {width: 1100, height: 850}});
      const page = await context.newPage();
      const errors = [], mutations = [];
      const site = {id, name: 'Legacy site', about: 'About this site', ipns: 'k51-test', tags: {art: 'art'}, croptopHost: host, croptopGateway: gateway, croptopName: 'legacy', lastPublishedCID: 'bafy-test', croptopURL: 'https://k51-test.eth.sucks/'};
      if (storage !== undefined) site.croptopStorage = storage;
      const settings = {backgroundColor: '', foregroundColor: '', highlightColor: '#f056c1', collectionCategory: '1', advancedOption: 'kept'};
      page.on('pageerror', error => errors.push(error.message));
      page.setDefaultTimeout(10000);
      await page.route('**/v0/**', async route => {
        const request = route.request(), path = new URL(request.url()).pathname;
        let result;
        if (request.method() !== 'GET') mutations.push({method: request.method(), path, body: request.postDataJSON()});
        if (path === '/v0/croptop/status') result = {version: 'test', ipfs: {running: true, peers: 1}};
        else if (path === '/v0/planets/my') result = [site];
        else if (path === '/v0/croptop/following') result = [];
        else if (path === '/v0/croptop/template') result = {settings: {highlightColor: {name: 'Link and button color'}, foregroundColor: {name: 'Foreground color'}, backgroundColor: {name: 'Background color'}, collectionCategory: {name: 'Category'}, advancedOption: {name: 'Advanced option', advanced: true}}};
        else if (path === '/v0/croptop/gateways') result = [{Key: 'crop.top', Name: 'crop.top'}, {Key: 'sucks', Name: 'eth.sucks'}];
        else if (path === `/v0/croptop/sites/${id}/settings`) {
          if (request.method() === 'PATCH') Object.assign(settings, request.postDataJSON());
          result = settings;
        } else if (path === `/v0/croptop/sites/${id}`) {
          const body = request.postDataJSON();
          Object.assign(site, {name: body.name, about: body.about, domain: body.domain, tags: body.tags, croptopStorage: body.storage, croptopHost: body.host, croptopGateway: body.gateway, ...body.custom});
          site.croptopURL = body.storage === 'hosted' ? 'https://crop.top/legacy/' : 'https://k51-test.eth.sucks/';
          result = site;
        } else if (path === `/v0/croptop/sites/${id}/name`) result = {croptopName: 'legacy'};
        else if (path === `/v0/planets/my/${id}/articles`) result = [];
        else if (path === `/v0/croptop/sites/${id}/hosts`) result = {count: 1, self: 'self', peers: ['self']};
        else { errors.push('Unexpected API: ' + path); result = {}; }
        await route.fulfill({json: result});
      });
      const base = `http://127.0.0.1:${server.address().port}`;
      const tab = name => page.getByRole('tab', {name, exact: true});
      await page.goto(`${base}/#/site/${id}/settings`);
      await tab('Storage').waitFor();
      assert.deepEqual(await page.getByRole('tab').allTextContents(), ['Site', 'Domain', 'Storage', 'Money', 'Advanced']);
      assert.deepEqual(await page.locator('#settings-site [name^="ts:"]').evaluateAll(inputs => inputs.map(input => ({name: input.name, label: input.closest('label').firstChild.textContent}))), [
        {name: 'ts:backgroundColor', label: 'Background color'},
        {name: 'ts:foregroundColor', label: 'Foreground color'},
        {name: 'ts:highlightColor', label: 'Link color'},
      ]);
      assert.equal(await page.getByRole('textbox', {name: 'Link color hex value', exact: true}).count(), 1);
      assert.equal(await page.locator('input[type=color][aria-label="Link color"]').count(), 1);
      assert.equal(await page.getByRole('button', {name: 'Use automatic colors'}).count(), 0);
      if (storage === undefined) await page.screenshot({path: '/tmp/croptop-colors-desktop.png', fullPage: true});
      await tab('Domain').click();
      assert.equal(await page.getByRole('button', {name: 'Claim', exact: true}).isDisabled(), true);
      await page.getByRole('button', {name: 'Open Storage'}).click();
      assert.equal(await page.locator('[name=storage]').isChecked(), false);
      assert.equal(await page.getByText(host ? 'Use a reliable host' : 'Use crop.top', {exact: true}).count(), 1);
      if (host) assert.equal(await page.getByText(`Publishing sends a copy to ${host} instead of crop.top.`, {exact: false}).isVisible(), true);
      if (storage === undefined) await page.screenshot({path: '/tmp/croptop-storage-desktop.png', fullPage: true});
      await page.locator('[name=storage]').check();
      await tab('Domain').click();
      assert.equal(await page.getByRole('button', {name: 'Claim', exact: true}).isDisabled(), true);
      assert.equal(mutations.length, 0, 'Checking an unsaved option must not enable hosting');
      await tab('Site').click();
      await page.locator('[name=about]').fill('Edited on the Site tab');
      await page.locator('[name="ts:highlightColor"]').fill('#abc');
      await tab('Money').click();
      await page.locator('[name="ts:collectionCategory"]').fill('2');
      await tab('Advanced').click();
      await page.locator('[name=customCodeHead]').fill('<meta name="example" content="kept">');
      await page.getByRole('button', {name: 'Save settings', exact: true}).click();
      await page.getByRole('heading', {name: 'Legacy site', exact: true}).waitFor();
      assert.equal(mutations[0].body.storage, 'hosted');
      assert.equal(mutations[0].body.host, host);
      assert.equal(mutations[0].body.gateway, gateway);
      assert.equal(mutations[0].body.about, 'Edited on the Site tab');
      assert.equal(mutations[0].body.custom.customCodeHeadEnabled, true);
      assert.deepEqual(mutations[1].body, {highlightColor: '#abc', collectionCategory: '2'});
      assert.equal(await page.locator('.state a').getAttribute('href'), site.croptopURL, 'Use the canonical URL returned by the server');
      await page.getByRole('link', {name: 'Settings', exact: true}).click();
      await tab('Domain').click();
      assert.equal(await page.getByRole('button', {name: 'Claim', exact: true}).isEnabled(), true);
      await tab('Storage').click();
      await page.getByText('Use a custom host', {exact: true}).click();
      await page.locator('[name=host]').fill('https://unsaved.example');
      await tab('Domain').click();
      assert.equal(await page.getByRole('button', {name: 'Claim', exact: true}).isDisabled(), true);
      await tab('Storage').click();
      await page.locator('[name=host]').fill(host);
      await tab('Domain').click();
      const beforeClaim = mutations.length;
      await page.getByRole('button', {name: 'Claim', exact: true}).click();
      await page.getByText('Claimed legacy. Publish to push the site there.', {exact: true}).waitFor();
      assert.equal(mutations.length, beforeClaim + 1);
      assert.equal(mutations.at(-1).path, `/v0/croptop/sites/${id}/name`, 'Claim must not perform an implicit storage or host save');
      await tab('Storage').click();
      await page.locator('[name=storage]').uncheck();
      await page.getByRole('button', {name: 'Save settings', exact: true}).click();
      await page.getByRole('heading', {name: 'Legacy site', exact: true}).waitFor();
      const saved = mutations.filter(mutation => mutation.method === 'PUT').at(-1).body;
      assert.equal(saved.storage, 'p2p');
      assert.equal(saved.host, host);
      assert.equal(saved.gateway, gateway);
      assert.equal(await page.locator('.state a').getAttribute('href'), 'https://k51-test.eth.sucks/');
      await page.getByRole('link', {name: 'Settings', exact: true}).click();
      await tab('Storage').click();
      assert.equal(await page.locator('[name=storage]').isChecked(), false);
      await tab('Site').click();
      await page.locator('[name="ts:backgroundColor"]').fill('invalid');
      await tab('Advanced').click();
      const beforeInvalid = mutations.length;
      await page.getByRole('button', {name: 'Save settings', exact: true}).click();
      assert.equal(await tab('Site').getAttribute('aria-selected'), 'true', 'Invalid color in a hidden tab is made visible');
      assert.equal(mutations.length, beforeInvalid);
      await page.locator('[name="ts:backgroundColor"]').fill('');
      await tab('Storage').click();
      await page.setViewportSize({width: 390, height: 844});
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
      if (storage === undefined) {
        await page.locator('#toast').waitFor({state: 'hidden'});
        await page.screenshot({path: '/tmp/croptop-storage-mobile.png', fullPage: true});
        await tab('Site').click();
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
        await page.screenshot({path: '/tmp/croptop-colors-mobile.png', fullPage: true});
      }
      assert.deepEqual(errors, []);
      await context.close();
    }
  } finally {
    await browser?.close();
    await new Promise(resolve => server.close(resolve));
  }
});

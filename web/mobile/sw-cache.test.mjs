import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { createHash } from 'node:crypto';

test('offline shell manifest covers exact shipped assets and never caches API/draft content', async () => {
  const source = await readFile(new URL('./sw.js', import.meta.url), 'utf8');
  const entries = JSON.parse(source.match(/const ASSET_DIGESTS = (\{[\s\S]*?\});/)[1]);
  assert.deepEqual(Object.keys(entries), ['/', '/index.html', '/app.js', '/protocol.js', '/pairing.js', '/storage.js', '/style.css', '/manifest.webmanifest', '/icon.svg', '/fonts/SimplonNorm-Regular-WebXL.woff2', '/fonts/SimplonNorm-Bold-WebXL.woff2']);
  for (const [path, expected] of Object.entries(entries)) {
    const file = path === '/' ? './index.html' : path.startsWith('/fonts/') ? '../../templates/croptop/assets/' + path.split('/').at(-1) : '.' + path;
    const actual = createHash('sha256').update(await readFile(new URL(file, import.meta.url))).digest('hex');
    assert.equal(actual, expected, `Update sw.js digest for ${path}; a release must install one complete asset version`);
  }
  assert.doesNotMatch(source, /self\.skipWaiting\(/, 'An active composer must not switch versions');
});

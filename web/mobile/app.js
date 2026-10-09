import { importSiteKey, signChallenge, signProposal, validateProposal } from './protocol.js';
import { read, save, remove } from './storage.js';
import { hasPairingLink, receivePairing } from './pairing.js';

const $ = id => document.getElementById(id);
const API = '/v0/mobile';
let connection, site, config, draft, session, normalizedImage, previewURL;
let busy = false, storageReady = false, saving = Promise.resolve(), pollTimer, confirmPairing;
let pairingActive = false, draftSaved = true, saveRevision = 0, pairingController;
let activeTab = false, initialized = false, releaseTab;
const hidden = (id, value) => { $(id).hidden = value; };
function error(message = '') { $('error').textContent = message; hidden('error', !message); }
function message(value = '') { $('message').textContent = value; hidden('message', !value); }
function explain(problem) {
  if (problem.name === 'AbortError' || problem.name === 'TimeoutError') return 'The service took too long to respond. Your draft is still here. Check publication before trying again.';
  if (problem instanceof TypeError) return config ? 'Couldn’t reach the publishing service. Your draft is still here. Check your connection and retry.' : 'Couldn’t reach the publishing service. Your draft stays on this phone. Reconnect and reload this page.';
  return problem.message || 'Something interrupted this post. Your draft is still here.';
}
function safeURL(value) {
  const url = new URL(value);
  if (url.protocol !== 'https:' || url.username || url.password) throw new Error('The published link is invalid. Check publication again.');
  return url.href;
}
function newDraft(image) {
  return { id: crypto.randomUUID().toUpperCase(), ipns: null, title: '', caption: '', image, submitted: false, attempts: 0, signed: false, operation: null, createdAt: Date.now() };
}
function canCopyDraft() {
  const operation = draft?.operation;
  return operation?.state === 'failed' && (operation.code === 'draft_expired' || (!draft.signed && !operation.proposal && ['image_invalid', 'heif_unavailable'].includes(operation.code)));
}
function persistDraft() {
  const snapshot = draft ? { ...draft } : null;
  const revision = ++saveRevision;
  draftSaved = false;
  $('draft-status').textContent = 'Saving on this phone…';
  saving = saving.catch(() => {}).then(() => snapshot ? save('draft', snapshot) : remove('draft'));
  saving.then(() => {
    if (revision !== saveRevision) return;
    draftSaved = true;
    render();
  }).catch(problem => { if (revision !== saveRevision) return; draftSaved = false; error(explain(problem)); $('draft-status').textContent = 'Draft not saved. Keep this page open and free up browser storage.'; render(); });
  return saving;
}
function showImage(blob, normalized = false) {
  if (previewURL) URL.revokeObjectURL(previewURL);
  previewURL = blob ? URL.createObjectURL(blob) : null;
  hidden('picker-label', !!blob);
  hidden('preview', !blob);
  hidden('image-fallback', true);
  hidden('image-tools', !blob);
  if (blob) {
    $('preview').src = previewURL;
    $('preview').alt = normalized ? 'Prepared image that will appear in your post' : 'Image for your post';
    $('image-name').textContent = normalized ? 'Ready to publish' : (blob.name || 'Your image');
  } else $('preview').removeAttribute('src');
}
$('preview').addEventListener('error', () => { hidden('preview', true); hidden('image-fallback', false); });
function render() {
  const operation = draft?.operation;
  const published = operation?.state === 'published';
  const locked = !!draft?.submitted;
  hidden('composer', published);
  hidden('receipt', !published);
  hidden('setup', !!connection || pairingActive);
  hidden('connected', !connection);
  hidden('destination', !connection);
  hidden('consent', !site?.ready || site.enabled || !config?.enabled);
  hidden('disable', !site?.enabled);
  hidden('preview-notice', !normalizedImage || operation?.state !== 'needs_signature');
  const editableFailure = canCopyDraft();
  hidden('copy-expired', !editableFailure);
  $('copy-expired').textContent = operation?.code === 'draft_expired' ? 'Use this image in a new draft' : 'Edit this draft';
  hidden('publish', editableFailure);
  $('caption').disabled = !activeTab || locked || busy;
  $('title').disabled = !activeTab || locked || busy;
  $('image').disabled = !activeTab || locked || busy;
  $('replace').disabled = !activeTab || locked || busy;
  $('remove-image').disabled = !activeTab || locked || busy;
  for (const id of ['enable', 'disable', 'disconnect', 'refresh-site', 'key-file', 'another', 'copy-expired']) $(id).disabled = !activeTab || busy;
  $('publish').disabled = !activeTab || busy || !storageReady || !draft?.image || !draftSaved || (!config?.enabled && !draft?.submitted);
  $('publish').textContent = busy ? 'Working…' : normalizedImage && operation?.state === 'needs_signature' ? 'Publish this preview' : draft?.submitted ? (operation?.state === 'failed' ? 'Prepare again' : 'Check publication') : 'Publish';
  if (connection) {
    $('connected-heading').textContent = site?.name || 'Connected site';
    $('destination-name').textContent = site?.name || connection.ipns.slice(0, 14) + '…';
    $('site-id').textContent = connection.ipns;
    $('site-status').textContent = site ? !site.ready ? site.reason || 'Open your existing publisher, enable hosted storage, and publish once with mobile support.' : site.enabled ? 'Ready. Your computer can sleep.' : 'Your site is ready. Allow phone posting to continue.' : 'Check your site’s hosted connection to continue.';
  }
  if (draftSaved) $('draft-status').textContent = draft?.image ? (draft.ipns && connection && draft.ipns !== connection.ipns ? 'This draft belongs to another site. Reconnect its original key to publish.' : locked ? 'Post saved on this phone. Reopening keeps the same post.' : 'Draft saved on this phone.') : 'Choose an image to start. Your draft stays on this phone.';
  $('publish-help').textContent = !connection ? 'Connect your site below to publish from anywhere.' : !site?.enabled ? 'Allow phone posting below before publishing.' : locked ? 'Keep this page open until publication is confirmed.' : 'You’ll review the prepared image before it goes live.';
  if (published) {
    try { $('post-link').href = safeURL(operation.url); } catch (problem) { $('post-link').removeAttribute('href'); error(explain(problem)); }
  }
}
async function raw(path, options = {}) {
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), 90000);
  try {
    const response = await fetch(API + path, { ...options, credentials: 'omit', cache: 'no-store', redirect: 'error', signal: controller.signal });
    if (!response.ok) {
      const detail = await response.json().catch(() => ({}));
      const problem = new Error(detail.error || `The service could not complete this request (${response.status}).`);
      problem.status = response.status; problem.code = detail.code;
      throw problem;
    }
    return response;
  } finally { clearTimeout(timeout); }
}
const jsonOptions = body => ({ method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
async function authenticate() {
  if (!connection || !config || config.origin !== location.origin) throw new Error('This service does not match the address of this Croptop app.');
  const challenge = await (await raw('/challenge', jsonOptions({ ipns: connection.ipns }))).json();
  const signature = await signChallenge(connection, challenge, location.origin);
  session = await (await raw('/session', jsonOptions({ id: challenge.id, signature }))).json();
  if (typeof session.token !== 'string' || !session.token || !Number.isSafeInteger(session.expiresAt) || session.expiresAt <= Date.now() / 1000) { session = null; throw new Error('The service returned an invalid connection session.'); }
}
async function api(path, options = {}, retry = true) {
  if (!session || session.expiresAt <= Date.now() / 1000 + 10) await authenticate();
  try { return await raw(path, { ...options, headers: { ...options.headers, Authorization: 'Bearer ' + session.token } }); }
  catch (problem) {
    if (retry && problem.status === 401) { session = null; await authenticate(); return api(path, options, false); }
    throw problem;
  }
}
async function loadSite() {
  const result = await (await api('/site')).json();
  if (result.ipns !== connection.ipns) throw new Error('The service returned a different site. Nothing was changed.');
  site = result;
  render();
}
async function connect(pem, expectedIPNS) {
  if (connection) throw new Error('Remove the current site from this phone before connecting another. Your draft keeps its original destination.');
  const imported = await importSiteKey(pem);
  if (expectedIPNS && imported.ipns !== expectedIPNS) throw new Error('The transferred key does not match the confirmed site.');
  await save('connection', imported);
  connection = imported;
  session = null;
  // Verify that this browser can persist a non-exportable CryptoKey before
  // presenting connection success. Private mode/storage failures stay visible.
  const stored = await read('connection');
  if (!stored?.key || stored.key.extractable || stored.ipns !== imported.ipns) throw new Error('This browser could not safely retain your site key.');
  connection = stored;
  render();
  await loadSite();
  if (navigator.storage?.persist) navigator.storage.persist().catch(() => {});
  message(site.ready ? 'Your site is connected. Allow phone posting below to start.' : 'Your key is saved. Your site needs one update from its existing publisher.');
}
async function acceptOperation(operation) {
  if (!draft || operation?.id !== draft.id || operation.ipns !== draft.ipns || operation.postID !== draft.id || operation.title !== draft.title || operation.caption !== draft.caption || !['preparing', 'needs_signature', 'committing', 'published', 'failed'].includes(operation.state)) throw new Error('The service returned a post that does not match your draft.');
  if (operation.state === 'published') safeURL(operation.url);
  draft.operation = operation;
  await persistDraft();
  normalizedImage = null;
  render();
  if (operation.state === 'published') {
    clearTimeout(pollTimer);
    message('Published and confirmed.');
    return;
  }
  if (operation.state === 'needs_signature') {
    const blob = await (await api('/operations/' + draft.id + '/image')).blob();
    await validateProposal(operation, draft, config, blob);
    normalizedImage = blob;
    showImage(blob, true);
    message('Your post is prepared. Review the image, then publish.');
  } else if (operation.state === 'failed') {
    error(operation.error || 'The post could not be prepared. Retry uses the same saved post.');
  } else {
    message(operation.state === 'committing' ? 'Confirming publication… Your saved post will recover if this page closes.' : 'Preparing your image and post…');
    schedulePoll();
  }
  render();
}
function schedulePoll() {
  clearTimeout(pollTimer);
  pollTimer = setTimeout(() => {
    if (document.hidden || busy) return;
    run(() => checkOperation()).catch(() => {});
  }, 2000);
}
async function checkOperation() {
  if (!draft?.submitted || !connection) return;
  if (draft.ipns !== connection.ipns) throw new Error('Reconnect this draft’s original site to check publication.');
  try { await acceptOperation(await (await api('/operations/' + draft.id)).json()); }
  catch (problem) {
    // A lost upload response may mean the server never got the request. Re-send
    // exactly the saved ID/body; an unknown commit is never assigned a new ID.
    if (problem.status === 404 && !draft.operation) return submitDraft();
    throw problem;
  }
}
async function submitDraft() {
  if (!connection || !site?.ready || !site.enabled) throw new Error('Connect your site and allow phone posting below first.');
  if (draft.ipns && draft.ipns !== connection.ipns) throw new Error('Reconnect this draft’s original site before publishing.');
  for (const [field, limit] of [['title', config.maxTitleBytes], ['caption', config.maxCaptionBytes]]) {
    if (!Number.isSafeInteger(limit) || limit < 1) throw new Error('The service did not provide its text limits. Check connection and try again.');
    if (new TextEncoder().encode(draft[field]).length > limit) throw new Error(`Your ${field} is too long. Shorten it before publishing; emoji and accented characters use extra space.`);
  }
  draft.ipns = connection.ipns;
  draft.submitted = true;
  draft.attempts = (draft.attempts || 0) + 1;
  await persistDraft();
  const body = new FormData();
  body.set('id', draft.id); body.set('title', draft.title); body.set('caption', draft.caption);
  body.set('image', draft.image, draft.image.name || 'image');
  try { await acceptOperation(await (await api('/operations', { method: 'POST', body })).json()); }
  catch (problem) {
    // Only a first, definitively rejected upload is editable. If any previous
    // response was lost, even a 400 now cannot rule out an earlier acceptance.
    if (draft.attempts === 1 && !draft.operation && [400, 413].includes(problem.status) && ['invalid_upload', 'invalid_request', 'invalid_id', 'invalid_text', 'image_too_large'].includes(problem.code)) {
      draft.submitted = false; draft.attempts = 0;
      await persistDraft();
    }
    throw problem;
  }
}
async function publish() {
  if (!draft?.image) throw new Error('Choose an image first.');
  await saving;
  if (!connection) { $('setup').scrollIntoView({ behavior: 'smooth', block: 'center' }); throw new Error('Connect your site below. Your image is saved while you set up.'); }
  if (draft.ipns && draft.ipns !== connection.ipns) throw new Error('This draft belongs to a different site. Reconnect the original site key to publish it.');
  if (!site) await loadSite();
  if (!site?.ready || !site?.enabled) { $('connected').scrollIntoView({ behavior: 'smooth', block: 'center' }); throw new Error(site?.reason || 'Allow phone posting below to publish.'); }
  if (!draft.submitted) return submitDraft();
  const operation = draft.operation;
  if (operation?.state === 'needs_signature' && normalizedImage) {
    if (operation.proposal.expiresAt <= Math.floor(Date.now() / 1000)) {
      await acceptOperation(await (await api('/operations/' + draft.id + '/prepare', jsonOptions({}))).json());
      return;
    }
    const signatures = await signProposal(connection, operation, draft, config, normalizedImage);
    // Retain the proposed operation before the request. A dropped response is
    // uncertain, not a failure; a subsequent click first looks up the outcome.
    draft.operation = { ...operation, state: 'committing' };
    draft.signed = true;
    await persistDraft();
    normalizedImage = null;
    message('Publishing…');
    await acceptOperation(await (await api('/operations/' + draft.id + '/commit', jsonOptions(signatures))).json());
  } else if (operation?.state === 'failed' || (operation?.state === 'needs_signature' && operation.proposal.expiresAt <= Math.floor(Date.now() / 1000))) {
    try { await acceptOperation(await (await api('/operations/' + draft.id + '/prepare', jsonOptions({}))).json()); }
    catch (problem) {
      if (problem.code === 'draft_expired') await checkOperation();
      else throw problem;
    }
  } else await checkOperation();
}
async function run(action) {
  if (!activeTab || busy) return;
  busy = true; error(); render();
  try { await action(); }
  catch (problem) { error(explain(problem)); }
  finally { busy = false; render(); }
}
$('composer').addEventListener('submit', event => { event.preventDefault(); run(publish); });
$('image').addEventListener('change', () => run(async () => {
  const image = $('image').files[0];
  if (!image) return;
  if (draft?.submitted) throw new Error('Finish checking the pending post before starting another.');
  if (config?.maxImageBytes && image.size > config.maxImageBytes) throw new Error(`Choose an image smaller than ${Math.floor(config.maxImageBytes / 1048576)} MB.`);
  if (!/\.(png|jpe?g|webp|hei[cf])$/i.test(image.name) && !['image/png', 'image/jpeg', 'image/webp', 'image/heic', 'image/heif'].includes(image.type)) throw new Error('Choose a PNG, JPEG, WebP, HEIC or HEIF image.');
  if (!draft) draft = newDraft(image); else draft.image = image;
  draft.title = $('title').value; draft.caption = $('caption').value;
  await persistDraft();
  normalizedImage = null; showImage(image); message();
  $('image').value = '';
}));
$('replace').addEventListener('click', () => $('image').click());
$('remove-image').addEventListener('click', () => run(async () => {
  if (draft?.submitted) return;
  draft.image = null;
  await persistDraft(); showImage(null);
}));
for (const field of ['title', 'caption']) $(field).addEventListener('input', () => {
  if (draft?.submitted || busy) return;
  if (!draft) draft = newDraft(null);
  draft[field] = $(field).value;
  persistDraft().catch(() => {});
});
$('key-file').addEventListener('change', () => run(async () => {
  const file = $('key-file').files[0];
  try {
    if (!file) return;
    if (file.size > 16384) throw new Error('This file is too large to be a site key. Choose your Croptop PRIVATE KEY PEM.');
    await connect(await file.text());
  } finally { $('key-file').value = ''; }
}));
$('enable').addEventListener('click', () => run(async () => {
  site = await (await api('/connection', { ...jsonOptions({ enabled: true }), method: 'PUT' })).json();
  if (site.ipns !== connection.ipns) { site = null; throw new Error('The service returned a different site.'); }
  message('Phone posting is ready. Choose an image and publish.');
}));
$('disable').addEventListener('click', () => run(async () => {
  if (!confirm('Stop phone posting through this service? Unpublished work will stop. Published posts and your local draft will stay.')) return;
  site = await (await api('/connection', { ...jsonOptions({ enabled: false }), method: 'PUT' })).json();
  normalizedImage = null;
  message('Phone posting is stopped. Already published posts stay online.');
}));
$('disconnect').addEventListener('click', () => run(async () => {
  if (!confirm('Remove the site key from this browser? Keep your original key backup to reconnect. Your draft keeps its original site.')) return;
  clearTimeout(pollTimer);
  await remove('connection');
  connection = null; site = null; session = null; normalizedImage = null;
  message('Site key removed from this browser. Your draft is still saved here.');
}));
$('refresh-site').addEventListener('click', () => run(loadSite));
$('another').addEventListener('click', () => run(async () => {
  if (draft?.operation?.state !== 'published') return;
  draft = null; normalizedImage = null;
  await persistDraft(); $('caption').value = ''; $('title').value = ''; showImage(null); message();
}));
$('copy-expired').addEventListener('click', () => run(async () => {
  // A 410 alone is not enough: re-read the authoritative receipt before making
  // a new identity, so an uncertain old commit can never become a second post.
  await checkOperation();
  if (!canCopyDraft()) return;
  const expired = draft.operation.code === 'draft_expired';
  await save('draft:' + draft.id, draft);
  draft = { ...newDraft(draft.image), title: draft.title, caption: draft.caption, ipns: draft.ipns };
  normalizedImage = null;
  await persistDraft();
  showImage(draft.image);
  error(); message(expired ? 'The old post expired without publication. Your image and words are ready in a new draft.' : 'This image could not be prepared and was not published. Replace it or shorten your text, then try again.');
}));
document.addEventListener('visibilitychange', () => {
  if (!document.hidden && draft?.submitted && !['published', 'failed', 'needs_signature'].includes(draft.operation?.state)) run(checkOperation);
});
window.addEventListener('online', () => { if (draft?.submitted && draft.operation?.state !== 'published') run(checkOperation); });
window.addEventListener('beforeunload', event => { if (!draftSaved) { event.preventDefault(); event.returnValue = ''; } });

async function pair() {
  if (!activeTab || !initialized || !storageReady || !hasPairingLink()) return;
  if (pairingActive) {
    history.replaceState(null, '', location.pathname + location.search);
    message('Finish or cancel the current connection before opening another link.');
    return;
  }
  if (connection) { history.replaceState(null, '', location.pathname + location.search); throw new Error('Remove the current site from this phone before opening a new connection link.'); }
  confirmPairing = null;
  hidden('pairing-code', true); hidden('confirm-pairing', true);
  $('pairing-site').textContent = '';
  error(); message();
  pairingActive = true; hidden('pairing', false); render();
  pairingController = new AbortController();
  try {
    const paired = await receivePairing({
      signal: pairingController.signal,
      onStatus(text) { $('pairing-status').textContent = text; },
      onConfirm(details) {
        $('pairing-site').textContent = details.ipns;
        $('pairing-code').textContent = details.code.slice(0, 4) + ' ' + details.code.slice(4);
        hidden('pairing-code', false); hidden('confirm-pairing', false);
        $('pairing-status').textContent = 'Check this site, then enter this code on your existing publisher.';
        return new Promise(resolve => { confirmPairing = resolve; });
      },
    });
    if (paired.origin !== location.origin) throw new Error('The connection was created for a different service.');
    // Image persistence may already be finishing when the desktop confirms.
    // Retain the received key until that short local action is complete.
    while (busy) await new Promise(resolve => setTimeout(resolve, 50));
    await run(() => connect(paired.pem, paired.ipns));
  } catch (problem) { error(explain(problem)); }
  finally { pairingActive = false; hidden('pairing', true); render(); }
}
$('confirm-pairing').addEventListener('click', () => { hidden('confirm-pairing', true); confirmPairing?.(true); });
$('cancel-pairing').addEventListener('click', () => { confirmPairing?.(false); pairingController?.abort(); });
window.addEventListener('hashchange', () => {
  pair().catch(problem => { error(explain(problem)); render(); });
});

async function start() {
  try {
    [connection, draft] = await Promise.all([read('connection'), read('draft')]);
    storageReady = true;
    if (draft) { $('caption').value = draft.caption; $('title').value = draft.title; showImage(draft.image); }
    render();
    config = await (await raw('/config')).json();
    if (config.version !== 1 || config.origin !== location.origin) throw new Error('This Croptop app and its publishing service do not match. Open the trusted address from Connect phone.');
    if (!config.enabled) message('Phone posting is not available on this service yet. You can keep your draft here.');
    if (config.maxImageBytes) $('image-help').textContent = `One image, up to ${Math.floor(config.maxImageBytes / 1048576)} MB. PNG, JPEG, WebP, HEIC or HEIF.`;
    if (Number.isSafeInteger(config.maxTitleBytes)) $('title').maxLength = config.maxTitleBytes;
    if (Number.isSafeInteger(config.maxCaptionBytes)) $('caption').maxLength = config.maxCaptionBytes;
    initialized = true;
    if (hasPairingLink()) await pair();
    else if (connection) await run(async () => { await loadSite(); if (draft?.submitted && draft.ipns === connection.ipns) await checkOperation(); });
    if ('serviceWorker' in navigator) navigator.serviceWorker.register('./sw.js', { scope: './' }).catch(() => {});
  } catch (problem) { error(explain(problem)); }
  render();
}
// One writer per browser origin, including an installed PWA and browser tabs.
// The lock protects both drafts and the in-memory copy of a stored signing key.
render();
if (!navigator.locks) {
  error('This browser cannot safely coordinate saved posts. Open Croptop in an up-to-date Safari or Chrome.');
} else navigator.locks.request('croptop-mobile-composer-v1', { ifAvailable: true }, async lock => {
  if (!lock) { error('Croptop is already open in another tab or home-screen window. Finish there, or close it and reload this page.'); return; }
  activeTab = true;
  await start();
  await new Promise(resolve => { releaseTab = resolve; });
});
window.addEventListener('pagehide', () => { activeTab = false; releaseTab?.(); });
window.addEventListener('pageshow', event => { if (event.persisted) location.reload(); });

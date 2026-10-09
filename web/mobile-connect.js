const $ = id => document.getElementById(id);
const siteID = location.hash.slice(1).toUpperCase();
const validSite = /^[0-9A-F]{8}(?:-[0-9A-F]{4}){3}-[0-9A-F]{12}$/.test(siteID);
const requestTimeout = 15000;
const pairingToken = /^[A-Za-z0-9_-]{42}[AEIMQUYcgkosw048]$/;
const stoppedMessage = 'Connection setup stopped. Published changes and any key already sent are not undone.';
let active, timer, clock, qrURL, generation = 0;

function status(text) { $('status').textContent = text; }
function fail(error) { $('error').textContent = error.message || String(error); $('error').hidden = false; }
function clearError() { $('error').hidden = true; $('error').textContent = ''; }
function current(flow) { return active === flow && flow.generation === generation; }
function clearLink() {
  $('connection').hidden = true;
  $('confirm').hidden = true;
  $('code').value = '';
  $('phone-link').removeAttribute('href');
  $('qr').removeAttribute('src');
  if (qrURL) URL.revokeObjectURL(qrURL);
  qrURL = undefined;
}
function invalidate() {
  generation++;
  clearTimeout(timer); clearInterval(clock);
  if (active) for (const controller of active.requests) controller.abort();
  active = undefined;
  clearLink();
  $('retry').hidden = true;
  $('timing').textContent = '';
}
function makeFlow(values) {
  invalidate();
  active = { ...values, generation, requests: new Set() };
  return active;
}
function controls(busy) {
  $('start').disabled = busy || !validSite;
  $('enable-hosting').disabled = busy;
  $('allow-publish').disabled = busy;
  $('cancel').hidden = !busy;
  $('cancel').disabled = active?.mode === 'cancelling';
  $('setup').setAttribute('aria-busy', String(busy));
}
async function request(path, { method = 'GET', body, flow, blob = false, keepalive = false } = {}) {
  const controller = new AbortController();
  flow?.requests.add(controller);
  const timeout = setTimeout(() => controller.abort(), requestTimeout);
  try {
    const response = await fetch(`/v0/croptop/sites/${siteID}/phone${path}`, {
      method, cache: 'no-store', referrerPolicy: 'no-referrer', redirect: 'error', signal: controller.signal, keepalive,
      headers: { 'Content-Type': 'application/json', 'X-Croptop-Phone': '1' },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    if (blob && response.ok) {
      const value = await response.blob();
      if (value.type !== 'image/png' || value.size > 262144) throw new Error('Could not load the connection QR code. Try again.');
      return value;
    }
    let data;
    try { data = await response.json(); }
    catch { throw new Error('Croptop returned an unreadable response. Check again or cancel this connection.'); }
    if (!response.ok) {
      const error = new Error(data.error || 'Could not connect your phone. Check again or cancel this connection.');
      error.code = data.code; error.status = response.status; throw error;
    }
    return data;
  } catch (error) {
    if (controller.signal.aborted) throw new Error('Croptop did not respond within 15 seconds. Check again or cancel this connection.');
    throw error;
  } finally { clearTimeout(timeout); flow?.requests.delete(controller); }
}
function later(flow, action, delay = 1000) {
  if (current(flow)) timer = setTimeout(() => { if (current(flow)) action(flow); }, delay);
}
function showRetry(flow, error) {
  if (!current(flow)) return;
  fail(error); $('retry').hidden = false;
  $('retry').textContent = flow.mode === 'cancelling' ? 'Retry stopping' : 'Check again';
}
function setDeadline(flow, value) {
  // The server owns the five-minute preparation deadline. Retries cannot extend it.
  if (!Number.isFinite(value.deadline) || value.deadline <= 0) throw new Error('Croptop did not return a preparation deadline. Cancel and try again.');
  flow.deadline = flow.deadline ? Math.min(flow.deadline, value.deadline) : value.deadline;
  clearInterval(clock);
  const tick = () => {
    if (!current(flow)) return;
    const remaining = Math.max(0, Math.ceil(flow.deadline - Date.now() / 1000));
    $('timing').textContent = remaining > 0 ? `Time left for this attempt: ${Math.floor(remaining / 60)}:${String(remaining % 60).padStart(2, '0')}. You can cancel at any time.` : 'The preparation time limit has been reached.';
    if (!remaining && flow.mode !== 'cancelling') cancel('Preparation reached its time limit. Stopping this attempt…');
  };
  clock = setInterval(tick, 1000); tick();
}
function finish(message) {
  invalidate(); controls(false); clearError();
  $('setup').hidden = false; status(message);
}
function rejected(value) {
  finish('');
  if (value.code === 'hosting_required') {
    $('hosting').hidden = false;
    status('Phone posting needs hosted storage. Review the hosting permission, then continue.');
  } else if (value.code === 'publication_required') {
    $('publication').hidden = false;
    $('start').textContent = 'Publish and connect';
    status('Your published site needs an update before it can connect. Review the publication permission before continuing.');
  } else fail(new Error(value.error || 'Could not prepare this site for your phone. Try again.'));
}
async function showConnection(flow, value) {
  // Pairing capabilities stay in the fragment and are never persisted here.
  if (!value || typeof value.url !== 'string') throw new Error('Croptop returned an invalid connection link. Cancel and try again.');
  const link = new URL(value.url);
  const parts = link.hash.slice(6).split('.');
  if (link.protocol !== 'https:' || link.username || link.password || link.search || link.pathname !== '/' ||
      !link.hash.startsWith('#pair=') || parts.length !== 2 || parts[0] !== value.id ||
      !pairingToken.test(value.id) || !pairingToken.test(parts[1])) throw new Error('Croptop returned an invalid connection link. Cancel and try again.');
  if (!Number.isFinite(value.expiresAt) || Date.now() >= value.expiresAt * 1000 || value.expiresAt * 1000 > Date.now() + 660000) throw new Error('This connection has an invalid expiry. Cancel and create a new connection.');
  flow.connection = value;
  flow.mode = 'pairing';
  clearInterval(clock); $('timing').textContent = '';
  clock = setInterval(() => {
    if (current(flow) && Date.now() >= value.expiresAt * 1000) cancel('This connection expired. Stopping this attempt…');
  }, 1000);
  status('Loading your secure connection code…');
  const qr = await request(`/${value.id}/qr`, { flow, blob: true });
  if (!current(flow)) return;
  qrURL = URL.createObjectURL(qr);
  $('qr').src = qrURL;
  $('qr').hidden = false; $('phone-link').hidden = false; $('copy').hidden = false;
  $('site-name').textContent = value.name;
  $('phone-link').href = value.url;
  $('confirm-button').disabled = false;
  $('setup').hidden = true; $('connection').hidden = false;
  status('Scan the code with your phone camera.');
  later(flow, pollConnection, 0);
}
async function acceptPreparation(flow, value) {
  if (!current(flow)) return;
  if (value.id !== flow.id || value.siteID !== siteID) throw new Error('Croptop returned a different preparation. Cancel and try again.');
  clearError(); $('retry').hidden = true;
  if (value.state === 'ready') return showConnection(flow, value.connection);
  if (value.state === 'failed') return rejected(value);
  if (value.state === 'cancelled') return finish(stoppedMessage);
  if (value.state !== 'preparing' && value.state !== 'cancelling') throw new Error('Croptop returned an unknown preparation state. Cancel and try again.');
  setDeadline(flow, value);
  if (!current(flow)) return;
  clearError(); $('retry').hidden = true;
  status(value.message || 'Checking your published site…');
  later(flow, pollPreparation);
}
async function pollPreparation(flow) {
  if (!current(flow) || flow.checking || flow.mode !== 'preparing') return;
  flow.checking = true;
  try { await acceptPreparation(flow, await request(`/preparations/${flow.id}`, { flow })); }
  catch (error) { showRetry(flow, error); }
  finally { flow.checking = false; }
}
async function start() {
  if (active || !validSite) return;
  if (!$('publication').hidden && !$('allow-publish').checked) {
    status('To continue, allow Croptop to publish this computer’s current site, including its saved changes.');
    $('allow-publish').focus(); return;
  }
  const flow = makeFlow({ id: crypto.randomUUID().toUpperCase(), mode: 'preparing' });
  clearError(); controls(true);
  setDeadline(flow, { deadline: Date.now() / 1000 + 300 });
  status('Checking the phone service and your published site…');
  try {
    const value = await request('/preparations', { flow, method: 'POST', body: {
      id: flow.id, enableHosting: $('enable-hosting').checked, allowPublish: $('allow-publish').checked,
    } });
    await acceptPreparation(flow, value);
  } catch (error) {
    if (!current(flow)) return;
    if (error.status && error.status < 500) rejected({ error: error.message, code: error.code });
    else {
      // A lost POST response does not prove failure. Query the same UUID;
      // never start a duplicate publication to recover a transport error.
      status('Checking whether Croptop started this preparation…');
      showRetry(flow, error);
      later(flow, pollPreparation);
    }
  }
}
async function pollConnection(flow) {
  if (!current(flow) || flow.checking) return;
  if (Date.now() >= flow.connection.expiresAt * 1000) return cancel('This connection expired. Stopping this attempt…');
  flow.checking = true;
  try {
    const value = await request(`/${flow.connection.id}`, { flow });
    if (!current(flow)) return;
    clearError(); $('retry').hidden = true;
    if (value.state === 'claimed' && !flow.confirming && !flow.confirmed) {
      $('confirm').hidden = false;
      status('Your phone is ready. Enter its confirmation code below.');
    } else if (value.state === 'ready') {
      $('confirm').hidden = true;
      status('Key sent securely. Finish connecting on your phone.');
    } else if (value.state === 'consumed') {
      finish('Key delivered. Your phone will confirm once it has saved the connection.');
      return;
    }
    later(flow, pollConnection, 1800);
  } catch (error) { showRetry(flow, error); }
  finally { flow.checking = false; }
}
async function stopRemote(flow) {
  if (!current(flow) || flow.checking) return;
  flow.checking = true;
  try {
    const value = await request(`/preparations/${flow.id}`, { flow, method: 'DELETE' });
    if (!current(flow)) return;
    if (value.id !== flow.id || value.siteID !== siteID) throw new Error('Croptop returned a different preparation. Retry stopping.');
    if (value.state === 'cancelling' || value.state === 'preparing') {
      if (Date.now() >= flow.stopDeadline) throw new Error('Croptop is still stopping this attempt. Retry stopping to check again; published changes and any key already sent are not undone.');
      status('Stopping preparation… A publication already sent may still finish.');
      later(flow, stopRemote); return;
    }
    if (value.state !== 'cancelled') throw new Error('Croptop has not confirmed that this attempt stopped. Retry stopping.');
    finish(stoppedMessage);
  } catch (error) { showRetry(flow, error); }
  finally { flow.checking = false; }
}
function cancel(message = 'Stopping this connection…') {
  if (!active || active.mode === 'cancelling') return;
  const flow = makeFlow({ id: active.id, mode: 'cancelling', stopDeadline: Date.now() + requestTimeout });
  clearError(); controls(true); $('setup').hidden = false;
  status(message); stopRemote(flow);
}
$('start').addEventListener('click', start);
$('cancel').addEventListener('click', () => cancel());
$('restart').addEventListener('click', () => cancel());
$('retry').addEventListener('click', () => {
  const flow = active;
  if (!flow || flow.checking) return;
  clearTimeout(timer); clearError(); $('retry').hidden = true;
  if (flow.mode === 'cancelling') { flow.stopDeadline = Date.now() + requestTimeout; stopRemote(flow); }
  else if (flow.mode === 'pairing' && !$('qr').hasAttribute('src')) {
    flow.checking = true;
    showConnection(flow, flow.connection).catch(error => showRetry(flow, error)).finally(() => { flow.checking = false; });
  }
  else if (flow.mode === 'pairing') pollConnection(flow);
  else pollPreparation(flow);
});
$('copy').addEventListener('click', async () => {
  const flow = active;
  if (!flow?.connection) return;
  try { await navigator.clipboard.writeText(flow.connection.url); if (current(flow)) status('Connection link copied. Keep it private.'); }
  catch { if (current(flow)) fail(new Error('Could not copy the link. Scan the QR code with your phone camera.')); }
});
$('confirm').addEventListener('submit', async event => {
  event.preventDefault();
  const flow = active;
  if (!flow?.connection || flow.mode !== 'pairing' || flow.confirming || flow.confirmed) return;
  flow.confirming = true;
  clearError(); $('confirm-button').disabled = true;
  try {
    await request(`/${flow.connection.id}/confirm`, { flow, method: 'POST', body: { code: $('code').value } });
    if (!current(flow)) return;
    flow.confirmed = true;
    $('confirm').hidden = true; $('code').value = ''; status('Finish connecting on your phone.');
  } catch (error) { if (current(flow)) { fail(error); $('confirm-button').disabled = false; } }
  finally { flow.confirming = false; }
});
window.addEventListener('pagehide', () => {
  const flow = active;
  invalidate();
  if (flow) request(`/preparations/${flow.id}`, { method: 'DELETE', keepalive: true }).catch(() => {});
});
window.addEventListener('pageshow', event => {
  if (event.persisted) finish('Create a new connection link when your phone is ready.');
});
if (!validSite) { controls(false); fail(new Error('Open Connect phone from the site you want to connect in Croptop.')); }

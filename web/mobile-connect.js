const $ = id => document.getElementById(id);
const siteID = location.hash.slice(1).toUpperCase();
const validSite = /^[0-9A-F]{8}(?:-[0-9A-F]{4}){3}-[0-9A-F]{12}$/.test(siteID);
let connection, timer, qrURL, generation = 0;

function status(text) { $('status').textContent = text; }
function fail(error) { $('error').textContent = error.message || String(error); $('error').hidden = false; }
function clearError() { $('error').hidden = true; $('error').textContent = ''; }
async function api(path, body) {
  const response = await fetch(`/v0/croptop/sites/${siteID}/phone${path}`, {
    method: body === undefined ? 'GET' : 'POST', cache: 'no-store', referrerPolicy: 'no-referrer',
    headers: { 'Content-Type': 'application/json', 'X-Croptop-Phone': '1' },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const data = await response.json();
  if (!response.ok) { const e = new Error(data.error || 'Could not connect your phone.'); e.code = data.code; throw e; }
  return data;
}

async function start() {
  const current = ++generation;
  clearTimeout(timer); clearError();
  $('start').disabled = true;
  $('connection').hidden = true;
  $('setup').hidden = false;
  status('Publishing your site and preparing a secure connection…');
  try {
    const value = await api('', { enableHosting: $('enable-hosting').checked });
    if (current !== generation) return;
    connection = value;
    $('site-name').textContent = value.name;
    $('phone-link').href = value.url;
    $('confirm').hidden = true;
    $('code').value = '';
    $('confirm-button').disabled = false;
    const qr = await fetch(`/v0/croptop/sites/${siteID}/phone/${value.id}/qr`, {
      headers: { 'X-Croptop-Phone': '1' }, cache: 'no-store', referrerPolicy: 'no-referrer',
    });
    if (!qr.ok) throw new Error('Could not load the connection QR code. Start a new connection.');
    if (qrURL) URL.revokeObjectURL(qrURL);
    qrURL = URL.createObjectURL(await qr.blob());
    $('qr').src = qrURL;
    $('setup').hidden = true;
    $('connection').hidden = false;
    status('Scan the code with your phone camera.');
    poll(current);
  } catch (error) {
    if (error.code === 'hosting_required') $('hosting').hidden = false;
    fail(error); status('');
  } finally { $('start').disabled = false; }
}

async function poll(current) {
  if (current !== generation || !connection) return;
  if (Date.now() >= connection.expiresAt * 1000) { status('This connection expired. Start a new connection.'); $('confirm').hidden = true; return; }
  try {
    const value = await api(`/${connection.id}`);
    if (current !== generation) return;
    if (value.state === 'claimed') {
      $('confirm').hidden = false;
      status('Your phone is ready. Enter its confirmation code below.');
    } else if (value.state === 'ready') {
      $('confirm').hidden = true;
      status('Key sent securely. Finish connecting on your phone.');
    } else if (value.state === 'consumed') {
      $('confirm').hidden = true;
      $('qr').hidden = true;
      $('phone-link').hidden = true;
      $('copy').hidden = true;
      status('Key delivered. Your phone will confirm once it has saved the connection.');
      return;
    }
  } catch (error) { fail(error); }
  timer = setTimeout(() => poll(current), 1800);
}

$('start').addEventListener('click', start);
$('restart').addEventListener('click', () => {
  generation++; clearTimeout(timer); clearError(); connection = null;
  $('setup').hidden = false; $('connection').hidden = true;
  $('qr').hidden = false; $('phone-link').hidden = false; $('copy').hidden = false;
  status('Create a new link when your phone is ready.');
});
$('copy').addEventListener('click', async () => {
  try { await navigator.clipboard.writeText(connection.url); status('Connection link copied. Keep it private.'); }
  catch { fail(new Error('Could not copy the link. Scan the QR code with your phone camera.')); }
});
$('confirm').addEventListener('submit', async event => {
  event.preventDefault(); clearError(); $('confirm-button').disabled = true;
  try { await api(`/${connection.id}/confirm`, { code: $('code').value }); $('confirm').hidden = true; status('Finish connecting on your phone.'); }
  catch (error) { fail(error); $('confirm-button').disabled = false; }
});
if (!validSite) { $('start').disabled = true; fail(new Error('Open Connect phone from the site you want to connect in Croptop.')); }

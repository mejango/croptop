// Small, dependency-free reader for the experimental Croptop manifest entry.
function croptopEmbedURL(manifest, siteURL, parentOrigin) {
  try {
    const site = new URL(siteURL), entry = manifest?.croptop;
    if (site.protocol !== 'https:' || site.username || site.password ||
        !entry || entry.version !== 1 || typeof entry.embed !== 'string' ||
        !Array.isArray(entry.origins) || !entry.origins.includes(parentOrigin) ||
        new URL(parentOrigin).protocol !== 'https:') return null;
    const target = new URL(entry.embed, site);
    if (target.origin !== site.origin || target.username || target.password ||
        target.origin === parentOrigin) return null;
    return target.href;
  } catch { return null; }
}

async function mountCroptopEmbed(root) {
  const host = root.querySelector('[data-embed-site]');
  if (!host || host.dataset.embedChecked) return;
  host.dataset.embedChecked = 'true';
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), 8000);
  try {
    const site = new URL(host.dataset.embedSite);
    if (site.protocol !== 'https:') return;
    // Deliberately no proxy, credentials, or cross-origin manifest redirects.
    const response = await fetch(new URL('/manifest.json', site), {
      mode: 'cors', credentials: 'omit', redirect: 'error', signal: controller.signal,
    });
    if (!response.ok) return;
    const target = croptopEmbedURL(await response.json(), site.href, window.origin);
    if (!target || !root.isConnected) return;
    const frame = document.createElement('iframe');
    frame.title = 'Juicebox website';
    frame.referrerPolicy = 'strict-origin-when-cross-origin';
    frame.setAttribute('sandbox', 'allow-scripts allow-same-origin allow-forms allow-popups allow-popups-to-escape-sandbox allow-downloads');
    frame.src = target;
    host.querySelector('.ct-embed-link').replaceWith(frame);
  } catch {
    // Missing opt-in, blocked CORS, and offline visitors keep the useful link.
  } finally { clearTimeout(timeout); }
}

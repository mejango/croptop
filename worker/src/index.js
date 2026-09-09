// crop.top on Cloudflare: a gateway for ENS, IPNS and claimed names, a store
// that sites push to on publish, and a registry of free names. No IPFS node:
// pushed sites live in R2, everything else is fetched from a public gateway.

const SKEW = 10 * 60;
const RESOLVE_TTL = 60;
const NAME_RE = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;
const RESERVED = new Set(["www", "api", "v0", "ipfs", "ipns", "push", "host", "admin", "mail", "static", "assets", "docs", "app", "directory"]);
const MAX_PUSH = 100 << 20;
const UA = { "User-Agent": "croptop-host/1 (+https://crop.top)" };
// gateways that resolve names with a real IPFS node and answer Workers; used for anything not pushed here.
// Two forms: "eth.sucks" is a subdomain gateway (<cid>.eth.sucks); "https://node.crop.top" is a path
// gateway (/ipfs/<cid>/..., /ipns/<name>/...), which is what a croptop host on one hostname offers.
const upstreams = (env) => (env.UPSTREAMS || "eth.sucks,eth.shop").split(",").map((s) => s.trim()).filter(Boolean);
const upstreamURL = (up, kind, label, path = "/") => up.startsWith("http") ? `${up}/${kind}/${label}${path}` : `https://${label}.${kind === "ipfs" || label.startsWith("k") ? up : up.replace(/^eth\./, "")}${path}`;

export default {
  async fetch(request, env, ctx) {
    try {
      return await route(request, env, ctx);
    } catch (e) {
      return text(`host error: ${e.message}`, 500);
    }
  },
  async scheduled(event, env, ctx) {
    ctx.waitUntil(republishAll(env));
  },
};

async function route(request, env, ctx) {
  const url = new URL(request.url);
  const host = url.hostname.toLowerCase();
  const domain = env.DOMAIN.toLowerCase();
  if (host === domain) return serveBare(request, url, env, ctx);
  if (host.endsWith("." + domain)) return serveLabel(request, url, env, ctx, host.slice(0, -domain.length - 1));
  return text(`unknown host ${host} (this host serves ${domain})`, 404);
}

// ---------- bare domain: API, directory, claimed names, then the root site

async function serveBare(request, url, env, ctx) {
  const p = url.pathname;
  if (p.startsWith("/v0/host/")) return api(request, url, env, ctx);
  if (p === "/directory" || (p === "/" && !env.ROOT)) return directory(env);
  const [name, ...restParts] = p.slice(1).split("/");
  const entry = name && (await entryByName(env, name));
  if (entry && entry.cid) {
    if (restParts.length === 0) return Response.redirect(url.origin + "/" + name + "/", 301);
    return serveSite(request, env, entry.cid, "/" + restParts.join("/"), `/${name}`);
  }
  if (!env.ROOT) return text(`no site named ${name} here`, 404);
  const cid = await resolveAny(env, env.ROOT);
  if (!cid) return p === "/" ? directory(env) : text(`could not resolve ${env.ROOT}`, 502);
  return serveSite(request, env, cid, p, "");
}

async function serveLabel(request, url, env, ctx, label) {
  const cid = await resolveAny(env, label.startsWith("bafy") || label.startsWith("qm") || label.startsWith("k51") || label.startsWith("k2k4") ? label : label + ".eth");
  if (!cid) return text(`could not resolve ${label}`, 502);
  return serveSite(request, env, cid, url.pathname, "");
}

// ---------- resolution: pushed entries first, then ENS DNSLink and delegated IPNS

async function resolveAny(env, what) {
  what = what.toLowerCase();
  if (what.startsWith("bafy") || what.startsWith("qm")) return what;
  if (what.startsWith("k51") || what.startsWith("k2k4")) return resolveKey(env, what);
  return resolveENS(env, what);
}

async function resolveKey(env, ipns) {
  const e = await entryByKey(env, ipns);
  if (e && e.cid) return e.cid;
  return cached(env, "ipns:" + ipns, async () => {
    // the delegated endpoint is fast but only knows records published to it;
    // the upstream gateways resolve the rest through their own nodes
    for (const src of [`https://delegated-ipfs.dev/routing/v1/ipns/${ipns}`]) {
      try {
        const r = await fetch(src, { headers: { Accept: "application/vnd.ipfs.ipns-record" }, redirect: "follow" });
        if (!r.ok || !(r.headers.get("content-type") || "").includes("ipns-record")) continue;
        const rec = parseRecord(new Uint8Array(await r.arrayBuffer()));
        if (rec.value && rec.value.startsWith("/ipfs/")) return rec.value.slice(6).split("/")[0];
      } catch (e) {
        console.log("ipns resolve", ipns, e.message);
      }
    }
    for (const up of upstreams(env)) {
      const c = await rootsOf(upstreamURL(up, "ipns", ipns));
      if (c) return c;
    }
    return null;
  });
}

async function resolveENS(env, ens) {
  const target = await cached(env, "dnslink:" + ens, async () => {
    const r = await fetch(`https://dns.eth.limo/dns-query?name=_dnslink.${ens}&type=TXT`, { headers: { Accept: "application/dns-json" } });
    if (!r.ok) { console.log("dnslink", ens, r.status); return null; }
    const j = await r.json();
    for (const a of j.Answer || []) {
      const m = /dnslink=(\/ip[fn]s\/[^"\s]+)/.exec(a.data);
      if (m) return m[1];
    }
    return null;
  });
  if (target && target.startsWith("/ipfs/")) return target.slice(6).split("/")[0];
  const viaKey = target ? await resolveKey(env, target.slice(6).split("/")[0]) : null;
  if (viaKey) return viaKey;
  // the upstream gateways resolve ENS with their own node, which hears IPNS updates over pubsub
  return cached(env, "ens:" + ens, async () => {
    for (const up of upstreams(env)) {
      const c = await rootsOf(upstreamURL(up, "ipns", ens));
      if (c) return c;
    }
    return null;
  });
}

// rootsOf asks a gateway for a path and reads the root CID it resolved to.
// A one-byte range GET, because some gateways refuse HEAD.
async function rootsOf(url) {
  try {
    const r = await fetch(url, { headers: { ...UA, Range: "bytes=0-0" }, redirect: "follow" });
    const roots = r.headers.get("x-ipfs-roots");
    return roots ? roots.split(",")[0] : null;
  } catch {
    return null;
  }
}

async function cached(env, key, fn) {
  const hit = await env.REGISTRY.get("cache:" + key);
  if (hit) return hit;
  const v = await fn();
  if (v) await env.REGISTRY.put("cache:" + key, v, { expirationTtl: RESOLVE_TTL });
  return v;
}

// ---------- serving a version: R2 when pushed, a public gateway otherwise

async function serveSite(request, env, cid, path, base) {
  if (request.method !== "GET" && request.method !== "HEAD") return text("method not allowed", 405);
  const pushed = await env.REGISTRY.get("pushed:" + cid);
  if (pushed) return serveFromR2(request, env, cid, path, base);
  // not pushed here: pass the request through to an upstream gateway by CID
  let last = null;
  for (const up of upstreams(env)) {
    try {
      const r = await fetch(upstreamURL(up, "ipfs", cid, path + new URL(request.url).search), { headers: { ...UA, Accept: request.headers.get("Accept") || "*/*" }, redirect: "follow" });
      last = r;
      if (r.status >= 500 || r.status === 429) continue;
      const h = new Headers();
      for (const k of ["content-type", "content-length", "etag", "last-modified", "x-ipfs-roots", "accept-ranges"]) if (r.headers.has(k)) h.set(k, r.headers.get(k));
      h.set("cache-control", "public, max-age=60");
      h.set("access-control-allow-origin", "*");
      return new Response(request.method === "HEAD" ? null : r.body, { status: r.status, headers: h });
    } catch (e) {
      console.log("upstream", up, e.message);
    }
  }
  return text(`no upstream could serve ${cid}${path}` + (last ? ` (${last.status})` : ""), 502);
}

async function serveFromR2(request, env, cid, path, base) {
  let key = `sites/${cid}${path}`;
  if (path.endsWith("/")) key += "index.html";
  let obj = await env.SITES.get(key);
  if (!obj && !path.endsWith("/")) {
    // a directory asked for without its slash
    const idx = await env.SITES.head(`sites/${cid}${path}/index.html`);
    if (idx) return Response.redirect(new URL(base + path + "/", request.url).toString(), 301);
  }
  if (!obj) return text("not found", 404);
  const h = new Headers();
  h.set("content-type", contentType(key));
  h.set("etag", `"${obj.httpEtag.replace(/"/g, "")}"`);
  h.set("x-ipfs-roots", cid);
  h.set("cache-control", "public, max-age=60");
  h.set("access-control-allow-origin", "*");
  if (request.headers.get("if-none-match") === h.get("etag")) return new Response(null, { status: 304, headers: h });
  return new Response(request.method === "HEAD" ? null : obj.body, { headers: h });
}

const TYPES = { html: "text/html; charset=utf-8", htm: "text/html; charset=utf-8", css: "text/css; charset=utf-8", js: "text/javascript; charset=utf-8", mjs: "text/javascript; charset=utf-8", json: "application/json", xml: "application/xml", rss: "application/rss+xml", txt: "text/plain; charset=utf-8", md: "text/markdown; charset=utf-8", png: "image/png", jpg: "image/jpeg", jpeg: "image/jpeg", gif: "image/gif", webp: "image/webp", avif: "image/avif", svg: "image/svg+xml", ico: "image/x-icon", mp4: "video/mp4", webm: "video/webm", mp3: "audio/mpeg", m4a: "audio/mp4", wav: "audio/wav", ogg: "audio/ogg", woff: "font/woff", woff2: "font/woff2", ttf: "font/ttf", otf: "font/otf", pdf: "application/pdf", wasm: "application/wasm", map: "application/json" };
function contentType(key) {
  const ext = key.slice(key.lastIndexOf(".") + 1).toLowerCase();
  return TYPES[ext] || "application/octet-stream";
}

// ---------- registry

async function entryByName(env, name) {
  const ipns = await env.REGISTRY.get("name:" + name);
  return ipns ? entryByKey(env, ipns) : null;
}
async function entryByKey(env, ipns) {
  return env.REGISTRY.get("key:" + ipns, { type: "json" });
}
async function saveEntry(env, e) {
  await env.REGISTRY.put("key:" + e.ipns, JSON.stringify(e));
}

async function directory(env) {
  const names = [];
  let cursor;
  do {
    const page = await env.REGISTRY.list({ prefix: "name:", cursor });
    for (const k of page.keys) names.push(k.name.slice(5));
    cursor = page.list_complete ? undefined : page.cursor;
  } while (cursor);
  names.sort();
  const esc = (s) => s.replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
  const d = esc(env.DOMAIN);
  return new Response(`<!doctype html><meta charset=utf-8><title>${d}</title><style>body{font:16px/1.5 system-ui;max-width:640px;margin:40px auto;padding:0 16px}li{margin:4px 0}</style><h1>${d}</h1><p>Sites published from croptop. ENS names live at <code>name.${d}</code>; free names at <code>${d}/name</code>.</p><ul>${names.map((n) => `<li><a href="/${esc(n)}/">${esc(n)}</a></li>`).join("")}</ul>`, { headers: { "content-type": "text/html; charset=utf-8" } });
}

// ---------- API

async function api(request, url, env, ctx) {
  const p = url.pathname.slice("/v0/host/".length);
  if (p === "names" && request.method === "POST") return claim(request, url, env);
  if (p === "push" && request.method === "POST") return push(request, url, env, ctx);
  if (p.startsWith("names/") && request.method === "GET") return entryResponse(await entryByName(env, p.slice(6)));
  if (p.startsWith("keys/") && request.method === "GET") return entryResponse(await entryByKey(env, p.slice(5)));
  if (p === "debug/resolve" && request.method === "GET") return debugResolve(env, url.searchParams.get("name") || "");
  if (p === "debug/nodeforward" && request.method === "GET" && env.NODE) {
    // forward a slice of a pushed site's real files to the node, to find what a firewall dislikes
    const cid = url.searchParams.get("cid"), from = Number(url.searchParams.get("from") || 0), to = Number(url.searchParams.get("to") || 1000);
    const list = await env.SITES.list({ prefix: `sites/${cid}/`, limit: 1000 });
    const keys = list.objects.map((o) => o.key).slice(from, to);
    const fd = new FormData();
    const b64 = !!url.searchParams.get("b64");
    for (const k of keys) { const o = await env.SITES.get(k); const buf = await o.arrayBuffer(); fd.append("file:" + k.slice(`sites/${cid}/`.length), new Blob([b64 ? toBase64(buf) : buf]), k.split("/").pop()); }
    const hdrs = { ...UA, "X-Croptop-Signed-Host": "test.crop.top" };
    if (b64) hdrs["X-Croptop-Encoding"] = "base64";
    if (url.searchParams.get("real")) {
      const last = await env.REGISTRY.get("lastpush:" + url.searchParams.get("real"), { type: "json" });
      for (const [k, v] of Object.entries(last || {})) if (v && !(url.searchParams.get("omit") || "").split(",").includes(k.replace("X-Croptop-", ""))) hdrs[k] = v;
    }
    const r = await fetch(`${env.NODE}/v0/host/push`, { method: "POST", headers: hdrs, body: fd });
    return json({ sent: keys.length, headers: Object.keys(hdrs), status: r.status, body: (await r.text()).slice(0, 60) });
  }
  if (p === "debug/nodepush" && request.method === "GET" && env.NODE) {
    // what does the node's edge say to a POST from a Worker?
    const kb = Number(url.searchParams.get("kb") || 1), parts = Number(url.searchParams.get("parts") || 1);
    const payload = url.searchParams.get("xss") ? "<html><script type=\"module\">const x = document.getElementById('a'); x.innerHTML = '<b>hi</b>'; fetch('https://example.com');</script></html>" : null;
    const fd = new FormData(); for (let i = 0; i < parts; i++) fd.append(`file:dir${i % 7}/probe${i}.${payload ? "html" : "txt"}`, new Blob([payload || new Uint8Array(kb * 1024)]), `probe${i}.${payload ? "html" : "txt"}`);
    const hdrs = { ...UA, "X-Croptop-Signed-Host": "test.crop.top" };
    if (url.searchParams.get("hdrs")) Object.assign(hdrs, { "X-Croptop-Ipns": "k51qzi5uqu5dilqjwgdm3zj0g24zaowqfxoargdm1lbj7s4frpwuzqp2tmxm4g", "X-Croptop-Cid": "bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi", "X-Croptop-Seq": "1", "X-Croptop-Time": String(Math.floor(Date.now() / 1000)), "X-Croptop-Sig": btoa(String.fromCharCode(...new Uint8Array(64))), "X-Croptop-Record": btoa(String.fromCharCode(...new Uint8Array(Number(url.searchParams.get("rec") || 400)))) });
    const r = await fetch(`${env.NODE}/v0/host/push`, { method: "POST", headers: hdrs, body: fd });
    return json({ status: r.status, server: r.headers.get("server"), cf: r.headers.get("cf-ray"), body: (await r.text()).slice(0, 300), headers: Object.fromEntries([...r.headers.entries()].slice(0, 12)) });
  }
  return text("not found", 404);
}

function entryResponse(e) {
  if (!e) return text("not found", 404);
  const { record, ...pub } = e;
  return json(pub);
}

const fresh = (t) => Math.abs(Date.now() / 1000 - t) < SKEW;
// Requests are signed over the hostname the client addressed: the Host header
// (behind Cloudflare it equals the URL host; in local dev it is localhost).
// SIGNING_HOST (dev only) pins it, because wrangler dev rewrites Host to the zone.
const signingHost = (request, url, env) => (env && env.SIGNING_HOST) || (request.headers.get("host") || url.hostname).split(":")[0].toLowerCase();
const claimMessage = (domain, name, ipns, t) => `croptop-name\n${domain}\n${name}\n${ipns}\n${t}`;
const pushMessage = (domain, ipns, cid, seq, t) => `croptop-push\n${domain}\n${ipns}\n${cid}\n${seq}\n${t}`;

async function claim(request, url, env) {
  const inp = await request.json().catch(() => ({}));
  const name = String(inp.name || "").trim().toLowerCase();
  if (!NAME_RE.test(name) || RESERVED.has(name)) return text("that name cannot be claimed", 400);
  const ok = fresh(inp.time) && (await verify(inp.ipns, claimMessage(signingHost(request, url, env), name, inp.ipns, inp.time), inp.sig));
  if (!ok) console.log("claim rejected", JSON.stringify({ host: signingHost(request, url, env), name, ipns: inp.ipns, time: inp.time, fresh: fresh(inp.time), sigLen: (inp.sig || "").length }));
  if (!ok) return text("bad signature", 403);
  const owner = await env.REGISTRY.get("name:" + name);
  if (owner && owner !== inp.ipns) return text("that name is taken", 409);
  const e = (await entryByKey(env, inp.ipns)) || { ipns: inp.ipns };
  if (e.name && e.name !== name) await env.REGISTRY.delete("name:" + e.name); // one name per key
  e.name = name;
  e.updated = new Date().toISOString();
  await env.REGISTRY.put("name:" + name, inp.ipns);
  await saveEntry(env, e);
  return entryResponse(e);
}

async function push(request, url, env, ctx) {
  const h = (k) => request.headers.get("X-Croptop-" + k) || "";
  const ipns = h("Ipns"), cid = h("Cid"), seq = Number(h("Seq")), t = Number(h("Time"));
  if (!/^(bafy|Qm)[a-zA-Z0-9]+$/.test(cid) || !Number.isFinite(seq)) return text("bad cid or sequence", 400);
  const ok = fresh(t) && (await verify(ipns, pushMessage(signingHost(request, url, env), ipns, cid, seq, t), h("Sig")));
  if (!ok) return text("bad signature", 403);
  const existing = await entryByKey(env, ipns);
  if (existing && seq < existing.sequence) return text(`host already has sequence ${existing.sequence}`, 409);
  if (Number(request.headers.get("content-length") || 0) > MAX_PUSH) return text("push too large", 413);
  const form = await request.formData();
  let n = 0;
  for (const [field, value] of form.entries()) {
    // the path rides in the field name ("file:<path>"): file names lose their directories in some parsers
    if (!field.startsWith("file:") || typeof value === "string") continue;
    const rel = field.slice(5).replace(/^\/+/, "");
    if (!rel || rel.includes("..")) continue;
    await env.SITES.put(`sites/${cid}/${rel}`, value.stream(), { httpMetadata: { contentType: contentType(rel) } });
    n++;
  }
  if (n === 0) return text("no files", 400);
  const e = existing || { ipns };
  e.cid = cid; e.sequence = seq; e.updated = new Date().toISOString();
  if (h("Record")) e.record = h("Record");
  await saveEntry(env, e);
  await env.REGISTRY.put("pushed:" + cid, "1");
  if (e.record) ctx.waitUntil(republish(ipns, e.record));
  await env.REGISTRY.put("lastpush:" + ipns, JSON.stringify(Object.fromEntries(["Ipns", "Cid", "Seq", "Time", "Sig", "Record"].map((k) => ["X-Croptop-" + k, request.headers.get("X-Croptop-" + k) || ""]))), { expirationTtl: 3600 });
  // replicate to the node so the IPFS network gets the site from a reachable
  // peer, not from the author's laptop; it re-adds the files and checks the cid
  if (env.NODE) ctx.waitUntil(forwardPush(env, request, form, signingHost(request, url, env)));
  return json({ cid, sequence: seq, files: n, name: e.name || "" });
}

async function resolveENSTarget(ens) {
  const r = await fetch(`https://dns.eth.limo/dns-query?name=_dnslink.${ens}&type=TXT`, { headers: { Accept: "application/dns-json" } });
  const j = await r.json();
  for (const a of j.Answer || []) { const m = /dnslink=(\/ip[fn]s\/[^"\s]+)/.exec(a.data); if (m) return m[1]; }
  return null;
}

// debugResolve shows each resolution step for a name, read-only.
async function debugResolve(env, name) {
  const out = { name };
  const probe = async (label, fn) => { try { out[label] = await fn(); } catch (e) { out[label] = "error: " + e.message; } };
  if (name.endsWith(".eth")) {
    await probe("dnslink", async () => { const r = await fetch(`https://dns.eth.limo/dns-query?name=_dnslink.${name}&type=TXT`, { headers: { Accept: "application/dns-json" } }); return r.status + " " + (await r.text()).slice(0, 200); });
  }
  const key = name.startsWith("k51") ? name : (await resolveENSTarget(name))?.replace("/ipns/", "");
  if (key) {
    await probe("delegated", async () => { const r = await fetch(`https://delegated-ipfs.dev/routing/v1/ipns/${key}`, { headers: { Accept: "application/vnd.ipfs.ipns-record" } }); return r.status + " " + r.headers.get("content-type"); });
  }
  if (key) for (const up of upstreams(env)) await probe("upstream:" + up, () => rootsOf(upstreamURL(up, "ipns", key)));
  await probe("resolveAny", () => resolveAny(env, name));
  return json(out);
}

async function forwardPush(env, request, form, signedHost) {
  try {
    const fd = new FormData();
    for (const [field, value] of form.entries()) if (field.startsWith("file:") && typeof value !== "string") fd.append(field, new Blob([toBase64(await value.arrayBuffer())]), value.name);
    // base64, because a firewall in front of the node reads a site's scripts as an attack
    const headers = { ...UA, "X-Croptop-Signed-Host": signedHost, "X-Croptop-Encoding": "base64" };
    for (const k of ["Ipns", "Cid", "Seq", "Time", "Sig", "Record"]) { const v = request.headers.get("X-Croptop-" + k); if (v) headers["X-Croptop-" + k] = v; }
    console.log("node push start", Object.keys(headers).join(","), [...fd.keys()].length + " files");
    const r = await fetch(`${env.NODE}/v0/host/push`, { method: "POST", headers, body: fd });
    console.log("node push result", r.status, r.headers.get("server") || "", r.headers.get("content-type") || "", (await r.text()).slice(0, 200));
  } catch (e) {
    console.log("node push failed", e.message);
  }
}

// ---------- IPNS records: keep them alive from here

async function republish(ipns, recordB64) {
  const body = Uint8Array.from(atob(recordB64), (c) => c.charCodeAt(0));
  await fetch(`https://delegated-ipfs.dev/routing/v1/ipns/${ipns}`, { method: "PUT", headers: { "content-type": "application/vnd.ipfs.ipns-record" }, body }).catch(() => {});
}

async function republishAll(env) {
  let cursor;
  do {
    const page = await env.REGISTRY.list({ prefix: "key:", cursor });
    for (const k of page.keys) {
      const e = await env.REGISTRY.get(k.name, { type: "json" });
      if (e && e.record) await republish(e.ipns, e.record);
    }
    cursor = page.list_complete ? undefined : page.cursor;
  } while (cursor);
}

// ---------- crypto: the site's ed25519 key lives inside its IPNS name

const B36 = "0123456789abcdefghijklmnopqrstuvwxyz";
function base36Decode(s) {
  let n = 0n;
  for (const c of s) {
    const d = B36.indexOf(c);
    if (d < 0) throw new Error("bad base36");
    n = n * 36n + BigInt(d);
  }
  const out = [];
  while (n > 0n) { out.unshift(Number(n & 255n)); n >>= 8n; }
  return Uint8Array.from(out);
}

// k51... = base36 of CIDv1(libp2p-key, identity multihash(protobuf PublicKey{Ed25519, 32 bytes}))
function publicKeyOf(ipns) {
  if (!ipns || ipns[0] !== "k") return null;
  const b = base36Decode(ipns.slice(1));
  // 01 72 | 00 <len> | 08 01 12 20 <32 bytes>
  if (b[0] !== 0x01 || b[1] !== 0x72 || b[2] !== 0x00 || b[4] !== 0x08 || b[5] !== 0x01 || b[6] !== 0x12 || b[7] !== 0x20) return null;
  const key = b.slice(8, 40);
  return key.length === 32 ? key : null;
}

async function verify(ipns, message, sigB64) {
  try {
    const pub = publicKeyOf(ipns);
    if (!pub || !sigB64) return false;
    const key = await crypto.subtle.importKey("raw", pub, { name: "Ed25519" }, false, ["verify"]);
    const sig = Uint8Array.from(atob(sigB64), (c) => c.charCodeAt(0));
    return crypto.subtle.verify({ name: "Ed25519" }, key, sig, new TextEncoder().encode(message));
  } catch (e) {
    console.log("verify failed:", e.message);
    return false;
  }
}

// ---------- IPNS record protobuf: field 1 value, field 5 sequence

function parseRecord(b) {
  let i = 0;
  const varint = () => { let r = 0n, s = 0n; for (;;) { const c = b[i++]; r |= BigInt(c & 127) << s; s += 7n; if (c < 128) return r; } };
  const out = {};
  while (i < b.length) {
    const key = Number(varint()), field = key >> 3, wt = key & 7;
    if (wt === 0) { const v = varint(); if (field === 5) out.sequence = Number(v); }
    else if (wt === 2) { const len = Number(varint()); const bytes = b.slice(i, i + len); i += len; if (field === 1) out.value = new TextDecoder().decode(bytes); }
    else break;
  }
  return out;
}

function toBase64(buf) {
  const bytes = new Uint8Array(buf);
  let bin = "";
  for (let i = 0; i < bytes.length; i += 0x8000) bin += String.fromCharCode.apply(null, bytes.subarray(i, i + 0x8000));
  return btoa(bin);
}

// ---------- small helpers

const text = (s, status = 200) => new Response(s, { status, headers: { "content-type": "text/plain; charset=utf-8" } });
const json = (v, status = 200) => new Response(JSON.stringify(v), { status, headers: { "content-type": "application/json" } });

export { publicKeyOf, verify, parseRecord, base36Decode };

// crop.top on Cloudflare: a gateway for ENS, IPNS and claimed names, a store
// that sites push to on publish, and a registry of free names. No IPFS node:
// pushed sites live in R2, everything else is fetched from a public gateway.

import installSh from "../../scripts/install.sh";
import installPs1 from "../../scripts/install.ps1";
import agentsMd from "../../docs/agents.md";
import { mobileOriginConfig, serveMobile } from "./mobile.js";

const SKEW = 10 * 60;
const RESOLVE_TTL = 60;
const NAME_RE = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;
const RESERVED = new Set(["www", "api", "v0", "ipfs", "ipns", "push", "host", "admin", "mail", "static", "assets", "docs", "app", "directory", "install", "install.sh", "install.ps1", "download", "routing", "agents", "agents.md"]);
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
  const mobile = mobileOriginConfig(env);
  if (host === mobile.defaultHost || host === mobile.host) return serveMobile(request, url, env, mobile);
  if (host === domain) return serveBare(request, url, env, ctx);
  if (host.endsWith("." + domain)) return serveLabel(request, url, env, ctx, host.slice(0, -domain.length - 1));
  return text(`unknown host ${host} (this host serves ${domain})`, 404);
}

// ---------- bare domain: API, directory, claimed names, then the root site

async function serveBare(request, url, env, ctx) {
  const p = url.pathname;
  if (p.startsWith("/routing/v1/ipns/")) return routing(request, env, p.slice("/routing/v1/ipns/".length).replace(/\/$/, ""));
  if (p.startsWith("/v0/host/")) return api(request, url, env, ctx);
  if (p.startsWith("/ipfs/")) {
    // a pushed version by CID, as croptop reads one to add a post to it
    const [cid, ...rest] = p.slice(6).split("/");
    if (!/^(bafy|qm)[a-z0-9]+$/i.test(cid)) return text("bad cid", 400);
    if (rest.length === 0) return Response.redirect(url.origin + p + "/", 301);
    return serveFromR2(request, env, cid, "/" + rest.join("/"), "/ipfs/" + cid, false);
  }
  if (p === "/install.sh") return new Response(installSh, { headers: { "content-type": "text/x-shellscript; charset=utf-8", "cache-control": "public, max-age=300" } });
  if (p === "/install.ps1") return new Response(installPs1, { headers: { "content-type": "text/plain; charset=utf-8", "cache-control": "public, max-age=300" } });
  if (p === "/agents.md" || p === "/agents" || p === "/agents/") return new Response(agentsMd, { headers: { "content-type": "text/markdown; charset=utf-8", "cache-control": "public, max-age=300" } });
  if (p === "/install" || p === "/download") return Response.redirect("https://github.com/" + "mejango/croptop/releases/latest", 302);
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
    // the node answers from the DHT; delegated-ipfs.dev is a fallback while it
    // lasts; the upstream gateways resolve the rest through their own nodes
    const sources = [env.NODE && [env.NODE + `/routing/v1/ipns/${ipns}`, 12000], [`https://delegated-ipfs.dev/routing/v1/ipns/${ipns}`, 3000]].filter(Boolean);
    for (const [src, ms] of sources) {
      try {
        const r = await fetch(src, { headers: { Accept: "application/vnd.ipfs.ipns-record" }, redirect: "follow", signal: AbortSignal.timeout(ms) });
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
  if (pushed) return serveFromR2(request, env, cid, path, base, true, pushed === "manifest");
  return serveUpstream(request, env, cid, path);
}

// serveUpstream passes a request through to an upstream gateway by CID.
async function serveUpstream(request, env, cid, path) {
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

async function serveFromR2(request, env, cid, path, base, upstream = true, isManifest = false) {
  let rel = path.slice(1);
  try { rel = decodeURIComponent(rel); } catch {} // stored under the names as pushed
  const obj = await r2(env, cid, path.endsWith("/") ? rel + "index.html" : rel);
  if (!obj && !path.endsWith("/")) {
    // a directory asked for without its slash
    const idx = await r2(env, cid, rel + "/index.html", true);
    if (idx) return Response.redirect(new URL(base + path + "/", request.url).toString(), 301);
  }
  if (!obj) {
    // manifest pushes don't fall back to upstream: if it's not in the manifest, it's gone
    if (isManifest) return text("not here", 404);
    return upstream ? serveUpstream(request, env, cid, path) : text("not here", 404);
  }
  const h = new Headers();
  h.set("content-type", contentType(obj.key));
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

// r2 finds a file of a pushed version: pushed with it, or carried from an
// earlier version when the push held only what changed.
async function r2(env, cid, rel, headOnly) {
  const op = headOnly ? "head" : "get";
  const o = await env.SITES[op](`sites/${cid}/${rel}`);
  if (o) return o;
  const from = (await carryOf(env, cid)).get(rel);
  return from ? env.SITES[op](`sites/${from}/${rel}`) : null;
}

// A push on a parent holds only what changed. carry/<cid>.json maps each
// other file of the version to the earlier version whose files hold it.
// Versions never change, so the maps are cached; a missing one is not, as it
// may be written when the version's last part arrives.
const carries = new Map();
async function carryOf(env, cid) {
  if (carries.has(cid)) return carries.get(cid);
  const o = await env.SITES.get(`carry/${cid}.json`);
  if (!o) return new Map();
  const carry = new Map(Object.entries(await o.json()));
  if (carries.size >= 64) carries.delete(carries.keys().next().value);
  carries.set(cid, carry);
  return carry;
}

async function saveCarry(env, cid, parent) {
  const carry = new Map(await carryOf(env, parent));
  for (const rel of await filesOf(env, parent)) carry.set(rel, parent);
  for (const rel of await filesOf(env, cid)) carry.delete(rel);
  await env.SITES.put(`carry/${cid}.json`, JSON.stringify(Object.fromEntries(carry)));
  carries.set(cid, carry);
}

// saveManifestCarry records a manifest push: the version is exactly its
// uploaded files plus the carried paths, each a file or a whole folder of
// the parent. Anything else of the parent is left behind, so deletions
// work. It returns why the manifest is refused, or "".
async function saveManifestCarry(env, cid, parent, carry) {
  const from = new Map(await carryOf(env, parent)); // the parent's files carried from older versions
  for (const rel of await filesOf(env, parent)) from.set(rel, parent);
  const uploaded = await filesOf(env, cid);
  const ancestors = (p) => { const out = []; for (let i = p.indexOf("/"); i >= 0; i = p.indexOf("/", i + 1)) out.push(p.slice(0, i)); return out; };
  const paths = carry.map((raw) => String(raw).replace(/^\/+|\/+$/g, ""));
  const set = new Set();
  for (const p of paths) {
    if (!p || p.split("/").some((s) => s === "" || s === "." || s === "..")) return `bad carried path ${p}`;
    if (set.has(p)) return `carried path ${p} is given twice`;
    set.add(p);
  }
  const up = new Set(uploaded);
  for (const p of paths) {
    if (ancestors(p).some((a) => set.has(a))) return `carried path ${p} is inside another carried path`;
    if (up.has(p) || ancestors(p).some((a) => up.has(a))) return `uploaded files overlap carried path ${p}`;
  }
  for (const rel of uploaded) if (ancestors(rel).some((a) => set.has(a))) return `uploaded files overlap carried path ${ancestors(rel).find((a) => set.has(a))}`;
  const out = new Map(), found = new Set();
  for (const [rel, src] of from) {
    const hit = set.has(rel) ? rel : ancestors(rel).find((a) => set.has(a));
    if (hit) { out.set(rel, src); found.add(hit); }
  }
  for (const p of paths) if (!found.has(p)) return `carried path ${p} is not in the parent`;
  await env.SITES.put(`carry/${cid}.json`, JSON.stringify(Object.fromEntries(out)));
  carries.set(cid, out);
  return "";
}

// held lists the files stored for a version, with their sizes: what an
// unfinished push left here, so its retry sends only the rest.
async function held(env, cid) {
  const out = [];
  let cursor;
  do {
    const page = await env.SITES.list({ prefix: `sites/${cid}/`, cursor, limit: 1000 });
    for (const o of page.objects) out.push({ path: o.key.slice(`sites/${cid}/`.length), size: o.size });
    cursor = page.truncated ? page.cursor : undefined;
  } while (cursor);
  return out;
}

// filesOf lists the files pushed with a version, not the ones it carries.
async function filesOf(env, cid) {
  return (await held(env, cid)).map((f) => f.path);
}

// heads/<ipns> in R2 is the version this host holds for a site. R2 reads are
// consistent everywhere at once, unlike KV, and a conditional put makes a push
// on a parent a compare-and-swap, so two posts never drop each other.
async function headOf(env, ipns, entry) {
  const o = await env.SITES.get(`heads/${ipns}`);
  if (o) return { ...(await o.json()), etag: o.etag };
  return entry && entry.cid ? { cid: entry.cid, sequence: entry.sequence } : null; // last pushed before heads existed
}

// claimVersion lets only the key that first pushed a version write its files.
// A signature proves who holds a key, not that the files hash to the CID, so
// without this any key could replace what crop.top serves for another site's
// version. Versions stored before owners were recorded take no more writes.
async function claimVersion(env, cid, ipns) {
  const o = await env.SITES.get(`owners/${cid}`);
  if (o) return (await o.text()) === ipns;
  const stored = await env.SITES.list({ prefix: `sites/${cid}/`, limit: 1 });
  if (stored.objects.length) return false;
  await env.SITES.put(`owners/${cid}`, ipns);
  return true;
}

// pushConflict says why a push of cid at seq cannot replace cur, or "". A push
// with a parent must be built on the version held here. A whole version at the
// held sequence with other content would replace it unseen: two machines that
// chose the same next sequence, a laptop and an agent say.
function pushConflict(cur, seq, cid, parent) {
  if (cur && seq < cur.sequence) return `host already has sequence ${cur.sequence}`;
  if (!parent && cur && cur.cid && seq === cur.sequence && cur.cid !== cid) return `host holds ${cur.cid} at sequence ${seq}`;
  if (parent && (!cur || cur.cid !== parent)) return `host holds ${cur ? cur.cid : "nothing"}, not ${parent}`;
  return "";
}

// putBlock keeps a folder block pushed with a version if it hashes to its CID
// (CIDv1, dag-pb, sha2-256: what croptop's folders are), so nobody can store a
// forged block under another version's CID.
async function putBlock(env, cid, value) {
  const want = cidDigest(cid);
  if (!want || (await env.SITES.head(`blocks/${cid}`))) return;
  const bytes = new Uint8Array(await value.arrayBuffer());
  const got = new Uint8Array(await crypto.subtle.digest("SHA-256", bytes));
  if (got.every((x, i) => x === want[i])) await env.SITES.put(`blocks/${cid}`, bytes);
}

// cidDigest returns the sha2-256 digest inside a CIDv1 dag-pb CID ("bafy…"), or null.
function cidDigest(cid) {
  const A = "abcdefghijklmnopqrstuvwxyz234567", out = [];
  let bits = 0, val = 0;
  for (const ch of cid.slice(1)) {
    const i = A.indexOf(ch);
    if (i < 0) return null;
    val = ((val << 5) | i) & 0xfff;
    bits += 5;
    if (bits >= 8) { bits -= 8; out.push((val >> bits) & 0xff); }
  }
  const b = Uint8Array.from(out);
  return cid[0] === "b" && b.length === 36 && b[0] === 1 && b[1] === 0x70 && b[2] === 0x12 && b[3] === 32 ? b.slice(4) : null;
}

// block serves one raw block: a folder of a pushed version, or anything the
// node holds. Clients check it against its CID.
async function block(env, cid) {
  if (!/^(bafy|Qm)[a-zA-Z0-9]+$/.test(cid)) return text("bad cid", 400);
  const raw = { "content-type": "application/vnd.ipld.raw" };
  const o = await env.SITES.get(`blocks/${cid}`);
  if (o) return new Response(o.body, { headers: raw });
  if (env.NODE) {
    const r = await fetch(`${env.NODE}/ipfs/${cid}?format=raw`, { headers: { ...UA, Accept: "application/vnd.ipld.raw" }, signal: AbortSignal.timeout(20000) }).catch(() => null);
    if (r && r.ok) return new Response(r.body, { headers: raw });
  }
  return text("not found", 404);
}

// routing is the IPNS part of the Delegated Routing V1 HTTP API, which
// delegated-ipfs.dev served until 2026-09-30. Pushed sites answer from the
// registry; everything else, and every PUT, goes to the node, which keeps
// records in the DHT.
async function routing(request, env, name) {
  if (!/^k[a-z0-9]{40,80}$/.test(name)) return text("bad name", 400);
  const raw = { "content-type": "application/vnd.ipfs.ipns-record", "cache-control": "public, max-age=60" };
  const get = request.method === "GET" || request.method === "HEAD";
  if (!get && request.method !== "PUT") return text("method not allowed", 405);
  if (get) {
    const e = await entryByKey(env, name);
    if (e && e.record) {
      const decoded = fromBase64(e.record);
      if (decoded) return new Response(request.method === "HEAD" ? null : decoded, { headers: raw });
    }
  }
  if (!env.NODE) return text("not found", 404);
  if (!get) {
    const contentLength = Number(request.headers.get("content-length") || 0);
    if (contentLength > 10240) return text("record too large", 413);
  }
  let body;
  if (!get) {
    body = await request.arrayBuffer();
    if (body.byteLength > 10240) return text("record too large", 413);
  }
  const r = await fetch(`${env.NODE}/routing/v1/ipns/${name}`, { method: request.method, headers: { ...UA, "content-type": raw["content-type"] }, body, signal: AbortSignal.timeout(35000) }).catch(() => null);
  if (!r) return text("routing unavailable", 502);
  const resHeaders = r.ok && get ? raw : { "content-type": r.headers.get("content-type") || "text/plain" };
  if (r.headers.get("retry-after")) resHeaders["retry-after"] = r.headers.get("retry-after");
  return new Response(r.body, { status: r.status, headers: resHeaders });
}

// peers lists the node's addresses, for new nodes to join the network through
// now that the public bootstrap nodes are gone.
async function peers(env) {
  if (!env.NODE) return text("not found", 404);
  const r = await fetch(`${env.NODE}/v0/host/peers`, { headers: UA, cf: { cacheTtlByStatus: { "200-299": 3600, "400-599": 0 }, cacheEverything: true }, signal: AbortSignal.timeout(10000) }).catch(() => null);
  if (!r || !r.ok) return text("peers unavailable", 502);
  return new Response(r.body, { headers: { "content-type": "application/json", "cache-control": "public, max-age=3600" } });
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
  if (p.startsWith("keys/") && request.method === "GET") {
    const ipns = p.slice(5), e = await entryByKey(env, ipns), cur = await headOf(env, ipns, e);
    // acceptsParent: pushes of only what changed are understood here
    return entryResponse(cur ? { ipns, ...e, cid: cur.cid, sequence: cur.sequence, acceptsParent: true, acceptsManifest: true } : e);
  }
  if (p.startsWith("versions/") && p.endsWith("/files") && request.method === "GET") {
    const c = p.slice("versions/".length, -"/files".length);
    if (!/^(bafy|Qm)[a-zA-Z0-9]+$/.test(c)) return text("bad cid", 400);
    return json(await held(env, c));
  }
  if (p.startsWith("blocks/") && request.method === "GET") return block(env, p.slice(7));
  if (p === "peers" && request.method === "GET") return peers(env);
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
  const ipns = h("Ipns"), cid = h("Cid"), seq = Number(h("Seq")), t = Number(h("Time")), parent = h("Parent");
  if (!/^(bafy|Qm)[a-zA-Z0-9]+$/.test(cid) || !Number.isFinite(seq)) return text("bad cid or sequence", 400);
  const ok = fresh(t) && (await verify(ipns, pushMessage(signingHost(request, url, env), ipns, cid, seq, t), h("Sig")));
  if (!ok) return text("bad signature", 403);
  const existing = await entryByKey(env, ipns);
  const cur = await headOf(env, ipns, existing);
  const conflict = pushConflict(cur, seq, cid, parent);
  if (conflict) return text(conflict, 409);
  if (!(await claimVersion(env, cid, ipns))) return text("that version belongs to another site", 409);
  if (Number(request.headers.get("content-length") || 0) > MAX_PUSH) return text("push too large", 413);
  if (h("File")) return pushChunk(request, env, ctx, cid, h, signingHost(request, url, env));
  const form = await request.formData();
  const manifest = typeof form.get("manifest") === "string" ? form.get("manifest") : null;
  let n = 0;
  for (const [field, value] of form.entries()) {
    if (typeof value === "string") continue;
    // the version's folder blocks, so the next reader can list it before any IPFS peer has it
    if (field.startsWith("block:")) { await putBlock(env, field.slice(6), value); continue; }
    // the path rides in the field name ("file:<path>"): file names lose their directories in some parsers
    if (!field.startsWith("file:")) continue;
    const rel = field.slice(5).replace(/^\/+/, "");
    if (!rel || rel.includes("..")) continue;
    const have = await env.SITES.head(`sites/${cid}/${rel}`);
    if (!have || have.size !== value.size) await env.SITES.put(`sites/${cid}/${rel}`, value.stream(), { httpMetadata: { contentType: contentType(rel) } });
    n++;
  }
  if (n === 0) return text("no files", 400);
  // a site bigger than one request arrives as "i/n" parts; the last one commits
  const [partNo, partCount] = (h("Part") || "1/1").split("/").map(Number);
  const final = !partCount || partNo >= partCount;
  const e = existing || { ipns };
  if (final) {
    // a push on a parent holds only what changed; record where the rest is,
    // then move the head, only if no other push moved it since this one began
    // (or, for a site last pushed before heads existed, only if none was made)
    if (manifest !== null) {
      let carry;
      try { carry = JSON.parse(manifest).carry || []; } catch { return text("bad manifest", 400); }
      if (!parent || !Array.isArray(carry)) return text("a manifest needs a parent and a carry list", 400);
      const bad = await saveManifestCarry(env, cid, parent, carry);
      if (bad) return text(bad, 400);
    } else if (parent) await saveCarry(env, cid, parent);
    // every push moves the head only from the one it began on: an agent's post
    // that landed while this push ran is never overwritten unseen. A push
    // without a parent that finds the head moved checks again, and goes ahead
    // only if what is there now is still no conflict.
    const head = JSON.stringify({ cid, sequence: seq });
    const from = (c) => ({ onlyIf: c && c.etag ? { etagMatches: c.etag } : { etagDoesNotMatch: "*" } });
    let moved = await env.SITES.put(`heads/${ipns}`, head, from(cur));
    if (!moved && !parent) {
      const now = await headOf(env, ipns, await entryByKey(env, ipns));
      const again = pushConflict(now, seq, cid, parent);
      if (again) return text(again, 409);
      moved = await env.SITES.put(`heads/${ipns}`, head, from(now));
    }
    if (!moved) return text("the site changed during this push; post again", 409);
    e.cid = cid; e.sequence = seq; e.updated = new Date().toISOString();
    if (h("Record")) e.record = h("Record");
    await saveEntry(env, e);
    await env.REGISTRY.put("pushed:" + cid, manifest !== null ? "manifest" : "1");
    if (e.record) ctx.waitUntil(republish(env, ipns, e.record));
  }
  await env.REGISTRY.put("lastpush:" + ipns, JSON.stringify(Object.fromEntries(["Ipns", "Cid", "Seq", "Time", "Sig", "Record"].map((k) => ["X-Croptop-" + k, request.headers.get("X-Croptop-" + k) || ""]))), { expirationTtl: 3600 });
  // the node mirrors the site so the IPFS network gets it from a reachable
  // peer: it pulls the files back from R2 through this Worker and checks the cid
  if (final && env.NODE) ctx.waitUntil(notifyNode(env, request, cid, signingHost(request, url, env)));
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

// pushChunk stores one chunk of a file too big for a single request, through
// R2's multipart upload. The first chunk opens the upload and returns its id;
// the last one completes it. Chunks are forwarded to the node as they come.
async function pushChunk(request, env, ctx, cid, h, signedHost) {
  const rel = h("File").replace(/^\/+/, "");
  const [i, n] = h("Chunk").split("/").map(Number);
  if (!rel || rel.includes("..") || !(i >= 1 && n >= i)) return text("bad chunk", 400);
  const key = `sites/${cid}/${rel}`;
  const stateKey = `upload:${cid}:${rel}`;
  let state = i === 1 ? null : await env.REGISTRY.get(stateKey, { type: "json" });
  let mp;
  if (!state) {
    mp = await env.SITES.createMultipartUpload(key, { httpMetadata: { contentType: contentType(rel) } });
    state = { uploadId: mp.uploadId, parts: [] };
  } else {
    mp = env.SITES.resumeMultipartUpload(key, state.uploadId);
  }
  const body = await request.arrayBuffer();
  const part = await mp.uploadPart(i, body);
  state.parts[i - 1] = part;
  if (i === n) {
    await mp.complete(state.parts.filter(Boolean));
    await env.REGISTRY.delete(stateKey);
  } else {
    await env.REGISTRY.put(stateKey, JSON.stringify(state), { expirationTtl: 3600 });
  }
  return json({ upload: state.uploadId, chunk: i, of: n });
}

// notifyNode tells the node a version is complete in R2; it pulls the files
// from <cid>.<domain>, re-adds them, checks the cid, and provides the blocks.
async function notifyNode(env, request, cid, signedHost) {
  try {
    const keys = await filesOf(env, cid);
    for (const rel of (await carryOf(env, cid)).keys()) keys.push(rel); // the node rebuilds the whole version
    const headers = { ...UA, "Content-Type": "application/json", "X-Croptop-Signed-Host": signedHost };
    for (const k of ["Ipns", "Cid", "Seq", "Time", "Sig", "Record"]) { const v = request.headers.get("X-Croptop-" + k); if (v) headers["X-Croptop-" + k] = v; }
    const r = await fetch(`${env.NODE}/v0/host/pull`, { method: "POST", headers, body: JSON.stringify({ base: `https://${cid}.${env.DOMAIN}/`, files: keys }) });
    if (!r.ok) console.log("node pull", r.status, (await r.text()).slice(0, 120));
  } catch (e) {
    console.log("node pull failed", e.message);
  }
}

// ---------- IPNS records: keep them alive from here

async function republish(env, ipns, recordB64) {
  const body = fromBase64(recordB64);
  if (!body) return;
  const putNode = env.NODE && fetch(`${env.NODE}/routing/v1/ipns/${ipns}`, { method: "PUT", headers: { "content-type": "application/vnd.ipfs.ipns-record" }, body, signal: AbortSignal.timeout(20000) }).catch(() => null).then((r) => { if (r && !r.ok) console.log("republish", ipns, "node", r.status); });
  const putDelegated = fetch(`https://delegated-ipfs.dev/routing/v1/ipns/${ipns}`, { method: "PUT", headers: { "content-type": "application/vnd.ipfs.ipns-record" }, body, signal: AbortSignal.timeout(3000) }).catch(() => {});
  await Promise.all([putNode, putDelegated]);
}

async function republishAll(env) {
  // the node re-puts its own sites; this cron is the backstop for ones others
  // claimed. pause to avoid overloading the node's 32 concurrent slots (429).
  let cursor;
  do {
    const page = await env.REGISTRY.list({ prefix: "key:", cursor });
    for (const k of page.keys) {
      const e = await env.REGISTRY.get(k.name, { type: "json" });
      if (e && e.record) {
        await republish(env, e.ipns, e.record);
        await new Promise((r) => setTimeout(r, 1000));
      }
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

// fromBase64 decodes base64, or gives null for anything that isn't.
function fromBase64(s) {
  try { return Uint8Array.from(atob(s), (c) => c.charCodeAt(0)); } catch { return null; }
}

// ---------- small helpers

const text = (s, status = 200) => new Response(s, { status, headers: { "content-type": "text/plain; charset=utf-8" } });
const json = (v, status = 200) => new Response(JSON.stringify(v), { status, headers: { "content-type": "application/json" } });

export { publicKeyOf, verify, parseRecord, base36Decode, republish };

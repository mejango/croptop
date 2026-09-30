import { test } from "node:test";
import assert from "node:assert/strict";
import { generateKeyPairSync, sign, createHash } from "node:crypto";

// the CIDv1 (dag-pb, sha2-256) a block of these bytes has
function blockCID(bytes) {
  const b = Uint8Array.from([1, 0x70, 0x12, 32, ...createHash("sha256").update(bytes).digest()]);
  const A = "abcdefghijklmnopqrstuvwxyz234567";
  let s = "b", bits = 0, val = 0;
  for (const x of b) { val = (val << 8) | x; bits += 8; while (bits >= 5) { bits -= 5; s += A[(val >> bits) & 31]; } val &= 0xff; }
  if (bits > 0) s += A[(val << (5 - bits)) & 31];
  return s;
}
import worker, { republish } from "../src/index.js";

function ipnsName(pub) {
  const proto = Uint8Array.from([0x08, 0x01, 0x12, 0x20, ...pub]);
  const cid = Uint8Array.from([0x01, 0x72, 0x00, proto.length, ...proto]);
  let n = 0n; for (const b of cid) n = (n << 8n) | BigInt(b);
  let s = ""; const A = "0123456789abcdefghijklmnopqrstuvwxyz";
  while (n > 0n) { s = A[Number(n % 36n)] + s; n /= 36n; }
  return "k" + s;
}

// R2 and KV in memory, with the parts of their APIs the Worker uses
function r2() {
  const m = new Map(); let n = 0;
  const obj = (key, v) => ({ key, size: v.bytes.length, etag: v.etag, httpEtag: `"${v.etag}"`, body: new Blob([v.bytes]).stream(), json: async () => JSON.parse(new TextDecoder().decode(v.bytes)), text: async () => new TextDecoder().decode(v.bytes) });
  return {
    async get(k) { const v = m.get(k); return v ? obj(k, v) : null; },
    async head(k) { const v = m.get(k); return v ? obj(k, v) : null; },
    async put(k, body, opts = {}) {
      const bytes = new Uint8Array(await new Response(body).arrayBuffer());
      // check and write with no await between, as R2 does a conditional put atomically
      const c = opts.onlyIf || {};
      if (c.etagMatches && (m.get(k) || {}).etag !== c.etagMatches) return null;
      if (c.etagDoesNotMatch === "*" && m.has(k)) return null; // create only
      const v = { bytes, etag: "e" + ++n };
      m.set(k, v);
      return obj(k, v);
    },
    async delete(k) { m.delete(k); },
    async list({ prefix, cursor, limit = 1000 }) {
      const keys = [...m.keys()].filter((k) => k.startsWith(prefix)).sort(), at = Number(cursor || 0);
      return { objects: keys.slice(at, at + limit).map((key) => ({ key })), truncated: at + limit < keys.length, cursor: String(at + limit) };
    },
  };
}
function kv() {
  const m = new Map();
  return {
    async get(k, o) { const v = m.get(k); return v === undefined ? null : o && o.type === "json" ? JSON.parse(v) : v; },
    async put(k, v) { m.set(k, String(v)); },
    async delete(k) { m.delete(k); },
    async list({ prefix }) { return { keys: [...m.keys()].filter((k) => k.startsWith(prefix)).map((name) => ({ name })), list_complete: true }; },
  };
}

test("a push on a parent carries the rest of the site and refuses a stale parent", async () => {
  const { publicKey, privateKey } = generateKeyPairSync("ed25519");
  const ipns = ipnsName(new Uint8Array(publicKey.export({ format: "der", type: "spki" }).slice(-32)));
  const env = { DOMAIN: "crop.test", NODE: "https://node.test", SITES: r2(), REGISTRY: kv() };
  const waits = [], ctx = { waitUntil: (p) => waits.push(p) };
  const pulls = [];
  const realFetch = globalThis.fetch;
  globalThis.fetch = async (u, init) => { pulls.push(JSON.parse(init.body)); return new Response("{}", { status: 202 }); };
  const call = (url, init) => worker.fetch(new Request(url, init), env, ctx);
  const push = (cid, seq, files, parent, blocks = {}) => {
    const t = Math.floor(Date.now() / 1000);
    const sig = sign(null, Buffer.from(`croptop-push\ncrop.test\n${ipns}\n${cid}\n${seq}\n${t}`), privateKey).toString("base64");
    const fd = new FormData();
    for (const [rel, body] of Object.entries(files)) fd.append("file:" + rel, new Blob([body]), rel.split("/").pop());
    for (const [c, data] of Object.entries(blocks)) fd.append("block:" + c, new Blob([data]), c);
    const headers = { "X-Croptop-Ipns": ipns, "X-Croptop-Cid": cid, "X-Croptop-Seq": String(seq), "X-Croptop-Time": String(t), "X-Croptop-Sig": sig };
    if (parent) headers["X-Croptop-Parent"] = parent;
    return call("https://crop.test/v0/host/push", { method: "POST", headers, body: fd });
  };
  const body = async (url) => { const r = await call(url); return [r.status, await r.text()]; };
  try {
    assert.equal((await push("bafyone", 1, { "planet.json": "v1", "old/photo one.png": "old photo", "old/index.html": "old page" })).status, 200);
    const folder = "a folder block", forged = blockCID("the real folder");
    assert.equal((await push("bafytwo", 2, { "planet.json": "v2", "new/index.html": "new page" }, "bafyone", { [blockCID(folder)]: folder, [forged]: "not it" })).status, 200);
    await Promise.all(waits);

    // the second version is whole: its own files and the ones it carries
    assert.deepEqual(await body("https://bafytwo.crop.test/planet.json"), [200, "v2"]);
    assert.deepEqual(await body("https://bafytwo.crop.test/old/photo%20one.png"), [200, "old photo"]);
    assert.deepEqual(await body("https://crop.test/ipfs/bafytwo/old/"), [200, "old page"]);
    assert.equal((await call("https://crop.test/ipfs/bafytwo/old")).status, 301);
    assert.deepEqual(await body("https://crop.test/ipfs/bafytwo/new/"), [200, "new page"]);
    assert.equal((await call("https://crop.test/ipfs/bafytwo/missing.png")).status, 404, "the path form serves stored versions only");
    assert.deepEqual(await body(`https://crop.test/v0/host/blocks/${blockCID(folder)}`), [200, folder]);
    assert.equal((await call(`https://crop.test/v0/host/blocks/${forged}`)).status, 404, "a block that does not hash to its CID is not kept");
    const [, entry] = await body(`https://crop.test/v0/host/keys/${ipns}`);
    assert.equal(JSON.parse(entry).cid, "bafytwo");
    assert.equal(JSON.parse(entry).acceptsParent, true);
    // the node is sent every file of the version, carried ones included
    assert.deepEqual(pulls.at(-1).files.sort(), ["new/index.html", "old/index.html", "old/photo one.png", "planet.json"]);

    // a third version on the second carries through it to the first
    assert.equal((await push("bafythree", 3, { "planet.json": "v3" }, "bafytwo")).status, 200);
    assert.deepEqual(await body("https://bafythree.crop.test/old/photo%20one.png"), [200, "old photo"]);
    assert.deepEqual(await body("https://bafythree.crop.test/new/"), [200, "new page"]);

    // a post built on a version that has since been replaced would drop a post
    const stale = await push("bafyfour", 4, { "planet.json": "v4" }, "bafytwo");
    assert.equal(stale.status, 409);
    assert.match(await stale.text(), /holds bafythree/);
    // of two posts built on the same version at once, exactly one lands: both
    // pass the first check, then wait for each other before moving the head
    const put = env.SITES.put, held = [];
    env.SITES.put = async (k, b, o) => {
      if (k.startsWith("heads/")) await new Promise((go) => { held.push(go); if (held.length === 2) held.forEach((g) => g()); });
      return put(k, b, o);
    };
    const both = await Promise.all([push("bafyfive", 4, { "a.html": "a" }, "bafythree"), push("bafysix", 4, { "b.html": "b" }, "bafythree")]);
    assert.deepEqual(both.map((r) => r.status).sort(), [200, 409]);

    // the same for a site last pushed before heads existed: its version is
    // known only from the registry, and the first head is made only once
    env.SITES.put = put;
    const legacy = await env.REGISTRY.get("key:" + ipns, { type: "json" });
    await env.REGISTRY.put("key:" + ipns, JSON.stringify({ ...legacy, cid: "bafylegacy", sequence: 9 }));
    const heads = [...(await env.SITES.list({ prefix: "heads/" })).objects];
    for (const o of heads) await env.SITES.delete(o.key);
    held.length = 0;
    env.SITES.put = async (k, b, o) => {
      if (k.startsWith("heads/")) await new Promise((go) => { held.push(go); if (held.length === 2) held.forEach((g) => g()); });
      return put(k, b, o);
    };
    const first = await Promise.all([push("bafyseven", 10, { "c.html": "c" }, "bafylegacy"), push("bafyeight", 10, { "d.html": "d" }, "bafylegacy")]);
    assert.deepEqual(first.map((r) => r.status).sort(), [200, 409]);
  } finally {
    globalThis.fetch = realFetch;
  }
});

test("only the key that first pushed a version can write its files", async () => {
  const env = { DOMAIN: "crop.test", SITES: r2(), REGISTRY: kv() };
  const ctx = { waitUntil: () => {} };
  const keys = () => {
    const { publicKey, privateKey } = generateKeyPairSync("ed25519");
    return { privateKey, ipns: ipnsName(new Uint8Array(publicKey.export({ format: "der", type: "spki" }).slice(-32))) };
  };
  const push = (k, cid, seq, files) => {
    const t = Math.floor(Date.now() / 1000);
    const sig = sign(null, Buffer.from(`croptop-push\ncrop.test\n${k.ipns}\n${cid}\n${seq}\n${t}`), k.privateKey).toString("base64");
    const fd = new FormData();
    for (const [rel, body] of Object.entries(files)) fd.append("file:" + rel, new Blob([body]), rel);
    return worker.fetch(new Request("https://crop.test/v0/host/push", { method: "POST", body: fd, headers: { "X-Croptop-Ipns": k.ipns, "X-Croptop-Cid": cid, "X-Croptop-Seq": String(seq), "X-Croptop-Time": String(t), "X-Croptop-Sig": sig } }), env, ctx);
  };
  const served = async (cid, rel) => (await worker.fetch(new Request(`https://crop.test/ipfs/${cid}/${rel}`), env, ctx)).text();
  const owner = keys(), attacker = keys();
  assert.equal((await push(owner, "bafysite", 1, { "index.html": "the owner's page" })).status, 200);
  const hijack = await push(attacker, "bafysite", 1, { "index.html": "someone else's page!!" });
  assert.equal(hijack.status, 409);
  assert.equal(await served("bafysite", "index.html"), "the owner's page");
  assert.equal((await push(owner, "bafysite", 1, { "index.html": "the owner's page" })).status, 200, "the owner may push its version again");
  // a version stored before owners were recorded takes no more writes from anyone
  await env.SITES.put("sites/bafyold/index.html", "legacy page");
  assert.equal((await push(attacker, "bafyold", 1, { "index.html": "replaced" })).status, 409);
  assert.equal(await served("bafyold", "index.html"), "legacy page");
});

// protobuf of an IPNS record with only value (field 1) and sequence (field 5), enough for parseRecord
const recordBytes = (value, seq) => Uint8Array.from([0x0a, value.length, ...new TextEncoder().encode(value), 0x28, seq]);

test("names resolve and route through the node, not delegated-ipfs.dev", async () => {
  const env = { DOMAIN: "crop.test", NODE: "https://node.test", UPSTREAMS: "https://up.test", SITES: r2(), REGISTRY: kv() };
  const calls = [];
  const realFetch = globalThis.fetch;
  globalThis.fetch = async (u, init = {}) => {
    u = String(u);
    calls.push({ u, method: init.method || "GET" });
    if (u.startsWith("https://node.test/routing/v1/ipns/")) return new Response(init.method === "PUT" ? null : recordBytes("/ipfs/bafyfromnode", 3), { headers: { "content-type": "application/vnd.ipfs.ipns-record" } });
    if (u === "https://node.test/v0/host/peers") return new Response('{"id":"12D3KooWnode","addrs":["/dns4/x/tcp/1"]}', { headers: { "content-type": "application/json" } });
    if (u.startsWith("https://up.test/ipfs/bafyfromnode/")) return new Response("from upstream");
    return new Response("nope", { status: 404 });
  };
  const call = (path, init, host = "crop.test") => worker.fetch(new Request(`https://${host}${path}`, init), env, { waitUntil() {} });
  const pushed = "k51qzi5uqu5dlgq33myrm8ik5j5m87add6c1vwaz2ubxhobjtibprs7nc9bnto";
  const other = "k51qzi5uqu5dhxiwvl4xx3sco13y50yoo28rbhe3sneirnj2qatinx75qpsqtb";
  try {
    await env.REGISTRY.put("key:" + pushed, JSON.stringify({ ipns: pushed, cid: "bafyx", sequence: 1, record: btoa("signed") }));
    const own = await call("/routing/v1/ipns/" + pushed);
    assert.equal(new TextDecoder().decode(await own.arrayBuffer()), "signed");
    assert.equal(calls.length, 0, "a pushed name is answered from the registry");
    const r = await call("/routing/v1/ipns/" + other);
    assert.equal(r.status, 200);
    assert.equal(r.headers.get("content-type"), "application/vnd.ipfs.ipns-record");
    assert.equal((await call("/routing/v1/ipns/" + other, { method: "PUT", body: new Uint8Array([1]) })).status, 200);
    assert.equal(calls.at(-1).method, "PUT");
    assert.equal((await call("/routing/v1/ipns/not-a-name")).status, 400);
    assert.equal((await (await call("/v0/host/peers")).json()).id, "12D3KooWnode");
    // a name crop.top does not host resolves through the node
    const site = await call("/planet.json", undefined, `${other}.crop.test`);
    assert.equal(await site.text(), "from upstream");
    assert.ok(!calls.some((c) => c.u.includes("delegated-ipfs.dev")), "the node answered first");
  } finally {
    globalThis.fetch = realFetch;
  }
});

test("the node being down gives 502, not a hung request", async () => {
  const env = { DOMAIN: "crop.test", NODE: "https://node.test", SITES: r2(), REGISTRY: kv() };
  const realFetch = globalThis.fetch;
  globalThis.fetch = async () => { throw new Error("connection refused"); };
  try {
    const r = await worker.fetch(new Request("https://crop.test/routing/v1/ipns/k51qzi5uqu5dhxiwvl4xx3sco13y50yoo28rbhe3sneirnj2qatinx75qpsqtb"), env, { waitUntil() {} });
    assert.equal(r.status, 502);
  } finally {
    globalThis.fetch = realFetch;
  }
});

test("republishing sends a record to the node and to delegated routing", async () => {
  const puts = [];
  const realFetch = globalThis.fetch;
  globalThis.fetch = async (u, init = {}) => { puts.push({ u: String(u), method: init.method }); return new Response(null); };
  try {
    await republish({ NODE: "https://node.test" }, "k51abc", btoa("signed"));
    assert.deepEqual(puts.map((p) => p.u).sort(), ["https://delegated-ipfs.dev/routing/v1/ipns/k51abc", "https://node.test/routing/v1/ipns/k51abc"]);
    assert.ok(puts.every((p) => p.method === "PUT"));
  } finally {
    globalThis.fetch = realFetch;
  }
});

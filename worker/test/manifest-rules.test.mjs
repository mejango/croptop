import { test } from "node:test";
import assert from "node:assert/strict";
import { generateKeyPairSync, sign } from "node:crypto";
import worker from "../src/index.js";

function ipnsName(pub) {
  const proto = Uint8Array.from([0x08, 0x01, 0x12, 0x20, ...pub]);
  const cid = Uint8Array.from([0x01, 0x72, 0x00, proto.length, ...proto]);
  let n = 0n; for (const b of cid) n = (n << 8n) | BigInt(b);
  let s = ""; const A = "0123456789abcdefghijklmnopqrstuvwxyz";
  while (n > 0n) { s = A[Number(n % 36n)] + s; n /= 36n; }
  return "k" + s;
}

// same mocks as push.test.mjs, with the maps exposed and reads counted
function r2() {
  const m = new Map(); let n = 0; const reads = [];
  const obj = (key, v) => ({ key, size: v.bytes.length, etag: v.etag, httpEtag: `"${v.etag}"`, body: new Blob([v.bytes]).stream(), json: async () => JSON.parse(new TextDecoder().decode(v.bytes)), text: async () => new TextDecoder().decode(v.bytes) });
  return {
    m, reads,
    async get(k) { reads.push("get " + k); const v = m.get(k); return v ? obj(k, v) : null; },
    async head(k) { reads.push("head " + k); const v = m.get(k); return v ? obj(k, v) : null; },
    async put(k, body, opts = {}) {
      const bytes = new Uint8Array(await new Response(body).arrayBuffer());
      const c = opts.onlyIf || {};
      if (c.etagMatches && (m.get(k) || {}).etag !== c.etagMatches) return null;
      if (c.etagDoesNotMatch === "*" && m.has(k)) return null;
      const v = { bytes, etag: "e" + ++n };
      m.set(k, v);
      return obj(k, v);
    },
    async delete(k) { m.delete(k); },
    async list({ prefix, cursor, limit = 1000 }) {
      const keys = [...m.keys()].filter((k) => k.startsWith(prefix)).sort(), at = Number(cursor || 0);
      return { objects: keys.slice(at, at + limit).map((key) => ({ key, size: m.get(key).bytes.length })), truncated: at + limit < keys.length, cursor: String(at + limit) };
    },
  };
}
function kv() {
  const m = new Map();
  return {
    m,
    async get(k, o) { const v = m.get(k); return v === undefined ? null : o && o.type === "json" ? JSON.parse(v) : v; },
    async put(k, v) { m.set(k, String(v)); },
    async delete(k) { m.delete(k); },
    async list({ prefix }) { return { keys: [...m.keys()].filter((k) => k.startsWith(prefix)).map((name) => ({ name })), list_complete: true }; },
  };
}

function harness() {
  const { publicKey, privateKey } = generateKeyPairSync("ed25519");
  const ipns = ipnsName(new Uint8Array(publicKey.export({ format: "der", type: "spki" }).slice(-32)));
  const SITES = r2(), REGISTRY = kv();
  const env = { DOMAIN: "crop.test", NODE: "https://node.test", SITES, REGISTRY };
  const waits = [], ctx = { waitUntil: (p) => waits.push(p) };
  const pulls = [];
  const realFetch = globalThis.fetch;
  globalThis.fetch = async (u, init) => { if (String(u).endsWith("/v0/host/pull")) pulls.push(JSON.parse(init.body)); return new Response("{}", { status: 202 }); };
  const call = (url, init) => worker.fetch(new Request(url, init), env, ctx);
  const push = (cid, seq, files, parent, manifest, part, rawManifest) => {
    const t = Math.floor(Date.now() / 1000);
    const sig = sign(null, Buffer.from(`croptop-push\ncrop.test\n${ipns}\n${cid}\n${seq}\n${t}`), privateKey).toString("base64");
    const fd = new FormData();
    for (const [rel, body] of Object.entries(files)) fd.append("file:" + rel, new Blob([body]), rel.split("/").pop());
    if (rawManifest !== undefined) fd.append("manifest", rawManifest);
    else if (manifest) fd.append("manifest", JSON.stringify(manifest));
    const headers = { "X-Croptop-Ipns": ipns, "X-Croptop-Cid": cid, "X-Croptop-Seq": String(seq), "X-Croptop-Time": String(t), "X-Croptop-Sig": sig };
    if (parent) headers["X-Croptop-Parent"] = parent;
    if (part) headers["X-Croptop-Part"] = part;
    return call("https://crop.test/v0/host/push", { method: "POST", headers, body: fd });
  };
  const get = async (url) => { const r = await call(url); return [r.status, await r.text()]; };
  return { ipns, env, SITES, REGISTRY, waits, pulls, call, push, get, done: () => { globalThis.fetch = realFetch; } };
}

const V1 = { "index.html": "h1", "foo/a.txt": "fa", "foobar/b.txt": "fb", "foo.txt": "ft", "assets/a.css": "css", "p1/index.html": "one", "p1/photo.jpg": "photo" };

test("chain carry, exact-file carry, and the / boundary", async () => {
  const h = harness();
  try {
    assert.equal((await h.push("bafyone", 1, V1)).status, 200);
    let r = await h.push("bafytwo", 2, { "index.html": "h2", "p3/index.html": "three" }, "bafyone", { carry: ["foo", "assets", "p1"] });
    assert.equal(r.status, 200, await r.text());
    await Promise.all(h.waits);
    assert.deepEqual(await h.get("https://bafytwo.crop.test/foo/a.txt"), [200, "fa"]);
    assert.equal((await h.get("https://bafytwo.crop.test/foobar/b.txt"))[0], 404, "foo carried, foobar not");
    assert.equal((await h.get("https://bafytwo.crop.test/foo.txt"))[0], 404, "foo.txt not carried");
    // v3 on v2: a folder v2 itself carried, exact files v2 carried and uploaded
    r = await h.push("bafythree", 3, { "index.html": "h3" }, "bafytwo", { carry: ["p1", "assets/a.css", "foo/a.txt", "p3/index.html"] });
    assert.equal(r.status, 200, await r.text());
    await Promise.all(h.waits);
    for (const [p, body] of [["p1/index.html", "one"], ["p1/photo.jpg", "photo"], ["assets/a.css", "css"], ["foo/a.txt", "fa"], ["p3/index.html", "three"], ["index.html", "h3"]]) {
      assert.deepEqual(await h.get("https://bafythree.crop.test/" + p), [200, body], p);
    }
    assert.deepEqual(h.pulls.at(-1).files.sort(), ["assets/a.css", "foo/a.txt", "index.html", "p1/index.html", "p1/photo.jpg", "p3/index.html"]);
  } finally { h.done(); }
});

const DEEP = { "index.html": "h", "assets/a.css": "css", "assets/img/logo.png": "L", "assets/img/deep/x.bin": "X", "other/o.txt": "o" };

test("deep folders: a carried folder brings every depth; nested refusal works at depth", async () => {
  const h = harness();
  try {
    assert.equal((await h.push("bafyone", 1, DEEP)).status, 200);
    let r = await h.push("bafytwo", 2, { "index.html": "h2" }, "bafyone", { carry: ["assets"] });
    assert.equal(r.status, 200, await r.text());
    await Promise.all(h.waits);
    assert.deepEqual(h.pulls.at(-1).files.sort(), ["assets/a.css", "assets/img/deep/x.bin", "assets/img/logo.png", "index.html"]);
    r = await h.push("bafythree", 3, { "index.html": "h3" }, "bafytwo", { carry: ["assets/img"] });
    assert.equal(r.status, 200, await r.text());
    await Promise.all(h.waits);
    assert.deepEqual(h.pulls.at(-1).files.sort(), ["assets/img/deep/x.bin", "assets/img/logo.png", "index.html"]);
    r = await h.push("bafyfour", 4, { "index.html": "h4" }, "bafythree", { carry: ["assets/img", "assets/img/deep/x.bin"] });
    assert.equal(r.status, 400, await r.text());
  } finally { h.done(); }
});

test("leading and trailing slashes on a carried path are trimmed", async () => {
  const h = harness();
  try {
    assert.equal((await h.push("bafyone", 1, V1)).status, 200);
    const r = await h.push("bafytwo", 2, { "index.html": "h2" }, "bafyone", { carry: ["/p1/", "foo/"] });
    assert.equal(r.status, 200, await r.text());
    await Promise.all(h.waits);
    assert.deepEqual(h.pulls.at(-1).files.sort(), ["foo/a.txt", "index.html", "p1/index.html", "p1/photo.jpg"]);
  } finally { h.done(); }
});

test("the carry map is written to R2, not only cached in the isolate", async () => {
  const h = harness();
  try {
    assert.equal((await h.push("bafyone", 1, V1)).status, 200);
    const r = await h.push("bafytwo", 2, { "index.html": "h2" }, "bafyone", { carry: ["foo", "p1/photo.jpg"] });
    assert.equal(r.status, 200, await r.text());
    const stored = JSON.parse(new TextDecoder().decode(h.SITES.m.get("carry/bafytwo.json").bytes));
    assert.deepEqual(stored, { "foo/a.txt": "bafyone", "p1/photo.jpg": "bafyone" });
  } finally { h.done(); }
});

test("a manifest rides the final part of a multi-part push", async () => {
  const h = harness();
  try {
    assert.equal((await h.push("bafyone", 1, V1)).status, 200);
    assert.equal((await h.push("bafytwo", 2, { "index.html": "h2" }, "bafyone", null, "1/2")).status, 200);
    assert.equal(h.SITES.m.has("carry/bafytwo.json"), false, "an unfinished push writes no carry map");
    const fin = await h.push("bafytwo", 2, { "p9/new.html": "n" }, "bafyone", { carry: ["foo"] }, "2/2");
    assert.equal(fin.status, 200, await fin.text());
    await Promise.all(h.waits);
    assert.deepEqual(h.pulls.at(-1).files.sort(), ["foo/a.txt", "index.html", "p9/new.html"]);
    assert.equal((await h.get("https://bafytwo.crop.test/foobar/b.txt"))[0], 404);
    assert.deepEqual(await h.get("https://bafytwo.crop.test/foo/a.txt"), [200, "fa"]);
  } finally { h.done(); }
});

test("foo, foobar and foo.txt carried together are not nested", async () => {
  const h = harness();
  try {
    assert.equal((await h.push("bafyone", 1, V1)).status, 200);
    const r = await h.push("bafytwo", 2, { "index.html": "h2" }, "bafyone", { carry: ["foo", "foobar", "foo.txt"] });
    assert.equal(r.status, 200, await r.text());
    await Promise.all(h.waits);
    assert.deepEqual(h.pulls.at(-1).files.sort(), ["foo.txt", "foo/a.txt", "foobar/b.txt", "index.html"]);
    // an upload named like a carried folder's prefix is not an overlap
    const r2_ = await h.push("bafythree", 3, { "foo2/x.txt": "x" }, "bafytwo", { carry: ["foo"] });
    assert.equal(r2_.status, 200, await r2_.text());
  } finally { h.done(); }
});

test("an uploaded file foo does not overlap a carried folder foobar", async () => {
  const h = harness();
  try {
    assert.equal((await h.push("bafyone", 1, V1)).status, 200);
    const r = await h.push("bafytwo", 2, { "foo": "a file named foo" }, "bafyone", { carry: ["foobar"] });
    assert.equal(r.status, 200, await r.text());
  } finally { h.done(); }
});

test("an uploaded file equal to a carried exact file is refused", async () => {
  const h = harness();
  try {
    assert.equal((await h.push("bafyone", 1, V1)).status, 200);
    const r = await h.push("bafytwo", 2, { "foo.txt": "new" }, "bafyone", { carry: ["foo.txt"] });
    assert.equal(r.status, 400, await r.text());
  } finally { h.done(); }
});

test("refusals leave no head move, no carry map, no registry change", async () => {
  const h = harness();
  try {
    assert.equal((await h.push("bafyone", 1, V1)).status, 200);
    assert.equal((await h.push("bafytwo", 2, { "index.html": "h2" }, "bafyone", { carry: ["p1"] })).status, 200);
    await Promise.all(h.waits);
    const kvBefore = JSON.stringify([...h.REGISTRY.m]);
    const headBefore = new TextDecoder().decode(h.SITES.m.get("heads/" + h.ipns).bytes);
    const carriesBefore = [...h.SITES.m.keys()].filter((k) => k.startsWith("carry/")).sort();
    const bad = [
      ["bafyxa", { "index.html": "x" }, "bafytwo", { carry: ["nope"] }],
      ["bafyxb", { "p1/x.txt": "x" }, "bafytwo", { carry: ["p1"] }],
      ["bafyxc", { "index.html": "x" }, "bafytwo", { carry: ["p1", "p1/photo.jpg"] }],
      ["bafyxd", { "index.html": "x" }, "", { carry: [] }],
      ["bafyxe", { "index.html": "x" }, "bafytwo", null, "not json"],
      ["bafyxf", { "index.html": "x" }, "bafytwo", null, '{"carry":"p1"}'],
      ["bafyxg", { "index.html": "x" }, "bafytwo", { carry: ["../x"] }],
      ["bafyxh", { "index.html": "x" }, "bafytwo", { carry: [""] }],
    ];
    for (const [cid, files, parent, manifest, raw] of bad) {
      const res = await h.push(cid, 3, files, parent, manifest, undefined, raw);
      assert.equal(res.status, 400, cid + " " + (await res.text()));
    }
    assert.equal(JSON.stringify([...h.REGISTRY.m]), kvBefore, "registry untouched");
    assert.equal(new TextDecoder().decode(h.SITES.m.get("heads/" + h.ipns).bytes), headBefore, "head unmoved");
    assert.deepEqual([...h.SITES.m.keys()].filter((k) => k.startsWith("carry/")).sort(), carriesBefore, "no carry map written");
    const traces = [...h.SITES.m.keys()].filter((k) => /bafyx/.test(k) && !k.startsWith("sites/") && !k.startsWith("owners/"));
    assert.deepEqual(traces, []);
    // a refused push can be retried at the same sequence with a good manifest
    const ok = await h.push("bafyxa", 3, { "index.html": "x" }, "bafytwo", { carry: ["p1"] });
    assert.equal(ok.status, 200, await ok.text());
  } finally { h.done(); }
});

test("Go-parity: duplicate carried paths are refused", async () => {
  const h = harness();
  try {
    assert.equal((await h.push("bafyone", 1, V1)).status, 200);
    const r = await h.push("bafytwo", 2, { "index.html": "x" }, "bafyone", { carry: ["p1", "p1"] });
    assert.equal(r.status, 400, await r.text());
  } finally { h.done(); }
});

test("Go-parity: uploaded file overlapping carried path is refused", async () => {
  const h = harness();
  try {
    assert.equal((await h.push("bafyone", 1, { "a/b.txt": "b", "index.html": "i" })).status, 200);
    const r = await h.push("bafytwo", 2, { "a": "a file named a" }, "bafyone", { carry: ["a/b.txt"] });
    assert.equal(r.status, 400, await r.text());
  } finally { h.done(); }
});

test("legacy and full versions keep the upstream fallback", async () => {
  const h = harness();
  let upstreamCalls = [];
  const realFetch = globalThis.fetch;
  globalThis.fetch = async (u, init) => {
    const url = String(u);
    if (url.endsWith("/v0/host/pull")) {
      if (h.pulls) h.pulls.push(JSON.parse(init.body));
      return new Response("{}", { status: 202 });
    }
    upstreamCalls.push(url);
    return new Response("upstream", { status: 200 });
  };
  try {
    // Plain push (legacy version, no manifest)
    assert.equal((await h.push("bafyone", 1, { "index.html": "h", "photo.jpg": "photo" })).status, 200);
    await Promise.all(h.waits);
    upstreamCalls.length = 0;
    const legacyMiss = await h.call("https://bafyone.crop.test/missing.txt");
    assert.equal(legacyMiss.status, 200, "legacy falls back to upstream");
    assert.ok(upstreamCalls.length > 0, "upstream was called");

    // Manifest push
    upstreamCalls.length = 0;
    h.waits.length = 0;
    assert.equal((await h.push("bafytwo", 2, { "index.html": "h2" }, "bafyone", { carry: ["photo.jpg"] })).status, 200);
    await Promise.all(h.waits);
    const manifestMiss = await h.call("https://bafytwo.crop.test/missing.txt");
    assert.equal(manifestMiss.status, 404, "manifest returns 404");
    assert.equal(upstreamCalls.length, 0, "manifest doesn't call upstream");
  } finally {
    globalThis.fetch = realFetch;
    h.done();
  }
});

test("held lists every page and bounds to the version's prefix", async () => {
  const h = harness();
  try {
    const files = {};
    for (let i = 0; i < 1205; i++) files[`d${i % 7}/f${i}.txt`] = "x".repeat(i % 5);
    assert.equal((await h.push("bafybig", 1, files, "", null, "1/2")).status, 200);
    assert.equal((await h.push("bafybigger", 2, { "z.txt": "z" }, "", null, "1/2")).status, 200); // a cid sharing the prefix
    const [st, body] = await h.get("https://crop.test/v0/host/versions/bafybig/files");
    const list = JSON.parse(body);
    assert.equal(st, 200);
    assert.equal(list.length, 1205, "all pages");
    assert.ok(!list.some((f) => f.path === "z.txt"), "bafybigger is not bafybig");
    assert.deepEqual(JSON.parse((await h.get("https://crop.test/v0/host/versions/bafyunknown/files"))[1]), []);
    for (const bad of ["..%2Fsites%2Fbafybig", "bafy%2Fx", "bafy.x", "bafy%00", ""]) {
      const r = await h.call(`https://crop.test/v0/host/versions/${bad}/files`);
      assert.equal(r.status, 400, bad + " -> " + r.status);
    }
  } finally { h.done(); }
});

test("versions/: a cid with trailing junk is refused", async () => {
  const h = harness();
  try {
    for (const bad of ["bafyabc.x", "bafyabc%2Fx", "bafyabc%00", "bafyabc-x"]) {
      const r = await h.call(`https://crop.test/v0/host/versions/${bad}/files`);
      assert.equal(r.status, 400, bad + " -> " + r.status);
    }
  } finally { h.done(); }
});

test("agents route, reserved names and claim", async () => {
  const h = harness();
  try {
    for (const name of ["agents", "agents.md", "Agents", "install", "routing"]) {
      const r = await h.call("https://crop.test/v0/host/names", { method: "POST", body: JSON.stringify({ name, ipns: h.ipns, time: Math.floor(Date.now() / 1000), sig: "x" }) });
      assert.equal(r.status, 400, `claim ${name} reserved`);
    }
    // a pre-existing claim of "agents" cannot shadow the route
    await h.REGISTRY.put("name:agents", h.ipns);
    await h.REGISTRY.put("key:" + h.ipns, JSON.stringify({ ipns: h.ipns, name: "agents", cid: "bafyone" }));
    const r = await h.call("https://crop.test/agents");
    assert.equal(r.status, 200);
    assert.match(r.headers.get("content-type"), /markdown/);
    const head = await h.call("https://crop.test/agents.md", { method: "HEAD" });
    assert.equal(head.status, 200);
    assert.match(head.headers.get("content-type"), /markdown/);
    const slash = await h.call("https://crop.test/agents/");
    assert.equal(slash.status, 200);
  } finally { h.done(); }
});

test("a miss on a plain pushed version: carry-map reads per 404", async () => {
  const h = harness();
  try {
    assert.equal((await h.push("bafyone", 1, V1)).status, 200);
    await Promise.all(h.waits);
    h.SITES.reads.length = 0;
    const r = await h.call("https://bafyone.crop.test/missing.png"); // upstream (mock fetch) answers 202
    assert.equal(r.status, 202);
    const carryReads = h.SITES.reads.filter((x) => x.includes("carry/")).length;
    assert.ok(carryReads <= 2, `${carryReads} carry-map reads: ${JSON.stringify(h.SITES.reads)}`);
  } finally { h.done(); }
});

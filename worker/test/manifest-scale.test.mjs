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
function r2() {
  const m = new Map(); let n = 0;
  const obj = (key, v) => ({ key, size: v.bytes.length, etag: v.etag, httpEtag: `"${v.etag}"`, body: new Blob([v.bytes]).stream(), json: async () => JSON.parse(new TextDecoder().decode(v.bytes)), text: async () => new TextDecoder().decode(v.bytes) });
  return {
    m,
    async get(k) { const v = m.get(k); return v ? obj(k, v) : null; },
    async head(k) { const v = m.get(k); return v ? obj(k, v) : null; },
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

test("10,000 carried folders of a seeded parent validate fast", async () => {
  const { publicKey, privateKey } = generateKeyPairSync("ed25519");
  const ipns = ipnsName(new Uint8Array(publicKey.export({ format: "der", type: "spki" }).slice(-32)));
  const SITES = r2(), REGISTRY = kv();
  const env = { DOMAIN: "crop.test", SITES, REGISTRY };
  const ctx = { waitUntil: () => {} };
  // seed a parent that holds 10,000 folders x 8 files, all carried from an older version: no 80,000 uploads needed
  const carryMap = {}, carry = [];
  for (let i = 0; i < 10000; i++) { carry.push(`post-${i}`); for (let j = 0; j < 8; j++) carryMap[`post-${i}/f${j}.jpg`] = "bafyold"; }
  await SITES.put("carry/bafyone.json", JSON.stringify(carryMap));
  await SITES.put("sites/bafyone/index.html", "home");
  await SITES.put(`heads/${ipns}`, JSON.stringify({ cid: "bafyone", sequence: 1 }));
  const t = Math.floor(Date.now() / 1000);
  const sig = sign(null, Buffer.from(`croptop-push\ncrop.test\n${ipns}\nbafytwo\n2\n${t}`), privateKey).toString("base64");
  const fd = new FormData();
  fd.append("file:index.html", new Blob(["new"]), "index.html");
  fd.append("manifest", JSON.stringify({ carry }));
  // CPU time, not wall time: a busy machine stretches the wall clock, not the
  // work. Linear validation uses well under a second; quadratic needs over 10 s.
  const cpu = process.cpuUsage();
  const r = await worker.fetch(new Request("https://crop.test/v0/host/push", { method: "POST", headers: { "X-Croptop-Ipns": ipns, "X-Croptop-Cid": "bafytwo", "X-Croptop-Seq": "2", "X-Croptop-Time": String(t), "X-Croptop-Sig": sig, "X-Croptop-Parent": "bafyone" }, body: fd }), env, ctx);
  const used = process.cpuUsage(cpu);
  const ms = (used.user + used.system) / 1000;
  console.log("manifest push of 10,000 folders:", r.status, Math.round(ms) + " ms of CPU");
  assert.equal(r.status, 200, await r.text());
  assert.ok(ms < 3000, `validation used ${ms} ms of CPU`);
});

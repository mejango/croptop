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
      return { objects: keys.slice(at, at + limit).map((key) => ({ key, size: m.get(key).bytes.length })), truncated: at + limit < keys.length, cursor: String(at + limit) };
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

// Exercises the public handler with signed multipart requests while keeping
// all storage and identities local to the test. Array entries preserve aliases
// and duplicate fields, unlike the object shorthand in the older fixtures.
function stagingHarness() {
  const { publicKey, privateKey } = generateKeyPairSync("ed25519");
  const ipns = ipnsName(new Uint8Array(publicKey.export({ format: "der", type: "spki" }).slice(-32)));
  const env = { DOMAIN: "crop.test", SITES: r2(), REGISTRY: kv() };
  const waits = [], ctx = { waitUntil: (promise) => waits.push(promise) };
  const push = (cid, seq, entries, { parent, part, manifest } = {}) => {
    const t = Math.floor(Date.now() / 1000);
    const sig = sign(null, Buffer.from(`croptop-push\ncrop.test\n${ipns}\n${cid}\n${seq}\n${t}`), privateKey).toString("base64");
    const form = new FormData();
    for (const [field, value] of entries) form.append(field, new Blob([value]), "payload");
    if (manifest !== undefined) form.append("manifest", JSON.stringify(manifest));
    const headers = { "X-Croptop-Ipns": ipns, "X-Croptop-Cid": cid, "X-Croptop-Seq": String(seq), "X-Croptop-Time": String(t), "X-Croptop-Sig": sig };
    if (parent) headers["X-Croptop-Parent"] = parent;
    if (part) headers["X-Croptop-Part"] = part;
    return worker.fetch(new Request("https://crop.test/v0/host/push", { method: "POST", headers, body: form }), env, ctx);
  };
  return { env, ipns, push, waits };
}

function stagingGate() {
  let open;
  const promise = new Promise((resolve) => { open = resolve; });
  return { promise, open };
}

const stagingTurn = () => new Promise((resolve) => setImmediate(resolve));

test("staging preserves normalized duplicates, resumed files, and verified blocks", async () => {
  const h = stagingHarness(), cid = "bafystagingsemantics";
  await h.env.SITES.put(`owners/${cid}`, h.ipns);
  await h.env.SITES.put(`sites/${cid}/resumed.txt`, "resume");
  await h.env.SITES.put(`sites/${cid}/resized.txt`, "old");
  const valid = "valid folder block", validCID = blockCID(valid);
  const forgedCID = blockCID("the authentic folder block");
  const existing = "already stored folder block", existingCID = blockCID(existing);
  await h.env.SITES.put(`blocks/${existingCID}`, existing);
  const put = h.env.SITES.put, writes = [];
  h.env.SITES.put = async (key, body, options) => {
    const bytes = new Uint8Array(await new Response(body).arrayBuffer());
    writes.push({ key, text: new TextDecoder().decode(bytes) });
    return put(key, bytes, options);
  };
  const response = await h.push(cid, 1, [
    ["file:/same.txt", "first"], ["file:same.txt", "other"],
    ["file:///changed.txt", "a"], ["file:changed.txt", "longer"],
    ["file:resumed.txt", "XXXXXX"], ["file:resized.txt", "new contents"],
    ["file:../unsafe.txt", "not stored"], ["file:/", "not stored"], ["ignored", "not stored"],
    ["block:" + validCID, valid], ["block:" + forgedCID, "forged"],
    ["block:" + existingCID, "cannot replace an already stored block"], ["block:not-a-cid", "invalid"],
  ]);
  assert.equal(response.status, 200, await response.clone().text());
  assert.equal((await response.json()).files, 6, "valid duplicate and resumed file parts still count; blocks and invalid paths do not");
  const saved = async (rel) => (await h.env.SITES.get(`sites/${cid}/${rel}`))?.text();
  assert.equal(await saved("same.txt"), "first", "same-size normalized duplicate is skipped");
  assert.equal(await saved("changed.txt"), "longer", "different-size normalized duplicate replaces its earlier value");
  assert.equal(await saved("resumed.txt"), "resume", "same-size staged file is reused");
  assert.equal(await saved("resized.txt"), "new contents");
  assert.deepEqual(writes.filter((entry) => entry.key === `sites/${cid}/same.txt`).map((entry) => entry.text), ["first"]);
  assert.deepEqual(writes.filter((entry) => entry.key === `sites/${cid}/changed.txt`).map((entry) => entry.text), ["a", "longer"]);
  assert.equal(writes.filter((entry) => entry.key === `sites/${cid}/resumed.txt`).length, 0);
  assert.equal(await h.env.SITES.get(`sites/${cid}/../unsafe.txt`), null);
  assert.equal(await h.env.SITES.get(`sites/${cid}/`), null);
  assert.equal(await (await h.env.SITES.get(`blocks/${validCID}`)).text(), valid);
  assert.equal(await h.env.SITES.get(`blocks/${forgedCID}`), null, "hash-mismatched folder block is never stored");
  assert.equal(await (await h.env.SITES.get(`blocks/${existingCID}`)).text(), existing);
  assert.equal(await h.env.SITES.get("blocks/not-a-cid"), null);
  assert.equal((await (await h.env.SITES.get(`heads/${h.ipns}`)).json()).cid, cid);
  assert.equal(h.waits.length, 0, "all staging completed in the request, not background tasks");
});

test("many independent files and blocks stage four at a time and finish before commit", { timeout: 10000 }, async () => {
  const h = stagingHarness(), cid = "bafyboundedstaging";
  const entries = [], blocks = new Set();
  const fileCount = 256, blockCount = 256;
  for (let i = 0; i < fileCount; i++) entries.push([`file:post-${i}/index.html`, `page ${i}`]);
  for (let i = 0; i < blockCount; i++) {
    const value = "existing folder block " + i, c = blockCID(value);
    await h.env.SITES.put("blocks/" + c, value);
    blocks.add("blocks/" + c);
    entries.push(["block:" + c, value]);
  }
  const first = stagingGate(), release = stagingGate();
  const head = h.env.SITES.head, put = h.env.SITES.put;
  let active = 0, peak = 0, started = 0, completed = 0, filesPersisted = 0, committed = false;
  h.env.SITES.head = async (key) => {
    if (!key.startsWith(`sites/${cid}/`) && !blocks.has(key)) return head(key);
    started++;
    peak = Math.max(peak, ++active);
    first.open();
    try {
      await release.promise;
      return await head(key);
    } finally {
      active--;
      completed++;
    }
  };
  h.env.SITES.put = async (key, body, options) => {
    if (key === `heads/${h.ipns}`) {
      assert.equal(active, 0, "no existence check remains in flight at commit");
      assert.equal(completed, fileCount + blockCount, "all supplied blocks are checked before commit");
      assert.equal(filesPersisted, fileCount, "all files are persisted before commit");
      committed = true;
    }
    const result = await put(key, body, options);
    if (key.startsWith(`sites/${cid}/`)) filesPersisted++;
    return result;
  };
  const pending = h.push(cid, 1, entries);
  try {
    await first.promise;
    await stagingTurn();
    assert.equal(started, 4, "exactly four independent objects enter the blocked first wave");
    assert.equal(active, 4);
    assert.equal(committed, false);
    release.open();
    const response = await pending;
    assert.equal(response.status, 200, await response.clone().text());
    assert.equal((await response.json()).files, fileCount);
    assert.equal(peak, 4, "concurrency stays bounded across every later wave");
    assert.equal(completed, fileCount + blockCount);
    assert.equal(committed, true);
    assert.equal(h.waits.length, 0);
  } finally {
    release.open();
    await pending;
  }
});

test("normalized duplicate object waits for its previous write while other objects proceed", { timeout: 10000 }, async () => {
  const h = stagingHarness(), cid = "bafyorderedstaging";
  const reached = stagingGate(), release = stagingGate(), independent = stagingGate();
  const head = h.env.SITES.head, put = h.env.SITES.put;
  const ordered = `sites/${cid}/ordered.txt`;
  let orderedHeads = 0, independentWrites = 0, held = false;
  h.env.SITES.head = async (key) => {
    if (key === ordered) orderedHeads++;
    return head(key);
  };
  h.env.SITES.put = async (key, body, options) => {
    if (key === ordered && !held) {
      held = true;
      reached.open();
      await release.promise;
    }
    const result = await put(key, body, options);
    if (key.startsWith(`sites/${cid}/other-`)) { independentWrites++; independent.open(); }
    return result;
  };
  const pending = h.push(cid, 1, [
    ["file:/ordered.txt", "a"], ["file:ordered.txt", "the later complete value"],
    ...Array.from({ length: 12 }, (_, i) => [`file:other-${i}.txt`, "independent"]),
  ]);
  try {
    await reached.promise;
    await independent.promise;
    assert.equal(orderedHeads, 1, "the alias is not inspected before the first value is persisted");
    assert.ok(independentWrites > 0, "independent keys can advance past a blocked object");
    assert.equal(await h.env.SITES.get(`heads/${h.ipns}`), null);
    release.open();
    const response = await pending;
    assert.equal(response.status, 200, await response.clone().text());
    assert.equal(orderedHeads, 2);
    assert.equal(await (await h.env.SITES.get(ordered)).text(), "the later complete value");
    assert.equal((await response.json()).files, 14);
  } finally {
    release.open();
    await pending;
  }
});

test("6,482 files and 680 existing blocks retain exact counts with bounded storage work", { timeout: 30000 }, async () => {
  const h = stagingHarness(), cid = "bafyrealisticstagingcount";
  const fileCount = 6482, blockCount = 680, entries = [], blockKeys = new Set();
  for (let i = 0; i < fileCount; i++) entries.push([`file:post-${i}/index.html`, `tiny page ${i}`]);
  for (let i = 0; i < blockCount; i++) {
    const value = `realistic existing folder ${i}`, c = blockCID(value);
    await h.env.SITES.put("blocks/" + c, value);
    blockKeys.add("blocks/" + c);
    entries.push(["block:" + c, value]);
  }
  const head = h.env.SITES.head, put = h.env.SITES.put;
  let active = 0, peak = 0, checks = 0, filesPersisted = 0, blockWrites = 0, headWrites = 0;
  const storageWork = async (operation) => {
    peak = Math.max(peak, ++active);
    try { await stagingTurn(); return await operation(); } finally { active--; }
  };
  h.env.SITES.head = async (key) => {
    if (!key.startsWith(`sites/${cid}/`) && !blockKeys.has(key)) return head(key);
    const result = await storageWork(() => head(key));
    checks++;
    return result;
  };
  h.env.SITES.put = async (key, body, options) => {
    if (key === `heads/${h.ipns}`) {
      assert.equal(active, 0);
      assert.equal(checks, fileCount + blockCount);
      assert.equal(filesPersisted, fileCount);
      headWrites++;
    }
    if (key.startsWith(`sites/${cid}/`) || blockKeys.has(key)) {
      const result = await storageWork(() => put(key, body, options));
      if (blockKeys.has(key)) blockWrites++; else filesPersisted++;
      return result;
    }
    return put(key, body, options);
  };
  // A large Go publication uses sequential multipart requests; every folder
  // block accompanies the final one. Keep that shape without allocating the
  // live site's media bytes or a huge synthetic Node FormData parse.
  const batchSize = 256, batches = Math.ceil(fileCount / batchSize);
  let acknowledgedFiles = 0;
  for (let i = 0; i < batches; i++) {
    const files = entries.slice(i * batchSize, Math.min((i + 1) * batchSize, fileCount));
    const body = i === batches - 1 ? [...files, ...entries.slice(fileCount)] : files;
    const response = await h.push(cid, 1, body, { part: `${i + 1}/${batches}` });
    assert.equal(response.status, 200, await response.clone().text());
    const reported = (await response.json()).files;
    assert.equal(reported, files.length);
    acknowledgedFiles += reported;
    assert.equal(headWrites, i === batches - 1 ? 1 : 0, "only the final multipart request commits");
  }
  assert.equal(acknowledgedFiles, fileCount);
  assert.equal(peak, 4);
  assert.equal(active, 0);
  assert.equal(checks, fileCount + blockCount);
  assert.equal(filesPersisted, fileCount);
  assert.equal(blockWrites, 0, "previously stored blocks are not rewritten");
  assert.equal(headWrites, 1);
  let cursor, storedFiles = 0;
  do {
    const page = await h.env.SITES.list({ prefix: `sites/${cid}/`, cursor });
    storedFiles += page.objects.length;
    cursor = page.truncated ? page.cursor : undefined;
  } while (cursor);
  assert.equal(storedFiles, fileCount);
  assert.equal(await (await h.env.SITES.get(`sites/${cid}/post-${fileCount - 1}/index.html`)).text(), `tiny page ${fileCount - 1}`);
  assert.equal(h.waits.length, 0);
});

test("synthetic stored-block latency measurement preserves the four-operation bound", { timeout: 10000 }, async () => {
  const h = stagingHarness(), cid = "bafysyntheticstaginglatency";
  const entries = [["file:index.html", "small change"]], keys = new Set();
  for (let i = 0; i < 128; i++) {
    const value = "existing block " + i, c = blockCID(value);
    await h.env.SITES.put("blocks/" + c, value);
    keys.add("blocks/" + c);
    entries.push(["block:" + c, value]);
  }
  const head = h.env.SITES.head;
  let headCalls = 0, active = 0, maxConcurrentHeads = 0;
  h.env.SITES.head = async (key) => {
    if (!keys.has(key) && key !== `sites/${cid}/index.html`) return head(key);
    headCalls++;
    maxConcurrentHeads = Math.max(maxConcurrentHeads, ++active);
    try {
      await new Promise((resolve) => setTimeout(resolve, 5));
      return await head(key);
    } finally {
      active--;
    }
  };
  const start = performance.now();
  const response = await h.push(cid, 1, entries);
  const elapsedMs = Math.round(performance.now() - start);
  console.log("synthetic staging latency", JSON.stringify({ status: response.status, existingBlocks: 128, changedFiles: 1, simulatedHeadDelayMs: 5, headCalls, maxConcurrentHeads, elapsedMs }));
  assert.equal(response.status, 200, await response.clone().text());
  assert.equal(headCalls, 129);
  assert.equal(maxConcurrentHeads, 4);
  assert.equal(active, 0);
  // Wall time is diagnostic only: scheduling and machine load vary. The
  // deterministic bound and completion assertions, not timing, gate CI.
});

test("staging failure stops admission, drains active objects without commit, and resumes safely", { timeout: 10000 }, async () => {
  const h = stagingHarness(), cid = "bafyfailedstaging";
  const reached = stagingGate(), fail = stagingGate(), drain = stagingGate(), failed = stagingGate();
  const head = h.env.SITES.head, put = h.env.SITES.put;
  const firstKey = `sites/${cid}/0.txt`, duplicateKey = `sites/${cid}/1.txt`;
  const entries = [
    ["file:0.txt", "zero"], ["file:/1.txt", "one"], ["file:1.txt", "one expanded"],
    ...Array.from({ length: 6 }, (_, i) => [`file:${i + 2}.txt`, `value ${i + 2}`]),
  ];
  const starts = [], writes = [];
  let active = 0, settled = false, headMoves = 0;
  h.env.SITES.head = async (key) => {
    if (!key.startsWith(`sites/${cid}/`)) return head(key);
    starts.push(key);
    active++;
    reached.open();
    try {
      if (key === firstKey) {
        await fail.promise;
        failed.open();
        throw new Error("injected staging read failure");
      }
      await drain.promise;
      return await head(key);
    } finally {
      active--;
    }
  };
  h.env.SITES.put = async (key, body, options) => {
    if (key === `heads/${h.ipns}`) headMoves++;
    const result = await put(key, body, options);
    if (key.startsWith(`sites/${cid}/`)) writes.push(key);
    return result;
  };
  const pending = h.push(cid, 1, entries).then((response) => { settled = true; return response; });
  try {
    await reached.promise;
    await stagingTurn();
    assert.equal(starts.length, 4);
    fail.open();
    await failed.promise;
    await stagingTurn();
    assert.equal(active, 3);
    assert.equal(settled, false, "failure response waits for already admitted storage work");
    assert.equal(starts.length, 4, "failure does not admit a fifth object");
    drain.open();
    const response = await pending;
    assert.equal(response.status, 500);
    assert.match(await response.text(), /injected staging read failure/);
    assert.equal(active, 0);
    assert.equal(headMoves, 0);
    assert.equal(starts.length, 4, "no later object or duplicate value starts during drain");
    assert.equal(starts.filter((key) => key === duplicateKey).length, 1);
    assert.equal(writes.length, 3, "already admitted complete objects are retained for resume");
    assert.equal(await h.env.SITES.get(`heads/${h.ipns}`), null);
    assert.equal(await h.env.SITES.get(`carry/${cid}.json`), null);
    assert.equal(await h.env.REGISTRY.get("key:" + h.ipns), null);
    assert.equal(h.waits.length, 0);

    // Recover on the same signed version. Persisted same-size objects are not
    // written again; the later larger alias advances only after its predecessor.
    h.env.SITES.head = head;
    const before = writes.length;
    const resumed = await h.push(cid, 1, entries);
    assert.equal(resumed.status, 200, await resumed.clone().text());
    assert.equal((await resumed.json()).files, entries.length);
    const resumedWrites = writes.slice(before);
    assert.equal(resumedWrites.includes(`sites/${cid}/2.txt`), false);
    assert.equal(resumedWrites.includes(`sites/${cid}/3.txt`), false);
    assert.equal(resumedWrites.filter((key) => key === duplicateKey).length, 1);
    assert.equal(await (await h.env.SITES.get(duplicateKey)).text(), "one expanded");
    assert.equal(headMoves, 1);
  } finally {
    fail.open();
    drain.open();
    await pending;
    h.env.SITES.head = head;
  }
});

test("parallel staging cannot move a parent head changed while files were in flight", { timeout: 10000 }, async () => {
  const h = stagingHarness(), parent = "bafystagingparent", cid = "bafystagingcandidate", other = "bafystagingother";
  await h.env.SITES.put(`heads/${h.ipns}`, JSON.stringify({ cid: parent, sequence: 1 }));
  await h.env.REGISTRY.put("key:" + h.ipns, JSON.stringify({ ipns: h.ipns, cid: parent, sequence: 1 }));
  const reached = stagingGate(), release = stagingGate();
  const head = h.env.SITES.head;
  let active = 0;
  h.env.SITES.head = async (key) => {
    if (!key.startsWith(`sites/${cid}/`)) return head(key);
    active++;
    reached.open();
    try { await release.promise; return await head(key); } finally { active--; }
  };
  const pending = h.push(cid, 20, Array.from({ length: 8 }, (_, i) => [`file:${i}.txt`, "staged"]), { parent, manifest: { carry: [] } });
  try {
    await reached.promise;
    await stagingTurn();
    assert.equal(active, 4);
    await h.env.SITES.put(`heads/${h.ipns}`, JSON.stringify({ cid: other, sequence: 2 }));
    release.open();
    const response = await pending;
    assert.equal(response.status, 409, await response.clone().text());
    assert.deepEqual(await (await h.env.SITES.get(`heads/${h.ipns}`)).json(), { cid: other, sequence: 2 });
    assert.equal(active, 0);
    assert.equal((await h.env.SITES.list({ prefix: `sites/${cid}/` })).objects.length, 8, "refused version remains resumable without replacing the newer head");
    assert.equal(await h.env.REGISTRY.get("pushed:" + cid), null);
  } finally {
    release.open();
    await pending;
  }
});

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

    // a whole version at the sequence the host holds, with other content,
    // would replace it unseen (two machines chose the same next sequence):
    // refused; the next sequence is not
    env.SITES.put = put;
    const head = JSON.parse((await body(`https://crop.test/v0/host/keys/${ipns}`))[1]);
    const twin = await push("bafytwin", head.sequence, { "e.html": "e" });
    assert.equal(twin.status, 409);
    assert.equal(await twin.text(), `host holds ${head.cid} at sequence ${head.sequence}`);
    assert.equal((await push("bafynext", head.sequence + 1, { "f.html": "f" })).status, 200);

    // a whole version whose head write comes after an agent's post landed
    // (its upload ran while the post was made) checks again, and does not
    // overwrite the post at the same sequence
    const at = JSON.parse((await body(`https://crop.test/v0/host/keys/${ipns}`))[1]);
    let release, reached;
    const gate = new Promise((go) => { release = go; });
    const atHead = new Promise((go) => { reached = go; });
    let holdNext = true;
    env.SITES.put = async (k, b, o) => {
      if (k.startsWith("heads/") && holdNext) { holdNext = false; reached(); await gate; }
      return put(k, b, o);
    };
    const laptop = push("bafylaptop", at.sequence + 1, { "g.html": "g" });
    await atHead;
    const agent = await push("bafyagent", at.sequence + 1, { "h.html": "h" }, at.cid);
    assert.equal(agent.status, 200);
    release();
    const late = await laptop;
    assert.equal(late.status, 409);
    assert.equal(await late.text(), `host holds bafyagent at sequence ${at.sequence + 1}`);
    env.SITES.put = put;
    assert.equal(JSON.parse((await body(`https://crop.test/v0/host/keys/${ipns}`))[1]).cid, "bafyagent");
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

test("routing PUT over 10240 bytes answers 413", async () => {
  const env = { DOMAIN: "crop.test", NODE: "https://node.test", SITES: r2(), REGISTRY: kv() };
  const calls = [];
  const realFetch = globalThis.fetch;
  globalThis.fetch = async (u, init = {}) => { calls.push({ u: String(u) }); return new Response(null); };
  try {
    const large = new Uint8Array(10241);
    const r = await worker.fetch(new Request("https://crop.test/routing/v1/ipns/k51qzi5uqu5dhxiwvl4xx3sco13y50yoo28rbhe3sneirnj2qatinx75qpsqtb", { method: "PUT", body: large }), env, { waitUntil() {} });
    assert.equal(r.status, 413);
    assert.equal(calls.length, 0, "node is never called for oversized PUT");
  } finally {
    globalThis.fetch = realFetch;
  }
});

test("bad registry record falls through to node", async () => {
  const env = { DOMAIN: "crop.test", NODE: "https://node.test", SITES: r2(), REGISTRY: kv() };
  const realFetch = globalThis.fetch;
  globalThis.fetch = async (u, init = {}) => {
    if (u.startsWith("https://node.test/routing/v1/ipns/")) return new Response(recordBytes("/ipfs/bafyfromnode", 3), { headers: { "content-type": "application/vnd.ipfs.ipns-record" } });
    return new Response("nope", { status: 404 });
  };
  try {
    const bad = "k51qzi5uqu5dhxiwvl4xx3sco13y50yoo28rbhe3sneirnj2qatinx75qpsqtb";
    await env.REGISTRY.put("key:" + bad, JSON.stringify({ ipns: bad, cid: "bafyx", sequence: 1, record: "%%%" }));
    const r = await worker.fetch(new Request("https://crop.test/routing/v1/ipns/" + bad), env, { waitUntil() {} });
    assert.equal(r.status, 200);
    assert.equal(r.headers.get("content-type"), "application/vnd.ipfs.ipns-record");
  } finally {
    globalThis.fetch = realFetch;
  }
});

test("republish with bad record resolves without throwing", async () => {
  const puts = [];
  const realFetch = globalThis.fetch;
  globalThis.fetch = async (u, init = {}) => { puts.push(String(u)); return new Response(null); };
  try {
    await republish({ NODE: "https://node.test" }, "k51qzi5uqu5dhxiwvl4xx3sco13y50yoo28rbhe3sneirnj2qatinx75qpsqtb", "%%%");
    assert.equal(puts.length, 0, "no fetch when record is malformed");
  } finally {
    globalThis.fetch = realFetch;
  }
});

test("routing passes through node 429 with retry-after", async () => {
  const env = { DOMAIN: "crop.test", NODE: "https://node.test", SITES: r2(), REGISTRY: kv() };
  const realFetch = globalThis.fetch;
  globalThis.fetch = async (u, init = {}) => {
    if (u.startsWith("https://node.test/routing/v1/ipns/")) return new Response("too many requests", { status: 429, headers: { "retry-after": "10" } });
    return new Response("nope", { status: 404 });
  };
  try {
    const r = await worker.fetch(new Request("https://crop.test/routing/v1/ipns/k51qzi5uqu5dhxiwvl4xx3sco13y50yoo28rbhe3sneirnj2qatinx75qpsqtb"), env, { waitUntil() {} });
    assert.equal(r.status, 429);
    assert.equal(r.headers.get("retry-after"), "10");
  } finally {
    globalThis.fetch = realFetch;
  }
});

test("a manifest push keeps only what it uploads and carries", async () => {
  const { publicKey, privateKey } = generateKeyPairSync("ed25519");
  const ipns = ipnsName(new Uint8Array(publicKey.export({ format: "der", type: "spki" }).slice(-32)));
  const env = { DOMAIN: "crop.test", NODE: "https://node.test", SITES: r2(), REGISTRY: kv() };
  const waits = [], ctx = { waitUntil: (p) => waits.push(p) };
  const pulls = [];
  const realFetch = globalThis.fetch;
  globalThis.fetch = async (u, init) => { if (String(u).endsWith("/v0/host/pull")) pulls.push(JSON.parse(init.body)); return new Response("{}", { status: 202 }); };
  const call = (url, init) => worker.fetch(new Request(url, init), env, ctx);
  const push = (cid, seq, files, parent, manifest, part) => {
    const t = Math.floor(Date.now() / 1000);
    const sig = sign(null, Buffer.from(`croptop-push\ncrop.test\n${ipns}\n${cid}\n${seq}\n${t}`), privateKey).toString("base64");
    const fd = new FormData();
    for (const [rel, body] of Object.entries(files)) fd.append("file:" + rel, new Blob([body]), rel.split("/").pop());
    if (manifest) fd.append("manifest", JSON.stringify(manifest));
    const headers = { "X-Croptop-Ipns": ipns, "X-Croptop-Cid": cid, "X-Croptop-Seq": String(seq), "X-Croptop-Time": String(t), "X-Croptop-Sig": sig };
    if (parent) headers["X-Croptop-Parent"] = parent;
    if (part) headers["X-Croptop-Part"] = part;
    return call("https://crop.test/v0/host/push", { method: "POST", headers, body: fd });
  };
  const body = async (url) => { const r = await call(url); return [r.status, await r.text()]; };
  try {
    assert.equal((await push("bafyone", 1, { "index.html": "home", "assets/a.css": "css", "p1/index.html": "one", "p1/photo.jpg": "photo", "p2/index.html": "two" })).status, 200);
    const r = await push("bafytwo", 2, { "index.html": "home 2", "p3/index.html": "three" }, "bafyone", { carry: ["assets", "p1"] });
    assert.equal(r.status, 200, await r.text());
    await Promise.all(waits);
    assert.deepEqual(await body("https://bafytwo.crop.test/index.html"), [200, "home 2"]);
    assert.deepEqual(await body("https://bafytwo.crop.test/assets/a.css"), [200, "css"]);
    assert.deepEqual(await body("https://bafytwo.crop.test/p1/photo.jpg"), [200, "photo"]);
    assert.deepEqual(await body("https://bafytwo.crop.test/p3/index.html"), [200, "three"]);
    assert.equal((await call("https://bafytwo.crop.test/p2/index.html")).status, 404, "neither uploaded nor carried: gone");
    assert.deepEqual(pulls.at(-1).files.sort(), ["assets/a.css", "index.html", "p1/index.html", "p1/photo.jpg", "p3/index.html"]);
    const [, entry] = await body(`https://crop.test/v0/host/keys/${ipns}`);
    assert.equal(JSON.parse(entry).acceptsManifest, true);
    // each bad manifest gets its own fake cid: files stored by a refused push stay under it
    for (const [cid, files, parent, manifest] of [
      ["bafythreea", { "index.html": "3" }, "bafytwo", { carry: ["nope"] }],
      ["bafythreeb", { "p1/x.txt": "x" }, "bafytwo", { carry: ["p1"] }],
      ["bafythreec", { "index.html": "3" }, "bafytwo", { carry: ["p1", "p1/photo.jpg"] }],
      ["bafythreed", { "index.html": "3" }, "", { carry: [] }],
    ]) {
      const res = await push(cid, 3, files, parent, manifest);
      assert.equal(res.status, 400, `${JSON.stringify(manifest)}: ${await res.text()}`);
    }
    // part 1 of 2 stays stored, uncommitted; the listing shows it
    assert.equal((await push("bafyfour", 4, { "a.txt": "aaaa" }, "", null, "1/2")).status, 200);
    assert.deepEqual(JSON.parse((await body("https://crop.test/v0/host/versions/bafyfour/files"))[1]), [{ path: "a.txt", size: 4 }]);
    assert.equal((await call("https://crop.test/v0/host/versions/not-a-cid/files")).status, 400);
  } finally {
    globalThis.fetch = realFetch;
  }
});

test("crop.top/agents.md is the instructions to hand a bot", async () => {
  const env = { DOMAIN: "crop.test", SITES: r2(), REGISTRY: kv() };
  for (const p of ["/agents.md", "/agents"]) {
    const r = await worker.fetch(new Request("https://crop.test" + p), env, { waitUntil() {} });
    assert.equal(r.status, 200);
    assert.match(r.headers.get("content-type"), /text\/markdown/);
    assert.match(await r.text(), /croptop post --key/);
  }
});

test("exact-file and folder carry work correctly", async () => {
  const { publicKey, privateKey } = generateKeyPairSync("ed25519");
  const ipns = ipnsName(new Uint8Array(publicKey.export({ format: "der", type: "spki" }).slice(-32)));
  const env = { DOMAIN: "crop.test", NODE: "https://node.test", SITES: r2(), REGISTRY: kv() };
  const waits = [], ctx = { waitUntil: (p) => waits.push(p) };
  const realFetch = globalThis.fetch;
  globalThis.fetch = async (u, init) => new Response("{}", { status: 202 });
  const call = (url, init) => worker.fetch(new Request(url, init), env, ctx);
  const push = (cid, seq, files, parent, manifest) => {
    const t = Math.floor(Date.now() / 1000);
    const sig = sign(null, Buffer.from(`croptop-push\ncrop.test\n${ipns}\n${cid}\n${seq}\n${t}`), privateKey).toString("base64");
    const fd = new FormData();
    for (const [rel, body] of Object.entries(files)) fd.append("file:" + rel, new Blob([body]), rel.split("/").pop());
    if (manifest) fd.append("manifest", JSON.stringify(manifest));
    const headers = { "X-Croptop-Ipns": ipns, "X-Croptop-Cid": cid, "X-Croptop-Seq": String(seq), "X-Croptop-Time": String(t), "X-Croptop-Sig": sig };
    if (parent) headers["X-Croptop-Parent"] = parent;
    return call("https://crop.test/v0/host/push", { method: "POST", headers, body: fd });
  };
  const get = async (url) => { const r = await call(url); return [r.status, await r.text()]; };
  try {
    const V1 = { "index.html": "h1", "foo/a.txt": "fa", "bar/b.txt": "bb", "assets/a.css": "css" };
    assert.equal((await push("bafyone", 1, V1)).status, 200);
    let r = await push("bafytwo", 2, { "index.html": "h2" }, "bafyone", { carry: ["foo", "assets/a.css"] });
    assert.equal(r.status, 200, await r.text());
    await Promise.all(waits);
    assert.deepEqual(await get("https://bafytwo.crop.test/foo/a.txt"), [200, "fa"]);
    assert.deepEqual(await get("https://bafytwo.crop.test/assets/a.css"), [200, "css"]);
    assert.equal((await get("https://bafytwo.crop.test/bar/b.txt"))[0], 404, "bar not carried");
  } finally {
    globalThis.fetch = realFetch;
  }
});

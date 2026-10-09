import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import worker from "../src/mobile-only.js";

const APP = "https://phone-pilot.croptop.workers.dev";
const NODE = "https://private-mobile-node.example";
const SECRET = "test-only-mobile-proxy-secret-not-a-production-credential";
const BINDINGS = ["MOBILE_API_LIMITER", "MOBILE_AUTH_LIMITER", "MOBILE_PAIRING_LIMITER", "MOBILE_UPLOAD_LIMITER"];
function environment(overrides = {}) {
  const env = { DOMAIN: "crop.test", MOBILE_ORIGIN: APP, MOBILE_NODE: NODE, MOBILE_ENABLED: "true", MOBILE_PROXY_SECRET: SECRET };
  for (const name of BINDINGS) env[name] = { calls: [], async limit(options) { this.calls.push(options); return { success: true }; } };
  return Object.assign(env, overrides);
}
function call(path = "/", init = {}, env = environment(), origin = APP) {
  return worker.fetch(new Request(origin + path, { ...init, headers: { "CF-Connecting-IP": "203.0.113.4", ...init.headers } }), env);
}
function secure(result) {
  assert.equal(result.headers.get("cache-control"), "no-store");
  assert.equal(result.headers.get("x-content-type-options"), "nosniff");
  assert.equal(result.headers.get("referrer-policy"), "no-referrer");
  assert.equal(result.headers.get("set-cookie"), null);
  assert.equal(result.headers.get("access-control-allow-origin"), null);
  assert.ok(result.headers.get("content-security-policy").includes("frame-ancestors 'none'"));
}

test("dedicated entrypoint serves only its exact configured HTTPS origin", async (t) => {
  t.mock.method(globalThis, "fetch", () => assert.fail("static or rejected requests must not reach any upstream"));
  assert.equal((await call()).status, 200);
  for (const origin of ["https://crop.test", "https://app.crop.test", "https://author.crop.test", "https://another.croptop.workers.dev", "https://old-phone.croptop.workers.dev", "http://phone-pilot.croptop.workers.dev", APP + ":444"]) {
    for (const path of ["/", "/v0/mobile/config", "/ipfs/bafyattacker/", "/v0/host/push"]) {
      const result = await call(path, {}, environment(), origin);
      assert.equal(result.status, 404, origin + path);
      secure(result);
    }
  }
});

test("dedicated origin never serves unknown APIs, author data or source files", async (t) => {
  t.mock.method(globalThis, "fetch", () => assert.fail("unknown paths cannot reach the backend"));
  for (const path of ["/ipfs/bafytest", "/ipns/k51test", "/planet.json", "/v0/host/push", "/v0/mobile/future", "/v0/mobile/operations/uuid/delete", "/v0/mobile/pairings/id/remove", "/v0/mobile/pairings/id/extra/claim", "/v0/mobile/operations/" + "A".repeat(129), "/src/mobile-only.js", "/test/mobile-only.test.mjs", "/wrangler.mobile.toml"]) {
    const result = await call(path);
    assert.equal(result.status, 404, path);
    secure(result);
  }
});

test("missing origin, proxy secret or any rate binding fails closed", async (t) => {
  t.mock.method(globalThis, "fetch", () => assert.fail("incomplete production controls cannot reach the backend"));
  const overrides = [
    { MOBILE_ORIGIN: undefined }, { MOBILE_ORIGIN: "https://app.crop.test" }, { MOBILE_ORIGIN: APP + "/" },
    { DOMAIN: undefined }, { MOBILE_PROXY_SECRET: undefined }, { MOBILE_PROXY_SECRET: "short" },
    { MOBILE_PROXY_SECRET: SECRET + "\n" }, { MOBILE_PROXY_SECRET: "A".repeat(257) },
    ...BINDINGS.flatMap((name) => [{ [name]: undefined }, { [name]: {} }]),
  ];
  for (const override of overrides) {
    for (const path of ["/", "/app.js", "/v0/mobile/config"]) {
      const result = await call(path, {}, environment(override));
      assert.equal(result.status, 503, JSON.stringify(override) + path);
      secure(result);
    }
  }
});

test("disable switch reserves the whole origin without requiring secret or bindings", async (t) => {
  t.mock.method(globalThis, "fetch", () => assert.fail("disabled Worker cannot reach the backend"));
  const env = environment({ MOBILE_ENABLED: "false", MOBILE_PROXY_SECRET: undefined, MOBILE_API_LIMITER: undefined });
  for (const path of ["/", "/v0/mobile/config", "/ipfs/bafyattacker/", "/unknown"]) {
    const result = await call(path, {}, env);
    assert.equal(result.status, 503);
    assert.equal((await result.json()).code, "mobile_disabled");
    secure(result);
  }
});

test("only implemented method/path pairs proxy, including capability-only pairing", async (t) => {
  const routes = [
    ["health", "GET", null],
    ["config", "GET", null], ["site", "GET", null], ["challenge", "POST", "MOBILE_AUTH_LIMITER"],
    ["session", "POST", "MOBILE_AUTH_LIMITER"], ["connection", "PUT", null],
    ["operations", "POST", "MOBILE_UPLOAD_LIMITER"], ["operations/POST-ID", "GET", null],
    ["operations/POST-ID/image", "GET", null], ["operations/POST-ID/prepare", "POST", "MOBILE_UPLOAD_LIMITER"],
    ["operations/POST-ID/commit", "POST", "MOBILE_UPLOAD_LIMITER"], ["pairings", "POST", "MOBILE_PAIRING_LIMITER"],
    ["pairings/pairing-id", "GET", "MOBILE_PAIRING_LIMITER"], ["pairings/pairing-id/claim", "POST", "MOBILE_PAIRING_LIMITER"],
    ["pairings/pairing-id/consume", "POST", "MOBILE_PAIRING_LIMITER"], ["pairings/pairing-id/complete", "POST", "MOBILE_PAIRING_LIMITER"],
  ];
  const requests = [];
  t.mock.method(globalThis, "fetch", async (url, init) => { requests.push({ url, init }); return init.method === "OPTIONS" ? new Response(null, { status: 204 }) : Response.json({}); });
  for (const [path, method, limiter] of routes) {
    for (const attempt of ["GET", "HEAD", "POST", "PUT", "DELETE", "OPTIONS"]) {
      requests.length = 0;
      const env = environment();
      const result = await call("/v0/mobile/" + path, { method: attempt, headers: { "X-Croptop-Pairing": "receiver-capability" } }, env);
      const allowed = attempt === method || (path === "health" && attempt === "HEAD") || attempt === "OPTIONS";
      assert.equal(result.status, allowed ? attempt === "OPTIONS" ? 204 : 200 : 405, path + " " + attempt);
      assert.equal(requests.length, allowed ? 1 : 0, path + " " + attempt);
      for (const name of BINDINGS) assert.equal(env[name].calls.length, allowed && (name === "MOBILE_API_LIMITER" || name === limiter) ? 1 : 0, name + " " + path + " " + attempt);
      if (allowed) {
        assert.equal(requests[0].init.headers.get("x-croptop-mobile-proxy"), SECRET);
        assert.equal(requests[0].init.headers.get("authorization"), null);
        assert.equal(requests[0].init.headers.get("x-croptop-pairing"), "receiver-capability");
        assert.equal(requests[0].url, NODE + "/v0/mobile/" + path);
      }
      secure(result);
    }
  }
});

test("caller proxy credentials and forwarding headers are ignored and never reflected", async (t) => {
  t.mock.method(globalThis, "fetch", async (url, init) => {
    assert.equal(url, NODE + "/v0/mobile/site");
    assert.deepEqual([...init.headers.entries()], [["authorization", "Bearer native-token"], ["x-croptop-mobile-proxy", SECRET]]);
    return Response.json({ ready: true }, { headers: { "X-Croptop-Mobile-Proxy": SECRET, "X-Upstream-Secret": SECRET } });
  });
  const result = await call("/v0/mobile/site", { headers: {
    Authorization: "Bearer native-token", "X-Croptop-Mobile-Proxy": "forged-secret", "X-Forwarded-For": "1.1.1.1",
    "X-Real-IP": "1.1.1.2", "X-Forwarded-Host": "evil.example", Cookie: "forged=cookie",
  } });
  assert.equal(result.status, 200);
  assert.equal(result.headers.get("x-croptop-mobile-proxy"), null);
  assert.equal(result.headers.get("x-upstream-secret"), null);
  assert.ok(!(await result.text()).includes(SECRET));
});

test("changing route IDs, credentials, queries or forwarding headers cannot reset rate buckets", async (t) => {
  t.mock.method(globalThis, "fetch", async () => Response.json({}));
  const env = environment();
  for (const [path, method, token] of [["challenge", "POST", "first"], ["session?nonce=changed", "POST", "second"], ["pairings/changed/consume", "POST", "third"], ["operations/other?nonce=other", "GET", "fourth"]]) {
    assert.equal((await call("/v0/mobile/" + path, { method, headers: { Authorization: "Bearer " + token, "X-Croptop-Pairing": token, "X-Forwarded-For": token, "X-Real-IP": token } }, env)).status, 200);
  }
  const keys = BINDINGS.flatMap((name) => env[name].calls.map((call) => call.key));
  assert.equal(new Set(keys).size, 1);
  assert.match(keys[0], /^[a-f0-9]{64}$/);
  assert.ok(!keys[0].includes("203.0.113.4"));
  assert.equal(env.MOBILE_AUTH_LIMITER.calls.length, 2);
  assert.equal((await call("/v0/mobile/config", { headers: { "CF-Connecting-IP": "203.0.113.5" } }, env)).status, 200);
  assert.notEqual(env.MOBILE_API_LIMITER.calls.at(-1).key, keys[0]);
});

test("exhausted limits reject before upstream with retry guidance and no cache", async (t) => {
  t.mock.method(globalThis, "fetch", () => assert.fail("rate-limited requests cannot reach backend"));
  for (const [name, path, method] of [["MOBILE_API_LIMITER", "config", "GET"], ["MOBILE_AUTH_LIMITER", "session", "POST"], ["MOBILE_PAIRING_LIMITER", "pairings/id/consume", "POST"], ["MOBILE_UPLOAD_LIMITER", "operations", "POST"]]) {
    const env = environment({ [name]: { async limit() { return { success: false }; } } });
    const result = await call("/v0/mobile/" + path, { method }, env);
    assert.equal(result.status, 429);
    assert.equal(result.headers.get("retry-after"), "60");
    assert.equal((await result.json()).code, "rate_limited");
    secure(result);
  }
});

test("Worker subrequests cannot evade limits by changing their source header or IP", async (t) => {
  t.mock.method(globalThis, "fetch", async () => Response.json({}));
  const env = environment();
  await call("/v0/mobile/config", {}, env);
  const phoneKey = env.MOBILE_API_LIMITER.calls[0].key;
  for (const headers of [
    { "CF-Worker": "first.example", "CF-Connecting-IP": "203.0.113.5" },
    { "CF-Worker": "second.example", "CF-Connecting-IP": "203.0.113.6" },
    { "CF-Worker": "", "CF-Connecting-IP": "2a06:98c0:3600::103" },
  ]) assert.equal((await call("/v0/mobile/config", { headers }, env)).status, 200);
  const workerKeys = env.MOBILE_API_LIMITER.calls.slice(1).map((entry) => entry.key);
  assert.equal(new Set(workerKeys).size, 1);
  assert.notEqual(workerKeys[0], phoneKey);
});

test("limiter failures and missing platform IP fail closed without secret logging", async (t) => {
  for (const name of ["log", "warn", "error"]) t.mock.method(console, name, () => assert.fail("credentials must never be logged"));
  t.mock.method(globalThis, "fetch", () => assert.fail("broken limits cannot reach backend"));
  for (const invalid of [undefined, {}, { success: "yes" }]) {
    const result = await call("/v0/mobile/config", {}, environment({ MOBILE_API_LIMITER: { async limit() { return invalid; } } }));
    assert.equal(result.status, 503);
    secure(result);
  }
  const exception = await call("/v0/mobile/config", {}, environment({ MOBILE_API_LIMITER: { async limit() { throw new Error(SECRET + " private-client-token private-pairing-code"); } } }));
  assert.equal(exception.status, 503);
  assert.ok(!(await exception.text()).includes("private-"));
  for (const ip of ["", "garbage", "1".repeat(46)]) {
    const result = await call("/v0/mobile/config", { headers: { "CF-Connecting-IP": ip } });
    assert.equal(result.status, 503);
  }
});

test("cross-origin requests cannot consume limits or forward credentials", async (t) => {
  t.mock.method(globalThis, "fetch", () => assert.fail("cross-origin requests must stop at edge"));
  for (const origin of ["https://author.crop.test", "null", APP + ".attacker.example"]) {
    const env = environment();
    const result = await call("/v0/mobile/challenge", { method: "POST", headers: { Origin: origin } }, env);
    assert.equal(result.status, 403);
    assert.equal(env.MOBILE_API_LIMITER.calls.length, 0);
  }
});

test("dedicated configuration has no author storage, gateway routes or cron", async () => {
  const config = await readFile(new URL("../wrangler.mobile.toml", import.meta.url), "utf8");
  assert.match(config, /^main = "src\/mobile-only.js"$/m);
  assert.match(config, /^workers_dev = true$/m);
  assert.match(config, /^preview_urls = false$/m);
  assert.doesNotMatch(config, /^\s*(?:routes|crons|MOBILE_PROXY_SECRET)\s*=/m);
  assert.doesNotMatch(config, /^\s*\[+(?:r2_buckets|kv_namespaces|triggers)\]+/m);
  for (const name of BINDINGS) assert.ok(config.includes('name = "' + name + '"'));
  const source = await readFile(new URL("../src/mobile-only.js", import.meta.url), "utf8");
  assert.doesNotMatch(source, /(?:\.\/index\.js|REGISTRY|SITES|scheduled\()/);
});

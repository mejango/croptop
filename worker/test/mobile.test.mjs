import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import worker from "../src/index.js";

const APP = "https://app.crop.test";
const NODE = "https://node.crop.test";
function environment(overrides = {}) {
  return {
    DOMAIN: "crop.test",
    NODE,
    MOBILE_ENABLED: "true",
    REGISTRY: { get() { throw new Error("The app must never resolve an author's site"); } },
    SITES: { get() { throw new Error("The app must never serve an author's files"); } },
    ...overrides,
  };
}
function call(path = "/", init, env = environment()) {
  return worker.fetch(new Request(APP + path, init), env, { waitUntil() {} });
}
function secure(response) {
  assert.equal(response.headers.get("cache-control"), "no-store");
  assert.equal(response.headers.get("referrer-policy"), "no-referrer");
  assert.equal(response.headers.get("x-content-type-options"), "nosniff");
  assert.equal(response.headers.get("cross-origin-resource-policy"), "same-origin");
  const csp = response.headers.get("content-security-policy");
  for (const rule of ["default-src 'self'", "script-src 'self'", "style-src 'self'", "img-src 'self' blob:", "connect-src 'self'", "object-src 'none'", "base-uri 'none'", "frame-ancestors 'none'"]) {
    assert.ok(csp.includes(rule), rule);
  }
  assert.ok(!csp.includes("unsafe-inline"));
  assert.ok(!csp.includes("unsafe-eval"));
  assert.equal(response.headers.get("access-control-allow-origin"), null);
  assert.equal(response.headers.get("set-cookie"), null);
}

test("app origin serves only the trusted composer, with strict security headers", async (t) => {
  t.mock.method(globalThis, "fetch", () => assert.fail("app assets cannot come from an upstream"));
  const result = await call("/");
  assert.equal(result.status, 200);
  assert.equal(await result.text(), await readFile(new URL("../../web/mobile/index.html", import.meta.url), "utf8"));
  secure(result);
  const head = await call("/", { method: "HEAD" });
  assert.equal(head.status, 200);
  assert.equal(await head.text(), "");
  const post = await call("/app.js", { method: "POST", body: "untrusted" });
  assert.equal(post.status, 405);
  secure(post);
});

test("disabled app origin remains reserved and never resolves public content", async (t) => {
  t.mock.method(globalThis, "fetch", () => assert.fail("disabled app cannot reach upstreams"));
  for (const value of [undefined, "false", "TRUE", true]) {
    for (const path of ["/", "/app.js", "/ipfs/bafyattacker/", "/v0/mobile/config"]) {
      const result = await call(path, undefined, environment({ MOBILE_ENABLED: value }));
      assert.equal(result.status, 503);
      assert.equal((await result.json()).code, "mobile_disabled");
      secure(result);
    }
  }
});

test("an explicitly configured fresh origin works outside the public gateway domain", async (t) => {
  const origin = "https://fresh-phone.example";
  const env = environment({ MOBILE_ORIGIN: origin });
  const requests = [];
  t.mock.method(globalThis, "fetch", async (url, init) => {
    requests.push(url);
    assert.equal(init.headers.get("origin"), origin);
    return Response.json({ origin });
  });
  const page = await worker.fetch(new Request(origin + "/"), env, {});
  assert.equal(page.status, 200);
  assert.match(await page.text(), /Choose a screenshot/);
  secure(page);
  const api = await worker.fetch(new Request(origin + "/v0/mobile/config", { headers: { origin } }), env, {});
  assert.equal(api.status, 200);
  assert.deepEqual(await api.json(), { origin });
  assert.deepEqual(requests, [NODE + "/v0/mobile/config"]);
  const unknown = await worker.fetch(new Request(origin + "/ipfs/bafyattacker/"), env, {});
  assert.equal(unknown.status, 404);
  secure(unknown);
});

test("moving the app keeps its old default origin reserved without keys, content, or redirects", async (t) => {
  t.mock.method(globalThis, "fetch", () => assert.fail("old origin must never reach upstreams"));
  for (const enabled of ["true", "false"]) {
    const env = environment({ MOBILE_ORIGIN: "https://fresh-phone.example", MOBILE_ENABLED: enabled });
    for (const path of ["/", "/app.js", "/v0/mobile/config", "/ipfs/bafyattacker/"]) {
      const result = await call(path, undefined, env);
      assert.equal(result.status, 503);
      const body = await result.json();
      assert.equal(body.code, "mobile_origin");
      assert.ok(body.error.includes("https://fresh-phone.example"));
      assert.equal(result.headers.get("location"), null);
      secure(result);
    }
  }
});

test("invalid custom origins fail closed on default and configured gateway hostnames", async (t) => {
  t.mock.method(globalThis, "fetch", () => assert.fail("invalid origin must not reach public or API upstreams"));
  for (const origin of ["not a URL", "http://fresh-phone.example", "https://user:password@fresh-phone.example", "https://fresh-phone.example/path", "https://fresh-phone.example/?query=true", "https://fresh-phone.example/#fragment", "https://crop.test", "https://phone.crop.test", "https://phone.nested.crop.test"]) {
    const env = environment({ MOBILE_ORIGIN: origin });
    const defaultResult = await call("/", undefined, env);
    assert.equal(defaultResult.status, 503, origin);
    assert.equal((await defaultResult.json()).code, "mobile_origin_unconfigured");
    let host;
    try { host = new URL(origin).hostname; } catch { continue; }
    const configuredResult = await worker.fetch(new Request("https://" + host + "/ipfs/bafyattacker/"), env, {});
    assert.equal(configuredResult.status, 503, origin);
    assert.equal((await configuredResult.json()).code, "mobile_origin_unconfigured");
    secure(configuredResult);
  }
});

test("configured origin requires its exact HTTPS scheme and port", async (t) => {
  t.mock.method(globalThis, "fetch", () => assert.fail("mismatched origins cannot reach upstreams"));
  const env = environment({ MOBILE_ORIGIN: "https://fresh-phone.example" });
  for (const origin of ["http://fresh-phone.example", "https://fresh-phone.example:444", "http://app.crop.test"]) {
    const result = await worker.fetch(new Request(origin + "/"), env, {});
    assert.equal(result.status, 503);
    assert.equal((await result.json()).code, "mobile_origin");
    secure(result);
  }
});

test("retired custom key origins cannot fall back to authored gateway content", async (t) => {
  t.mock.method(globalThis, "fetch", () => assert.fail("retired origins cannot reach upstreams"));
  for (const activeOrigin of [undefined, "https://next-phone.example"]) {
    const result = await worker.fetch(new Request("https://fresh-phone.example/"), environment({ MOBILE_ORIGIN: activeOrigin }), {});
    assert.equal(result.status, 404);
    assert.match(await result.text(), /unknown host/);
  }
});

test("gateway, host API, unknown assets, and unlisted source are unavailable at app origin", async (t) => {
  t.mock.method(globalThis, "fetch", () => assert.fail("unknown app paths cannot reach upstreams"));
  for (const path of ["/ipfs/bafyattacker/", "/ipns/k51attacker/", "/routing/v1/ipns/attacker", "/v0/host/push", "/planet.json", "/someone/index.html", "/docs/agents.md", "/app.js.map", "/protocol.test.mjs", "/mobile.test.mjs", "/v0/mobile", "/v0/mobile-attacker", "/%61pp.js", "/fonts/attacker.woff2"]) {
    const result = await call(path);
    assert.equal(result.status, 404, path);
    secure(result);
  }
});

test("static allowlist includes every app module, install asset, and local font", async (t) => {
  t.mock.method(globalThis, "fetch", () => assert.fail("static requests must stay local"));
  const expected = new Map([
    ["index.html", "text/html"], ["app.js", "text/javascript"], ["protocol.js", "text/javascript"],
    ["pairing.js", "text/javascript"], ["storage.js", "text/javascript"], ["sw.js", "text/javascript"],
    ["style.css", "text/css"], ["manifest.webmanifest", "application/manifest+json"], ["icon.svg", "image/svg+xml"],
  ]);
  const sources = new Map();
  for (const [file, type] of expected) {
    const result = await call("/" + file);
    assert.equal(result.status, 200, file);
    assert.ok(result.headers.get("content-type").startsWith(type), file);
    const source = await result.text();
    assert.equal(source, await readFile(new URL("../../web/mobile/" + file, import.meta.url), "utf8"), file);
    sources.set(file, source);
    secure(result);
  }
  for (const [file, source] of sources) {
    const refs = file.endsWith(".js")
      ? [...source.matchAll(/(?:from\s*|import\s*)['"](\.\/?[^'"]+)['"]/g)].map((match) => match[1])
      : file === "index.html" ? [...source.matchAll(/(?:src|href)="(\.\/[^\"]+)"/g)].map((match) => match[1]) : [];
    for (const ref of refs) assert.ok(expected.has(ref.replace(/^\.\//, "")), `${file} imports missing asset ${ref}`);
  }
  for (const weight of ["Regular", "Bold"]) {
    const file = `SimplonNorm-${weight}-WebXL.woff2`;
    const result = await call("/fonts/" + file);
    assert.equal(result.status, 200);
    assert.equal(result.headers.get("content-type"), "font/woff2");
    const font = Buffer.from(await result.arrayBuffer());
    assert.equal(font.subarray(0, 4).toString(), "wOF2");
    assert.deepEqual(font, await readFile(new URL("../../templates/croptop/assets/" + file, import.meta.url)));
  }
  // Text handling for the Worker must not change real module imports in tests.
  const protocol = await import("../../web/mobile/protocol.js");
  assert.ok(Object.values(protocol).some((value) => typeof value === "function"));
});

test("API uses a fixed node and forwards only the required authorization headers", async (t) => {
  const requests = [];
  t.mock.method(globalThis, "fetch", async (url, init) => {
    requests.push({ url, init, body: await new Response(init.body).text() });
    return Response.json({ token: "response-token" }, { headers: {
      "cache-control": "public, max-age=3600", "set-cookie": "private=value", "access-control-allow-origin": "*",
      "location": "https://untrusted.test", "x-private": "hidden",
    } });
  });
  const result = await call("/v0/mobile/session?upstream=https://untrusted.test", {
    method: "POST",
    headers: {
      Authorization: "Bearer private-session", Origin: APP, "X-Croptop-Pairing": "private-capability",
      "Content-Type": "application/json", Cookie: "secret=cookie", "X-Forwarded-Host": "untrusted.test",
      "X-Upstream": "https://untrusted.test", Accept: "text/html",
    },
    body: JSON.stringify({ id: "session-challenge", signature: "signature" }),
  }, environment({ MOBILE_NODE: "https://mobile-node.crop.test" }));
  assert.equal(result.status, 200);
  assert.deepEqual(await result.json(), { token: "response-token" });
  assert.equal(requests.length, 1);
  assert.equal(requests[0].url, "https://mobile-node.crop.test/v0/mobile/session?upstream=https://untrusted.test");
  assert.deepEqual([...requests[0].init.headers.entries()], [
    ["authorization", "Bearer private-session"], ["content-type", "application/json"], ["origin", APP], ["x-croptop-pairing", "private-capability"],
  ]);
  assert.deepEqual(JSON.parse(requests[0].body), { id: "session-challenge", signature: "signature" });
  assert.equal(requests[0].init.redirect, "manual");
  assert.ok(requests[0].init.signal instanceof AbortSignal);
  assert.equal(result.headers.get("location"), null);
  assert.equal(result.headers.get("x-private"), null);
  secure(result);
});

test("API falls back to NODE and accepts native clients without Origin", async (t) => {
  t.mock.method(globalThis, "fetch", async (url, init) => {
    assert.equal(url, NODE + "/v0/mobile/site");
    assert.equal(init.headers.get("origin"), null);
    assert.equal(init.headers.get("authorization"), "Bearer native-token");
    assert.equal(init.body, undefined);
    return Response.json({ ready: true });
  });
  assert.equal((await call("/v0/mobile/site", { headers: { authorization: "Bearer native-token" } })).status, 200);
});

test("browser requests from public sites cannot call the mobile service", async (t) => {
  t.mock.method(globalThis, "fetch", () => assert.fail("cross-origin requests must stop at the Worker"));
  for (const origin of ["https://crop.test", "https://author.crop.test", "https://app.crop.test.untrusted.test", "null"]) {
    const result = await call("/v0/mobile/challenge", { method: "POST", headers: { origin }, body: "{}" });
    assert.equal(result.status, 403);
    secure(result);
  }
});

test("API paths cannot escape the prefix through encoded or double separators", async (t) => {
  t.mock.method(globalThis, "fetch", () => assert.fail("ambiguous paths must stop at the Worker"));
  for (const path of ["/v0/mobile/%2e%2e%2fhost/push", "/v0/mobile/%252e%252e%252fhost/push", "/v0/mobile/site%3fupstream=evil", "/v0/mobile/site%5c..", "/v0/mobile//untrusted.test", "/v0/mobile/../host/push", "/v0/mobile/%2e%2e/host/push", "/v0/mobile/site.json"]) {
    const result = await call(path);
    assert.ok([400, 404].includes(result.status), `${path}: ${result.status}`);
    secure(result);
  }
  assert.equal((await call("/v0/mobile/site", { method: "DELETE" })).status, 405);
});

test("API rejects unsafe or missing operator configuration without exposing it", async (t) => {
  t.mock.method(globalThis, "fetch", () => assert.fail("invalid upstream must never receive credentials"));
  for (const node of [undefined, "", "not a URL", "http://public.test", "https://user:password@node.test", "https://node.test/base", "https://node.test/?secret=value", "https://node.test/#secret", APP, "file:///private/key"]) {
    const result = await call("/v0/mobile/config", undefined, environment({ NODE: node }));
    assert.equal(result.status, 503, String(node));
    assert.equal((await result.json()).code, "mobile_unconfigured");
    secure(result);
  }
});

test("API permits loopback HTTP only for local development", async (t) => {
  const seen = [];
  t.mock.method(globalThis, "fetch", async (url) => { seen.push(url); return Response.json({}); });
  for (const node of ["http://localhost:8788", "http://127.0.0.1:8788", "http://[::1]:8788"]) {
    const result = await call("/v0/mobile/config", undefined, environment({ NODE: node }));
    assert.equal(result.status, 200);
    assert.equal(seen.at(-1), node + "/v0/mobile/config");
  }
});

test("service redirects are neither followed nor exposed to clients", async (t) => {
  const requests = [];
  t.mock.method(globalThis, "fetch", async (url, init) => {
    requests.push(url);
    assert.equal(init.redirect, "manual");
    return new Response("redirect", { status: 307, headers: { location: "https://untrusted.test/receive-token" } });
  });
  const result = await call("/v0/mobile/site", { headers: { authorization: "Bearer private-token" } });
  assert.equal(result.status, 502);
  assert.equal((await result.json()).code, "mobile_redirect");
  assert.deepEqual(requests, [NODE + "/v0/mobile/site"]);
  secure(result);
});

test("only inert API responses and raster operation previews share the app origin", async (t) => {
  let type = "text/html";
  t.mock.method(globalThis, "fetch", async () => new Response("untrusted response", { headers: { "content-type": type } }));
  for (const contentType of ["text/html", "image/svg+xml", "text/javascript", "application/xhtml+xml", "image/png"]) {
    type = contentType;
    const result = await call("/v0/mobile/site");
    assert.equal(result.status, 502, contentType);
    secure(result);
  }
  for (const contentType of ["image/png", "image/jpeg", "image/webp"]) {
    type = contentType;
    const result = await call("/v0/mobile/operations/ABCD-1234/image");
    assert.equal(result.status, 200, contentType);
    assert.equal(result.headers.get("content-type"), contentType);
    secure(result);
  }
  type = "image/svg+xml";
  assert.equal((await call("/v0/mobile/operations/ABCD-1234/image")).status, 502);
});

test("API preserves useful JSON errors and empty preflight replies without caching", async (t) => {
  t.mock.method(globalThis, "fetch", async (_, init) => init.method === "OPTIONS"
    ? new Response(null, { status: 204 })
    : Response.json({ error: "Please connect this site again.", code: "unauthorized" }, { status: 401 }));
  const error = await call("/v0/mobile/site");
  assert.equal(error.status, 401);
  assert.equal((await error.json()).code, "unauthorized");
  secure(error);
  const preflight = await call("/v0/mobile/site", { method: "OPTIONS", headers: { origin: APP } });
  assert.equal(preflight.status, 204);
  assert.equal(await preflight.text(), "");
  secure(preflight);
});

test("network failures and bounded timeouts do not log or expose private credentials", async (t) => {
  for (const method of ["log", "warn", "error"]) t.mock.method(console, method, () => assert.fail("mobile requests must not log secrets"));
  const controller = new AbortController();
  t.mock.method(AbortSignal, "timeout", (duration) => {
    assert.equal(duration, 60_000);
    return controller.signal;
  });
  let timedOut = false;
  t.mock.method(globalThis, "fetch", async () => {
    if (timedOut) controller.abort();
    throw new Error("private-token private-capability private-node-address");
  });
  const init = { headers: { authorization: "Bearer private-token", "x-croptop-pairing": "private-capability" } };
  const network = await call("/v0/mobile/site", init);
  assert.equal(network.status, 502);
  assert.equal((await network.json()).code, "mobile_unavailable");
  timedOut = true;
  const timeout = await call("/v0/mobile/site", init);
  assert.equal(timeout.status, 504);
  const body = await timeout.text();
  assert.equal(JSON.parse(body).code, "mobile_timeout");
  assert.ok(!body.includes("private-"));
  secure(timeout);
});

test("the app subdomain hook does not change bare-domain utility routes", async (t) => {
  t.mock.method(globalThis, "fetch", () => assert.fail("installer is local"));
  const result = await worker.fetch(new Request("https://crop.test/install.sh"), environment(), {});
  assert.equal(result.status, 200);
  assert.equal(result.headers.get("content-type"), "text/x-shellscript; charset=utf-8");
  assert.match(await result.text(), /croptop/);
});

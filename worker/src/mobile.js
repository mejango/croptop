// This origin holds device keys. Its routes must never fall through to the
// public gateway, ENS resolution, or files supplied by a site author.
import indexHTML from "../../web/mobile/index.html";
import appJS from "../../web/mobile/app.js";
import protocolJS from "../../web/mobile/protocol.js";
import pairingJS from "../../web/mobile/pairing.js";
import storageJS from "../../web/mobile/storage.js";
import styleCSS from "../../web/mobile/style.css";
import workerJS from "../../web/mobile/sw.js";
import manifest from "../../web/mobile/manifest.webmanifest";
import icon from "../../web/mobile/icon.svg";
import fontRegular from "../../templates/croptop/assets/SimplonNorm-Regular-WebXL.woff2";
import fontBold from "../../templates/croptop/assets/SimplonNorm-Bold-WebXL.woff2";

const API_PREFIX = "/v0/mobile/";
const API_TIMEOUT_MS = 60_000;
const SECURITY_HEADERS = {
  "content-security-policy": "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' blob:; connect-src 'self'; font-src 'self'; worker-src 'self'; manifest-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'",
  "referrer-policy": "no-referrer",
  "x-content-type-options": "nosniff",
  "x-frame-options": "DENY",
  "cross-origin-opener-policy": "same-origin",
  "cross-origin-resource-policy": "same-origin",
  "permissions-policy": "camera=(), microphone=(), geolocation=()",
};
const JS_TYPE = "text/javascript; charset=utf-8";
const ASSETS = new Map([
  ["/", [indexHTML, "text/html; charset=utf-8"]],
  ["/index.html", [indexHTML, "text/html; charset=utf-8"]],
  ["/app.js", [appJS, JS_TYPE]],
  ["/protocol.js", [protocolJS, JS_TYPE]],
  ["/pairing.js", [pairingJS, JS_TYPE]],
  ["/storage.js", [storageJS, JS_TYPE]],
  ["/style.css", [styleCSS, "text/css; charset=utf-8"]],
  ["/sw.js", [workerJS, JS_TYPE]],
  ["/manifest.webmanifest", [manifest, "application/manifest+json"]],
  ["/icon.svg", [icon, "image/svg+xml"]],
  ["/fonts/SimplonNorm-Regular-WebXL.woff2", [fontRegular, "font/woff2"]],
  ["/fonts/SimplonNorm-Bold-WebXL.woff2", [fontBold, "font/woff2"]],
]);

function response(body, status, contentType, method) {
  return new Response(method === "HEAD" ? null : body, {
    status,
    headers: { ...SECURITY_HEADERS, "cache-control": "no-store", "content-type": contentType },
  });
}

function error(status, code, message, method) {
  return response(JSON.stringify({ error: message, code }), status, "application/json; charset=utf-8", method);
}

export function mobileOriginConfig(env) {
  const defaultHost = "app." + env.DOMAIN.toLowerCase();
  const config = { defaultHost, host: null, origin: null };
  try {
    const url = new URL(env.MOBILE_ORIGIN || "https://" + defaultHost);
    // Reserve even a malformed configuration's hostname so it can never fall
    // through to author content. Only a validated origin can hold device keys.
    config.host = url.hostname.toLowerCase();
    // A retired custom origin must never become an ENS gateway after a later
    // configuration change. The default is permanently reserved; every other
    // key origin must be outside this Worker's public gateway namespace.
    const gatewayHost = config.host === env.DOMAIN.toLowerCase() || config.host.endsWith("." + env.DOMAIN.toLowerCase());
    if (url.protocol !== "https:" || url.username || url.password ||
        url.pathname !== "/" || url.search || url.hash || (gatewayHost && config.host !== defaultHost)) return config;
    config.origin = url.origin;
  } catch { /* Invalid configuration leaves the app unavailable. */ }
  return config;
}

export async function serveMobile(request, url, env, config = mobileOriginConfig(env)) {
  try {
    if (!config.origin) {
      return error(503, "mobile_origin_unconfigured", "The trusted phone app address has not been configured correctly.", request.method);
    }
    if (url.origin !== config.origin) {
      return error(503, "mobile_origin", "Open the trusted Croptop phone app at " + config.origin + ".", request.method);
    }
    if (env.MOBILE_ENABLED !== "true") {
      return error(503, "mobile_disabled", "Phone publishing is not available yet. Your existing Croptop publisher still works.", request.method);
    }
    if (url.pathname.startsWith(API_PREFIX)) return await proxyMobile(request, url, env);
    const asset = ASSETS.get(url.pathname);
    if (!asset) return error(404, "not_found", "This address is not part of the Croptop phone app.", request.method);
    if (request.method !== "GET" && request.method !== "HEAD") {
      return error(405, "method_not_allowed", "Use GET to open the phone app.", request.method);
    }
    return response(asset[0], 200, asset[1], request.method);
  } catch {
    // Never expose request contents, pairing capabilities, or session tokens in
    // Worker errors or logs (including errors thrown by a network implementation).
    return error(502, "mobile_unavailable", "Phone publishing is temporarily unavailable. Your saved draft is still on this device.", request.method);
  }
}

async function proxyMobile(request, url, env) {
  // Encoded separators, dots, or percent escapes can be decoded differently by
  // intermediaries. Mobile routes use only simple ASCII segments; fail closed.
  if (!/^\/v0\/mobile\/[A-Za-z0-9_/-]+$/.test(url.pathname) || url.pathname.includes("//")) {
    return error(400, "invalid_path", "Invalid phone publishing address.", request.method);
  }
  if (!["GET", "HEAD", "POST", "PUT", "OPTIONS"].includes(request.method)) {
    return error(405, "method_not_allowed", "This request method is not supported.", request.method);
  }
  const origin = request.headers.get("origin");
  if (origin && origin !== url.origin) {
    return error(403, "origin", "Open the Croptop phone app to publish.", request.method);
  }
  let upstream;
  try {
    upstream = new URL(env.MOBILE_NODE || env.NODE);
    const loopback = ["localhost", "127.0.0.1", "[::1]"].includes(upstream.hostname);
    if ((upstream.protocol !== "https:" && !(upstream.protocol === "http:" && loopback)) ||
        upstream.username || upstream.password || upstream.pathname !== "/" || upstream.search || upstream.hash || upstream.origin === url.origin) {
      throw new Error("invalid configured origin");
    }
  } catch {
    return error(503, "mobile_unconfigured", "Phone publishing has not been configured on this host.", request.method);
  }
  // The destination comes only from operator configuration. Request headers,
  // paths, and query parameters cannot select a different upstream origin.
  upstream.pathname = url.pathname;
  upstream.search = url.search;
  const headers = new Headers();
  for (const name of ["authorization", "origin", "x-croptop-pairing", "content-type"]) {
    if (request.headers.has(name)) headers.set(name, request.headers.get(name));
  }
  const signal = AbortSignal.timeout(API_TIMEOUT_MS);
  let result;
  try {
    result = await fetch(upstream.toString(), {
      method: request.method,
      headers,
      body: request.method === "GET" || request.method === "HEAD" ? undefined : request.body,
      redirect: "manual",
      signal,
      // Node's fetch requires this for streamed bodies; Workers also stream the
      // body and ignore the option, keeping uploads out of Worker memory.
      duplex: "half",
    });
  } catch {
    return error(signal.aborted ? 504 : 502, signal.aborted ? "mobile_timeout" : "mobile_unavailable",
      "The publishing service did not respond. Your draft is saved; check its status before retrying.", request.method);
  }
  if (result.status >= 300 && result.status < 400) {
    await result.body?.cancel();
    return error(502, "mobile_redirect", "The publishing service returned an unexpected redirect.", request.method);
  }
  const type = (result.headers.get("content-type") || "").split(";")[0].trim().toLowerCase();
  const empty = result.status === 204;
  const image = /^\/v0\/mobile\/operations\/[A-Za-z0-9_-]+\/image$/.test(url.pathname);
  if (!empty && type !== "application/json" && type !== "text/plain" && !(image && ["image/png", "image/jpeg", "image/webp"].includes(type))) {
    await result.body?.cancel();
    return error(502, "mobile_response", "The publishing service returned an unexpected response.", request.method);
  }
  // Do not forward cookies, redirects, CORS policy, or caching headers from the
  // service. In particular, arbitrary HTML/SVG can never share the key origin.
  return response(empty ? null : result.body, result.status, result.headers.get("content-type") || "application/json", request.method);
}

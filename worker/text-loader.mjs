// wrangler imports text imports (scripts and docs) as text (the Text rule in wrangler.toml);
// under node this hook does the same: node --experimental-loader ./text-loader.mjs --test test/
import { readFile } from "node:fs/promises";

export async function resolve(specifier, context, next) {
  const resolved = await next(specifier, context);
  // Only the Worker imports browser modules as asset text. The browser's own
  // protocol tests must still be able to import those same modules as code.
  if (context.parentURL?.endsWith("/worker/src/mobile.js") && resolved.url.includes("/web/mobile/")) {
    return { ...resolved, url: resolved.url + "?worker-text" };
  }
  return resolved;
}

export async function load(url, context, next) {
  if (url.endsWith("?worker-text")) return { format: "module", shortCircuit: true, source: `export default ${JSON.stringify(await readFile(new URL(url), "utf8"))};` };
  if (/\/templates\/croptop\/assets\/SimplonNorm-(Regular|Bold)-WebXL\.woff2$/.test(url)) {
    return { format: "module", shortCircuit: true, source: `export default new Uint8Array(${JSON.stringify([...await readFile(new URL(url))])}).buffer;` };
  }
  if (/\.(sh|ps1|md)$/.test(url)) return { format: "module", shortCircuit: true, source: `export default ${JSON.stringify(await readFile(new URL(url), "utf8"))};` };
  return next(url, context);
}

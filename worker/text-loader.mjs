// wrangler imports the install scripts as text (the Text rule in wrangler.toml);
// under node this hook does the same: node --experimental-loader ./text-loader.mjs --test test/
import { readFile } from "node:fs/promises";

export async function load(url, context, next) {
  if (/\.(sh|ps1|md)$/.test(url)) return { format: "module", shortCircuit: true, source: `export default ${JSON.stringify(await readFile(new URL(url), "utf8"))};` };
  return next(url, context);
}

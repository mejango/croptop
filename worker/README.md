# crop.top Worker

The Cloudflare side of crop.top: a gateway, a store that sites push to on
publish, and a registry of free names. See `docs/host.md` for the protocol and
deployment. `wrangler.toml` is production (`crop.top/*`, `*.crop.top/*`);
`wrangler.test.toml` is staging on `test.crop.top`. Tests: `node --test test/`.

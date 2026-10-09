# crop.top Worker

The Cloudflare side of crop.top: a gateway, a store that sites push to on
publish, and a registry of free names. See `docs/host.md` for the protocol and
deployment. `wrangler.toml` is production (`crop.top/*`, `*.crop.top/*`);
`wrangler.test.toml` is staging on `test.crop.top`. Tests: `node --experimental-loader ./text-loader.mjs --test test/`.

## Isolated phone pilot

`wrangler.mobile.toml` builds `src/mobile-only.js` as a separate Worker on a
fresh `workers.dev` hostname. It has no author gateway, R2/KV bindings, scheduled
work, or routes on `crop.top`. Only the exact configured HTTPS origin can serve
the composer; unknown origins and paths never resolve author content. Preview
URLs are disabled. Keep this hostname reserved even after disabling the pilot.

The configuration starts disabled. Before enabling it, set `MOBILE_NODE` to the
isolated node's HTTPS origin and provision the `MOBILE_PROXY_SECRET` Worker
secret to match the node's `CROPTOP_MOBILE_PROXY_SECRET`. Use a random 32-byte
secret encoded as base64url or hex. Never put it in `[vars]`, source control,
command arguments, logs, browser configuration, or client applications. The
proxy overwrites this header from its secret binding; clients cannot supply it.
The node must reject direct publishing API calls without it. The status-only
`/v0/mobile/health` endpoint accepts GET/HEAD through the Worker and is included
in its general rate budget; the node also allows it for platform health checks.

Four required [Cloudflare rate-limit bindings](https://developers.cloudflare.com/workers/runtime-apis/bindings/rate-limit/)
cover general API access (240/minute), shared challenge/session issuance
(30/minute), pairing including polling (120/minute), and upload/prepare/commit
(12/minute). A request consumes the general budget and its endpoint budget.
Keys hash the Cloudflare-supplied source IP, not client forwarding headers,
credentials, IDs, or query parameters. Worker subrequests share one separate
bucket because same-zone Workers can affect their connecting-IP header.
These are per-location, eventually
consistent abuse controls, not exact global quotas. Shared mobile-carrier IPs
can share a budget; tune only after observing the pilot. Backend concurrency,
draft limits, and private-volume limits remain necessary. Missing/failing
controls fail closed; exhausted budgets return 429 with `Retry-After: 60`.

Verify without changing Cloudflare resources:

```sh
node --experimental-loader ./text-loader.mjs --test test/*.test.mjs
npx --yes wrangler@4.145.0 deploy --dry-run --config wrangler.mobile.toml
```

Only deploy this config for the phone pilot; do not deploy `wrangler.toml` as
part of its rollout. Verify disabled status first, backend persistence and
direct-access rejection, matching public `/v0/mobile/config`, origin isolation,
and a complete signed test publication before sharing the link. The kill switch
is `MOBILE_ENABLED = "false"` on this Worker; retain the node's private data and
receipts during rollback so clients can reconcile an uncertain publication.

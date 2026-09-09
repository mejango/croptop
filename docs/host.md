# Running a host

A host is a gateway, a pin store, and a name registry for one domain. Two
implementations speak the same protocol:

- `croptop host`, a Go process with the embedded IPFS engine, for anyone with a
  server. It also serves blocks to the IPFS network.
- `worker/`, a Cloudflare Worker with R2 and KV, which is what crop.top runs.
  No server at all; pushed sites live in R2 and anything else is fetched from a
  public gateway.

## Cloudflare (what crop.top uses)

```sh
cd worker
export CLOUDFLARE_API_TOKEN=... CLOUDFLARE_ACCOUNT_ID=...
npx -p node@22 -p wrangler wrangler kv namespace create croptop-registry   # put the id in wrangler.toml
npx -p node@22 -p wrangler wrangler r2 bucket create croptop-sites
npx -p node@22 -p wrangler wrangler deploy
```

The token needs Workers Scripts, Workers Routes, KV and R2 edit on the account
and the zone. `wrangler.toml` attaches the routes `crop.top/*` and
`*.crop.top/*`, so the zone's DNS only needs proxied records for `crop.top` and
`*.crop.top` pointing anywhere. A cron trigger re-publishes every pushed IPNS
record to the delegated routing endpoint every half hour.

Local development: `wrangler dev --local` rewrites every request's host to the
zone name, so `.dev.vars` sets `SIGNING_HOST=localhost` to make signatures from
a local console verify. Never set that in production.

## Railway (or any container host)

The repo has a `Dockerfile` and `railway.toml`. On Railway:

1. New project from the GitHub repo. Add a volume mounted at `/data`.
2. Variables: `CROPTOP_DOMAIN` (e.g. `node.crop.top`), `CROPTOP_ROOT` if the
   bare domain should show a site, and `CROPTOP_ANNOUNCE` once you know the
   TCP proxy address (next step).
3. Networking: add a public HTTPS domain for port 8090 (a custom domain such
   as `node.crop.top` is one CNAME in Cloudflare), and enable a TCP proxy for
   port 4001. Railway gives it an address like `host.proxy.rlwy.net:12345`;
   set `CROPTOP_ANNOUNCE=/dns4/host.proxy.rlwy.net/tcp/12345` and redeploy.
   With an announced address the node runs as a DHT server, so other nodes
   and gateways fetch straight from it.

The container answers `/v0/host/health`, the push and name API, the classic
`/ipfs/<cid>/...` and `/ipns/<name>/...` paths, and `<label>.<domain>` if you
give it a wildcard. The Worker can use it as an upstream in path form:
`UPSTREAMS = "https://node.crop.top,eth.sucks,eth.shop"`.

## Go (your own server)

`croptop host` turns a server into a gateway, a pin host, and a name registry
for one domain.

## What it serves

| address | what |
|---|---|
| `yoursite.crop.top` | the ENS site `yoursite.eth`, resolved through DNSLink and IPNS |
| `crop.top/yoursite/` | a free name claimed from the console |
| `k51….crop.top`, `bafy….crop.top` | any IPNS name or CID |
| `crop.top/` | the `--root` site (croptop.eth), or the directory of claimed names |
| `crop.top/directory` | the directory when a root site is set |

Sites that push to the host are served from the host's own blocks the moment a
publish finishes; everything else is fetched from the network on first request.
The host also re-announces every pushed IPNS record and root every half hour,
so a site stays fresh and reachable while its owner's computer is closed.

## Server

```sh
croptop host --domain crop.top --listen 127.0.0.1:8090 --data /var/lib/croptop --root croptop.eth
```

`--root` names the site the bare domain shows: crop.top itself is the ENS site
`croptop.eth`, and claimed names sit beside it at `crop.top/<name>/`. Without
`--root`, the bare domain shows the directory of claimed names; with it, the
directory is at `/directory`.

Run it under systemd or any supervisor. It uses the embedded engine, so no kubo
is needed. It needs an open swarm port (4001 to 4009, whichever is free, TCP and
UDP) to be a good peer; the HTTP side stays local behind your proxy.

`CROPTOP_DEBUG=1` prints the engine's log lines.

## DNS

Two proxied records in Cloudflare, both pointing at the server:

- `A crop.top`
- `A *.crop.top`

Cloudflare's universal certificate covers one level of subdomain, which is all
the host uses. No API token is required.

## nginx

```nginx
server {
    listen 443 ssl;
    server_name crop.top *.crop.top;
    # your existing ssl_certificate lines (a Cloudflare origin certificate works)

    client_max_body_size 600m;   # site pushes
    proxy_read_timeout 600s;

    location / {
        proxy_pass http://127.0.0.1:8090;
        proxy_set_header Host $host;        # the host routes on this
        proxy_set_header X-Forwarded-Proto https;
        proxy_request_buffering off;
    }
}
```

Do not cache `/v0/host/` and keep any cache for the rest short: the host already
answers from local blocks.

## The app side

In a site's Settings, set Host to `https://crop.top` and optionally claim a free
name. From then on every publish pushes the site to the host in the background,
signed with the site's IPNS key; the log line `pushed <site> to <host>` confirms
it. Choosing crop.top as the site's gateway makes `yoursite.crop.top` or
`crop.top/yoursite/` the address written into the site's absolute links.

## API

- `POST /v0/host/names` `{name, ipns, time, sig}`: claim a free name. `sig` is
  the site key's ed25519 signature over `croptop-name\n<domain>\n<name>\n<ipns>\n<time>`.
  First come, one name per key; claiming again with a new name releases the old.
- `POST /v0/host/push`: headers `X-Croptop-Ipns`, `X-Croptop-Cid`, `X-Croptop-Seq`,
  `X-Croptop-Time`, `X-Croptop-Sig` (over `croptop-push\n<domain>\n<ipns>\n<cid>\n<seq>\n<time>`),
  optional `X-Croptop-Record` (the signed IPNS record, base64); body is the block
  stream `croptop` produces. Refused when the host already has a newer sequence.
- `GET /v0/host/names/<name>`, `GET /v0/host/keys/<ipns>`: what the host knows.

Everything is signed by the site's own key, so there are no accounts.

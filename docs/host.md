# Running a host

`croptop host` turns a server into a gateway, a pin host, and a name registry
for one domain. crop.top runs it. Anyone can run one for their own domain.

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

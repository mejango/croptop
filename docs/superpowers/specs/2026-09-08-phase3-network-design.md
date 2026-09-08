# Phase 3: Network

Date: 2026-09-08. Parent: 2026-09-08-croptop-roadmap.md.

## Goal

Make a croptop node useful to sites it does not own. Follow a site and
your node keeps its latest version, serves it locally, and re-provides it
to the network, so sites stay online because their readers host them. Show
site owners who is hosting them. Let a node run headless on an always-on
box.

## Follow

`croptop follow <ipns-name | ens-name>` and *Follow a site* in the console.

Stored under `<data>/following/<key>/` where key is the IPNS name:

```
follow.json   { "name": "<what the user typed>", "ipns": "k51…", "cid": "<last fetched root>",
                "title": "<planet.json name>", "checked": <apple time>, "changed": <apple time> }
site/         the fetched published tree
```

Following resolves ENS through the engine, fetches the tree with the same
IPFS-then-gateway fallback adopt uses, reads `planet.json` for the title,
and provides the root. A refresh loop runs on start and every 6 hours:
resolve, compare the CID, fetch and provide when it changed. Unfollow
removes the folder; blocks stay until a later GC phase.

The rail gets a *Following* section listing titles; each opens the site at
`/f/<ipns>/` served from `following/<ipns>/site/`, the same way owned sites
are served from `public/`. A followed site's page shows the name, when it
last changed, the current CID, and an Unfollow button.

## Providing

`Engine.Provide` runs for every owned site's last published root after
each publish (already) and for every followed root after each fetch. A
12-hour ticker re-provides all of them, since DHT provider records expire
in 48 hours. Kubo engine: `pin add` covers keep and provide.

## Hosts

`Engine.FindProviders(ctx, cid) ([]string, error)` (kubo: `routing
findprovs`; embedded: `dht.FindProvidersAsync` for 20 s). The console's site
page gets a *Hosts* line: "Hosted by N nodes" with the peer IDs on hover,
our own node marked. `GET /v0/croptop/sites/{id}/hosts`.

## Headless node

`croptop serve --role node` runs without opening a browser and logs to
stdout; everything else is the same process. Combined with `passcode set`
and `--listen 0.0.0.0:8086`, a laptop or phone edits through the API while
the box publishes and hosts. `croptop status` prints, through the API, each
owned site's sequence and last publish and each followed site's CID and
last change, for cron and monitoring.

## API additions

```
GET    /v0/croptop/following                 list
POST   /v0/croptop/following  {name}         follow (resolves, fetches, provides)
DELETE /v0/croptop/following/{ipns}          unfollow
POST   /v0/croptop/following/{ipns}/refresh  fetch now
GET    /v0/croptop/sites/{id}/hosts          providers of the site's root
GET    /f/{ipns}/{path...}                   followed site tree (open, like /<uuid>/)
```

## Tests

- `follow` package: follow with a fake engine whose Resolve/Get write a
  fixture tree; `follow.json` written; refresh with an unchanged CID does not
  refetch; a changed CID does; unfollow removes the folder.
- `server`: follow through the API, list, serve `/f/<ipns>/planet.json`,
  unfollow.
- Acceptance: follow FOLLO from a second data dir on this Mac; publish a
  change from the first; the follower picks it up on refresh; `hosts` for
  FOLLO lists both nodes; stop the publisher's node and fetch the site
  through the follower's `/f/` route.

## Out of scope

Garbage collection of unfollowed blocks, following by RSS or non-Croptop
sites, a public HTTP gateway from the node, notifications on change.

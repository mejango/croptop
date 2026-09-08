# Croptop roadmap

Date: 2026-09-08. Decisions taken with Jango on this date.

## The idea

A Croptop site is a key plus a self-describing published tree. The tree is
the truth; any node holding the key can edit it (adopt, sync); any node can
host it (follow, pin). The app is a client of a documented format, not the
owner of a database.

Three layers with hard edges:

1. **Format.** `planet.json`, `<post>/article.json`, `nft.json` and its CID,
   `templateSettings.json`, attachments. Frozen as "Croptop site format v1"
   and documented in `docs/format.md` (phase 1).
2. **Node.** One binary, roles by config: console (today), headless
   always-on publisher, follower that pins and re-provides sites it reads.
3. **Page.** Template plus the post's own HTML in the viewer's browser with
   the viewer's wallet. Widgets live here.

## Decisions

- **IPFS engine:** embed boxo (kubo's libraries) in the binary. Phase 2.
  The kubo sidecar stays behind the same `ipfs.Node` interface until the
  embedded node has published and adopted a real site, then it is removed.
- **Gateways:** a table of gateways (`eth.sucks`, `eth.shop`, `eth.limo`,
  more later). Each site names its canonical gateway, written into absolute
  URLs; default `eth.sucks`. Reads and links fall back through the list.
- **Order:** Trust, Engine, Network, Templates, Widgets.
- **Network MVP:** follow by IPNS or ENS name, pin and re-provide the latest
  tree, serve it at `/<name>/` on the node, show which peers provide your own
  site, headless `--role node`.

## Phases

### Phase 1: Trust (spec: 2026-09-08-phase1-trust-design.md)
Close the gaps a reader or subscriber would notice, and finish the tests.
RSS output identical in shape to Planet's; pin, navigation, and hero image
controls; live markdown preview and drag-and-drop images in the editor;
per-site gateway; format document; two-machine takeover and Linux runs.

### Phase 2: Engine
`internal/ipfs` gets a second implementation of the same interface built on
boxo: libp2p host with DHT client, bitswap, flatfs or badger blockstore,
unixfs add and get, IPNS record create, publish, and resolve with explicit
sequence numbers, DNSLink for ENS via DoH, keystore already pure Go. Feature
flag `--engine kubo|embedded`, default flips after a real publish and adopt
succeed on all three OSes.

### Phase 3: Network
Follow a site: resolve, fetch, pin, re-provide on a schedule, serve at
`/<name>/`. Followed sites appear in the rail. "Hosts" view for your own
site from `routing findprovs`. `croptop --role node` for a headless box:
no UI, keepalive and pins only, API for thin clients.

### Phase 4: Templates
Site points at a template by CID with an optional upstream name. Fork
copies the template into `sites/<id>/template/`. Console gets a code editor
with the rendered preview beside it. Template browser lists templates by
CID or ENS name. Upstream update is a diff against the forked CID.

### Phase 5: Widgets
Runtime in the template head: `croptop.site`, `croptop.post`, `croptop.env`,
`croptop.wallet`, `croptop.ipfs`. Widgets are ES modules attached to the
post and referenced from its HTML. Console gets a widget palette with an
isolated post preview. A registry site where each widget is a post.

Each phase gets its own spec, plan, and release tag.

# Phase 2: Engine

Date: 2026-09-08. Parent: 2026-09-08-croptop-roadmap.md.

## Goal

Replace the downloaded kubo sidecar with an IPFS node embedded in the
croptop binary, built on boxo (kubo's own libraries), behind the interface
the rest of the program already uses. Same keys, same CIDs, same IPNS
behavior. One file to download, nothing to spawn, no port scouting.

## Why now

kubo's maintainers wind down this month. Our kubo surface is eight calls.
The follower and headless roles in Phase 3 want in-process control of
providing and pinning. Embedding is the right seam and it is small enough.

## Verified feasibility (scratch module, 2026-09-08)

boxo v0.42.2, go-libp2p v0.49.0, go-libp2p-kad-dht v0.42.1, go-ds-flatfs
v0.6.1, go-ds-leveldb v0.5.2, go-doh-resolver v0.6.0 build together on Go
1.27. `ipns.NewRecord(sk, path, seq, eol, ttl)`, `ipns.UnmarshalRecord`,
`dht.PutValue/GetValue/Provide/FindProvidersAsync`,
`bitswap.New(ctx, bsnet.NewFromIpfsHost(h), dht, bstore)`,
`merkledag.NewDAGService(blockservice.New(bs, bitswap))`, unixfs
`DagBuilderParams` with `balanced.Layout`, `io.NewDirectory`, and
`unixfile.NewUnixfsFile` are all present.

## Interface

`internal/ipfs` gains:

```go
type Engine interface {
	Start(ctx) error
	Stop() error
	Running() bool
	LastError() string
	Keystore() *Keystore
	Info(ctx) (Info, error)
	FileCIDv0(ctx, path string) (string, error)
	AddDir(ctx, dir string) (cid string, err error)
	Get(ctx, ipfsPath, dest string) error
	Resolve(ctx, path string) (string, error)          // /ipns/<name|ens> one step
	NetworkRecord(ctx, name string) (*Record, error)
	NamePublish(ctx, key, cid string, seq uint64) error
	ConnectLocalNodes(ctx) int
	Provide(ctx, cid string) error                      // new; kubo: pin add
}
```

`*Node` (kubo) implements it unchanged. `*Embedded` is the new
implementation. `Publisher`, `Server`, and `main` hold an `Engine`.
`--engine kubo|embedded` selects; config.json remembers. Default stays
`kubo` until the acceptance run below passes, then flips to `embedded` and
the kubo download path stays one release for people who set it explicitly.

## Embedded node

Data under `<data>/node/`: `identity.key` (libp2p ed25519 node key, 0600),
`blocks/` (flatfs, sharding next-to-last/2, like kubo), `datastore/`
(leveldb: DHT records, provider records, IPNS state). Keystore stays
`<data>/ipfs/keystore/` so keys are shared with the kubo engine.

libp2p host: `Identity`, listen on `/ip4/0.0.0.0/tcp/<p>`, `/ip6/::/tcp/<p>`,
`/ip4/0.0.0.0/udp/<p>/quic-v1`, `/ip6/::/udp/<p>/quic-v1` with the same
free-port probe as today; `NATPortMap`, `EnableHolePunching`,
`EnableNATService`, `ConnectionManager(connmgr 64 low, 192 high)`. No
relay client in this phase.

DHT: `dht.New(h, dht.Mode(dht.ModeAuto), dht.BootstrapPeers(default...),
dht.NamespacedValidator("ipns", ipns.Validator{KeyBook: h.Peerstore()}),
dht.Datastore(ds))`, then `Bootstrap`. Peering: dial the same four content
providers Planet peers with, and any local kubo found on 5981-5991.

Blocks: `blockstore.NewBlockstore(flatfs)`, `bitswap.New(ctx, bsnet, dht,
bs)`, `blockservice.New(bs, bitswap)`, `merkledag.NewDAGService`.

### CID parity (must match kubo exactly)

- `FileCIDv0`: chunker size 262144, balanced layout, `Maxlinks` 174,
  `RawLeaves` false, `cid.V0Builder`. No blocks are stored (hash only).
- `AddDir`: same chunker and layout, `RawLeaves` true, `cid.V1Builder{Codec:
  DagPb, MhType: Sha2_256}`; directories via `io.NewDirectory` with the V1
  builder and `io.HAMTShardingSize = 256 KiB`; the root wraps the directory
  (`-H` semantics: the directory itself is the root, no wrapping folder).
  Hidden files included. Result must equal `ipfs add -r -H --cid-version=1
  -Q` on the same tree; the test compares against the kubo binary when it
  is present under `.data/kubo`.

### IPNS

- `NamePublish`: load the site key from the keystore as
  `crypto.UnmarshalEd25519PrivateKey`, `ipns.NewRecord(sk, "/ipfs/<cid>",
  seq, now+7200h, 1m)`, `MarshalRecord`, `dht.PutValue(ctx,
  string(name.RoutingKey()), bytes)`. Also `Provide` the root CID.
- `NetworkRecord`: `dht.SearchValue` for up to 25s, keep the record with the
  highest sequence, `UnmarshalRecord` → `Record{Value, Sequence, Validity}`.
  Not found → `ErrNoRecord`.
- `Resolve("/ipns/x.eth")`: DoH TXT lookup of `_dnslink.x.eth` through
  `https://dns.eth.limo/dns-query` (go-doh-resolver), parse
  `dnslink=/ipns/<name>` (or `/ipfs/<cid>`). `Resolve("/ipns/k51…")`:
  `NetworkRecord` then its value.

### Get

`merkledag.NewSession`, `unixfile.NewUnixfsFile`, `files.WriteTo(node,
dest)`. Timeout as today.

### Providing

After `AddDir` and on a 12-hour ticker: `dht.Provide(root, true)` for every
site's last published CID and for the top-level entries of each tree
(planet.json, index.html, each post directory). Bitswap sessions fetch the
rest from whoever served the root. Kubo provides every block; we provide
roots. Phase 3 extends this to followed sites.

### Info

Peer ID, "embedded/boxo v0.42.2", connected peer count from
`h.Network().Peers()`.

## Migration

Nothing to migrate. Site data is files; keys are shared; the kubo repo
under `<data>/ipfs/` is left alone (blocks are re-added on the next
publish). `croptop engine` prints which engine is active.

## Tests

- `ipfs/embedded_test.go`: FileCIDv0 of `render/testdata/public-post/nft.json`
  equals `nft.json.cid.txt` (no network). AddDir of a temp tree equals the
  kubo binary's CID when `.data/kubo/ipfs` exists, else skipped. IPNS record
  round trip: NewRecord → Marshal → Unmarshal → sequence and value.
- Acceptance, manual, recorded in the plan: start `--engine embedded`,
  `NetworkRecord` for FOLLO returns the live sequence; `adopt` FOLLO into a
  fresh data dir over the embedded node; publish FOLLO from the embedded
  node; the kubo-engine console and `https://follo.eth.sucks/planet.json`
  see the new CID. Then Linux in Docker with `--engine embedded`.

## Out of scope

Local HTTP gateway from the blockstore (Phase 3 uses it for followed
sites), relay client, pinning of arbitrary CIDs, garbage collection.

## Acceptance record (2026-09-08)

- Embedded node read FOLLO's live record (sequence 30) from the DHT after
  waiting for its routing table.
- Adopted FOLLO through the embedded node (blocks came from the local kubo).
- Published FOLLO at 31 (CLI) and 32 (console); the node's own read-back
  returned 32; a fresh Linux arm64 node in Docker adopted at sequence 32;
  eth.sucks served the new CID; kubo listed the embedded node as provider.
- Kubo's `name get` and `routing get` on the same machine lagged one
  publish behind and `name resolve --nocache` two behind, so kubo's own view
  is not the freshness oracle; the fresh node and the read-back are.
- Direct bitswap fetch from Docker to the NAT'd Mac node timed out and the
  gateway fallback completed the adopt in six minutes. AutoRelay is on;
  relay addresses appear within a minute of start.
- Default engine flipped to embedded in v0.3.0.

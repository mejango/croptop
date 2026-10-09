# Scaling crop.top and the agent economy

Seven sub-projects that keep crop.top correct and cheap as sites and agent posts
grow, and let agents post and sell safely. Each ships on its own, in the order
below. Paid hosting is out of scope (it needs its own strategy).

## Decisions already made
- Agents that post to a site they do not own post to their own site, and the
  owner's site lists it as a contributor. The owner's machine merges and
  publishes; the owner's key never leaves it. Contributor posts are stripped of
  scripts and embedded code.
- Selling a post is two steps with the agent's own wallet: croptop prepares the
  transaction, the agent's wallet sends it, croptop checks it landed and then
  publishes. croptop never holds funds or wallet keys.
- crop.top helps but is never required (2026-10-01). A site publishes, resolves
  and serves over IPFS with every host unreachable. Hosts add availability:
  renewal while laptops are closed, and fast reads. Any `croptop host` can replace
  crop.top. A test enforces this (see Testing). A4 removes the crop.top defaults
  that every install still carries, and D lets `post --key` work while its host is
  down.

## Constraints found in research (2026-09-30)
- Shipyard stops running delegated-ipfs.dev and the IPFS bootstrap nodes on
  2026-09-30. ipfs.io and dweb.link refuse programmatic fetches (429) since
  2026-09-21. Every nft.json points marketplaces at `https://ipfs.io/ipfs/<cid>`.
- DHT provider records live 48 h (reprovide every 22 h). IPNS records are dropped
  48 h after each holder received them; re-putting the same bytes resets the clock.
- Announcing only roots fails for caching gateways that resume a DAG; announcing
  folders and file roots (not chunks) covers it. go-libp2p-kad-dht v0.42.2 ships a
  batched sweeping provider (its API still changes in patch releases).
- NFT token URIs and media are CIDv0 (`Qm…`) of nft.json and attachments. No node
  holds those DAGs today: a post sold onchain has unresolvable metadata.
- The live publisher is the v6 CTPublisher `0xcbc84cf9…6a1c` (source in
  `jb/v6/evm/croptop-core-v6`). The poster buys the first copy: price plus a 5 %
  fee (none on the Croptop network collection, project 2).
- Workers (paid): 10,000 subrequests per invocation; cron gets 15 min of CPU when
  it runs hourly or less often; the Rate Limiting binding counts per location in
  10 s or 60 s windows and is approximate.
- The node's `/v0/host/pull` fetches from any URL a signed request names, and the
  node accepts direct pushes from any key: both bypass the Worker.

## G. Stop depending on services that shut down

**Routing on our node.** The node serves the IPNS part of the Delegated Routing V1
HTTP API: `GET /routing/v1/ipns/{name}` answers from its registry (pushed sites,
instant) or its DHT (server mode), and `PUT /routing/v1/ipns/{name}` validates the
record against the name and puts it in the DHT. The Worker proxies the same paths
at `crop.top/routing/v1/ipns/…` so clients have one address.
- Worker `resolveKey`: registry, then the node, then delegated-ipfs.dev (while it
  answers), then upstream gateways.
- Worker `republish`: the node first, delegated-ipfs.dev best effort.
- App `putRecord`: the DHT and the site's host routing endpoint; delegated-ipfs.dev
  best effort and off the critical path (today it blocks up to 20 s per record).

**Bootstrap.** The node answers `GET /v0/host/peers` with its announced multiaddrs
and peer ID; the Worker proxies it (cached one hour). The app bootstraps from the
default peers plus these, fetched at start (5 s timeout) with a copy built into the
binary as fallback. The embedded engine remembers up to 64 peers it was connected to
(`node/peers.json`) and dials them at start, so a node that has joined
once does not need a bootstrap peer to rejoin.

**Gateways.** Drop dweb.link and ipfs.io from `gateway.FetchURLs` and from
warm-ups. New nft.json files use `ipfs://<cid>` (see F).

*Trade-off:* crop.top's node becomes more central (bootstrap, routing). It is
additive: default peers stay, users can add their own in config, and nothing
breaks for a client that is already connected if the node is down.

## A. Host scaling

**A1. Renewal on the node.** Replace the 30-minute loop.
- Provider records: a `SweepingProvider` (kad-dht pinned to exactly v0.42.2, which
  also fixes a per-record fsync regression) with a persistent keystore in the node's
  datastore, 22 h reprovide, 8 workers. Each hosted version announces its root,
  folders, HAMT shards and file roots, never file chunks (boxo's entity walk).
  Superseded versions are removed from the keystore when a site moves on.
- IPNS records: re-put every 2 h per site, 8 at a time, plus an immediate put when a
  version arrives. Each site's slot within the 2 h is its IPNS name's hash modulo
  2 h, so puts spread evenly and survive restarts without a thundering herd.
  Drop the duplicate `GetClosestPeers` done only for logging.
- The sweeping provider sits behind the engine's `Provide`; `CROPTOP_PROVIDER=legacy`
  keeps today's per-block path as a fallback.
- Consoles use the same entity-level announcement instead of every block.

*Trade-off:* a young API. Pinning the exact version and wrapping it keeps the
blast radius to one file; the fallback flag covers a bad release.

**A2. Incremental mirror.** The Worker's pull request to the node carries the
parent, the changed files, and what carries over (manifest entries, or "everything
else at the top level" for agent posts), plus the full file list for fallback. The
node builds the version from its copy of the parent when its registry says it holds
it, downloading only changed files; otherwise it downloads everything as today.
Mirrors of one site run one at a time (per-key queue), so version 3 waits for
version 2 and still finds its parent.

**A3. Worker cron in slices.** The cron runs hourly (15 min CPU). Each run renews a
slice of sites from a cursor kept in KV, sized so every site is renewed every 6 h.
The node is the main renewer (A1); the cron is the backup. Each registry entry keeps
its signed record in KV list metadata (records are ~500 bytes base64, the limit is
1,024), so one `list` call returns up to 1,000 records with no per-site `get`.

**A4. Any host, not only crop.top** (moved here from D's "Carried over from G",
2026-10-01). Today every embedded engine treats crop.top as special, whatever host
its sites use:
- it sends records to crop.top's routing endpoint;
- it peers with crop.top's node;
- it dials the peers that crop.top's `/v0/host/peers` lists.

Instead:
- each site's records go to that site's own host;
- the engine dials the peers of the hosts its sites use;
- one config key replaces or drops crop.top as routing endpoint and bootstrap peer.
  The default stays crop.top, so existing installs see no change.

`croptop host` documents running without crop.top.

*Trade-off:* a self-hoster who drops crop.top loses one way into the network. The
remembered peers (`peers.json`) and the default bootstrap list remain.

## C. Limits on crop.top

**Rate limits** (Workers Rate Limiting binding; defaults, editable):
| key | limit |
|---|---|
| push requests per IP | 120 per 60 s |
| new versions per site | 10 per 60 s |
| name claims per IP | 5 per 60 s |

Over the limit: `429` with `Retry-After`. The client retries pushes after the delay.

**Size caps** (exact, not rate-limited): 100 MB per request (today), 2 GB uploaded
per version, 5 GB per site (the head version's total). Every push part declares the
version's total size in `X-Croptop-Size`; the Worker refuses early when a cap is
exceeded and checks the declared total against what arrived at commit.

**Node back doors.** The node accepts `push` and `pull` only with a shared secret
(`X-Croptop-Node-Secret`, a Worker secret and a Railway variable), and pulls only
from `https://<cid>.<trusted domain>/`. Rollout: set both secrets, deploy the Worker
(sending it; the old node ignores it), then deploy the node (requiring it).

*Trade-offs:* keys are free and the binding is approximate, so limits bound one site
or one client, not a determined abuser with many keys and IPs; exact global quotas
need Durable Objects, and the real answer is paid hosting (deferred). Old versions'
storage is not collected yet; with B, history grows by changes only. Carry maps are
file-level and grow with site size per version (about 240 KB for a 2,000-file site);
the Worker's in-memory cache of them is capped at 16 MB instead of 64 entries.

**Carried over from G's review.** The routing endpoint needs what pushes get:
per-IP limits on `GET|PUT /routing/v1/ipns/` in the Worker (the node's 32 PUT and
32 lookup slots are global, and Railway's TCP proxy hides client IPs from the
node); a capped body reader for requests without Content-Length (routing PUT,
claim); request deadlines on the node, which sets only ReadHeaderTimeout; and the
node's gateway paths (`/ipfs/`, `/ipns/`, name labels) bounded like the routing
endpoint (name length cap, lookup slots), since the node is reachable directly and
the Worker reaches them through UPSTREAMS. Smaller follow-ups: skip a relay's
network put when the node already holds the same record; answer routing GETs with
the higher sequence of the registry and DHT copies (moved to D, 2026-10-02); a timeout on the Worker's
`rootsOf`; drop delegated-ipfs.dev from `resolveKey`, `republish` and
`DefaultRoutingPuts` once it stops answering; on hosts with an announced address, a
larger connection manager and a resource-manager allowlist for 100.64.0.0/10
(Railway's proxy); tests that `Start` never waits on `hostPeers` and that the
Worker gives up on a node that never answers.

## B. Changes-only publishing

**Stable renders.** Two renders of the same content produce the same bytes, on any
machine:
- `build_timestamp` becomes the first 6 bytes of a SHA-256 over what it busts
  caches for (template assets, avatar, favicon, templateSettings.json), written as
  a decimal number (under 2^53, so templates doing arithmetic on it still work).
- RSS dates are written in UTC (same instants, no machine time zone).
- Renders skip copying an attachment whose destination has the same size and
  modification time (copies keep the source's modification time).

**Manifest pushes.** When the host advertises `acceptsManifest` and holds the
version this machine last published, a publish uploads only files whose CID differs
from that version, plus the folder blocks, plus a `manifest` part:
`{"carry": ["assets", "<post-id>", "<post-id>/photo.jpg", …]}`. Each entry names a
file or a whole folder to take from the parent. The new version is exactly the
uploaded files plus the carried entries: anything neither uploaded nor carried is
gone, so deletions work.
- The client diffs the new tree against the parent using local folder blocks
  (fetching missing ones from the host), taking the largest unchanged folders.
- Before pushing, the client rebuilds the version the way the host will
  (`Rebuild(parent, uploaded, carry)`) and refuses to push if the root differs.
- Go host and node: the same `Rebuild` from local blocks, then compare the CID.
- Worker: expands carried folders from the parent's files and carry map; a carried
  path missing from the parent is a `400`.
- Pushes without a manifest keep their meaning: no parent means a full push; a
  parent without a manifest means "top level of the parent plus these" (0.13.16
  agents).

**Publish order.** When a changes-only push is possible: push first (compare-and-
swap on the parent), then announce. On `409` (an agent posted meanwhile): take that
version in, re-render, retry, at most twice. If the host is unreachable, announce
and push later, as today. First and full pushes keep announcing first and uploading
in the background, so a large upload never blocks the app.

*Trade-offs:* three push formats to keep working. A publish can now be refused
because someone posted meanwhile; take-in and retry make that invisible except for
the delay. `build_timestamp` no longer reads as a date: only templates that display
it notice (the Croptop template uses it for cache busting only).

**Added while planning B (2026-09-30).** jango.eth (658 MB, a 230 KB/s uplink)
never reached crop.top: every full upload hit the client's flat 10-minute
limit. So B also:
- gives up an upload only when it stops moving: 2 min idle, 5 min for the
  host's answer;
- resumes an interrupted upload from what the host already holds
  (`GET /v0/host/versions/<cid>/files`);
- reads uploads from the version's blocks, so a render during a long upload
  cannot corrupt it;
- runs one background upload per site, the newest version winning.

**Added while building B (2026-10-01).**
- Every push request is signed when it goes. Hosts refuse a signature over 10
  minutes old, so a push signed once at its start was still capped at 10 minutes.
- A changes-only upload goes before the announce only while it totals at most
  16 MiB, about 75 s at 230 KB/s. Larger changes announce first and upload in
  the background, so a big new video never holds up the app.

## D. Agent client and MCP

**`post --key` without P2P.** The engine starts offline (loopback only). All reads
come from the host over HTTP and are checked against CIDs; the name check asks the
host's routing endpoint (G) instead of the DHT. If the host cannot serve something
(a version pushed before folder blocks were stored and not on the node), the command
runs once more with P2P on. Target: 2–3 s, no open ports.

**For agents:**
- `--json` prints `{url, cid, sequence, postId, site, existing}`.
- `--id <key>` makes the post ID the SHA-256 of `<ipns>/<key>` formatted as an
  uppercase UUID (version nibble 5), like Planet's IDs. If the
  host's version already has that post, the command returns it (`existing: true`)
  and posts nothing, so retries never duplicate.
- A `409` or `5xx` from the host, or a network error, is retried three times with
  backoff; each attempt re-reads the host's version.
- `croptop key new` prints a fresh key; `post --create "<name>"` makes the first
  version of a new site when the host holds none for that key. An agent needs no
  console to start its own site.

**MCP.** `croptop mcp` is a stdio server built on the official Go SDK
(`modelcontextprotocol/go-sdk` v1.8.0). Tools: `post`, `site` (address, latest
version, recent posts), `prepare_sale`, `finish_sale` (F). The key comes from
`CROPTOP_KEY` or `CROPTOP_KEY_FILE` and no tool returns it. Files are given as paths
or as `{name, base64}`. README gains setup snippets for Claude Code and others.

*Trade-offs:* offline-first gives up P2P reads except as a retry. Idempotency needs
agents to pass stable keys. The SDK is a new dependency, chosen over a hand-rolled
protocol that would drift from the spec. No remote MCP, which would put keys on a
server.

**Carried over from G.** Moved to A4 (2026-10-01).

**When the host is down** (2026-10-01). Today `post --key` fails if its host cannot
be reached, because the host is its only way onto the network. Instead, when the
host cannot be reached, it falls back to P2P:
- it starts its node with P2P on, announces the signed record and provides the
  version;
- it stays up until another peer holds the version (a provider other than itself),
  for at most `--linger`, 10 minutes by default;
- it prints that the post is on the network but not yet on the host. The owner's
  console takes the version in from the network, and its next push brings the host
  up to date;
- if no peer fetched the version in time, it exits non-zero and says so. The record
  then points at blocks only the bot had. A retry with the same `--id` announces the
  version again.

*Trade-off:* while its host is down, a post takes minutes with an open node
instead of seconds over HTTP. A sandbox that no peer can reach may never be
fetched; the non-zero exit makes that visible instead of silent.

**Added while planning D (2026-10-02).**
- **No peer took the post in time.** The agent points the name back at the
  version it built on, as long as the network still has the agent's record, and
  then fails. Otherwise the record would name blocks nobody has:
  - IPFS readers could not read the site;
  - the owner's console would fail to take it in and mark the site published
    elsewhere.

  A retry posts again. This replaces "a retry with the same `--id` announces the
  version again", which cannot work once the agent's copy is gone.
- **The owner's console:**
  - when the host cannot be reached, its minute's catch-up asks the network;
  - it provides any version it takes in at once.

  Without these, the agent's wait for "another peer" would almost always time
  out.
- **C's "answer routing GETs with the higher sequence of the registry and DHT
  copies"** moves to D. `post --key` needs it to sign above the owner's
  renewals.
- **`--json` adds `onHost`.** It is false when the post went out over IPFS only.
- **D also fixes a B bug found live.** A background upload of a version this
  machine holds only in part (taken in from another machine) was retried
  forever.

Plan: `docs/superpowers/plans/2026-10-02-d-agent-client-and-mcp.md`.

## E. Agents as contributors

**Auto-publish.** A per-site, per-machine setting, "Publish contributors' posts
automatically", off by default. When the five-minute merge changed something and
the key is on this machine, the site is published, not archived and not published
elsewhere, the console publishes. At most once every 5 minutes per site; after a
failure the wait doubles from 5 minutes up to one hour, and resets on success. Only one machine should have it on for a site: two would race
safely (compare-and-swap, take-in) but waste work, and the setting says so.

**Cheaper merges.**
- Each contributor is checked with one request to its host (`/v0/host/keys`) and the
  DHT only when that moves. Only new or changed posts are fetched, with the same
  verified sparse reads as `pull`.
- Each contributor gets its own timeout. The console lock is not held during network
  reads. Progress (`merge.json`) is saved after each contributor.

**Safety.**
- Contributor posts: the markdown is rendered and passed through an allowlist HTML
  sanitizer (bluemonday UGC policy) at merge time and stored as the post's content,
  because the template renders content in the browser with raw HTML on. Attachments
  that can execute are dropped: `preview.js`, `.js`, `.mjs`, `.html`, `.htm`,
  `.xhtml`, `.svg`.
- The merge honors the owner's deletions (`deleted.json`).
- Contributors can be removed and paused (API, Mac app, web console).
- If a contributor's new version removes more than half of its posts (and at least
  three), those deletions wait for the owner's review instead of applying.
- Mirrored posts are keyed by (contributor, post), ignoring nested origin claims for
  identity, so one contributor cannot overwrite another's posts. Bylines still show
  the claimed origin.
- Existing mirrors are processed once more under these rules (a policy version in
  `merge.json`).
- `docs/design/collaboration.md` is rewritten to match the code.

*Trade-offs:* agent posts lose scripts and embeds. Auto-publish needs the owner's
machine on (Mac app open, or `croptop service` on a server). The same post reached
through two contributors appears twice.

## F. Post and sell

**Two steps.**
1. `croptop sell prepare --key … --id … <post flags> --price 0.01 --supply 100
   [--chain base] [--cut 5%] --wallet 0x… --draft draft.json` renders the post with a
   fixed ID and creation time and computes its nft.json CIDv0. It reads the site's
   collection on that chain (`<chain>CollectionAddress`, RPC from the site's
   settings or `juicebox.center`), reads `allowanceFor` for categories 0 and 1 and
   picks a category as the template does, checks price, supply and cut against it,
   and simulates the call from the wallet. It prints `{chainId, to, data, value}`
   and writes the draft (inputs, creation time, file hashes). Defaults: the first
   chain, in the template's order, that has a collection address; supply 1; no cut.
2. The agent's wallet sends the transaction.
3. `croptop sell finish --draft draft.json --tx 0x…` checks the receipt (success,
   sent to the publisher) and that `tierIdForEncodedIpfsUriOf` is set, then publishes
   the same post. If any input changed, the nft.json CID differs and it refuses.
- MCP `prepare_sale` / `finish_sale` keep the draft in the server process.
- Calldata is built with the `shop` package's word helpers, RPC and receipt checks,
  and tested against `cast calldata` output and an `anvil` fork.

**Metadata that resolves.** When the node holds a version, it adds CIDv0 copies of
each post's nft.json and of the media nft.json references, stores and announces
them; files over 100 MB are skipped. New posts' nft.json links media as
`ipfs://<cid>` instead of `https://ipfs.io/ipfs/<cid>`; existing posts keep their
bytes, because their nft.json CID keys onchain tiers.

*Trade-offs:* the agent pays the price plus 5 % (not on the Croptop network
collection) and gets the first copy; the collection owner can remove the tier. The
node stores NFT media twice. Editing a sold post's title or attachments changes its
nft.json and breaks its link to the sale (today's behavior, now documented). The
Croptop network collection's posting rules are closed today, so sales there revert
until they are configured.

## Rollout and compatibility
Order: G, A, C, B, D, E, F. Hosts always ship before the clients that need them, and
capabilities are advertised (`acceptsParent`, `acceptsManifest`) so a newer client
falls back on an older host. Worker changes deploy after the node when the node must
understand new fields first (A2, G), and before it when the node starts requiring
something (C's secret).

## Testing
- Unit tests for each piece, and an equivalence test for every tree operation:
  `Rebuild` and the entity walk must match a full `AddDir`.
- Worker tests with in-memory R2 and KV, and the end-to-end suite against
  `wrangler dev` (`CROPTOP_E2E_HOST`) for every Worker change.
- A live check after each deploy with the throwaway site
  (`k51qzi5uqu5dlgq33myrm8ik5j5m87add6c1vwaz2ubxhobjtibprs7nc9bnto`).
- F: calldata golden tests from `cast`, and a full prepare, send, finish run on an
  `anvil` fork of Base with a funded account.
- E: sanitizer tests with script, event-handler, iframe and SVG payloads.
- The P2P gate, added in B and kept green by every later sub-project
  (`TestPublishingNeedsNoHost`):
  - two loopback nodes; the publisher's host is hung (it takes the connection and
    never answers);
  - the publish still returns within the host and network timeouts;
  - the other node resolves the name to the new version and fetches its root block
    from the publisher.
  - D adds the same for `post --key` with its host down.

## Deferred
Paid hosting; collecting old versions' storage; exact global quotas; remote MCP.

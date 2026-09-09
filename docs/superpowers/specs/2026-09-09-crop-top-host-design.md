# crop.top host

A `croptop host` role that runs on the operator's server behind Cloudflare and makes
crop.top a gateway, a pin host, and a name registry, all from the embedded engine.

## Serving
- `<label>.crop.top`: ENS site `<label>.eth`, resolved through DNSLink over DoH
  (dns.eth.limo), then IPNS. `<k51...>.crop.top` and `<bafy...>.crop.top` serve raw
  IPNS names and CIDs.
- `crop.top/<name>/...`: a claimed free name, served from the registry. One origin
  for all free names; ENS sites get their own subdomain.
- `crop.top/`: directory of claimed names.
- Content is served with boxo's gateway handler over the host's own blockstore,
  so pushed sites are instant and anything else is fetched from the network once.

## Pushing
`POST /v0/host/push` with the site's IPNS name, root CID, sequence, the signed IPNS
record, and a block stream of the whole site. The request is signed with the site's
IPNS key. The host stores the blocks, remembers the record, provides the root, and
republishes the record to the DHT, pubsub, and delegated routing every 30 minutes.
A pushed record is the freshest the host knows, so `<label>.crop.top` and
`crop.top/<name>` serve it without waiting for the network.

## Names
`POST /v0/host/names` claims a free name for an IPNS key: first come, one per key,
renames release the old name. Signed with the site's key. Names are lowercase
letters, digits, and hyphens; a reserved list keeps `www`, `api`, `v0`, and the like.

## App side
Site settings gain a host URL (default https://crop.top) and a claimed name. After
each publish the app pushes to the host in the background. The gateway table gains
crop.top with the URL rules above.

## Operator
Two proxied DNS records (`crop.top`, `*.crop.top`) and an nginx server block that
proxies both to the host process with the Host header intact. No Cloudflare API.

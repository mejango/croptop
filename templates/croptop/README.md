# Croptop

A [Planet](https://planetable.xyz) site template. Every post can be bought as an NFT
through the Croptop publisher on Juicebox v5, and any visitor can deploy their own
collection (a revnet plus a 721 hook) from the `$` button in the header.

## How Planet uses this repo

- `template.json` declares the template and its settings. Planet shows `settings`
  in its UI and writes the chosen values to `templateSettings.json` next to the
  rendered site. The browser code fetches that file into the `env` global.
- `templates/*.html` are [Stencil](https://github.com/stencilproject/Stencil)
  templates. Planet renders `index.html` once, `blog.html` once per post at
  `<post-id>/index.html` (or `<slug>/`), and `simple.html` as the in-app view.
- `{{ assets_prefix }}` is `./` on the index and `../` on post pages.
- Planet also writes, per post: `article.json`, `nft.json`, `nft.json.cid.txt`
  (the CID the onchain tier is keyed by), attachments, and `_videoThumbnail.png`
  / `_audioThumbnail.png` when relevant. `planet.json` lists all posts;
  timestamps are seconds since 2001-01-01 (Apple epoch).
- `hooks/pre-commit.sh` bumps `buildNumber` in `template.json` on each commit.
  Enable it once with `git config core.hooksPath hooks` (needs `jq`).

## Files

| Path | What it does |
| --- | --- |
| `templates/base.html` | Page shell: head, theme color, `env` global, custom code slots |
| `templates/top-dom.html` | Header: avatar, nav, collection button |
| `templates/index.html` | Post grid and list, tag filters, infinite scroll, post modal |
| `templates/post-page.html` | One post: cover media, content, and the buy & collect form |
| `templates/fork-modal.html` | "Deploy your own collection" form and Relayr multi-chain status |
| `templates/blog.html` | Standalone post page wrapping `post-page.html` |
| `assets/scripts/chains.js` | The chain table. Add or change a chain here and nowhere else |
| `assets/scripts/utils.js` | Formatting, markdown, IPFS CID encoding, bendystraw queries |
| `assets/scripts/tx.js` | Providers, signing, ERC-2771 forwarding, Relayr bundles |
| `assets/scripts/txs/*.js` | Contract addresses, ABIs, and the `tx_*` calls |
| `assets/scripts/croptop.js` | `window.croptop`: the runtime widgets attached to posts can use (site, post, env, chains, wallet, ipfs) |
| `dev/` | Render and serve the template locally without Planet |

## Local development

```
python3 -m venv dev/.venv && dev/.venv/bin/pip install jinja2   # once
dev/.venv/bin/python dev/serve.py                                 # http://localhost:8642
```

`dev/serve.py` renders `templates/` with jinja2 (the Stencil subset in use is
translated on the fly) against the fixture site in `dev/fixture`, and
re-renders on every request when anything changed. To render a site Planet has
already published, pass `--site` with its folder under
`~/Library/Containers/xyz.planetable.Lite/Data/Documents/Planet/Public/`.
Put your own collection addresses in `dev/settings.local.json` (gitignored) to
exercise the buy form against a real collection.

To exercise deploys and buys without a wallet, load `dev/simchain.js` in the dev
site's console (instructions in its header). It installs a fake wallet and routes
Sepolia reads through `eth_simulateV1`, so transactions the buttons send become
simulated state that later reads and transactions see; nothing is broadcast.
Set `sim.signer` to switch accounts.

## Onchain (Juicebox v6)

Addresses come from the v6 deployment artifacts in `~/Documents/jb/v6/evm`
(`<repo>/deployments/<chain>/<Contract>.json`). `assets/scripts/abis.js` is
generated from those artifacts and trimmed to the functions this template calls.
Addresses are the same on every supported chain unless noted. Supported chains
are listed in `assets/scripts/chains.js`.

| Contract | Address | Used for |
| --- | --- | --- |
| CTPublisher | `0xcbc84cf9b0293efe3ac7dd1bea128a404f2e6a1c` | `tiersFor`, `allowanceFor`, `mintFrom` |
| REVDeployer | `0xb552eb94284f94b833837d4b2cbb237128415d4e` | `deployFor` (revnet + 721 hook + posting rules) |
| JBProjects | `0x6017d1fba9dc279bfa0b03fd931c22e242ab3691` | `creationFee()`, sent as value with each deploy |
| ERC-2771 forwarder | `0x3ba60b60933916a7c87d0860dcee62a0ce34e3e2` | multi-chain deploys via Relayr |
| Sucker deployers | see `SUCKER_DEPLOYERS` in `croptop_deployer.js` | one per chain pair; L2 pairs exist on mainnets only |
| Croptop network collection | `0x779957e5376571adaf544e63ffc8404e98f30d30` mainnets, `0x2b70edc0b7db710f7d8708c6240d6fa9e62b71f5` testnets | buys into it pay no fee |

A shop has up to two posting categories, set at deploy from the `$` form:
category 0 is the revenue manager's own posts (allowlist = their address, free
minimum, no copy limit, any split), category 1 is everyone else's, only if the
operator enables it, with its own minimum price, copy range, max cut, and
optional allowlist. Whoever adds a post buys its first copy and sets that post's
price, copy count, and optionally a cut: a percentage of every sale of that post
routed to an address they choose (`splitPercent` + one `JBSplit`, out of
`SPLITS_TOTAL_PERCENT`), capped by the category's `maximumSplitPercent`. The
buy form picks the category that admits the connected wallet (`postingRulesFor`
in post-page.html). Each buy mints one copy: v6's
publisher rejects the same post twice in one `mintFrom` batch. Buys into any collection other
than the network's pay `price / 20` extra, matching the publisher's
`FEE_DIVISOR`. The fee reward goes to the `curatorAddress` template setting,
falling back to the buyer. New collections use the native token for accounting,
tier pricing, and revnet base currency, so no price feed is involved.

Header stats come from [bendystraw](https://bendystraw.xyz) (Juicebox's indexer),
filtered to `version: 6`. The owners count is capped at 1000 by the query limit.
Stat links go to `revnet.money/<chain>:<projectId>`.

Verification status: reads (`allowanceFor`, `tiersFor`, `projectId`,
`creationFee`) were checked against Sepolia, and a single-chain `deployFor` with
the form's exact calldata was simulated with `eth_call` on Sepolia and returned a
revnet id and hook address. No real buy or deploy transaction has been sent from
this template yet. To re-run the simulation from the dev site's console, build
arguments with `buildRevnetDeployArgs(...)`, encode with `REV_DEPLOYER_ABI`, and
`eth_call` them with `creationFeeOn(chainId)` as value.

## Vendored libraries

ethers 6.13.7, markdown-it 13.0.1, markdown-it-task-lists 2.1.1,
markdown-it-anchor and markdown-it-footnote (version unlabeled), bs58
(browserify bundle, version unlabeled). Re-vendor from npm to pin the last three.

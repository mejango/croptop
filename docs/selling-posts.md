# Selling a site's posts into a Juicebox project

"Configure site X to sell (buy) posts to project Y on all chains" means: point
every per-chain `<chain>CollectionAddress` template setting of site X at
project Y's Juicebox v6 NFT hook (its JB721TiersHook), publish the site, and
make sure the Croptop publisher is allowed to add posts to that hook. Buyers
then pay into project Y and receive its NFTs and tokens. This doc is the
recipe; the app's Site settings → Money screen does the wallet half.

## Vocabulary

| Term | Meaning |
| --- | --- |
| collection, shop, hook | The same thing: a project's JB721TiersHook. The template setting is `<chain>CollectionAddress`; the app calls it a shop. |
| CTPublisher | `0xcbc84cf9b0293efe3ac7dd1bea128a404f2e6a1c` on every chain. `mintFrom` buys a post, `allowanceFor(hook, category)` reads the posting rules, `configurePostingCriteriaFor` sets them. |
| category | A posting rule set on the hook. The built-in template checks 0 and 1 and picks whichever admits the connected wallet. No rule (`minimumTotalSupply == 0`) means nobody can post and the site says "This site isn't accepting posts yet." |
| REVOwner | `0x2ba4705ad0332cdfb299b452068438bcba3faaf3`. Owns every revnet's project NFT and hook. `tiered721HookOf(projectId)` returns the hook. The revnet operator holds `ADJUST_721_TIERS` (24) from it. |

Project ids are per chain in general; the projects deployed by
`deploy-all-v6` share ids across the four production chains. Known ones on
Ethereum, Optimism, Arbitrum and Base (v6): 1 NANA, 2 CPN (the Croptop
network), 3 REV, 4 BAN. Verify rather than trust a name: the deploy scripts
under `~/Documents/jb/v6/evm/deploy-all-v6/script/*.s.sol` carry
`_CPN_PROJECT_ID = 2`, and `JBTokens.projectIdOf(token)` settles it on chain.

## 1. Resolve the hook on each chain

Artifacts first: `~/Documents/jb/v6/evm/deploy-all-v6/deployments/<chain>/JB721TiersHook__Project<NAME>.json`
has `address`. For anything not in the artifacts, read it on chain (Foundry's
`cast` is installed; RPCs are `https://juicebox.center/v1/rpc/<chainId>`, the table in `assets/scripts/chains.js`):

```sh
RPC=https://juicebox.center/v1/rpc/1
REVOWNER=0x2ba4705ad0332cdfb299b452068438bcba3faaf3
cast call $REVOWNER 'tiered721HookOf(uint256)(address)' <projectId> --rpc-url $RPC   # revnets
# Ordinary projects: JBRulesets.currentOf(projectId).metadata, data hook = (metadata >> 82) & (2^160 - 1)
cast call <hook> 'projectId()(uint256)' --rpc-url $RPC                                # must equal projectId
```

CPN: `0x779957e5376571adaf544e63ffc8404e98f30d30` on all four mainnets,
`0x2b70edc0b7db710f7d8708c6240d6fa9e62b71f5` on the four Sepolias. Buys into
CPN pay no publisher fee.

## 2. Check the posting rules

```sh
PUB=0xcbc84cf9b0293efe3ac7dd1bea128a404f2e6a1c
cast call $PUB 'allowanceFor(address,uint256)(uint256,uint256,uint256,uint256,address[])' <hook> 0 --rpc-url $RPC
cast call $PUB 'allowanceFor(address,uint256)(uint256,uint256,uint256,uint256,address[])' <hook> 1 --rpc-url $RPC
```

Returns `minimumPrice minimumTotalSupply maximumTotalSupply maximumSplitPercent allowedAddresses`.
All zeros means unconfigured. Who can fix it: an address with
`ADJUST_721_TIERS` from the hook owner for that project id.

```sh
PERMS=0xf92ac1ab5a00033e35a3975739124f61928c36b0
cast call $PERMS 'hasPermission(address,address,uint256,uint256,bool,bool)(bool)' <wallet> <hookOwner> <projectId> 24 true true --rpc-url $RPC
```

For CPN the operator is `0x240dc2085caEF779F428dcd103CFD2fB510EdE82` (from the
deploy script; confirmed with `hasPermission` on Ethereum and Base). As of
2026-09-19 CPN has no posting rules on any chain and the publisher has no
permission on it.

## 3. Point the site at the hook

Through the running console (`http://127.0.0.1:8086`), PATCH merges keys:

```sh
SITE=<site uuid>   # ls ~/Library/Application\ Support/croptop/sites
curl -X PATCH -H 'Content-Type: application/json' http://127.0.0.1:8086/v0/croptop/sites/$SITE/settings -d '{
  "ethereumMainnetCollectionAddress": "0x...", "optimismMainnetCollectionAddress": "0x...",
  "arbitrumMainnetCollectionAddress": "0x...", "baseMainnetCollectionAddress": "0x...",
  "ethereumSepoliaCollectionAddress": "0x...", "optimismSepoliaCollectionAddress": "0x...",
  "arbitrumSepoliaCollectionAddress": "0x...", "baseSepoliaCollectionAddress": "0x..."
}'
curl -X POST -H 'Content-Type: application/json' -d '{}' http://127.0.0.1:8086/v0/croptop/sites/$SITE/publish
```

While there, check the `<chain>RPC` keys: old sites carry llamarpc URLs, which
fail CORS preflight in browsers, so the header shows `Balance: $0`, `Owners: 0`
and posts never load. The template's defaults in `template.json` are
`https://juicebox.center/v1/rpc/<chainId>` (CORS `*`, all eight networks; its
`eth_getLogs` wants hex block bounds and at most 50,000 blocks per call).
Only `ethereumMainnetCollectionAddress` has a default; set the other chains
explicitly when a site should sell on all of them. The `collectionCategory`
key is ignored by the built-in template (it reads rules on chain).

The same settings are editable in the app: Site settings → Money, one address
per network, then Publish.

## 4. Open posting (needs the wallet)

Nothing sells until step 2 shows a category with `minimumTotalSupply > 0`.
The app does this: Site settings → Money → **Set up posting** → choose the
network and enter the project id. The page resolves the hook on every chain
through JBDirectory/REVOwner and the sucker registry, reads the current rules,
and signs the missing pieces through the Juicebox ERC-2771 forwarder and
Relayr: a `ADJUST_721_TIERS` grant to the publisher when the project lacks it,
then `configurePostingCriteriaFor` per chain. An unconfigured category gets
one copy minimum, unlimited copies, a 10% poster cut and an open allowlist.
Details and recovery rules: `docs/shop-creation.md`.

The signer must hold the permission from step 2. For a revnet that is its
operator; for CPN, `0x240d…`. Nobody else can do this part, so when the
operator is not at the keyboard, stop after step 3 and say exactly which
wallet has to sign.

## Worked example (2026-09-19)

CROPTOP (`DFF00C4E-05B8-4DD3-8CF4-95DF628F4C95`, https://crop.top) was pointed
at CPN on all eight networks, its RPCs moved to juicebox.center, and published. Posting is still closed until the CPN operator runs Set up
posting for project 2.

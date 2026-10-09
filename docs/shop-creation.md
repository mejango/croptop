# Shop creation

In the browser console, open Site settings → Shop → Create shop. In the macOS app, Shop is under Site settings → Money. Both open the same local browser wallet page, with the saved site name prefilled. Existing addresses can still be entered manually.

Enter a name, token ID, revenue wallet, minimum post price and networks. Review creation fees plus estimated gas, then approve each network in the wallet. Every network is tracked independently. Confirmed addresses refresh in the settings window; use its normal Publish action to publish the changes. No automatic publication occurs.

## Deployment and verification

The wallet page uses the built-in template's `buildRevnetDeployArgs`, v6 ABI, chain table, creation-fee reader and wallet switching helper. `croptopRevnetStages()` now supplies the same economics to the website form and node flow. It signs direct transactions one network at a time; it does not use Relayr bundles. A mainnet or testnet set must have bridge deployers for every selected pair. Unsupported testnet L2-to-L2 pairs fail before a signing intent is created.

Before approval, the page checks deployer code, reads the current creation fee, simulates the exact calldata and estimates gas. A fee change can be repriced for an unattempted network or after a finalized revert/replacement; uncertain transactions retain their original intent.

The node reads receipts using pinned RPC endpoints, checks the RPC chain, sender, nonce, target, exact input and value, canonical block and finalized height. It accepts only `HookDeployed(uint256,address,address)` from the configured v6 JB721TiersHookDeployer, with REVDeployer as caller. It rejects malformed, missing, duplicate and removed events, checks deployed hook code and `projectId()`, and saves only that chain's address. A finalized transaction with different input/target/value is a replacement, not a successful deployment.

The browser remains the trusted transaction-construction and signing surface. The node pins the submitted intent; it does not reimplement the full ABI argument builder. Wallets retain keys. Only injected Ethereum wallets with normal transaction/nonces are supported; native WebViews, WalletConnect and account-abstraction user-operation receipts are not integrated.

## Session security and recovery

Sessions are bound to a site UUID and a random session ID, with a 256-bit capability. The launch URL carries the capability in its fragment, which the page moves to session storage. Callback requests require the capability and session/site match. The node rejects cross-origin and opaque-origin requests, and requires a custom request header. Status responses never include the capability. The wallet page has a restrictive CSP, no framing, no referrer and no site-authored scripts.

Local public site and feed documents receive a CSP sandbox without `allow-same-origin`. This isolates author-controlled scripts from the console and its session-launch endpoint. Public assets allow anonymous CORS reads so these previews can still render. Published sites are unchanged; browser storage and privileged local-node access are unavailable inside local previews.

Progress is stored with atomic rename and file/directory sync in private `shop-sessions/<site-id>.json` under the node data root. It is outside the public site trees. The journal preserves the salt, start time, exact calldata/value and all attempts. It records sender and nonce **before** requesting a wallet signature and the transaction hash immediately after broadcast. Session storage holds a second copy of each returned hash.

- Explicit wallet rejection: resume with the same sender and nonce.
- Lost response or app restart during signing: review remaining networks to resume with the same sender, nonce and calldata, or supply the transaction hash from wallet history. Never switch to a new nonce merely because a hash is missing.
- Pending or replaced transaction: use Check transaction; a replacement hash must match the recorded sender and nonce. Only a finalized revert/replacement permits a new nonce.
- Partial completion: save each finalized network independently and resume remaining networks.
- Crash between confirmation and settings save: repeating verification repairs the apply. Replays do not overwrite a subsequently connected manual address.
- Settings edited during deployment: preserve unrelated fields. A conflicting manually changed collection address is reported; the confirmed address remains visible for manual connection.

Automatic checks run while the wallet page is open. After restarting the node or browser, reopen Create shop. A lost transaction hash may require wallet history, especially if a repeated simulation now reverts because the original transaction already deployed the shop. Finality can take substantially longer than first inclusion. An unavailable RPC or unsupported finalized-block query leaves progress recoverable without saving an unverified address. There is one active deployment per site; a completed session remains available for inspection.

Settings clients send PATCH requests containing only edited keys. Confirmation merges its address into current settings, updates the site's `updated` timestamp and rerenders it. If the active template has a `collectionCategory` setting, new shops use open-posting category 1; the built-in v6 template selects the category from onchain posting rules and has no such setting. The maintenance banner remains a separate setting.

## Validation (2026-09-13)

The local v6 deployment artifacts match the configured REVDeployer, JB721TiersHookDeployer and JBProjects addresses on all eight networks. Read-only RPC checks found code at all three addresses on each network and a 0.0001 ETH creation fee at that time. An `eth_call` on Sepolia with the new flow's exact shared-builder calldata successfully returned a project ID and hook address. No real deployment or paid transaction was sent. The upstream [v6 deployer](https://github.com/rev-net/revnet-core-v6) and [721 hook deployer](https://github.com/Bananapus/nana-721-hook-v6) are the protocol sources.

Tests:

```sh
go test ./internal/shop ./internal/server ./internal/store ./internal/render
swift test --package-path apps/macos
PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs node --test web/shop.test.mjs
```

Go tests exercise chain/address mapping, capability/origin checks, receipt validation, finality, replacement/revert handling, nonce retention, partial completion, persistence/reload, interrupted settings apply and replay protection. The Playwright test runs the real ABI/builder with mocked wallet and node responses, covering review before signing, rejection, retry, distinct per-chain results and reload recovery. The macOS app compiles and its 70 existing tests pass. Real wallet-extension interaction and funded multichain deployment remain untested.


## Connecting existing v6 shops

Enter the shop's NFT hook address in Site settings → Shop (Money in the macOS app), then choose **Set up posting** for its network. The wallet page reads the hook owner, project ID, pricing context, the publisher's current permission, and category posting criteria. The default is category 1, which the built-in template uses for audience posts. A stored explicit category is retained. The template still discovers its category 0/1 posting rules on chain.

Two separate operations may be needed:

1. Grant the pinned CTPublisher `ADJUST_721_TIERS` (24), scoped to the hook's resolved owner and project ID. Preserve every existing permission in that project's bitmap. No wildcard or ROOT grant is added. The caller must be that owner or an eligible ROOT delegate.
2. Call `CTPublisher.configurePostingCriteriaFor` for the selected hook/category. The caller needs `ADJUST_721_TIERS` from the hook owner. Changing price retains existing minimum/maximum supply, maximum split percent, and allowed posters. A previously unconfigured category gets at least one copy, unlimited maximum copies, up to a 10% poster split, and an open allowlist. The review displays these rules. Price uses `pricingContext()` currency and decimals, with the contract's uint104 bound.

`REVDeployer._deployRevnet` grants the publisher permission only when deployment includes nonempty Croptop `allowedPosts`. The setup checks the actual grant, rather than treating every revnet as authorized. An authorized revnet operator can configure criteria without repeating the publisher grant. If a revnet lacks the grant and its operator cannot manage permissions, setup explains the limitation instead of trying an impossible transaction.

Contract sources inspected under `~/Documents/jb/v6/evm`:

- `croptop-core-v6/src/CTPublisher.sol`: `configurePostingCriteriaFor`, `allowanceFor`.
- `croptop-core-v6/src/structs/CTAllowedPost.sol`: exact posting tuple widths.
- `nana-721-hook-v6/src/JB721TiersHook.sol`: `adjustTiers`, `pricingContext`.
- `nana-ownable-v6/src/JBOwnable.sol`: dynamically resolved hook ownership.
- `nana-core-v6/src/JBPermissions.sol` and `abstract/JBPermissioned.sol`: account/project scope, ROOT and wildcard behavior, replacing permission bitmaps.
- `nana-core-v6/src/structs/JBPermissionsData.sol`: `uint64 projectId`, `uint8[] permissionIds`.
- `nana-permission-ids-v6/src/JBPermissionIds.sol`: permission 24.
- `revnet-core-v6/src/REVDeployer.sol` and `REVOwner.sol`: conditional publisher grant and operator permissions.

Pinned addresses match the v6 deployment artifacts: CTPublisher `0xcbc84cf9b0293efe3ac7dd1bea128a404f2e6a1c`, JBPermissions `0xf92ac1ab5a00033e35a3975739124f61928c36b0`, REVOwner `0x2ba4705ad0332cdfb299b452068438bcba3faaf3`. The hook and publisher must both report the pinned permissions contract. Other protocol versions are rejected.

Connection sessions are separate from creation sessions and scoped to site/network. The node constructs the exact calldata and saves each wallet intent before signing. A rejected or unknown signature retains its sender and nonce. Status refresh does not replace the review snapshot: changed ownership, permissions, or criteria requires another review. Before connecting, the node verifies exact transaction receipts when present, then rereads both finalized and latest permission/criteria state from the pinned chain RPC. Concurrent edits to the target address or category are rejected; unrelated settings remain intact. Connecting does not publish the site automatically.

Validation: Go tests cover normal projects and revnets, permission preservation, non-ETH pricing precision, current/finalized state, capability protection, rejection and same-nonce recovery, and settings persistence. ABI fixtures are encoded independently with ethers using the deployed v6 ABIs. Browser tests exercise both wallet flows under the page CSP, reload recovery, mobile layout, and compact price fields with mocked wallet/RPC responses. Real signatures remain in the user's wallet.

A read-only Ethereum check on 2026-09-14 against the template's existing default hook (`0x779957e5376571adaf544e63ffc8404e98f30d30`, project 2) returned REVOwner ownership, ETH/18-decimal pricing, no CTPublisher permission, and unconfigured category 1. This confirms why the revnet path must inspect permission rather than assume the grant. No transaction was submitted.


## Posting setup across networks (build 1133)

Site settings now has one **Set up posting** button below the addresses. It sends every nonempty collection address in the selected production/testnet family to `/shop-setup`. It uses the entered addresses, not automatic project-peer discovery. Production and testnets cannot be mixed. An unfinished legacy direct transaction opens its original recovery page before a new bundle can start.

The private bundle session persists at `shop-sessions-setup/<siteID>.json`. Reviewing reads each hook's owner, project, price currency and posting criteria. All selected hooks must use the same price currency. Missing permission 24 and the allowance update become consecutive EIP-712 requests to the pinned Juicebox ERC-2771 forwarder. Ordinary projects may require two signatures per chain; already-authorized revnets need only the criteria signature. The native creation page still uses direct transactions; the existing template creation page uses Relayr.

Each destination is one `executeBatch` with consecutive forwarder nonces and a zero refund receiver. Gas is estimated per inner call with headroom. Both destinations must trust the forwarder. The backend derives all calldata and simulates the inner calls and signed batch. Requests expire after one hour. The plan is saved before signing, each signature is saved before quoting, and reloads reuse the same authorization. A fresh plan cannot replace live authorizations: every affected chain must finalize past their deadline. Recovery recalculates only missing setup, preserving completed chains' rules.

Relayr receives the signed batches at its fixed prepaid endpoint, with virtual nonces disabled because there is one batch per destination. The page offers one funding network from the same network family. Payment target, native token, exact bundle UUID/calldata, deadline, amount and payment contract runtime hash are validated before the wallet opens. The funding wallet and nonce are saved first. An unknown wallet result cannot trigger a fresh payment; its saved nonce or transaction hash must be checked. Only explicit rejection clears an unbroadcast funding intent.

Relayr status is never evidence that setup succeeded: a zero-value inner call can fail even when the batch receipt succeeds. The node reads both finalized and current permissions and exact expected criteria for every hook. It displays per-chain progress and connects the addresses only after all are confirmed, preserving unrelated settings and refusing conflicting edits. Funding receipts are independently checked for exact sender, nonce, target, calldata, amount, canonical block and finality. Setup does not publish the site automatically.

Validation: core and server tests cover multi-chain plans, ordinary/revnet differences, ABI bytes compared with ethers, preserved rules, malformed payment quotes, capability checks, pending recovery and partial finality. The browser test covers signature rejection/reload, a single funding payment after wallet rejection, funding-chain choice and partial completion on mobile. Read-only checks on 2026-09-14 confirmed both targets trust the forwarder and the pinned Relayr runtime hash matches on all four production chains. A funded Relayr transaction was not performed by these tests.


## Project lookup (build 1134)

The posting setup page initially asks only for a network and project ID. It hides the price and per-network cards until lookup succeeds, and uses generic network-fee wording in its controls. The button can open without any shop addresses entered.

Discovery uses the pinned v6 JBDirectory to find the controller and current ruleset's data hook. For REVOwner, it resolves `tiered721HookOf`; for ordinary projects it validates the data hook as the NFT shop. It checks the hook's project ID and v6 permission contract. Registered active suckers supply peer chains and peer addresses. Each remote peer must belong to its claimed project in the registry and point back to the source chain and sucker. Traversal handles differing IDs across networks, rejects conflicting or unsupported peers, and reports lookup failure rather than silently omitting a network. No indexer or matching-address heuristic is used.

Changing the selection hides the previous results until the next lookup succeeds. An active signed setup retains its recovery view. Core tests cover ordinary/revnet hooks and differing cross-chain project IDs, and reject nonreciprocal peers. A live read-only lookup of Ethereum project 2 resolved and verified its four production networks on 2026-09-14.

# Mobile posting: website and native companions

Status: release scope proposed on 2026-10-09. The user selected the mobile website and small native companion apps. This document scopes their delivery; it does not record an implementation or deployment.

## Outcome and release order

An existing Croptop author can take a screenshot on iPhone or Android and publish it without an agent, a local network connection, or an awake computer. Connecting the site is a one-time setup; normal posting works over cellular internet.

One hosted publishing service serves three clients: the mobile website, an iPhone companion with a Share extension, and an Android companion with a share-receiving activity. Devices hold the publishing key. The service prepares the files and submits only the update the device has signed.

| Milestone | User outcome | Scope boundary |
|---|---|---|
| M0: internal foundation | A test phone can authorize a real update without the desktop | Shared publisher, connection protocol, compatibility checks, and native lifecycle proof |
| R1: mobile website | Open Croptop, choose screenshot, add optional caption, Publish | Existing sites; one image per post; local drafts; connection and recovery |
| R2a: iPhone companion | Screenshot or Photos → Share → Croptop → Publish | Small native app plus an actual Share extension |
| R2b: Android companion | Screenshot or Gallery → Share → Croptop → Publish | Small native app plus share-receiving activity |

R2a and R2b have the same product scope and can be built in parallel after the shared contract stabilizes. They may ship independently when their device and distribution checks pass. R1 does not wait for app-store distribution. M0 includes a small iPhone extension experiment so R1's API can support interruption and later signing.

## Shared decisions

### Publishing and ownership

- Hosted publishing is opt-in. Initial launch supports crop.top-hosted sites with a verified compatible Croptop template. The protocol should allow other service origins later; arbitrary user-supplied upstream URLs are not part of the first hosted endpoint.
- Connection records explicit service consent as well as verifying published hosted-storage policy. Recheck service enablement, destination, and the current published policy before commit. Provide a signed **Stop phone posting** action for this service, which stops uncommitted work. An offline/unpublished desktop storage change cannot be observed remotely; document this limitation rather than implying immediate cancellation. Service suspension does not revoke the root key or undo a committed post.
- Site keys are imported/transferred to the client and used there. The service must not receive or store private keys. Browser keys belong to a dedicated trusted origin, proposed `app.crop.top`, which never serves arbitrary site HTML, gateway paths, or third-party scripts.
- The existing IPNS key grants full site authority. This release does not introduce independently revocable, posting-only phone keys. “Remove site from this device” deletes its local connection; it is not remote revocation and cannot invalidate another copy of the key. The existing publisher or a key backup remains the recovery source.
- The hosted frontend and publisher are trusted software. Keeping the private key out of the service does not make browser-delivered code or server-prepared content trustless. Clients validate the signed proposal against the intended site, draft, media, and parent; a protocol/security review defines what is verified before signing.
- No blockchain transaction, wallet connection, managed account, or recurring payment is introduced in these releases. Operating cost and abuse limits must be measured before opening the service beyond the pilot.

### One publishing contract

The shared core owns post identity, canonical draft/media representation, template compatibility, storage policy, and conflict handling. Platform adapters own photo selection, local key access, local draft storage, and display. A versioned service contract exposes supported formats and limits so clients do not invent their own rules.

Persist a stable operation ID and post ID before submission. The service authenticates the site before expensive work, accepts bounded uploads, prepares an immutable proposed update, and returns the data needed for device signing. Commit verifies signatures bound to that exact proposal. An authenticated status lookup recovers the outcome after a lost response. A repeated operation must return its result or resume it, rather than create another post.

A concurrent desktop, agent, or phone post must survive. A stale parent causes re-preparation on the latest version with the same draft identity and fresh device authorization. Do not force an overwrite. A commit receipt must remain discoverable even if another post has already advanced the site head.

Keep existing host wire signatures and parent semantics. The host's current authorization window is ten minutes; delayed/rebased work can need another device signature. The service cannot refresh one on its own. Refresh signing requires the same retained operation, not a new post, and an expired staging job must reconcile any previous commit before rebuilding. Define retention and recovery windows before the pilot.

The operation lifecycle distinguishes local draft, preparing/uploading, awaiting device signature, committing/unconfirmed, published, and recoverable failure. User copy can be simpler, but “Published” requires a confirmed commit and a usable post link. Neither an uploaded image nor a background task being scheduled is publication. No exact-once claim may rest only on disabling the Publish button.

### Connection and first-use experience

1. From an existing Croptop publisher, choose **Connect phone**. Explain and record hosted-storage consent if needed; complete the first hosted publication and compatibility check before indicating readiness.
2. Open a short-lived pairing link or scan its QR code on the phone; confirm the site and matching connection code on the existing publisher. Transfer the key through an encrypted, single-use exchange. No raw key in a URL, QR code, request log, or analytics event. Specify and review the pairing protocol during M0.
3. Offer **Import site key** as the fallback for someone who already has the key, including an author currently posting through an agent. This import stays local. It can connect directly when the published site passes hosting and compatibility checks; otherwise show the exact setup/republish action needed.
4. Show the connected site identity and a ready-to-post composer. After setup, the original computer may sleep.

R1 supports one connected site per browser/app installation. Switching sites must preserve the original destination on existing drafts and prevent accidental posting to the new site. Multi-site libraries and cross-device draft sync are deferred.

Browser setup does not automatically provision native app storage. R2 uses the same connection protocol or local key import inside the native app. A browser-stored, non-exportable key must not silently become exportable to ease migration; pairing again from the original publisher is acceptable.

### Image scope

One still image per post. Include PNG/JPEG/WebP screenshots and normalize HEIC/HEIF input into a browser-compatible image before publication. Test real iPhone SDR and HDR screenshots; Apple documents PNG for SDR and HEIF for HDR, so PNG-only testing does not establish iPhone screenshot support. [Apple screen-capture formats](https://support.apple.com/en-qa/guide/iphone/iph2d2500abc/ios)

Proposed pilot caps are 20 MiB of input and 40 megapixels decoded, subject to M0 device/decoder measurements before freezing the contract. Enforce file type, byte, pixel, time, and memory bounds in the owning service module and publish the resulting limits to clients. Preserve readable screenshot text and orientation, omit location metadata, and show the normalized preview before signing. Reject corrupt/unsupported files without losing the draft. Do not accept HTML/SVG as a shortcut to supporting every `image/*` type.

## M0: foundation and risk retirement

Deliverables:

- Refactor `Publisher.Post` into preparation and signing/submission boundaries with its current CLI behavior preserved and existing tests green before adding mobile behavior.
- Establish authenticated prepare/sign/commit/status operations with durable recovery, expiry/cleanup of unfinished staging, retry identity, and parent-conflict checks. Keep draft media private until publication; sharing a public staging URL is not draft storage.
- Add a compatibility handshake. The current public metadata does not identify the exact template source/build, so do not infer compatibility from a name alone. Put the compatibility descriptor inside the existing signed, CID-verified publication emitted by a supported desktop/CLI bootstrap; no separate signature format is needed. Legacy sites require verification and a one-time bootstrap publication before mobile-ready status.
- Verify the current desktop catches up after phone posts and preserves its edits, deletions, and storage choice. Resolve interaction with pending storage work through a focused release integration; do not ship the entire dirty worktree incidentally.
- Prove one-time connection, local Ed25519 import/signing in Safari and Chrome, and recovery when local browser storage is unavailable or cleared.
- Prove image normalization on real screenshot fixtures, including HEIF.
- Build a throwaway iPhone Share-extension slice: receive image, retain draft, sign locally, commit, and recover after termination. Also prove Android image intake and native key access.

Exit gate: the shared API can survive termination between any two phases without losing a confirmed post or duplicating it, and both native clients can use the same contract. This is the point to estimate implementation dates from measured work; no calendar promise is part of this scope.

Mandatory recovery fixture: the host commits, the response and service-journal update are lost, another author advances the head, and the service restarts. Status/retry must still recover the original post. Also test expired signatures, client clock skew, and service suspension racing an uncommitted operation. Treat already committed work as published even if its confirmation arrives after suspension.

## R1: mobile website

User-facing scope:

- Dedicated mobile composer on the trusted app origin, linked from the Croptop product/download entry point and Connect phone flow.
- Connect one existing site through pairing or local key import; show useful missing-host, incompatible-template, unsupported-browser, and reconnect states.
- Photo-library picker visible immediately. Preview, replace/remove image, optional caption, optional title under More, and one primary **Publish** action. Set the selected image as hero through the shared post builder.
- Save a local draft and recover it on reopening. Expose storage failures rather than claiming the draft was saved. Browser data is not the sole key backup; persistent-storage requests can help, but site data can be removed. [WebKit storage policy](https://webkit.org/blog/14403/updates-to-storage-policy/)
- Clear preparation/publication progress, retry, and published link. Preserve the draft and operation identity through a dropped connection or uncertain commit result.
- Home-screen installation guidance. The core posting path works in the ordinary browser and does not require installation or access to the system Share menu.

Release gate: an author starting with no mobile editor completes setup, puts the desktop to sleep, selects a screenshot on a physical iPhone and Android phone over cellular, and gets one working public post. Cover page reload, network loss before/after commit, repeated taps, simultaneous desktop/agent posting, cleared site data, and service restart. Assert existing site files remain intact, template output is compatible, desktop catch-up succeeds, and the private key never enters requests/logs. Run browser regressions in WebKit and Chromium plus real-device picker tests.

Distribution: limited pilot including kmac, then wider web release after real posting and recovery are demonstrated. The desktop/CLI connection/bootstrap update is part of this release, not a prerequisite users must discover separately.

## R2a: iPhone companion

Main app scope: connect/import site; connected-site identity; photo picker and compact composer; local drafts; pending/failed/published operations; retry; open/copy post link; remove local connection. Use native key storage and the same service. Do not embed the full desktop editor or run an IPFS node.

Onboarding includes finding/enabling Croptop in the system Share menu. A draft captured before connection must survive setup and require confirmation of its destination before publication. The same acceptance rule applies to Android.

The Share extension shows a thumbnail, optional caption, destination, Publish, and Save draft. It handles screenshots and single images from Photos/Files. It owns enough UI and publishing logic to perform the flow itself. Apple's documented extension lifecycle allows termination after completion; background transfer support is not permission to assume the entire multi-phase signing flow will execute unattended. [Apple Share extension](https://developer.apple.com/library/archive/documentation/General/Conceptual/ExtensibilityPG/Share.html)

Retain drafts in the shared App Group container and share the site's key with the extension through a narrowly scoped Keychain access group. Persist operation state before dismissal. If setup is missing, retain the image and tell the user to connect their site in the app. Do not depend on automatically launching the containing app from a Share extension. [Extension shared storage](https://developer.apple.com/library/archive/documentation/General/Conceptual/ExtensibilityPG/ExtensionScenarios.html), [extension communication](https://developer.apple.com/library/archive/documentation/General/Conceptual/ExtensibilityPG/ExtensionOverview.html), [Keychain sharing](https://developer.apple.com/documentation/xcode/configuring-keychain-sharing)

Release gate: real-device sharing from screenshot preview, Photos, and Files; cold app; share before connection; offline capture; image available only in iCloud; close/terminate during prepare, signing, or commit; reopen/retry; and one published post per operation. If device execution is required to finish an interrupted post, show a pending state and resume on opening the app. Do not promise guaranteed background completion.

Distribution: TestFlight pilot, then App Store release after physical-device acceptance and normal distribution preparation. Store review timing does not block R1 or R2b.

## R2b: Android companion

Match R2a's main-app scope and service behavior. Receive a single image through `ACTION_SEND`, display the review composer, and require the user's Publish action. Support launch from the app's own photo picker as well. Android documents the receiving activity/intent model and review before use. [Android sharing](https://developer.android.com/develop/ui/compose/sharing/receive)

Copy incoming image bytes into owned draft storage while the temporary URI permission is valid. Protect the imported site key with platform-backed storage/encryption appropriate to the supported devices; do not assume every Android Keystore supports the site's Ed25519 key directly. Key/signing compatibility is an M0 test, not a reason to export a key to the service. [Android shared-file access](https://developer.android.com/training/secure-file-sharing/request-file)

Release gate: same product/recovery matrix as iPhone, plus temporary URI expiry, activity recreation, repeated intents, and app process death. Test screenshot preview, system gallery and Google Photos on physical devices. Unsupported multi-image shares must not silently drop all but the first image. Background retries are best effort; reopening the app must recover the operation safely.

Distribution: signed internal/closed test build, then Play release. This client does not require a separate website publishing backend.

## Deferred scope

Video/audio, multiple images, image editing/cropping, PDF full-page captures, text/URL-only posts, link scraping, existing-post editing/deletion, site creation, feeds/following, shop/wallet features, scheduled posts, cross-device drafts, custom template execution, and arbitrary-host onboarding are outside R1/R2.

Independently revocable device permissions need a separate protocol design: existing IPNS signatures are authorized by the root key. Passkeys/account recovery and managed custody are separate product choices, not implied by a connection QR code. The Android web-share target is optional later work because R1 already supports the picker and R2 supplies native sharing.

## Rollout and verification ownership

Ship the backwards-compatible service behind a capability flag before enabling the web client; the UI must check that capability and give a useful unavailable state. Then ship connection/bootstrap support, pilot R1, and expand native pilots. Disable new mobile operations independently if needed while retaining status/recovery for accepted operations. Existing CLI/desktop publishing and confirmed content must remain usable.

Service and protocol tests own authorization, idempotency, storage policy, template compatibility, media limits and concurrent writers. Client tests own persistence, correct destination, lifecycle handling, useful errors and accessible mobile controls. Real-device acceptance owns photo selection, screenshot formats and share-sheet integration. Measure upload/prepare/commit timing and failed operations without recording keys, pairing secrets, image contents or captions.

Source evidence from the active checkout: `internal/publish/post.go`, `post_test.go`, `storage_test.go`; `internal/store/storage.go` and public field allowlist in `site.go`; `internal/host/host.go` and `host_test.go`; `worker/src/index.js`, `worker/test/push.test.mjs`; `web/app.js`; `apps/macos/Sources/Croptop/API.swift`. Reuse their existing host-conflict, preservation, key-only-posting and storage-consent tests. The earlier investigation passed `TestPostWithOnlyTheKey`; no mobile implementation has been validated yet.

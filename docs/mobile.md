# Phone posting: pilot build and release gates

The mobile website, iPhone companion with Share extension, and Android companion with share receiver use one keyless publishing service. After a one-time connection to an existing site, a phone can select a screenshot, preview it, add optional text, and publish while the desktop is asleep. Native sharing can save an image before connection is set up.

This is implementation/pilot documentation, not a statement that the hosted service or store releases are live. The original desktop checkout remains untouched; this build lives in the isolated `app-mobile` source snapshot. The signed publication format and the desktop publishing path are retained.

## Release boundaries

| Release | Delivered surface | Gate before external release |
|---|---|---|
| Foundation | Device authentication, encrypted pairing, private image normalization, unsigned preparation, device-signed commit, durable status/retry | Deploy a patched service on a verified trusted origin; verify real-host recovery and resource limits |
| Mobile website | Image-first composer, browser-held nonexportable key, saved draft, preview/sign/retry and result link | Safari/Chrome device matrix, origin history, privacy review and screenshot fidelity |
| iPhone companion | Native photo intake and Share extension, shared protected key/draft storage | Developer signing/App Group setup, physical-device share and lifecycle checks, TestFlight/App Store distribution |
| Android companion | Native photo intake and ACTION_SEND receiver, Keystore-protected key, owned draft image | Physical-device share/lifecycle checks, release signing and Play distribution |

The two native releases may ship independently after the foundation. Scope is one existing connected site, one still image per post, optional title/caption, and the exact compatible Croptop template. This is not a mobile desktop editor. Video, multiple attachments, arbitrary templates/hosts, old-post editing, wallets, feeds, and separately revocable posting credentials are not included.

## Trust and privacy

The existing site root key stays on the phone. Browser keys are nonexportable WebCrypto keys in IndexedDB; native storage uses platform protection. Connecting a phone grants **full site authority**, not a limited posting credential. Removing local keys or disabling this service does not revoke a copied root key. Keep the original site key backed up independently.

The service receives private image/text drafts and renders proposed site versions. It cannot publish without a fresh device signature, but rendering is still trusted: clients validate protocol bindings and the normalized preview, not every file in the proposed website. This is keyless hosting, not a trustless renderer.

Use a dedicated HTTPS composer origin that has **never served author-controlled content**. Reserving a route now does not remove a malicious service worker installed there previously. In particular, historical generic routing could resolve `app.crop.top` through `app.eth`; prove this did not occur or select a fresh dedicated origin. Do not turn on browser key storage until this gate is resolved.

The whole composer origin is reserved even when disabled. It never falls through to site HTML, IPFS gateways, arbitrary files, or ENS resolution. API requests use bearer tokens, no cookies; cross-origin browser requests are rejected. Do not add analytics or third-party scripts on the key-holding origin.

Unpublished blocks use temporary offline, non-listening IPFS engines, separate from the public host node. The service retains bounded private operation files, reconciles uncertain commits before cleanup, and keeps published receipt IDs for duplicate prevention. Pairing relays ciphertext only, expires after ten minutes, and requires matching codes plus confirmation on both devices. The pairing link contains a temporary capability, never the site key.

## Configure the service

Choose the origin before distributing clients. `https://composer.example` below is a placeholder, not a configured deployment.

```sh
go build -o croptop ./cmd/croptop
./croptop host --domain crop.top --listen 127.0.0.1:8090 \
  --data /path/to/private/croptop-data \
  --mobile-origin https://composer.example \
  --mobile-host https://crop.top
```

The host needs the embedded engine. Terminate HTTPS in a trusted reverse proxy. Route the configured composer origin and `/v0/mobile/*` to this process. `--mobile-origin` is intentionally empty by default, so the service is not enabled accidentally. Environment equivalents are `CROPTOP_MOBILE_ORIGIN` and `CROPTOP_MOBILE_HOST`. The publishing host is a fixed operator setting, never a user-supplied URL. One process exclusively owns the private mobile data directory; do not run replicas against it.

The Dockerfile includes a pinned patched HEIF decoder. Run its actual-image test target before building/releasing the production container:

```sh
docker build --target mobile-media-test -t croptop-mobile-media-test:local .
docker build -t croptop-mobile:local .
```

Use persistent private storage for `/data`, encrypted at rest and in backups. Start the pilot with one normalization slot (implemented) and at least 2 GiB process/container memory, then measure. Keep decoder dependencies patched. [Media bounds and the HDR/color-fidelity gate](design/mobile-media.md) explain why successful HEIF decoding alone is insufficient for a public iPhone release.

### Cloudflare Worker

The Worker reserves `app.<DOMAIN>` before all author routing. `MOBILE_ENABLED` defaults to `"false"`. Set `MOBILE_ORIGIN` to the selected fresh HTTPS composer origin **outside the public gateway domain** and attach an explicit Worker route for that hostname. Custom gateway subdomains are rejected: after a later origin migration they could revert to author content. The legacy default remains reserved but inert if a different origin is configured. Set `MOBILE_NODE` to the fixed HTTPS API process origin, or omit it to use existing `NODE`. The Go service's `--mobile-origin` must match exactly.

Before enabling, bundle without deployment, confirm all static/API paths stay isolated, and verify that config reports the expected origin, host, limits and HEIF capability. Add edge rate limits for challenge/session/pairing/upload endpoints, a private-volume quota, monitoring without request bodies/tokens, and operational backup/restore. In-process request/identity limits are not a substitute for network-level abuse protection.

```sh
cd worker
node --experimental-loader ./text-loader.mjs --test test/*.test.mjs
npx --yes wrangler@4.86.0 deploy --dry-run
```

No deployment command is required for local verification. Production routing, DNS, TLS, rate-limit configuration and enabling `MOBILE_ENABLED` are explicit release operations.

## Connect an existing site

Run the desktop publisher from this build with `--mobile-origin` pointing to the configured service. Open the site's **Connect phone** action in the web console or Mac app. The page checks hosting consent and publishes current compatibility metadata through the normal conflict-checked publisher. A stale local copy must be synchronized, not force-published.

Scan/open the short-lived link on the phone (native companions can accept the link), compare the displayed confirmation code on both devices, and confirm. This is a one-time desktop step; later posting does not require that desktop to stay awake. Local PKCS8 Ed25519 PEM import remains available. Import alone does not silently enable service use or change a site's hosting policy.

If the site uses an unsupported template or another publishing host, connection fails explicitly. Do not migrate the site or opt it into hosting automatically. To stop, disable phone publishing in the connected client; disconnecting locally removes that client's key/session only. Already published posts remain public.

## Verification and device acceptance

Shared contracts: [API](design/mobile-api.md), [pairing](design/mobile-pairing.md), `docs/design/mobile-protocol-fixture.json`, and `testdata/mobile-pairing-v1.json`. Fixtures contain deterministic test-only keys. Never use them for a real site.

Run `go vet ./...` and `go test ./...`; run the mobile service with `go test -race ./internal/mobile`. `.github/workflows/mobile.yml` gates browser protocol/pairing, Worker behavior/bundling, iOS shared tests plus unsigned app/extension compilation, and Android unit tests plus APK compilation. Native app READMEs contain their platform build and signing instructions.

Before a public pilot, test on real iPhone and Android devices:

- First screenshot share before connection; setup preserves the image and returns to it.
- Photos/Files/Google Photos and remotely backed photo-library images; app switching, extension termination, device lock and process death.
- Safari/Chrome website and installed PWA, duplicate open composer tabs, private/storage-restricted modes and storage pressure.
- Real PNG/JPEG/WebP/HEIF screenshots, rotation, transparency, small text and HDR/wide-gamut appearance.
- Airplane mode before upload, during preparation, immediately after commit and before receipt; reopening/retrying publishes exactly once.
- Another device publishes first; same draft is rebased and explicitly reviewed/signed again.
- Service restart, stale session/proposal, definitive draft expiry, malformed/oversized image, disabling service during work, and reconnecting after local key removal.

Never treat a lost response as a failed publication or automatically generate a new post ID. A queued/saved image is not a published post; only a reconciled published receipt earns that status.

## Local verification record — 2026-10-09

| Check | Result |
|---|---|
| Go | All packages pass `go test ./...` and `go vet ./...`; mobile and local-server suites pass with `-race` |
| Real host | Stopped-desktop publication, retained old files, lost receipt, newer head, service restart and exactly-once recovery pass |
| Draft privacy | Prepared blocks absent from public node before signed commit; normal and crash cleanup plus exclusive-owner tests pass |
| Browser | 14 tests pass: Chromium/WebKit flows plus signing, pairing and exact offline-shell integrity fixtures |
| Worker | 52 tests pass; production/staging Wrangler dry-runs build without deployment |
| iOS | 22 simulator XCTest tests pass; app and Share extension compile; locally ad-hoc signed simulator app launches with shared storage |
| macOS | Existing native app builds with Connect phone action |
| Android | 53 JVM tests pass; debug APK assembles and signature verifies; lint has zero errors and five version/SDK warnings |
| Linux media | Pinned libheif 1.23.6; real HEIC/rotation and kernel-limit tests pass in an isolated Linux/arm64 container with 2 GiB memory and networking disabled |
| CLI | Native Darwin/arm64 and Windows/amd64 binaries compile |

Artifacts in this workspace:

- CLI: `dist/mobile/croptop`; cross-build: `dist/mobile/croptop-windows-amd64.exe`.
- Android debug APK: `apps/android/app/build/outputs/apk/debug/app-debug.apk` (SHA-256 `48e3f4eeeaafbee0e51c6f928a56ec470ef4020f3aa6997c2333fd58a8e88f52`). This is not a release-signed Play artifact.
- iOS simulator bundle: `/tmp/croptop-ios-derived/Build/Products/Debug-iphonesimulator/Croptop.app`, including `PlugIns/CroptopShare.appex`. This cannot be installed directly onto an iPhone; use the signing instructions in the iOS README.
- iOS test report: `/tmp/croptop-ios-derived/Logs/Test/Test-Croptop-2026.10.09_12-16-58--0300.xcresult`.
- Browser screenshots: `/private/tmp/croptop-mobile-web-check/`; iOS launch: `/tmp/croptop-ios-first-launch-signed.png`.
- Local media-test Docker image: `croptop-mobile-media-test:local`.

Independent reviews covered service/origin security, browser recovery and native storage/signing. Findings were fixed and regression-tested. Local tests and simulator success do not remove the physical-device, HDR/color-fidelity, origin-history or distribution gates above. No production posts, deployments or app-store submissions were made.

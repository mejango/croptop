# Phone posting: setup and native release gates

The mobile website, iPhone companion with Share extension, and Android companion with share receiver use one keyless publishing service. After a one-time connection to an existing site, a phone can select a screenshot, preview it, add optional text, and publish while the desktop is asleep. Native sharing can save an image before connection is set up.

The phone website is live, and Croptop **0.13.21 (Mac build 1162)** is available through **Croptop → Check for Updates…** with native in-app phone setup. The isolated Railway service and dedicated Cloudflare composer let connected phones publish while the desktop sleeps. Native app-store releases remain separate. The original desktop checkout remains untouched; the released source is on `release/0.13.21` and tag `v0.13.21`, from the isolated `app-mobile` snapshot. The signed publication format and ordinary desktop publishing path are retained.

## Unreleased follow-up

Local changes after 0.13.21 reuse saved hosting permission, shorten the setup title to “Post from your phone”, and label its normal action “Connect”. Hosting permission does not authorize publishing saved desktop changes: setup checks the published site first and offers “Publish and connect” only when the service reports that an update is required. Preparation distinguishes sending files, checking the hosted publication and checking phone compatibility; the timer reports total time without a middot. The existing five-minute preparation deadline is unchanged.

The hosting Worker now stages up to four independent objects concurrently, preserving duplicate-path ordering, resumability and the existing conditional commit. Tests cover 6,482 files plus 680 directory blocks across 26 batches. A synthetic 129-object fixture with 5 ms storage-read latency improved from 766 ms to 202 ms; this is not a measured production speedup or a guarantee that every large site completes within five minutes. The phone composer clears stale progress after an error and preserves the failed draft and uncertain-publication recovery controls.

These changes are not deployed and are not in the normal updater yet. Image-format diagnostics and any required decoder change remain separate from these setup/presentation fixes.

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

Use a dedicated HTTPS composer origin that has **never served author-controlled content**. Reserving a route now does not remove a malicious service worker installed there previously. The pilot therefore uses the fresh `https://croptop-phone-923c1bafd14ea328.croptop.workers.dev` origin, outside the public gateway namespace. It does not enable or reuse `app.crop.top`, whose historical generic routing could resolve `app.eth`.

The whole composer origin is reserved even when disabled. It never falls through to site HTML, IPFS gateways, arbitrary files, or ENS resolution. API requests use bearer tokens, no cookies; cross-origin browser requests are rejected. Do not add analytics or third-party scripts on the key-holding origin.

The dedicated service's base IPFS engine and temporary per-operation engines are private, offline and non-listening. It runs no public host gateway, swarm listeners, DHT bootstrapping, followers or desktop console. The service retains bounded private operation files, reconciles uncertain commits before cleanup, and keeps published receipt IDs for duplicate prevention. Pairing relays ciphertext only, expires after ten minutes, and requires matching codes plus confirmation on both devices. The pairing link contains a temporary capability, never the site key.

Retention is not a blanket seven-day deletion promise: the private operation journal retains site/post identifiers, submitted titles and captions, media hashes/types, and status/receipt information indefinitely. Background cleanup expires private media and staging after seven days only after successful reconciliation; uncertain commits retain their bytes. Removing a local connection does not erase that server journal or previously published content. Public IPFS copies may remain available independently of this service. Native store privacy disclosures and the operator's privacy-policy URL must reflect this behavior before distribution.

## Configure the service

Run the pilot as a separate service, not as a replacement for the existing crop.top host. Inject `CROPTOP_MOBILE_PROXY_SECRET` through the platform secret store before starting; its value must match the dedicated Worker's `MOBILE_PROXY_SECRET`. Never commit or log it. Public configuration and the start command are:

```sh
go build -o croptop ./cmd/croptop
CROPTOP_MOBILE_ORIGIN=https://croptop-phone-923c1bafd14ea328.croptop.workers.dev \
CROPTOP_MOBILE_HOST=https://crop.top \
CROPTOP_MOBILE_REQUIRE_HOSTED_SITE=true \
CROPTOP_MOBILE_MAX_OPEN_OPERATIONS=20 \
./croptop mobile --listen 0.0.0.0:8090 --data /data
```

`croptop mobile` requires an explicit composer origin and starts only the trusted composer/API. The publishing host is a fixed operator setting, never a user-supplied URL. Every backend API request requires the edge secret when configured, except `GET`/`HEAD /v0/mobile/health`; readiness returns only `{"status":"ready"}` after initialization and becomes unavailable on shutdown. It does not assert publication-host reachability. Direct requests to the Railway API without the secret are denied, including `/config`; author/gateway paths return 404 on every hostname.

Enrollment is automatic for an eligible site's proven owner: a one-time Ed25519 signature is verified before a bounded fetch checks the signed crop.top head, explicit hosted-storage policy, compatible template and conflict-safe host capability. Enrollment does not enable phone-publishing consent. Arbitrary new keys cannot allocate durable sessions merely by signing a challenge. `CROPTOP_MOBILE_ALLOW_SITES` / `--mobile-allow-sites` is an optional emergency restriction, left empty for this pilot: kmac does not need manual site registration.

The global 20-open-operation cap includes all sites and concurrent incoming uploads, survives restart, and rejects excess uploads before reading their bodies. Published receipts and definitively expired drafts do not consume this cap. Existing operation status, prepare and commit remain available at capacity; keep the same operation ID. The existing per-site limit, four upload slots and single image-normalization slot remain in force. One process exclusively owns the private data directory; do not run replicas against it.

The Dockerfile includes pinned libheif 1.23.6 and the Little CMS color helper. Run the actual-image/color test target for the production architecture before releasing:

```sh
docker build --platform linux/amd64 --target mobile-media-test -t croptop-mobile-media-test:local .
docker build --platform linux/amd64 -t croptop-mobile:local .
```

The provisioned Railway pilot has one replica, a private persistent `/data` volume, 2 GiB memory and 2 vCPU caps. Use encrypted storage/backups and monitor actual volume consumption; the provisioned 50 GB capacity ceiling is not a 50 GB draft budget. Keep decoder dependencies patched. SDR Display-P3/other supported RGB ICC profiles are converted to sRGB; recognized unsupported HDR/high-bit-depth input fails with export guidance rather than losing its color declaration. [Media bounds and remaining device-fidelity checks](design/mobile-media.md) document the supported behavior and limitations; HDR tone mapping is not included.

### Cloudflare Worker

The dedicated entry point is `worker/src/mobile-only.js`, configured by `worker/wrangler.mobile.toml`; do not deploy the generic gateway entry point to this origin. It has no author storage, registry, gateway routes or scheduled triggers. `MOBILE_ENABLED` remains `"false"` until the verified backend is ready. Its fixed settings are:

- `MOBILE_ORIGIN=https://croptop-phone-923c1bafd14ea328.croptop.workers.dev`, exactly matching the Go service.
- `MOBILE_NODE=https://croptop-mobile-pilot-production.up.railway.app`, with no fallback to the existing public host node.
- Secret `MOBILE_PROXY_SECRET`, injected by the Worker on upstream requests. Caller-supplied proxy headers are never forwarded.
- Required rate-limit bindings: `MOBILE_API_LIMITER` 240/minute, `MOBILE_AUTH_LIMITER` 30/minute, `MOBILE_PAIRING_LIMITER` 120/minute, and `MOBILE_UPLOAD_LIMITER` 12/minute per client IP. These Cloudflare counters are per location and eventually consistent abuse controls, not exact global quotas.

Missing secret, missing rate-limit bindings or a failed limiter makes the dedicated Worker fail closed. Keep the existing gateway Worker's mobile setting disabled; this pilot neither migrates gateway routes nor enables the legacy `app.crop.top` surface. Before enabling the dedicated Worker, confirm all static/API paths remain isolated and config reports the intended origin, host and media capability. Monitor without request bodies, tokens, key material or pairing URLs; retain operational backup/restore procedures.

```sh
cd worker
node --experimental-loader ./text-loader.mjs --test test/*.test.mjs
npx --yes wrangler@4.145.0 deploy --dry-run --config wrangler.mobile.toml
```

### Production targets and rollback

| Target | Pilot resource |
|---|---|
| Trusted composer | `https://croptop-phone-923c1bafd14ea328.croptop.workers.dev` |
| Dedicated backend | `https://croptop-mobile-pilot-production.up.railway.app` |
| Railway service | `croptop-mobile-pilot` (`a3e07070-2013-441a-b1a6-a9eccd42eea9`) |
| Backend command | `croptop mobile --listen 0.0.0.0:8090 --data /data` |
| Backend readiness | `/v0/mobile/health` |
| Normal Mac setup update | [Croptop 0.13.21 build 1162](https://github.com/mejango/croptop/releases/tag/v0.13.21), or **Croptop → Check for Updates…** |

Railway uses explicitly configured service settings for this pilot. Exclude the original `railway.toml` from the mobile deployment archive: its public-host health path must not override the dedicated configuration. The Docker image's default command is still the public host command, so the explicit start-command override is required. `railway.mobile.toml` is a reference for platforms accepting a custom config-as-code file, not proof that Railway selected it.

Record the exact source revision, container deployment and Worker version before enabling. If validation fails, set `MOBILE_ENABLED="false"` on the **dedicated** Worker and redeploy that configuration while retaining its hostname and trusted code; never delete/reassign the key-holding origin or fall through to author content. Preserve the private backend volume and publication receipts. Roll back only the dedicated service to a known-good immutable build; do not restore stale journals over newer commits. Reconcile uncertain operations before re-enabling. Never change the stable crop.top host, stable desktop appcast, existing site storage policies or production site heads as a rollback step.

The backend is deployed from `df717b583177bd3640a53cf6068791c11a0c2efe`: Railway deployment `ba90d61b-aa5c-4b5b-9331-a85370add58f`, image `sha256:f67cce10420bdb231b400303a320d75d0a663bd77037a5adbceb9e1377a04219`. Current Worker version `43ae869d-a56c-45f7-ada3-b53afcecbbf3` is deployed from `bc6c0008244ddf4337a2e308a6f86fd73c7276d8` with 0.13.21 native-setup guidance. Only onboarding HTML and its shell-integrity entries changed from previous version `345eeafb-768d-4c77-bd88-5f6c5dec7f2f`, which remains the rollback target; all nine bindings are unchanged. Live checks confirm health200, direct API403 and no host/IPFS gateway. The backend retains its verified 2CPU/2GiB limits and private0700 data directories; it was not redeployed for this Mac fix. A prior controlled mobile-only restart retained both published receipts, paired-phone authentication and the same volume/image. The original hosting deployment is unchanged.

The earlier immutable Mac pilot 1160 remains a separate, non-latest [prerelease](https://github.com/mejango/croptop/releases/tag/phone-pilot-20261009-1160). Existing pilot users can also upgrade to stable 1161 through the normal updater.

### Normal updater release — 2026-10-09

Stable 0.13.20/build 1161 is pinned to `06c69d6a921a16ceb0352ef0d7dc699de191266c`, release 408137039. Its universal Apple Silicon/Intel app and DMG are Developer ID signed, notarized, stapled and Gatekeeper accepted. Both the packaged engine and the ordinary GoReleaser CLI use the deployed trusted phone origin without user-supplied flags. DMG SHA256 is `6391bf6bc81c98f92ba942fa96e9aa08054dc616faa4dea1ae41d8eb02b79550`; signed appcast SHA256 is `89b3d10c223ff7d425ccb41ea8564b84c1ec2761a0dcf071fd1dd746a2c1a269`.

All 20 attached files were downloaded anonymously and checked against GitHub digests and the complete `SHA256SUMS.txt` (SHA256 `73ec93a1ed004ecff7c88ac9deeb80128d6300faf8b3ea92fc1744719aee7838`). The real Sparkle frameworks shipped in 1159 and 1160 accept 1161 on arm64 and Intel via Rosetta and reject tampering; no installed updater or user preferences were touched. The bare normal latest-feed URL serves those exact verified bytes. Minimum macOS 13 is verified in feed policy and both binary deployment targets, not on a separate physical macOS 13 machine.

Stable verification includes all Go tests/vet, mobile/server/bootstrap race checks, 88 Mac tests, 16 web tests and 64 Worker tests. The release also keeps private host credentials out of public metadata and explains the new explicit storage choice before affected legacy Mac publishing. Unspecified storage remains P2P; unrelated settings saves do not acknowledge that choice or enable hosting. Existing hosted content is not deleted.

The tag workflow staged artifacts before promotion. A published-only GitHub lookup failed closed after creating the draft; corrected helpers at `c9d2efe` and source-pinned recovery run 37963699615 added only the missing Windows installers. The original tag and signed Mac files were not changed. Complete prerelease downloads passed before latest promotion. Homebrew formula commit `7f9bc2dc86f37818fc944eda851f92ef42cf42e1` uses the same verified archives. Main remains untouched; future release work must retain the corrected draft lookup and staging guards.

## Connect an existing site

On a Mac, choose **Croptop → Check for Updates…** and install 0.13.21 or later. Close any older browser setup tab, open the site and choose **Connect phone** (the phone button beside Settings). Setup now stays inside Croptop. Review the combined permission to host the site and publish saved changes if a compatibility update is needed, then choose **Create connection**. A signed [Mac download](https://github.com/mejango/croptop/releases/tag/v0.13.21) is available if needed. The app and normal CLI already use the correct trusted service origin; no shell flags or advance site-URL registration are required. Custom deployments may explicitly override `--mobile-origin`.

Scan/open the short-lived link on the phone (native companions can accept the link), compare the displayed confirmation code on both devices, and confirm. This is a one-time desktop step; later posting does not require that desktop to stay awake. Local PKCS8 Ed25519 PEM import remains available. Import alone does not silently enable service use or change a site's hosting policy.

If the site uses an unsupported template or another publishing host, connection fails explicitly. Do not migrate the site or opt it into hosting automatically. To stop, disable phone publishing in the connected client; disconnecting locally removes that client's key/session only. Already published posts remain public.

### In-app setup and hosted-readiness fix (0.13.21/build1162)

The Mac app replaces the browser redirect with a native sheet containing hosting/publication consent, preparation stages, a locally generated private QR/link, the phone's eight-digit confirmation code, expiry and retry. It captures the selected site and keeps the updater from relaunching while setup or its cancellation is active. The CLI's local browser setup remains available and uses the same preparation API.

Preparation first checks the already-published site. A verified compatible hosted site connects without rendering or publishing saved desktop edits. If initial hosting or compatibility metadata needs publication, explicit permission is required; the Mac checkbox says that the current desktop site will be published if necessary. The browser fallback asks separately before that publication. A temporary service error is not treated as permission to publish.

Required bootstrap publication renders and uploads synchronously to the host, retains the ordinary conflict checks, uses a parent-checked manifest even for full uploads, and verifies the signed hosted head before session enrollment. The desktop does not wait for a foreground DHT announcement. An unchanged hosted CID remains valid even if the local sequence is higher. A previously published site whose hosted head is absent must be published through the ordinary publisher first: the existing host protocol cannot safely compare-and-set an absent historical head. Never force/reset its sequence to bypass this check.

Preparation has a five-minute overall context, bounded network stages and cancellation-aware operation gates. Polling/cancellation stay responsive while publication runs. Cancellation waits for the worker to acknowledge stopping; local rendering checks cancellation between operations, not by interrupting an in-flight filesystem/codec call. Hosting permission, a publication already committed, and a key already sent are not undone. Retrying uses verified current state rather than assuming a failed response means nothing happened. These changes do not require a hosted service or composer deployment.

Released from `ebef97650c0117d17918b54f3318a63247ac3c58` as stable/latest release408369126 at 2026-10-09 21:47:56UTC. CI run37994146174 passed all staging jobs. Universal app and DMG are Developer ID signed, notarized, stapled and Gatekeeper accepted; the read-only mounted DMG contains the exact tested app. All20 assets were downloaded anonymously and matched pinned lengths, GitHub digests and complete manifest SHA256 `c693ee2eb2cbef640b417b46e16a9cfba3b53fde5e98ce714989f43085f9d085`. DMG SHA256 `81d173a865c907cb248d213d27982b9ef1c3cc78f20d88e3bb7589f6618ea992`; appcast SHA256 `feb84f9953a364762f2f4f683f63b8fee36f71deb86ad87e2d16ed166a6e216d`; source archive SHA256 `2fce44d30fe64ce4bf9eff4b66f8e2a98b10ea03c4f001d05fb03bc6f0cfaa6d`. Bare latest feed and manual download alias match without cache-busting. Actual shipped1159/1160/1161 Sparkle frameworks select1162 on Apple Silicon and Intel via Rosetta, accept these signatures and reject tampering/wrong keys. No installer/updater was instantiated and no installed app, preferences or user sites were changed. macOS13 remains a deployment-target/feed compatibility check, not a physical OS13 run. Homebrew commit `d49dc646c0e3c9712bacd058d95f56ca622a8e3f` publishes the retained, hash-verified CI formula; main and older release artifacts/tags remain unchanged.

Real isolated host/service tests exercise a17MiB first upload against old public P2P metadata, both with and without a previously checked local hosting setting. They prove no session is admitted before commit, encrypted pairing, cancellation-aware progress, and a ready-site fast path that leaves pending edits untouched while the render gate is occupied. Full serial Go tests/vet pass. A deterministic stale-registry/new-signed-record regression also proves conservative status rejection and bounded same-operation recovery without weakening verification. Native Mac107 tests and release build pass; the rendered QR was decoded and three actual SwiftUI states inspected. Chromium/WebKit cover26 desktop fallback cases and eight existing composer cases, plus eight protocol/pairing/cache tests; Worker64 tests pass. New CI gates cover Mac tests, browser fallback, and serial publication/render/lock/mobile race suites.

The separate onboarding-only Worker deployment `c50ad35d-5dc7-4bb4-b3b6-51f7ae266fc7` received100% traffic at21:49:31UTC. Fresh360px Chromium/WebKit verify updated instructions, all11 shell hashes, clean layout and origin isolation. Chromium offline reload passes; WebKit's automation offline error remains inconclusive for physical Safari. Existing open composer windows retain their complete old shell until reopened. Release evidence is retained under `/private/tmp/croptop-stable-1162-anonymous-verification`, `/private/tmp/croptop-updater-1162.cZNE9t`, and `/private/tmp/croptop-mobile-01321-deployment.json`. Native iPhone/Android distribution gates below remain unchanged.

## Verification and device acceptance

Shared contracts: [API](design/mobile-api.md), [pairing](design/mobile-pairing.md), `docs/design/mobile-protocol-fixture.json`, and `testdata/mobile-pairing-v1.json`. Fixtures contain deterministic test-only keys. Never use them for a real site.

Run `go vet ./...` and `go test ./...`; run the mobile service with `go test -race ./internal/mobile`. `.github/workflows/mobile.yml` gates browser protocol/pairing and Worker behavior/bundling. Native CI is configured to execute iOS core, real provider-intake and UI tests, verify generated assets/origin settings, and compile the app/extension; Android runs unit tests, lint, emulator sharing/process-death recovery, signing-configuration rejection checks, and debug/unsigned release packaging. Local equivalents are verified below; configuring these jobs is not a claim that the new workflow has already run on GitHub. Native app READMEs contain the exact platform commands and distribution gates.

The scoped pilot is for device acceptance. Before a broad release, test on real iPhone and Android devices:

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
| Go | All packages pass serial `go test -p 1 ./...` and `go vet ./...`; targeted lock/render/publishing/phone-setup/recovery regressions pass with `-race` |
| Real host | Stopped-desktop publication, retained old files, lost receipt, newer head, service restart and exactly-once recovery pass |
| Draft privacy | Prepared blocks absent from public node before signed commit; normal and crash cleanup plus exclusive-owner tests pass |
| Browser | 26 desktop-setup cases and eight composer cases pass across Chromium/WebKit; eight signing/pairing/offline-shell integrity tests pass |
| Worker | 64 tests pass, including dedicated-origin isolation, trusted proxy, required edge limits and fail-closed configuration; current dedicated deployment and all11 shell hashes verified |
| iOS | 33 core and eight real-provider intake tests pass on the simulator; two UI tests pass, including photo/caption retention after abrupt termination and relaunch; app and Share extension compile |
| macOS | 107 tests pass; stable 0.13.21/build1162 is signed, notarized and verified live in the normal updater |
| Android | 65 JVM and eight Android 16 emulator tests pass, including first-share setup, retained image/caption, repeated intents and real force-stop recovery; lint has zero errors and three dependency-version warnings; debug APK and unsigned release APK/AAB build |
| Linux media | Production Linux/amd64 color/HEIF gate passes with libheif 1.23.6, lcms2 2.19 and libpng 1.6.59; real HEIC/rotation, Display-P3 transform, alpha/metadata, unsupported-color rejection and kernel-limit checks pass |
| CLI | Native Darwin/arm64 and Windows/amd64 binaries compile |
| Dedicated service | Race-tested private-engine publication/restart, automatic hosted-site enrollment, optional allowlist, proxy-secret protection and global/concurrent draft admission; actual local CLI returns health200, direct API403, trusted API200, gateway404 and exits cleanly |

Artifacts in this workspace:

- CLI: `dist/mobile/croptop`; cross-build: `dist/mobile/croptop-windows-amd64.exe`.
- Android debug APK: `apps/android/app/build/outputs/apk/debug/app-debug.apk` (SHA-256 `d4f41f026648cc401a4a17c452dd51a0260a29b05ede82a443c8a640a9ca045f`). This is development-signed, not a Play release.
- Android unsigned APK: `apps/android/app/build/outputs/apk/release/app-release-unsigned.apk` (SHA-256 `68ae3d16c2e60f90bc35a1c8d9d027e482d5b959e0e22fd79eb5e567971b17bc`); unsigned AAB: `apps/android/app/build/outputs/bundle/release/app-release.aab` (SHA-256 `14ccfb28bfbc31a5c9529e94f663edba78dd92ecf139ae0881a50ee65f27339c`). Both are byte-identical across two forced builds; neither is a signed distribution artifact.
- iOS simulator bundle: `/tmp/croptop-ios-native-release/Build/Products/Debug-iphonesimulator/Croptop.app`, including `PlugIns/CroptopShare.appex`. Locally ad-hoc signed and launch-tested; not installable directly onto an iPhone.
- iOS unsigned arm64 device archive: `/tmp/Croptop-iOS-0.1.0-unsigned.xcarchive`, version 0.1.0/build 1, with both bundles' configuration/privacy manifests and the app icon verified. It still requires authorized provisioning/signing, not merely an IPA filename.
- iOS test reports: `/tmp/croptop-ios-native-release/Logs/Test/Test-Croptop-2026.10.09_14-33-00--0300.xcresult` (41 core/provider tests) and `Test-CroptopUI-2026.10.09_14-32-03--0300.xcresult` in the same directory (two UI tests).
- Browser screenshots: `/private/tmp/croptop-mobile-web-check/`; current iOS screenshots: `/tmp/croptop-ios-release-screenshots/`.
- Local media-test Docker image: `croptop-mobile-media-test:local`.

Independent reviews covered service/origin security, browser recovery, native storage/signing and the production media path. Findings were fixed and regression-tested. Local tests and simulator success do not replace physical iPhone/Android sharing, Safari/Chrome screenshot-fidelity and lifecycle acceptance, or signed native distribution. HDR tone mapping remains unsupported. Live Chromium and WebKit show clean360px layouts and11/11 offline-shell assets matching integrity hashes; Chromium offline reload passes. WebKit's automation offline mode fails with an engine error, so iPhone airplane-mode behavior is explicitly unverified. No app-store release is implied.

Native release preparation was recorded at `99afeb6` (iOS) and `5b3715c` (Android and native CI), now included as source in the0.13.21 tag but not distributed as phone apps. It closes stale-connection signing, pairing-failure recovery, capability persistence and delayed photo-selection races. Final pinned source and release metadata/privacy received independent reviews. The current source is ready for the next signing/device-test stage, not a TestFlight or Play release. Apple identifier/App Group/app-record registration still needs explicit authority and a matching local iOS signing identity/profiles; Android needs an approved release-signing/distribution identity. Both need an operator-approved hosted privacy policy, accurate store disclosures, verified domain association files and physical-device acceptance. No store registrations, uploads or invitations were made, and no native test published to a production site. See [iOS](../apps/ios/README.md) and [Android](../apps/android/README.md) for reproducible commands and the remaining checks.

### Live acceptance — 2026-10-09

- QR: actual image decoded; wrong code rejected; one encrypted transfer confirmed on both devices; nonexportable phone key reauthenticated after the desktop stopped and after the backend restarted. No PEM import or plaintext key in outbound requests; the dedicated QR site stayed empty.
- WebKit: operation `407651CE-F3AE-4E79-84D6-073024D3F0CB` published and reopened after restart at sequence2, exactly one article, public image matching the reviewed normalized SHA256.
- Chromium: operation `F77247E8-52B5-4F12-B2FE-AAB2928E86FA` recovered after the accepted commit response was deliberately withheld, then after backend restart, always exactly one article at sequence2.
- Existing stylesheet, font and site script on both synthetic sites remain byte-identical. Public receipts work with all synthetic desktop publishers stopped.
- Live harness safety/recovery guards pass eleven tests, including actual dispatch evidence, expired proposals, hidden receipt startup and prevention of an accidental signing click. Synthetic sites and private test profiles are separate from every user site/library.

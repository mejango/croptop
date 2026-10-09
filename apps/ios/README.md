# Croptop for iPhone and iPad

Native SwiftUI companion and a real `com.apple.share-services` Share extension. Both use the shared mobile publishing API; neither embeds the desktop editor or an IPFS node. Minimum iOS version is 17.

## Native release preparation plan

- [x] Centralize the approved production origin and validate incoming pairing links before any request, preserving drafts and refusing connection/busy replacement.
- [x] Serialize connection changes with in-flight publishing; add regression tests for incoming links, teardown and bounded intake.
- [x] Align version metadata, prepare associated domains, reuse the existing vector app icon, and audit privacy/export metadata.
- [x] Build app/extension and an unsigned device archive; exercise XCTest plus simulator share intake and inspect screenshots.
- [x] Record results and the remaining physical-device, signing, association-hosting and distribution gates. No account registrations or uploads are part of this preparation.

The app connects one existing site through an encrypted Connect phone link or local PKCS8 key import. It saves screenshots before setup, offers a photo picker and image replacement, and shows local drafts, pending operations and confirmed post links. The extension accepts exactly one still image, retains the provider's bytes before showing its composer, and can prepare, review and publish without opening the containing app. When setup is missing, it retains the draft and asks the person to open Croptop manually.

## Building and verification

The committed Xcode project is generated from `project.yml` with XcodeGen. The Foundation/CryptoKit core is also a standalone Swift package. From `apps/ios`:

```sh
xcodegen generate
swift Scripts/generate-app-icon.swift --check
swift Scripts/generate-release-settings.swift --check
swift test
xcodebuild -project Croptop.xcodeproj -scheme Croptop \
  -configuration Debug -sdk iphonesimulator \
  -destination 'generic/platform=iOS Simulator' \
  -derivedDataPath /tmp/croptop-ios-native-release CODE_SIGNING_ALLOWED=NO build
xcodebuild -project Croptop.xcodeproj -scheme Croptop \
  -destination 'platform=iOS Simulator,name=iPhone 17 Pro,OS=26.1' \
  -derivedDataPath /tmp/croptop-ios-native-release CODE_SIGNING_ALLOWED=NO test
```

Use Xcode 26.1 and XcodeGen 2.45.4, with `DEVELOPER_DIR=/Applications/Xcode-26.1.app/Contents/Developer` when that Xcode is installed alongside other versions. In a restricted shell, Xcode needs access to its normal simulator and package caches. The macOS package tests can instead use writable caches:

```sh
CLANG_MODULE_CACHE_PATH=/tmp/croptop-ios-module-cache \
  swift test --disable-sandbox --scratch-path /tmp/croptop-ios-tests \
  -Xswiftc -module-cache-path -Xswiftc /tmp/croptop-ios-module-cache
```

Tests cover the same Go IPNS fixture and independent Node pairing fixture used by other clients, strict PEM and session/proposal validation, malformed CBOR, preview substitution, pairing tampering, retained destinations, lost upload and commit responses, expiry recovery, and UTF-8 byte limits. The Xcode test target bundles those shared fixture files directly from the repository; there is no separately maintained fixture copy.

Lifecycle tests cover exact incoming-link validation, capability redaction, app/extension lease contention, preserving the composer across setup, and a delayed image replacement targeting only its original draft. The platform intake suite uses real `NSItemProvider` file callbacks, including a file removed immediately after callback completion, sharing before connection, malformed/animated images and oversized files.

An unsigned simulator build intentionally cannot open App Group storage. For interactive verification, build with `CODE_SIGNING_ALLOWED=YES CODE_SIGN_IDENTITY=-` instead; this adds local ad-hoc entitlements without an Apple account or distribution certificate. The `CroptopUI` scheme exercises the home, secure connection input, privacy screen and retained photo/caption after relaunch. Seed only a task-owned simulator with the public repository icon before that UI suite:

```sh
xcrun simctl addmedia "$CROPTOP_TEST_SIMULATOR" Assets.xcassets/AppIcon.appiconset/AppIcon.png
xcodebuild -project Croptop.xcodeproj -scheme CroptopUI \
  -destination "platform=iOS Simulator,id=$CROPTOP_TEST_SIMULATOR" \
  -derivedDataPath /tmp/croptop-ios-native-release \
  CODE_SIGNING_ALLOWED=YES CODE_SIGN_IDENTITY=- test
xcodebuild -project Croptop.xcodeproj -scheme Croptop -configuration Release \
  -destination 'generic/platform=iOS' -derivedDataPath /tmp/croptop-ios-native-release \
  -archivePath /tmp/Croptop-iOS-0.1.0-unsigned.xcarchive CODE_SIGNING_ALLOWED=NO archive
```

The archive is unsigned build evidence, not an installable IPA or a TestFlight submission. A physical-device build still needs provisioned capabilities.

### Verification review — 2026-10-09

Xcode 26.1 / iOS 26.1 on a task-owned iPhone 17 Pro simulator: **41 tests passed** (33 core and 8 real provider-intake tests), plus **2 UI tests passed** with ad-hoc signing. The 33 core tests also passed through macOS SwiftPM. UI verification selected the seeded public icon through the system Photos picker, edited a unique caption, terminated without saving or dismissing, then reopened the same retained image and caption. Home, connection, privacy and retained-draft screenshots were inspected. The app and embedded extension compiled for simulator and arm64 iPhoneOS; the Release device archive's versions, icon, privacy manifests and unsigned status were checked. Icon/origin generation checks and plist validation passed. CI is configured for these checks; this local verification does not claim a completed hosted CI run.

Local evidence: `/tmp/croptop-ios-native-release/Logs/Test/Test-Croptop-2026.10.09_14-33-00--0300.xcresult`, `/tmp/croptop-ios-native-release/Logs/Test/Test-CroptopUI-2026.10.09_14-32-03--0300.xcresult`, `/tmp/croptop-ios-release-screenshots/`, and `/tmp/Croptop-iOS-0.1.0-unsigned.xcarchive`. The provider tests exercise the exact intake helper used by the extension, not the complete system Share-sheet UI on a physical phone. Those acceptance gates remain below.

## State and key ownership

`Core/` owns API models, local file transactions, connection cryptography and the API client. `Shared/` owns platform storage, intake validation and one composer model/view used by the app and extension.

Site seeds live only in Keychain, with `WhenUnlockedThisDeviceOnly`, no synchronization, and the narrow `$(AppIdentifierPrefix)top.crop.mobile.keys` access group. Key input uses secure text entry. Draft images, text, destination and operation records live in the `group.top.crop.mobile` App Group with complete file protection, atomic writes and a cross-process advisory lock. Draft storage is excluded from backup; the original publisher/key backup remains the recovery source. Removing the connection deletes its local key but keeps drafts. It is not remote key revocation.

A separate cross-process activity lease spans networking and signing. Connection removal/import/pairing completion cannot race a publish from the app or Share extension. Each operation reloads the shared connection after acquiring the lease and invalidates a stale API client. After successful removal, a cached client cannot begin new signing. Already-dispatched commits may finish; their receipts remain recoverable. A process exit releases the lease automatically.

An operation UUID and immutable input are persisted before upload. Commit intent is persisted before sending signatures. Lost responses remain pending; reopening checks the same operation. A confirmed result cannot be replaced by a late pending response. The client validates the service's normalized image hash and the exact draft, destination, host, IPNS CBOR record and push payload before signing. It displays the normalized image and requires the author's review. It refuses redirects for authenticated or pairing requests.

Service consent is explicit after key import. Limits and input formats come from `/config`. Invalid text or image size is checked before freezing the local draft. Definitive pre-insert rejection leaves it editable. An expired operation, or an image rejected before any proposal/signing, offers an explicit corrected copy with a new UUID; the old operation remains in history. Generic failures and uncertain commits never rotate identity. A signature-expired or parent-changed operation is prepared again under its original UUID and reviewed again.

The app makes short foreground status checks. It does not promise completion after iOS terminates the app or extension. Pending operations resume when opened. A consumed pairing response that is lost needs a new Connect phone link; it does not affect retained post operations.

## Origin, assets and disclosures

`Core/Resources/Service.json` owns the approved production origin, currently `https://croptop-phone-923c1bafd14ea328.croptop.workers.dev`. The project generator derives the associated-domain host into `Config/Service.generated.xcconfig` from that same resource. Debug and Release use the same approved origin; neither silently falls back to the former hosting origin. Import and pairing are pinned to it. Incoming links must match the exact HTTPS origin, root path and canonical `#pair=ID.CAPABILITY` format; queries, alternate ports, encoded characters and extra fields are refused. Their capability stays in memory, is never logged, and is cleared from text entry before a request. An existing site or busy operation is never silently replaced.

The app entitlement prepares Associated Domains, but the production AASA document and signed app association were not live during this verification. `onOpenURL` handling and parser tests do not establish universal-link delivery. Paste a Connect phone link until the association gate is completed.

`project.yml` owns app and extension version `0.1.0` / build `1`. `Scripts/generate-app-icon.swift` deterministically renders the existing `web/mobile/icon.svg` into the opaque AppIcon asset; `--check` detects stale output. No new artwork or external image dependency is used.

The shared privacy manifest declares uploaded images, text and the site identifier as linked app-functionality data, without tracking. Its File Timestamp reason `3B52.1` covers `fstat` on explicitly selected/shared files in `BoundedFile`; file metadata is used only to bound safe loading, not sent to the service. This follows Apple's [required-reason API guidance](https://developer.apple.com/documentation/bundleresources/app-privacy-configuration/nsprivacyaccessedapitypes/nsprivacyaccessedapitype). There are no third-party SDKs. `ITSAppUsesNonExemptEncryption=false` reflects this build's exclusive use of Apple CryptoKit, Security and URLSession, following Apple's [OS-provided encryption exemption](https://developer.apple.com/help/app-store-connect/reference/app-information/export-compliance-documentation-for-encryption/); changes to crypto dependencies require a new review.

The in-app privacy information describes actual transmission and retention; it is not a substitute for the operator-approved public privacy-policy URL required for distribution. Server operation journals retain site/post/operation identifiers, title, caption, image hash/type, status and receipts indefinitely for reconciliation. Seven-day cleanup removes private media/staging only after successful reconciliation; uncertain operations retain their bytes. Published content is public and IPFS copies can persist. App Store privacy labels, the public policy and any operator-specific declarations remain distribution gates; do not claim that no data is collected or that all uploaded content expires after seven days.

## Device and distribution gates

This source is not an App Store or TestFlight release. No Apple account or production site was changed by building it. For a signed physical-device build, select the intended development team and provision both bundle IDs, the App Group and the shared Keychain group:

| Target | Bundle identifier |
| --- | --- |
| App | `top.crop.mobile` |
| Share extension | `top.crop.mobile.share` |

The identifiers are explicit in `project.yml` and `DeviceStorage.swift`; changing the App Group requires updating both. Keep app and extension entitlements aligned. Account inventory found no matching app/bundle registrations and no installed iOS signing identity for the intended distribution team. Registration authority, matching signing private key/profiles, an App Store Connect app record, associated-domain hosting and an operator-approved privacy-policy URL are required before TestFlight. No account writes, registrations, invitations or uploads were performed.

Before inviting users, verify on physical iPhones and iPads:

- Share from screenshot preview, Photos and Files, including an iCloud-only image, SDR PNG and HDR HEIF.
- Share before connection, save a caption offline, close the extension, connect in the app, and explicitly confirm the retained draft's destination.
- Publish over cellular with the computer asleep; inspect the public post and preserved site files.
- Terminate during upload, preview and commit; reopen and recover exactly one logical post. Also test a lost commit response followed by another author's publication.
- Exercise App Group/Keychain access from both processes, locked-device interruption, service suspension, stale parent, expired authorization, seven-day draft expiry and rejected images.
- Complete privacy-policy/store disclosures, accessibility/iPad layout review, signing, TestFlight and App Store distribution preparation.

The simulator/core tests do not establish the real-device share-sheet, iCloud, secure-storage entitlement or cellular acceptance gates.

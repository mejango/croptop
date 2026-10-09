# Croptop for iPhone and iPad

Native SwiftUI companion and a real `com.apple.share-services` Share extension. Both use the shared mobile publishing API; neither embeds the desktop editor or an IPFS node. Minimum iOS version is 17.

The app connects one existing site through an encrypted Connect phone link or local PKCS8 key import. It saves screenshots before setup, offers a photo picker and image replacement, and shows local drafts, pending operations and confirmed post links. The extension accepts exactly one still image, retains the provider's bytes before showing its composer, and can prepare, review and publish without opening the containing app. When setup is missing, it retains the draft and asks the person to open Croptop manually.

## Building and verification

The committed Xcode project is generated from `project.yml` with XcodeGen. The Foundation/CryptoKit core is also a standalone Swift package. From `apps/ios`:

```sh
xcodegen generate
swift test
xcodebuild -project Croptop.xcodeproj -scheme Croptop \
  -configuration Debug -sdk iphonesimulator \
  -destination 'generic/platform=iOS Simulator' \
  -derivedDataPath /tmp/croptop-ios-derived CODE_SIGNING_ALLOWED=NO build
xcodebuild -project Croptop.xcodeproj -scheme Croptop \
  -destination 'platform=iOS Simulator,name=iPhone 17 Pro,OS=26.1' \
  -derivedDataPath /tmp/croptop-ios-derived CODE_SIGNING_ALLOWED=NO test
```

Validated locally with Xcode 26.1 and XcodeGen 2.45.4: both app/extension builds pass, and 22 tests pass on iPhone 17 Pro with iOS 26.1. Use `DEVELOPER_DIR=/Applications/Xcode-26.1.app/Contents/Developer` when that Xcode is installed alongside other versions. In a restricted shell, Xcode needs access to its normal simulator and package caches. The macOS package tests can instead use writable caches:

```sh
CLANG_MODULE_CACHE_PATH=/tmp/croptop-ios-module-cache \
  swift test --disable-sandbox --scratch-path /tmp/croptop-ios-tests \
  -Xswiftc -module-cache-path -Xswiftc /tmp/croptop-ios-module-cache
```

Tests cover the same Go IPNS fixture and independent Node pairing fixture used by other clients, strict PEM and session/proposal validation, malformed CBOR, preview substitution, pairing tampering, retained destinations, lost upload and commit responses, expiry recovery, and UTF-8 byte limits. The Xcode test target bundles those shared fixture files directly from the repository; there is no separately maintained fixture copy.

An unsigned simulator build intentionally cannot open App Group storage. For an interactive simulator smoke test, build with `CODE_SIGNING_ALLOWED=YES CODE_SIGN_IDENTITY=-` instead; this adds local ad-hoc entitlements without an Apple account or distribution certificate. The ad-hoc build was installed and launched successfully with active screenshot-picker and connection controls and no storage error. A physical-device build still needs provisioned capabilities.

## State and key ownership

`Core/` owns API models, local file transactions, connection cryptography and the API client. `Shared/` owns platform storage, intake validation and one composer model/view used by the app and extension.

Site seeds live only in Keychain, with `WhenUnlockedThisDeviceOnly`, no synchronization, and the narrow `$(AppIdentifierPrefix)top.crop.mobile.keys` access group. Key input uses secure text entry. Draft images, text, destination and operation records live in the `group.top.crop.mobile` App Group with complete file protection, atomic writes and a cross-process advisory lock. Draft storage is excluded from backup; the original publisher/key backup remains the recovery source. Removing the connection deletes its local key but keeps drafts. It is not remote key revocation.

An operation UUID and immutable input are persisted before upload. Commit intent is persisted before sending signatures. Lost responses remain pending; reopening checks the same operation. A confirmed result cannot be replaced by a late pending response. The client validates the service's normalized image hash and the exact draft, destination, host, IPNS CBOR record and push payload before signing. It displays the normalized image and requires the author's review. It refuses redirects for authenticated or pairing requests.

Service consent is explicit after key import. Limits and input formats come from `/config`. Invalid text or image size is checked before freezing the local draft. Definitive pre-insert rejection leaves it editable. An expired operation, or an image rejected before any proposal/signing, offers an explicit corrected copy with a new UUID; the old operation remains in history. Generic failures and uncertain commits never rotate identity. A signature-expired or parent-changed operation is prepared again under its original UUID and reviewed again.

The app makes short foreground status checks. It does not promise completion after iOS terminates the app or extension. Pending operations resume when opened. A consumed pairing response that is lost needs a new Connect phone link; it does not affect retained post operations.

## Device and distribution gates

This source is not an App Store or TestFlight release. No Apple account or production site was changed by building it. For a signed physical-device build, select the intended development team and provision both bundle IDs, the App Group and the shared Keychain group:

| Target | Bundle identifier |
| --- | --- |
| App | `top.crop.mobile` |
| Share extension | `top.crop.mobile.share` |

The identifiers are explicit in `project.yml` and `DeviceStorage.swift`; changing the App Group requires updating both. Keep app and extension entitlements aligned. The default service address is `https://app.crop.top`; a configured, reachable mobile service must be deployed before an author can connect. Local development permits HTTP only on loopback.

Before inviting users, verify on physical iPhones and iPads:

- Share from screenshot preview, Photos and Files, including an iCloud-only image, SDR PNG and HDR HEIF.
- Share before connection, save a caption offline, close the extension, connect in the app, and explicitly confirm the retained draft's destination.
- Publish over cellular with the computer asleep; inspect the public post and preserved site files.
- Terminate during upload, preview and commit; reopen and recover exactly one logical post. Also test a lost commit response followed by another author's publication.
- Exercise App Group/Keychain access from both processes, locked-device interruption, service suspension, stale parent, expired authorization, seven-day draft expiry and rejected images.
- Complete app icon/launch assets, privacy disclosures, accessibility/device layout review, signing, TestFlight and App Store distribution preparation.

The simulator/core tests do not establish the real-device share-sheet, iCloud, secure-storage entitlement or cellular acceptance gates.

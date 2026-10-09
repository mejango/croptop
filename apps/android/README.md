# Croptop for Android

Native companion for one connected Croptop site. Choose a screenshot in the app or use **Share → Croptop**, review its destination and prepared image, then publish. A running desktop is unnecessary after connection and hosted publishing setup.

## Native pilot preparation — 2026-10-09

The stable Mac updater is released. This pass prepares the Android pilot without creating a signing key, uploading to Play, or publishing a post.

- [x] Centralize the deployed service origin and manifest host; accept only an exact approved pairing link and discard incoming capability-bearing intents after intake.
- [x] Target Android 16/API 36 with a compatible pinned build toolchain; reuse the existing crop-mark artwork for adaptive and monochrome icons.
- [x] Add unsigned release APK/AAB outputs and optional external signing inputs that reject incomplete configuration.
- [x] Exercise sharing before connection, repeated intents, setup navigation, recreation/process death, malformed links, and system-bar/back behavior on a task-owned emulator.
- [x] Run JVM tests, Android instrumentation, lint, debug/release packaging and artifact checks; inspect emulator screenshots and record remaining distribution gates.

Implementation scope is `apps/android` and the Android CI job. Keep additional SDK/emulator storage within a 5 GB budget where possible. Existing user installations and published content remain outside this verification pass.

## Build

Requires JDK 17, Android SDK 36 and build tools 35.0.0. Set `ANDROID_HOME` to the SDK (or add an ignored `local.properties` containing `sdk.dir=…`), then run:

```sh
./gradlew --no-daemon testDebugUnitTest lintDebug assembleDebug assembleDebugAndroidTest assembleRelease bundleRelease
bash scripts/check-release-signing.sh
```

The wrapper pins Gradle 8.13 and verifies its distribution SHA-256. Android Gradle Plugin 8.11.1 supports API 36 with this toolchain; a major plugin migration is unnecessary ([official compatibility](https://developer.android.com/build/releases/agp-8-11-0-release-notes)). The minimum Android version remains API 26; target/compile SDK is 36. Package `top.crop.mobile`, version `0.1.0`, version code `1` are the initial pilot identity, not an existing Play listing claim.

Build outputs:

- Installable, development-signed APK: `app/build/outputs/apk/debug/app-debug.apk`.
- Unsigned release APK: `app/build/outputs/apk/release/app-release-unsigned.apk`.
- Unsigned release bundle: `app/build/outputs/bundle/release/app-release.aab`.

Install the debug APK with `adb install -r app/build/outputs/apk/debug/app-debug.apk`. Unsigned release outputs cannot be installed or uploaded as signed releases. Release builds exclude the debug-only synthetic image provider and test libraries, have only the Internet permission, prohibit cleartext traffic, and do not fall back to debug signing.

The single build-owned `croptop.mobileOrigin` in `gradle.properties` supplies both `BuildConfig.MOBILE_SERVICE_ORIGIN` and the manifest host. It currently selects `https://croptop-phone-923c1bafd14ea328.croptop.workers.dev` for debug and release. Manual key import, API requests, and exact-root-path pairing links enforce that origin; there is no editable service override in the app. A deliberately different build may set that Gradle property, but its manifest and client always move together.

### External release signing

No production signing key is generated or committed. An authorized release operator can supply all four environment variables through their secret manager: `CROPTOP_ANDROID_KEYSTORE` (existing readable absolute file path), `CROPTOP_ANDROID_STORE_PASSWORD`, `CROPTOP_ANDROID_KEY_ALIAS`, and `CROPTOP_ANDROID_KEY_PASSWORD`. Then run:

```sh
./gradlew --no-daemon -Pcroptop.requireReleaseSigning=true assembleRelease bundleRelease
```

Missing required signing, partially supplied variables, and a malformed signing requirement fail configuration. With all variables unset and no signing requirement, release output is intentionally unsigned. Never put signing passwords in Gradle command arguments, source files, or logs. Play App Signing, upload-key custody, certificate identity and store upload remain separate operator decisions.

## Connect and post

1. On the existing publisher, enable hosted storage and open **Connect phone**. On Android, open **Open connection link** and paste its HTTPS link. Compare the eight-digit code and confirm on both devices. An unencrypted Ed25519 PKCS8 key file can also be imported locally.
2. Check **Site settings** for compatibility/readiness and explicit phone-publishing consent. Importing a key alone does not enable the service. An older site may need one bootstrap publication from the updated original publisher.
3. Choose or share one PNG, JPEG, WebP, HEIC, or HEIF image. Croptop retains its own copy before the temporary share URI disappears. Add an optional caption/title, prepare the normalized preview, then tap **Publish screenshot**.
4. Reopen the app and use **Check status / retry** after a disconnect or process termination. Pending work keeps its destination, operation ID, image bytes/digest, text, and last known service state. A new operation is never silently substituted for an uncertain one.

The root site key grants full site control. Android Keystore holds a non-exportable AES-GCM wrapping key; the imported Ed25519 key is encrypted in app-private, backup-excluded storage. Signing briefly decrypts it in-process and wipes the temporary DER bytes. Private keys are never included in API requests. Removing the connection deletes this phone's local key; it does not revoke other copies. Keep an original backup.

Draft files and metadata are private and excluded from Android backup. Clearing app data/uninstalling deletes those local drafts and the connection. Failed or unfinished drafts are retained, and a re-delivered share reopens an existing unfinished draft of the same source image. Multiple-image shares are rejected without silently choosing the first image. Capturing before connection is supported.

Once uploaded, a draft's destination, text, and source image are immutable. After a confirmed expired upload, **Create new draft from screenshot** makes an explicit fresh operation and preserves the original receipt. **Edit this draft** is available only after a fresh service check confirms a rejected preparation with no proposal and this phone has never authorized signing. Network failures and uncertain commits retain their original identity. Text limits come from the service and are checked as UTF-8 bytes before submission.

The app accepts only the approved service's exact HTTPS root pairing link by paste or an Android link association. Incoming intents are scrubbed after intake. Pasted links and PEM fields disable autofill, saved view state and personalized keyboard learning and clear on pause. Closing setup cancels the ephemeral pairing; it never discards the screenshot. Failed claim/consume and rejected task submission release the flow for a fresh link. Local key mutation and signing share a lock and generation guard, so stale setup callbacks cannot restore a removed key or remove a replacement.

Automatic verified App Links require the deployed domain's `/.well-known/assetlinks.json` to include the actual distributed release signing certificate (the Play app-signing certificate when using Play App Signing). No association file or certificate identity is invented here; paste remains available ([Android association requirements](https://developer.android.com/training/app-links/configure-assetlinks)).

## Privacy and storage

The app includes a factual **Privacy and storage** disclosure. Preparing sends screenshot/title/caption to the service; the private site key remains local. Service operation journals—including site identity, text, media hashes, status and receipts—are retained indefinitely. Private media/staging are eligible for cleanup after seven days only following successful reconciliation; uncertain operations retain their bytes. Published content is public and IPFS or third-party copies may persist. Removing a local connection stops new local signing after removal commits, but cannot revoke other key copies or cancel already-authorized requests.

This bundled disclosure is not a hosted operator privacy policy. Before public distribution, the operator must supply an accessible policy URL, complete accurate Play Data Safety/content-rating declarations, choose the Play account/app-signing identity, confirm any account-specific testing requirements, prepare store listing assets, and increment the version code for subsequent uploads. No store/account writes are part of this source preparation.

## Verification

JVM tests exercise the same IPNS/Ed25519 and pairing fixtures as the service, exact signed payload validation, tampered proposals, durable recovery, bounded intake, and duplicate share delivery. The UI uses framework Android views and the system photo picker (Android 13+) or system document picker (Android 8–12); no broad photo-library permission is requested.

The Android CI job also boots an API 36 AOSP emulator and actually runs `scripts/emulator-smoke.sh`, not just its compilation. For a task-owned emulator, install both debug APKs, set `CROPTOP_TEST_EMULATOR_SERIAL`, `CROPTOP_TEST_AVD_NAME` (must begin `croptop-`), `CROPTOP_TEST_OUTPUT_DIR`, and `ANDROID_HOME`, then run `bash scripts/emulator-smoke.sh`. `ADB_SERVER_SOCKET` may select an isolated ADB server. The script checks the target AVD, never clears app data, uses only synthetic images and the public deterministic test key, and does not contact the service or publish. It runs instrumentation plus separate seed/force-stop/relaunch/verify processes and captures screenshots. Native tests exercise the real resolver/Keystore/Activity, but the debug provider is same-UID; genuine third-party URI grant handling remains a physical-device check.

### Verification review — 2026-10-09

- 65 JVM tests passed. Eight native instrumentation tests passed on Android 16/API 36 AOSP ARM64, including real Keystore generation guards, transient share intake/recreation, sensitive-field clearing, and keyboard/back navigation.
- The separate force-stop check observed the old process disappear and a new PID reopen the same draft identity, text and retained image after the original provider file was removed. Before/after screenshots were inspected.
- Debug APK, instrumentation APK, unsigned release APK and unsigned AAB built successfully. Lint has zero errors and three advisory newer-dependency warnings. The scoped SDK command-line tooling also prints a harmless repository-XML-version warning.
- Required-signing-without-key, partial signing input, and malformed signing flag all failed closed. APK signature verification rejected the unsigned release; AAB inspection reported it unsigned. Manifest inspection confirmed the production origin, API 26–36 range, no debug flag/test provider, backup exclusion and Internet-only permission.
- Two forced, cache-disabled release builds produced byte-identical APK and AAB outputs from the same inputs. This proves the checked build/toolchain combination, not equivalence across arbitrary JDK, OS or dependency changes.
- CI YAML and shell syntax were checked locally; the new remote Linux emulator job is configured but has not yet run on a pushed revision. No production post, release certificate generation, Play upload or association deployment occurred.

Local verification used task-owned SDK/AVD/cache storage under `/private/tmp/croptop-android-tools/session.87ujuo` (about 3.8 GiB added). The stock emulator/ADB tools additionally created the explicitly authorized, previously absent `/Users/jango/.android/adbkey`, `adbkey.pub`, and `/Users/jango/.emulator_console_auth_token`; these are local test-host authentication files, not app/site release-signing keys. They were not printed or committed. Test logs and screenshots are in that task directory's `native-tests-2026-10-09` subdirectory.

Before a pilot release, run these physical-device checks with a configured hosted service:

- Screenshot preview, system gallery, Files, and Google Photos sharing; cold launch and cloud-only media.
- Share before connection, connect, and explicitly confirm the destination; screen rotation and app process death during intake/prepare/commit.
- Offline capture; lost upload/commit responses; repeated taps and intents; restart the service and retry the retained operation.
- One confirmed public post over cellular with the original computer asleep; concurrent desktop publication must remain present.
- Local key removal and re-import, consent suspension, low-storage failures, and the smallest supported Android version (API 26).

Publication requires the app to be open. The app reports pending work and resumes through status/retry; it does not promise unattended background completion. `Published` comes only from a confirmed service operation with a post URL.

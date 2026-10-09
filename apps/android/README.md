# Croptop for Android

Native companion for one connected Croptop site. Choose a screenshot in the app or use **Share → Croptop**, review its destination and prepared image, then publish. A running desktop is unnecessary after connection and hosted publishing setup.

## Build

Requires JDK 17 and Android SDK 35 with build tools 35.0.0. Set `ANDROID_HOME` to the SDK (or add an ignored `local.properties` containing `sdk.dir=…`), then run:

```sh
./gradlew assembleDebug testDebugUnitTest lintDebug
```

The debug APK is `app/build/outputs/apk/debug/app-debug.apk`. Install with `adb install -r app/build/outputs/apk/debug/app-debug.apk`. The wrapper pins Gradle 8.13 and verifies its distribution SHA-256. Release signing, Play Console setup, and distribution are deliberately separate from the debug build.

## Connect and post

1. On the existing publisher, enable hosted storage and open **Connect phone**. On Android, open **Open connection link** and paste its HTTPS link. Compare the eight-digit code and confirm on both devices. An unencrypted Ed25519 PKCS8 key file can also be imported locally.
2. Check **Site settings** for compatibility/readiness and explicit phone-publishing consent. Importing a key alone does not enable the service. An older site may need one bootstrap publication from the updated original publisher.
3. Choose or share one PNG, JPEG, WebP, HEIC, or HEIF image. Croptop retains its own copy before the temporary share URI disappears. Add an optional caption/title, prepare the normalized preview, then tap **Publish screenshot**.
4. Reopen the app and use **Check status / retry** after a disconnect or process termination. Pending work keeps its destination, operation ID, image bytes/digest, text, and last known service state. A new operation is never silently substituted for an uncertain one.

The root site key grants full site control. Android Keystore holds a non-exportable AES-GCM wrapping key; the imported Ed25519 key is encrypted in app-private, backup-excluded storage. Signing briefly decrypts it in-process and wipes the temporary DER bytes. Private keys are never included in API requests. Removing the connection deletes this phone's local key; it does not revoke other copies. Keep an original backup.

Draft files and metadata are private and excluded from Android backup. Clearing app data/uninstalling deletes those local drafts and the connection. Failed or unfinished drafts are retained, and a re-delivered share reopens an existing unfinished draft of the same source image. Multiple-image shares are rejected without silently choosing the first image. Capturing before connection is supported.

Once uploaded, a draft's destination, text, and source image are immutable. After a confirmed expired upload, **Create new draft from screenshot** makes an explicit fresh operation and preserves the original receipt. **Edit this draft** is available only after a fresh service check confirms a rejected preparation with no proposal and this phone has never authorized signing. Network failures and uncertain commits retain their original identity. Text limits come from the service and are checked as UTF-8 bytes before submission.

The app accepts the service's HTTPS pairing link by paste or an Android link association. Automatic verified App Links require the deployed domain's `/.well-known/assetlinks.json` to include the eventual release signing certificate; no distribution identity is invented by this project.

## Verification

JVM tests exercise the same IPNS/Ed25519 and pairing fixtures as the service, exact signed payload validation, tampered proposals, durable recovery, bounded intake, and duplicate share delivery. The UI uses framework Android views and the system photo picker (Android 13+) or system document picker (Android 8–12); no broad photo-library permission is requested.

Before a pilot release, run these physical-device checks with a configured hosted service:

- Screenshot preview, system gallery, Files, and Google Photos sharing; cold launch and cloud-only media.
- Share before connection, connect, and explicitly confirm the destination; screen rotation and app process death during intake/prepare/commit.
- Offline capture; lost upload/commit responses; repeated taps and intents; restart the service and retry the retained operation.
- One confirmed public post over cellular with the original computer asleep; concurrent desktop publication must remain present.
- Local key removal and re-import, consent suspension, low-storage failures, and the smallest supported Android version (API 26).

Publication requires the app to be open. The app reports pending work and resumes through status/retry; it does not promise unattended background completion. `Published` comes only from a confirmed service operation with a post URL.

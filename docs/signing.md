# Signing and notarizing the Mac app

The macOS build (`installer/macos.sh`) signs and notarizes when it is given a
Developer ID Application identity and notarization credentials, and otherwise
produces an ad-hoc (unsigned) build that still runs locally. There are two ways
to get a signed, notarized `Croptop.dmg`.

## A. In GitHub Actions (preferred: every tag is signed)

Add these repository secrets (`gh secret set <NAME> --repo mejango/croptop`).
Run the commands on the Mac that holds your Developer ID Application certificate.

| Secret | What it is | How to produce it |
|---|---|---|
| `MACOS_CERT_P12` | Your Developer ID Application cert and key, base64'd | Keychain Access, right-click the "Developer ID Application: … (SY2W527QJA)" identity, Export as `cert.p12` with a password. Then `base64 -i cert.p12 \| gh secret set MACOS_CERT_P12 --repo mejango/croptop` |
| `MACOS_CERT_PASSWORD` | The password you set on that `.p12` | `gh secret set MACOS_CERT_PASSWORD --repo mejango/croptop` (type it) |
| `AC_API_KEY_P8` | An App Store Connect API key (`.p8`), base64'd | App Store Connect, Users and Access, Integrations, Team Keys, generate a key with the "Developer" role. Download `AuthKey_XXXX.p8` once. Then `base64 -i AuthKey_XXXX.p8 \| gh secret set AC_API_KEY_P8 --repo mejango/croptop` |
| `AC_API_KEY_ID` | The key's ID (the `XXXX` in the filename) | `gh secret set AC_API_KEY_ID --repo mejango/croptop` |
| `AC_API_ISSUER_ID` | The issuer UUID shown above the keys list | `gh secret set AC_API_ISSUER_ID --repo mejango/croptop` |

With those set, the `macos-app` job imports the cert into a throwaway keychain,
signs with hardened runtime and a secure timestamp, notarizes the app and the
dmg, and staples both. Publishing also requires `SPARKLE_ED25519_KEY`. Without
signing credentials a local build can still be ad-hoc signed, but the release
publisher refuses to upload it. If you have the banny App Store Connect key, reuse
it here.

## B. Locally, on the Mac that has the certificate

Mirrors the banny release flow. Needs the Developer ID Application identity in
your keychain and a stored notary profile (once):

```
xcrun notarytool store-credentials croptop-notary \
  --key AuthKey_XXXX.p8 --key-id XXXX --issuer <issuer-uuid>
```

Then, for a tag that goreleaser has already published the darwin tarballs for:

```
installer/release-macos.sh 0.11.0
```

That downloads the release's darwin builds, signs and notarizes the app, and
uploads the stapled `Croptop.dmg` to the release. It reads:

- `MACOS_SIGN_IDENTITY` (default: the one Developer ID Application identity in
  your keychain)
- `NOTARY_PROFILE` (default `croptop-notary`)

## Notes

- The app carries hardened runtime with `installer/Croptop.entitlements` (no App
  Sandbox: it runs a bundled node that binds a local port and writes the data
  dir). The bundled Go engine is signed too, so hardened runtime accepts it.
- Sparkle 2 updates the complete app through its signed helper. Use Croptop →
  Check for Updates… or “Update available” beside the version at the bottom of
  the sidebar, then Install and Relaunch.
  Editing and publishing defer relaunch. The CLI keeps its own self-update.
- Developer ID team is `SY2W527QJA`; the identity is `Developer ID Application: Jango De La Noche (SY2W527QJA)`.
- On this release Mac the signing keychain, private key and `.p12` live under `~/Documents/croptop-signing/`, and the App Store Connect notary key under `~/Downloads/` (see local notes for the exact ids). Keep them out of the repo.

## Sparkle updates

Existing installations without Sparkle need one manual installation of the new
DMG. Subsequent versions install through Sparkle. The feed is the latest GitHub
release's `appcast.xml`; it and every update archive are Ed25519 signed.
`SUVerifyUpdateBeforeExtraction` and `SURequireSignedFeed` are required.

Increment `installer/macos-build-number` for every distributed Mac build,
including rebuilds with the same marketing version. Never reuse a published
build number. `CROPTOP_BUILD_NUMBER` may override this for isolated local tests.
The feed uses `Croptop-<build>.dmg`, an immutable asset; `Croptop.dmg` remains the
manual download alias. The publisher refuses rollbacks and conflicting builds,
and uploads the feed last. It never creates tags.

The private key lives outside the repository. On this signing Mac it is
`~/Documents/croptop-signing/sparkle-ed25519.key` (mode 0600). Back it up securely;
losing it breaks updates for existing installations. Only the public key in
`installer/sparkle-public-key.txt` is shipped. To publish an already built app:

```sh
export SPARKLE_KEY_FILE="$HOME/Documents/croptop-signing/sparkle-ed25519.key"
python3 installer/publish-macos.py 0.11.0 /path/to/build/out --prepare-only
python3 installer/publish-macos.py 0.11.0 /path/to/build/out
```

Preparation validates Developer ID notarization and creates the signed appcast
with checksum-pinned Sparkle tools. The release must already exist and be the
latest release. Preserve the same key for all future releases. Never put it in
command arguments, logs, or source control. In CI use the `SPARKLE_ED25519_KEY`
secret, written to a temporary file with restricted permissions.

An already running external console service is not owned by the GUI app and is
not stopped during updates. The bundled engine updates with the app; an older
external service still needs its own upgrade/restart.

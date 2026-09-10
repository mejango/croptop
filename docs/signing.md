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
| `MACOS_CERT_P12` | Your Developer ID Application cert and key, base64'd | Keychain Access, right-click the "Developer ID Application: … (229P4A97W6)" identity, Export as `cert.p12` with a password. Then `base64 -i cert.p12 \| gh secret set MACOS_CERT_P12 --repo mejango/croptop` |
| `MACOS_CERT_PASSWORD` | The password you set on that `.p12` | `gh secret set MACOS_CERT_PASSWORD --repo mejango/croptop` (type it) |
| `AC_API_KEY_P8` | An App Store Connect API key (`.p8`), base64'd | App Store Connect, Users and Access, Integrations, Team Keys, generate a key with the "Developer" role. Download `AuthKey_XXXX.p8` once. Then `base64 -i AuthKey_XXXX.p8 \| gh secret set AC_API_KEY_P8 --repo mejango/croptop` |
| `AC_API_KEY_ID` | The key's ID (the `XXXX` in the filename) | `gh secret set AC_API_KEY_ID --repo mejango/croptop` |
| `AC_API_ISSUER_ID` | The issuer UUID shown above the keys list | `gh secret set AC_API_ISSUER_ID --repo mejango/croptop` |

With those set, the `macos-app` job imports the cert into a throwaway keychain,
signs with hardened runtime and a secure timestamp, notarizes the app and the
dmg, and staples both. Without them the job still builds an unsigned dmg, so
forks and PRs keep working. If you have the banny App Store Connect key, reuse
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
- The signed app cannot replace its own binary, so its update banner sends you
  to the download instead of self-updating. The CLI install keeps self-update.
- Team ID is `229P4A97W6`.

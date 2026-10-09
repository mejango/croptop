# Signing and notarizing the Mac app

The release workflow stages CLI/Linux/Windows artifacts into a **draft**, never
marks it latest, and does not update Homebrew. Mac signing, notarization, signed
feed generation, and promotion are explicitly owned by the release operator on
the signing Mac. There is no automatic CI Mac publisher or unsigned fallback.

`installer/macos.sh` still permits ad-hoc development builds when credentials
are absent. Such builds are not distributable. Release preparation verifies
Developer ID signing, Gatekeeper acceptance, and notarization before generating
the signed updater feed.

## Stage a complete release before promotion

1. Commit and test the exact release source. Set a new numeric
   `installer/macos-build-number` greater than every distributed stable or pilot
   build. Verify the bundled engine's default phone origin against the live
   trusted composer; all CLI architectures and the Mac engine must agree.
2. Create the version tag once from that commit. Do not move an existing tag or
   rewrite the pilot. Wait for the tag's `release` workflow to finish staging
   all six CLI archives, Linux packages, and both Windows installers. Its
   `signed-mac-handoff` job is a handoff, not a claim that Mac is released.
3. On the signing Mac, use a clean checkout of that same tag. Download its draft
   Darwin archives with authenticated `gh release download`, verify their
   `checksums.txt`, and build with `installer/macos.sh`. Set
   `MACOS_SIGN_IDENTITY` and either the existing `NOTARY_PROFILE` or
   `AC_API_KEY_PATH`/`AC_API_KEY_ID`/`AC_API_ISSUER_ID`. Alternatively, the local
   `installer/pilot-macos.py build` helper supports a stable numeric version,
   an explicit `--commit`, and an isolated temporary signing keychain; it never
   uploads or changes tags/releases.
4. Set `SPARKLE_KEY_FILE` to the protected key outside the repository and run
   `python3 installer/publish-macos.py <version> <out> --prepare-only`. This
   validates signing/notarization and prepares `out/update/Croptop-<build>.dmg`
   and `out/update/appcast.xml` without changing GitHub.
5. Immediately before uploading, run
   `python3 installer/release_guard.py <tag> --commit <full-commit-sha>`. It
   requires the remote tag to resolve to that exact commit and refuses all
   published releases, including prereleases. Upload `Croptop.dmg`, the
   immutable numbered DMG, signed appcast, provenance, and complete checksums
   into the draft. Do not use `--clobber` on published artifacts.
6. Verify every required asset, hash, signature, notarization ticket, version,
   build number, source pin, and appcast enclosure URL. Publish the complete
   release initially with `--draft=false --prerelease --latest=false`, verify
   anonymous exact-tag downloads, then promote that same release with
   `--prerelease=false --latest=true`. The existing latest appcast URL then
   changes only after its numbered DMG is downloadable. Immutable-release
   repositories lock assets at initial publication, so all uploads precede it.
7. Verify the live latest feed and a real existing Mac's update check. Only then
   publish the Homebrew formula retained as the workflow artifact
   `homebrew-formula-<tag>`, using the already verified release checksums.

The draft-only settings in `.goreleaser.yaml`, runtime guards in
`installer/release_guard.py`, and `installer/test_release_guard.py` prevent
ordinary release reruns from overwriting a published release or exposing a
half-built updater release. Tag-triggered workflows must be safe before a tag
is created; using the GitHub API to create a tag is not a CI-bypass mechanism.

Draft lookup uses the authenticated, paginated releases list. GitHub's
release-by-tag REST endpoint returns published releases only; its 404 is not
proof that a draft is absent. Any lookup error fails staging closed.

If staging stops after GoReleaser has uploaded its archives, preserve the tag
and existing assets. `installer/stage-windows.ps1` owns Windows compilation and
append-only uploads, verifies frozen installer inputs and archive checksums,
and rejects any existing installer with different bytes. The narrowly scoped
`release-staging-repair.yml` workflow repairs only the pinned v0.13.20 draft;
it never rebuilds the engine or publishes a release. Its retained Windows
outputs can be used to recover a partially completed upload without rebuilding
or replacing an existing installer. A skipped Homebrew artifact must be
regenerated from the verified release archive hashes, not described as a
retained CI output.

## Legacy latest-only Mac repair helper

`installer/release-macos.sh <version>` and `installer/publish-macos.py` **without**
`--prepare-only` are legacy helpers for an already published latest release.
They are not the first-release staging path and cannot promote a draft. The
shell helper reads `MACOS_SIGN_IDENTITY` (otherwise finds a local Developer ID
identity) and `NOTARY_PROFILE` (default `croptop-notary`); update signing also
requires `SPARKLE_KEY_FILE`. Do not use these helpers to make a new release
latest before its artifacts and signed feed are ready.

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
manual download alias. Preparation never creates tags or releases. The legacy
latest-only publisher refuses rollbacks and conflicting numbered builds and
uploads the feed last; normal new releases use the complete-draft flow above.

The private key lives outside the repository. On this signing Mac it is
`~/Documents/croptop-signing/sparkle-ed25519.key` (mode 0600). Back it up securely;
losing it breaks updates for existing installations. Only the public key in
`installer/sparkle-public-key.txt` is shipped. To prepare an already built app:

```sh
export SPARKLE_KEY_FILE="$HOME/Documents/croptop-signing/sparkle-ed25519.key"
python3 installer/publish-macos.py 0.11.0 /path/to/build/out --prepare-only
```

Preparation validates Developer ID notarization and creates the signed appcast
with checksum-pinned Sparkle tools; it does not require the release to be
latest. Preserve the same key for all future releases. Never put it in command
arguments, logs, or source control. CI does not currently consume Mac signing
secrets. A future automated signing path must fail closed on missing
credentials and retain the draft/promotion gates.

For rollback, mark the prior complete stable release latest again to halt
further rollout. Keep published tags and numbered artifacts intact. Already
updated Macs do not automatically downgrade; distribute a corrected, higher
build instead, considering any data-format migrations.

An already running external console service is not owned by the GUI app and is
not stopped during updates. The bundled engine updates with the app; an older
external service still needs its own upgrade/restart.

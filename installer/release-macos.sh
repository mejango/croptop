#!/bin/sh
# Sign, notarize, and upload Croptop.dmg for a released tag, on a Mac that holds
# the Developer ID Application certificate. Mirrors the banny release flow.
# Usage: installer/release-macos.sh <version>   (e.g. 0.11.0, no leading v)
set -eu
VER=$1
: "${SPARKLE_KEY_FILE:?Set SPARKLE_KEY_FILE to the private Sparkle signing key}"
HERE=$(cd "$(dirname "$0")" && pwd)
: "${MACOS_SIGN_IDENTITY:=$(security find-identity -v -p codesigning | sed -nE 's/.*"(Developer ID Application: .*)"/\1/p' | head -1)}"
: "${NOTARY_PROFILE:=croptop-notary}"
if [ -z "$MACOS_SIGN_IDENTITY" ]; then
  echo "no Developer ID Application identity in the keychain; set MACOS_SIGN_IDENTITY" >&2
  exit 2
fi
export MACOS_SIGN_IDENTITY NOTARY_PROFILE
WORK=$(mktemp -d)
echo "downloading darwin builds for v$VER"
gh release download "v$VER" --repo mejango/croptop --pattern "croptop_${VER}_darwin_*.tar.gz" --dir "$WORK/in"
"$HERE/macos.sh" "$VER" "$WORK/in" "$WORK/out"
python3 "$HERE/publish-macos.py" "$VER" "$WORK/out"

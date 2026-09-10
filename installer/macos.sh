#!/bin/sh
# Builds Croptop.app (universal) and Croptop.dmg from the two darwin archives
# of a release. Usage: installer/macos.sh <version> <dir with the tar.gz files> <out dir>
#
# The app is the native SwiftUI client in apps/macos. The Go engine lives in
# Contents/Resources/croptop (a different directory from the executable, since
# the file system is case-insensitive), the brand fonts in Resources/fonts.
set -eu
VER=$1; IN=$2; OUT=$3
HERE=$(cd "$(dirname "$0")" && pwd)
WORK=$(mktemp -d)
for a in amd64 arm64; do mkdir -p "$WORK/$a"; tar -xzf "$IN/croptop_${VER}_darwin_${a}.tar.gz" -C "$WORK/$a" croptop; done
APP="$WORK/Croptop.app"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
swift build -c release --arch arm64 --arch x86_64 --package-path "$HERE/../apps/macos" --scratch-path "$WORK/swift"
cp "$WORK/swift/apple/Products/Release/Croptop" "$APP/Contents/MacOS/Croptop"
mkdir -p "$APP/Contents/Resources/fonts" && cp "$HERE/fonts/"*.ttf "$APP/Contents/Resources/fonts/"
lipo -create -output "$APP/Contents/Resources/croptop" "$WORK/amd64/croptop" "$WORK/arm64/croptop"
chmod +x "$APP/Contents/MacOS/Croptop" "$APP/Contents/Resources/croptop"
lipo -info "$APP/Contents/Resources/croptop"
cp "$HERE/Croptop.icns" "$APP/Contents/Resources/Croptop.icns"
cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleName</key><string>Croptop</string>
  <key>CFBundleDisplayName</key><string>Croptop</string>
  <key>CFBundleIdentifier</key><string>top.crop.croptop</string>
  <key>CFBundleVersion</key><string>$VER</string>
  <key>CFBundleShortVersionString</key><string>$VER</string>
  <key>CFBundleExecutable</key><string>Croptop</string>
  <key>CFBundleIconFile</key><string>Croptop</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>LSMinimumSystemVersion</key><string>13.0</string>
  <key>NSHighResolutionCapable</key><true/>
  <key>NSAppTransportSecurity</key><dict><key>NSAllowsLocalNetworking</key><true/></dict>
  <key>CFBundleDocumentTypes</key><array><dict>
    <key>CFBundleTypeName</key><string>Media</string>
    <key>CFBundleTypeRole</key><string>Viewer</string>
    <key>LSHandlerRank</key><string>Alternate</string>
    <key>LSItemContentTypes</key><array>
      <string>public.image</string><string>public.movie</string><string>public.audio</string><string>public.data</string>
    </array>
  </dict></array>
</dict></plist>
PLIST
plutil -lint "$APP/Contents/Info.plist"

# Signing. With MACOS_SIGN_IDENTITY set (a "Developer ID Application" identity),
# sign for distribution with hardened runtime and a secure timestamp; otherwise
# ad-hoc sign so dev builds still run locally. Sign the nested Go engine first,
# then seal the app bundle (no --deep, which is deprecated).
ENTITLEMENTS="$HERE/Croptop.entitlements"
if [ -n "${MACOS_SIGN_IDENTITY:-}" ]; then
  codesign --force --options runtime --timestamp --entitlements "$ENTITLEMENTS" --sign "$MACOS_SIGN_IDENTITY" "$APP/Contents/Resources/croptop"
  codesign --force --options runtime --timestamp --entitlements "$ENTITLEMENTS" --sign "$MACOS_SIGN_IDENTITY" "$APP"
  codesign --verify --strict --verbose=2 "$APP"
else
  codesign --force --deep -s - "$APP"
fi

mkdir -p "$OUT"
# a dmg with the app and a link to Applications
DMGDIR="$WORK/dmg"; mkdir -p "$DMGDIR"; cp -R "$APP" "$DMGDIR/"; ln -s /Applications "$DMGDIR/Applications"
hdiutil create -volname Croptop -srcfolder "$DMGDIR" -ov -format UDZO "$OUT/Croptop.dmg" >/dev/null

# Notarization. notarytool authenticates with either an App Store Connect API
# key (AC_API_KEY_PATH/AC_API_KEY_ID/AC_API_ISSUER_ID) or a stored keychain
# profile (NOTARY_PROFILE). Notarize the app first and staple it (so first
# launch works offline), then notarize and staple the dmg wrapper.
notarize() { # $1 = file to submit
  if [ -n "${AC_API_KEY_PATH:-}" ]; then
    xcrun notarytool submit "$1" --key "$AC_API_KEY_PATH" --key-id "$AC_API_KEY_ID" --issuer "$AC_API_ISSUER_ID" --wait
  else
    xcrun notarytool submit "$1" --keychain-profile "$NOTARY_PROFILE" --wait
  fi
}
if [ -n "${MACOS_SIGN_IDENTITY:-}" ] && { [ -n "${AC_API_KEY_PATH:-}" ] || [ -n "${NOTARY_PROFILE:-}" ]; }; then
  ditto -c -k --keepParent "$APP" "$WORK/Croptop.zip"
  notarize "$WORK/Croptop.zip"
  xcrun stapler staple "$APP"
  # rebuild the dmg from the now-stapled app, then notarize and staple the dmg
  rm -rf "$DMGDIR"; mkdir -p "$DMGDIR"; cp -R "$APP" "$DMGDIR/"; ln -s /Applications "$DMGDIR/Applications"
  hdiutil create -volname Croptop -srcfolder "$DMGDIR" -ov -format UDZO "$OUT/Croptop.dmg" >/dev/null
  notarize "$OUT/Croptop.dmg"
  xcrun stapler staple "$OUT/Croptop.dmg"
  xcrun stapler validate "$OUT/Croptop.dmg"
  echo "signed and notarized $OUT/Croptop.dmg (Gatekeeper: Notarized Developer ID)"
else
  echo "built unsigned $OUT/Croptop.dmg (set MACOS_SIGN_IDENTITY and notary credentials to sign)"
fi
cp -R "$APP" "$OUT/Croptop.app"
echo "built $OUT/Croptop.app and $OUT/Croptop.dmg"

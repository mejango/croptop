#!/bin/sh
# Builds Croptop.app (universal) and Croptop.dmg from the two darwin archives
# of a release. Usage: installer/macos.sh <version> <dir with the tar.gz files> <out dir>
#
# The app is a small native window (installer/CroptopApp/main.swift) around the
# web console. It starts the node when it opens, stops it when it quits, and
# turns files dropped on it into posts. The Go binary lives in
# Contents/Resources/croptop (a different directory from the executable, since
# the file system is case-insensitive).
set -eu
VER=$1; IN=$2; OUT=$3
HERE=$(cd "$(dirname "$0")" && pwd)
WORK=$(mktemp -d)
for a in amd64 arm64; do mkdir -p "$WORK/$a"; tar -xzf "$IN/croptop_${VER}_darwin_${a}.tar.gz" -C "$WORK/$a" croptop; done
APP="$WORK/Croptop.app"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
for t in arm64 x86_64; do
  swiftc -O -swift-version 5 -target "$t-apple-macos12.0" -o "$WORK/Croptop-$t" "$HERE/CroptopApp/main.swift" -framework Cocoa -framework WebKit
done
lipo -create -output "$APP/Contents/MacOS/Croptop" "$WORK/Croptop-arm64" "$WORK/Croptop-x86_64"
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
  <key>LSMinimumSystemVersion</key><string>12.0</string>
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
codesign --force --deep -s - "$APP"
mkdir -p "$OUT"; cp -R "$APP" "$OUT/Croptop.app"
# a dmg with the app and a link to Applications
DMGDIR="$WORK/dmg"; mkdir -p "$DMGDIR"; cp -R "$APP" "$DMGDIR/"; ln -s /Applications "$DMGDIR/Applications"
hdiutil create -volname Croptop -srcfolder "$DMGDIR" -ov -format UDZO "$OUT/Croptop.dmg" >/dev/null
echo "built $OUT/Croptop.app and $OUT/Croptop.dmg"

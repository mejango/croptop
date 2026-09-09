#!/bin/sh
# Builds Croptop.app (universal) and Croptop.dmg from the two darwin archives
# of a release. Usage: installer/macos.sh <version> <dir with the tar.gz files> <out dir>
set -eu
VER=$1; IN=$2; OUT=$3
WORK=$(mktemp -d)
for a in amd64 arm64; do mkdir -p "$WORK/$a"; tar -xzf "$IN/croptop_${VER}_darwin_${a}.tar.gz" -C "$WORK/$a" croptop; done
APP="$OUT/Croptop.app"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
# The binary is the app's executable: with no arguments it runs the console
# and opens the browser. (No separate launcher: macOS file systems are
# case-insensitive, so "Croptop" and "croptop" would be the same file.)
lipo -create -output "$APP/Contents/MacOS/croptop" "$WORK/amd64/croptop" "$WORK/arm64/croptop"
chmod +x "$APP/Contents/MacOS/croptop"
lipo -info "$APP/Contents/MacOS/croptop"
cp "$(dirname "$0")/Croptop.icns" "$APP/Contents/Resources/Croptop.icns"
cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleName</key><string>Croptop</string>
  <key>CFBundleDisplayName</key><string>Croptop</string>
  <key>CFBundleIdentifier</key><string>top.crop.croptop</string>
  <key>CFBundleVersion</key><string>$VER</string>
  <key>CFBundleShortVersionString</key><string>$VER</string>
  <key>CFBundleExecutable</key><string>croptop</string>
  <key>CFBundleIconFile</key><string>Croptop</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>LSMinimumSystemVersion</key><string>12.0</string>
  <key>NSHighResolutionCapable</key><true/>
</dict></plist>
PLIST
# a dmg with the app and a link to Applications
DMGDIR="$WORK/dmg"; mkdir -p "$DMGDIR"; cp -R "$APP" "$DMGDIR/"; ln -s /Applications "$DMGDIR/Applications"
hdiutil create -volname Croptop -srcfolder "$DMGDIR" -ov -format UDZO "$OUT/Croptop.dmg" >/dev/null
echo "built $APP and $OUT/Croptop.dmg"

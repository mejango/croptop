#!/bin/sh
# Builds Croptop.app (universal) and Croptop.dmg from the two darwin archives
# of a release. Usage: installer/macos.sh <version> <dir with the tar.gz files> <out dir>
#
# The app is a stay-open AppleScript droplet: it sits in the Dock, files dropped
# on it become a post, and it installs the login service that keeps the console
# and the IPFS node running. The Go binary lives in Contents/Resources/croptop.
set -eu
VER=$1; IN=$2; OUT=$3
HERE=$(cd "$(dirname "$0")" && pwd)
WORK=$(mktemp -d)
for a in amd64 arm64; do mkdir -p "$WORK/$a"; tar -xzf "$IN/croptop_${VER}_darwin_${a}.tar.gz" -C "$WORK/$a" croptop; done
APP="$WORK/Croptop.app"
osacompile -s -o "$APP" "$HERE/droplet.applescript"
lipo -create -output "$APP/Contents/Resources/croptop" "$WORK/amd64/croptop" "$WORK/arm64/croptop"
chmod +x "$APP/Contents/Resources/croptop"
lipo -info "$APP/Contents/Resources/croptop"
# the droplet's own icons are named droplet.icns / applet.icns; ours replaces them
for i in droplet applet; do [ -f "$APP/Contents/Resources/$i.icns" ] && cp "$HERE/Croptop.icns" "$APP/Contents/Resources/$i.icns"; done
PB=/usr/libexec/PlistBuddy; PL="$APP/Contents/Info.plist"
$PB -c "Add :CFBundleName string Croptop" "$PL" || $PB -c "Set :CFBundleName Croptop" "$PL"
$PB -c "Add :CFBundleIdentifier string top.crop.croptop" "$PL" || $PB -c "Set :CFBundleIdentifier top.crop.croptop" "$PL"
$PB -c "Add :CFBundleShortVersionString string $VER" "$PL" || $PB -c "Set :CFBundleShortVersionString $VER" "$PL"
$PB -c "Add :CFBundleVersion string $VER" "$PL" || $PB -c "Set :CFBundleVersion $VER" "$PL"
$PB -c "Add :CFBundleDisplayName string Croptop" "$PL" || true
$PB -c "Add :NSHighResolutionCapable bool true" "$PL" || true
# accept images, video and audio (and anything else) on the Dock icon
$PB -c "Delete :CFBundleDocumentTypes" "$PL" 2>/dev/null || true
$PB -c "Add :CFBundleDocumentTypes array" \
  -c "Add :CFBundleDocumentTypes:0 dict" -c "Add :CFBundleDocumentTypes:0:CFBundleTypeName string Media" \
  -c "Add :CFBundleDocumentTypes:0:CFBundleTypeRole string Viewer" \
  -c "Add :CFBundleDocumentTypes:0:LSItemContentTypes array" \
  -c "Add :CFBundleDocumentTypes:0:LSItemContentTypes:0 string public.image" \
  -c "Add :CFBundleDocumentTypes:0:LSItemContentTypes:1 string public.movie" \
  -c "Add :CFBundleDocumentTypes:0:LSItemContentTypes:2 string public.audio" \
  -c "Add :CFBundleDocumentTypes:0:LSItemContentTypes:3 string public.data" "$PL"
plutil -lint "$PL"
# osacompile signs the applet; our edits broke that signature, so sign ad hoc again or macOS calls the app damaged
codesign --force --deep -s - "$APP"
mkdir -p "$OUT"; cp -R "$APP" "$OUT/Croptop.app"
# a dmg with the app and a link to Applications
DMGDIR="$WORK/dmg"; mkdir -p "$DMGDIR"; cp -R "$APP" "$DMGDIR/"; ln -s /Applications "$DMGDIR/Applications"
hdiutil create -volname Croptop -srcfolder "$DMGDIR" -ov -format UDZO "$OUT/Croptop.dmg" >/dev/null
echo "built $OUT/Croptop.app and $OUT/Croptop.dmg"

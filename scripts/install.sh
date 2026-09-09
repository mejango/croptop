#!/bin/sh
# Installs the latest croptop release for macOS or Linux.
#   curl -fsSL https://crop.top/install.sh | sh
set -eu
REPO=mejango/croptop
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in x86_64|amd64) ARCH=amd64 ;; arm64|aarch64) ARCH=arm64 ;; *) echo "unsupported architecture: $ARCH" >&2; exit 1 ;; esac
case "$OS" in darwin|linux) ;; *) echo "this script is for macOS and Linux; on Windows use install.ps1" >&2; exit 1 ;; esac
TAG=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1)
[ -n "$TAG" ] || { echo "could not find the latest release" >&2; exit 1; }
VER=${TAG#v}
FILE="croptop_${VER}_${OS}_${ARCH}.tar.gz"
DEST=${CROPTOP_INSTALL_DIR:-/usr/local/bin}
[ -w "$DEST" ] || [ -n "${CROPTOP_INSTALL_DIR:-}" ] || DEST="$HOME/.local/bin"
mkdir -p "$DEST"
TMP=$(mktemp -d)
echo "downloading croptop $VER for $OS/$ARCH"
curl -fsSL "https://github.com/$REPO/releases/download/$TAG/$FILE" -o "$TMP/$FILE"
curl -fsSL "https://github.com/$REPO/releases/download/$TAG/checksums.txt" -o "$TMP/checksums.txt"
if command -v sha256sum >/dev/null 2>&1; then SUM=$(sha256sum "$TMP/$FILE" | cut -d' ' -f1); else SUM=$(shasum -a 256 "$TMP/$FILE" | cut -d' ' -f1); fi
grep -q "$SUM  $FILE" "$TMP/checksums.txt" || { echo "checksum mismatch" >&2; exit 1; }
tar -xzf "$TMP/$FILE" -C "$TMP" croptop
install -m 755 "$TMP/croptop" "$DEST/croptop"
rm "$TMP/croptop" "$TMP/$FILE" "$TMP/checksums.txt"; rmdir "$TMP"
echo "installed $DEST/croptop"
case ":$PATH:" in *":$DEST:"*) ;; *) echo "add $DEST to your PATH, then" ;; esac
echo "run: croptop"
